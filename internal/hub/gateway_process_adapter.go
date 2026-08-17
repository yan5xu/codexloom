package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	goruntime "runtime"
	"strings"
)

type gatewayServiceEffectOutcome string

const (
	// Applied means the adapter completed its registration/service request; it
	// is never process success until an exact heartbeat proof is committed.
	gatewayServiceEffectApplied gatewayServiceEffectOutcome = "applied"
	// Rejected proves the adapter made no service/registration mutation because
	// a precondition failed. Runtime latches manual recovery without rollback.
	gatewayServiceEffectRejected gatewayServiceEffectOutcome = "rejected"
	// Failed proves the target registration was not committed; Runtime may
	// restore the already-validated anchor using the recovery generation.
	gatewayServiceEffectFailed gatewayServiceEffectOutcome = "failed"
	// Indeterminate means a mutation may have landed. Runtime must inspect and
	// reconcile; it may not resend the target operation.
	gatewayServiceEffectIndeterminate gatewayServiceEffectOutcome = "indeterminate"
)

type gatewayServiceEffectResult struct {
	Outcome gatewayServiceEffectOutcome
	Err     error
}

type gatewayServiceEffect struct {
	AttemptID    string
	ConnectionID string
	Generation   string
	Descriptor   gatewayLaunchDescriptor
	Plan         gatewayLaunchPlan
}

type gatewayServiceObservedState string

const (
	gatewayServiceObservedTarget   gatewayServiceObservedState = "target"
	gatewayServiceObservedRecovery gatewayServiceObservedState = "recovery"
	gatewayServiceObservedAnchor   gatewayServiceObservedState = "anchor"
	gatewayServiceObservedAbsent   gatewayServiceObservedState = "absent"
	gatewayServiceObservedUnknown  gatewayServiceObservedState = "unknown"
)

type gatewayServiceInspectionRequest struct {
	AttemptID          string
	ConnectionID       string
	Plan               gatewayLaunchPlan
	TargetGeneration   string
	RecoveryGeneration string
}

type gatewayServiceInspection struct {
	State            gatewayServiceObservedState
	Generation       string
	Build            string
	ExecutableDigest string
}

type gatewayServiceAdapter interface {
	ValidateAnchor(context.Context, gatewayLaunchPlan) error
	Apply(context.Context, gatewayServiceEffect) gatewayServiceEffectResult
	Restore(context.Context, gatewayServiceEffect) gatewayServiceEffectResult
	Inspect(context.Context, gatewayServiceInspectionRequest) (gatewayServiceInspection, error)
}

type platformGatewayServiceAdapter struct {
	manager gatewayServiceManager
}

type gatewayCommandOutput struct {
	data      []byte
	truncated bool
}

func (b *gatewayCommandOutput) Write(value []byte) (int, error) {
	const limit = 16 << 10
	remaining := limit - len(b.data)
	if remaining > 0 {
		if len(value) < remaining {
			remaining = len(value)
		}
		b.data = append(b.data, value[:remaining]...)
	}
	if remaining < len(value) {
		b.truncated = true
	}
	return len(value), nil
}

func (b *gatewayCommandOutput) String() string {
	value := strings.TrimSpace(string(b.data))
	if b.truncated {
		value += " …(truncated)"
	}
	return value
}

func (a *platformGatewayServiceAdapter) ValidateAnchor(_ context.Context, plan gatewayLaunchPlan) error {
	if err := validateGatewayLaunchPlan(plan); err != nil {
		return err
	}
	if plan.Target.Manager != a.manager {
		return fmt.Errorf("Gateway service adapter mismatch")
	}
	want, err := renderGatewayServiceUnitForAttempt(plan.Anchor.Descriptor, plan.Anchor.AttemptID, plan.Anchor.Generation)
	if err != nil {
		return err
	}
	got, err := readGatewayServiceUnit(plan.Anchor.Descriptor.UnitPath)
	if err != nil {
		return fmt.Errorf("read Gateway registration anchor: %w", err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("Gateway registration does not match the integrity-checked anchor")
	}
	return nil
}

func defaultGatewayServiceAdapter(plan gatewayLaunchPlan) (gatewayServiceAdapter, error) {
	if err := validateGatewayLaunchPlan(plan); err != nil {
		return nil, err
	}
	switch goruntime.GOOS {
	case "darwin":
		if plan.Target.Manager != gatewayServiceManagerLaunchd {
			return nil, fmt.Errorf("Gateway service manager %q is unsupported on darwin", plan.Target.Manager)
		}
	case "linux":
		if plan.Target.Manager != gatewayServiceManagerSystemd {
			return nil, fmt.Errorf("Gateway service manager %q is unsupported on linux", plan.Target.Manager)
		}
	default:
		return nil, fmt.Errorf("Gateway process recovery is unsupported on %s", goruntime.GOOS)
	}
	return &platformGatewayServiceAdapter{manager: plan.Target.Manager}, nil
}

func (a *platformGatewayServiceAdapter) Apply(ctx context.Context, effect gatewayServiceEffect) gatewayServiceEffectResult {
	if err := validateGatewayLaunchPlan(effect.Plan); err != nil || !gatewayLaunchDescriptorsEqual(effect.Descriptor, effect.Plan.Target) {
		return gatewayServiceEffectResult{Outcome: gatewayServiceEffectRejected, Err: fmt.Errorf("Gateway target effect does not match its frozen plan")}
	}
	if err := a.ValidateAnchor(ctx, effect.Plan); err != nil {
		return gatewayServiceEffectResult{Outcome: gatewayServiceEffectRejected, Err: fmt.Errorf("Gateway registration precondition changed: %w", err)}
	}
	if err := verifyGatewayExecutable(effect.Plan.Target); err != nil {
		return gatewayServiceEffectResult{Outcome: gatewayServiceEffectRejected, Err: fmt.Errorf("Gateway target executable changed: %w", err)}
	}
	return a.replaceAndRestart(ctx, effect)
}

func (a *platformGatewayServiceAdapter) Restore(ctx context.Context, effect gatewayServiceEffect) gatewayServiceEffectResult {
	if err := validateGatewayLaunchPlan(effect.Plan); err != nil || !gatewayLaunchDescriptorsEqual(effect.Descriptor, effect.Plan.Anchor.Descriptor) {
		return gatewayServiceEffectResult{Outcome: gatewayServiceEffectRejected, Err: fmt.Errorf("Gateway recovery effect does not match its frozen anchor")}
	}
	if err := verifyGatewayExecutable(effect.Plan.Anchor.Descriptor); err != nil {
		return gatewayServiceEffectResult{Outcome: gatewayServiceEffectRejected, Err: fmt.Errorf("Gateway recovery executable changed: %w", err)}
	}
	return a.replaceAndRestart(ctx, effect)
}

func (a *platformGatewayServiceAdapter) replaceAndRestart(ctx context.Context, effect gatewayServiceEffect) gatewayServiceEffectResult {
	if effect.AttemptID == "" || effect.ConnectionID == "" || effect.Generation == "" || effect.Descriptor.ConnectionID != effect.ConnectionID {
		return gatewayServiceEffectResult{Outcome: gatewayServiceEffectFailed, Err: fmt.Errorf("incomplete Gateway service effect")}
	}
	if err := validateGatewayLaunchDescriptor(effect.Descriptor); err != nil {
		return gatewayServiceEffectResult{Outcome: gatewayServiceEffectFailed, Err: err}
	}
	if effect.Descriptor.Manager != a.manager {
		return gatewayServiceEffectResult{Outcome: gatewayServiceEffectFailed, Err: fmt.Errorf("Gateway service adapter mismatch")}
	}
	content, err := renderGatewayServiceUnitForAttempt(effect.Descriptor, effect.AttemptID, effect.Generation)
	if err != nil {
		return gatewayServiceEffectResult{Outcome: gatewayServiceEffectFailed, Err: err}
	}
	committed, err := replaceGatewayServiceUnit(effect.Descriptor.UnitPath, content)
	if err != nil {
		outcome := gatewayServiceEffectFailed
		if committed {
			outcome = gatewayServiceEffectIndeterminate
		}
		return gatewayServiceEffectResult{Outcome: outcome, Err: err}
	}
	if err := restartGatewayServiceUnit(ctx, effect.Descriptor); err != nil {
		return gatewayServiceEffectResult{Outcome: gatewayServiceEffectIndeterminate, Err: err}
	}
	return gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}
}

func (a *platformGatewayServiceAdapter) Inspect(_ context.Context, request gatewayServiceInspectionRequest) (gatewayServiceInspection, error) {
	if request.AttemptID == "" || request.ConnectionID == "" || request.Plan.ConnectionID != request.ConnectionID {
		return gatewayServiceInspection{}, fmt.Errorf("incomplete Gateway service inspection")
	}
	if err := validateGatewayLaunchPlan(request.Plan); err != nil {
		return gatewayServiceInspection{}, err
	}
	data, err := readGatewayServiceUnit(request.Plan.Target.UnitPath)
	if os.IsNotExist(err) {
		return gatewayServiceInspection{State: gatewayServiceObservedAbsent}, nil
	}
	if err != nil {
		return gatewayServiceInspection{}, err
	}
	target, err := renderGatewayServiceUnitForAttempt(request.Plan.Target, request.AttemptID, request.TargetGeneration)
	if err != nil {
		return gatewayServiceInspection{}, err
	}
	recovery, err := renderGatewayServiceUnitForAttempt(request.Plan.Anchor.Descriptor, request.AttemptID, request.RecoveryGeneration)
	if err != nil {
		return gatewayServiceInspection{}, err
	}
	anchor, err := renderGatewayServiceUnitForAttempt(request.Plan.Anchor.Descriptor, request.Plan.Anchor.AttemptID, request.Plan.Anchor.Generation)
	if err != nil {
		return gatewayServiceInspection{}, err
	}
	switch {
	case bytes.Equal(data, target):
		return gatewayServiceInspection{State: gatewayServiceObservedTarget, Generation: request.TargetGeneration, Build: request.Plan.Target.Build, ExecutableDigest: request.Plan.Target.ExecutableDigest}, nil
	case bytes.Equal(data, recovery):
		return gatewayServiceInspection{State: gatewayServiceObservedRecovery, Generation: request.RecoveryGeneration, Build: request.Plan.Anchor.Descriptor.Build, ExecutableDigest: request.Plan.Anchor.Descriptor.ExecutableDigest}, nil
	case bytes.Equal(data, anchor):
		return gatewayServiceInspection{State: gatewayServiceObservedAnchor, Build: request.Plan.Anchor.Descriptor.Build, ExecutableDigest: request.Plan.Anchor.Descriptor.ExecutableDigest}, nil
	default:
		return gatewayServiceInspection{State: gatewayServiceObservedUnknown}, nil
	}
}

func invokeGatewayServiceEffect(fn func() gatewayServiceEffectResult) (result gatewayServiceEffectResult) {
	defer func() {
		if recovered := recover(); recovered != nil {
			detail := gatewaySafeEffectError(fmt.Errorf("Gateway service adapter panicked: %v", recovered))
			result = gatewayServiceEffectResult{Outcome: gatewayServiceEffectIndeterminate, Err: errors.New(detail)}
		}
	}()
	result = fn()
	switch result.Outcome {
	case gatewayServiceEffectApplied:
		return result
	case gatewayServiceEffectRejected, gatewayServiceEffectFailed, gatewayServiceEffectIndeterminate:
		if result.Err == nil {
			result.Err = fmt.Errorf("Gateway service adapter returned %s without detail", result.Outcome)
		}
		result.Err = errors.New(gatewaySafeEffectError(result.Err))
		return result
	default:
		return gatewayServiceEffectResult{Outcome: gatewayServiceEffectIndeterminate, Err: fmt.Errorf("Gateway service adapter returned unknown outcome %q", result.Outcome)}
	}
}

func renderGatewayServiceUnit(descriptor gatewayLaunchDescriptor, generation string) ([]byte, error) {
	return renderGatewayServiceUnitForAttempt(descriptor, "", generation)
}

func renderGatewayServiceUnitForAttempt(descriptor gatewayLaunchDescriptor, attemptID, generation string) ([]byte, error) {
	if err := validateGatewayLaunchDescriptor(descriptor); err != nil {
		return nil, err
	}
	if descriptor.Provider != "" {
		return renderLarkGatewayServiceUnit(descriptor, attemptID, generation)
	}
	args := []string{
		descriptor.Executable,
		"--hub", descriptor.HubURL,
		"--connection-id", descriptor.ConnectionID,
		"--data-dir", descriptor.DataDir,
		"--generation", generation,
		"--build", descriptor.Build,
		"--executable-digest", descriptor.ExecutableDigest,
	}
	switch descriptor.Manager {
	case gatewayServiceManagerLaunchd:
		var builder strings.Builder
		builder.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict>\n")
		writeLaunchdString(&builder, "Label", descriptor.ServiceID)
		builder.WriteString("<key>ProgramArguments</key><array>")
		for _, argument := range args {
			builder.WriteString("<string>")
			gatewayWriteXMLText(&builder, argument)
			builder.WriteString("</string>")
		}
		builder.WriteString("</array>\n")
		writeLaunchdString(&builder, "WorkingDirectory", descriptor.WorkingDirectory)
		writeLaunchdString(&builder, "StandardOutPath", descriptor.LogPath)
		writeLaunchdString(&builder, "StandardErrorPath", descriptor.LogPath)
		builder.WriteString("<key>RunAtLoad</key><true/>\n<key>KeepAlive</key><false/>\n</dict></plist>\n")
		return []byte(builder.String()), nil
	case gatewayServiceManagerSystemd:
		quoted := make([]string, len(args))
		for index, argument := range args {
			quoted[index] = systemdGatewayQuote(argument)
		}
		content := "[Unit]\nDescription=CodexLoom Gateway " + descriptor.ConnectionID + "\n\n[Service]\nType=simple\nExecStart=" + strings.Join(quoted, " ") +
			"\nWorkingDirectory=" + systemdGatewayQuote(descriptor.WorkingDirectory) + "\nStandardOutput=" + systemdGatewayQuote("append:"+descriptor.LogPath) +
			"\nStandardError=" + systemdGatewayQuote("append:"+descriptor.LogPath) + "\nRestart=no\n\n[Install]\nWantedBy=default.target\n"
		return []byte(content), nil
	case gatewayServiceManagerFake:
		return jsonCanonicalGatewayUnit(descriptor, attemptID, generation)
	default:
		return nil, fmt.Errorf("unsupported Gateway service manager %q", descriptor.Manager)
	}
}

func renderLarkGatewayServiceUnit(descriptor gatewayLaunchDescriptor, attemptID, generation string) ([]byte, error) {
	if (attemptID == "") != (generation == "") || len(attemptID) > gatewayProcessStringMax || len(generation) > gatewayProcessStringMax ||
		strings.ContainsAny(attemptID, "\r\n\x00") || strings.ContainsAny(generation, "\r\n\x00") ||
		gatewayStringMayContainSecret(attemptID) || gatewayStringMayContainSecret(generation) {
		return nil, fmt.Errorf("Gateway process identity is incomplete, unbounded, or sensitive")
	}
	args := []string{
		descriptor.Executable,
		"--hub", descriptor.HubURL,
		"--connection", descriptor.ConnectionID,
		"--address", descriptor.AddressID,
		"--app-id", descriptor.AccountRef,
	}
	environment := [][2]string{{"CODEX_LOOM_DATA", descriptor.DataDir}}
	if descriptor.ManagedCredentialRef != "" {
		environment = append(environment, [2]string{"CODEX_LOOM_MANAGED_CREDENTIAL_REF", descriptor.ManagedCredentialRef})
	}
	if attemptID != "" {
		environment = append(environment,
			[2]string{"CODEX_LOOM_GATEWAY_ATTEMPT_ID", attemptID},
			[2]string{"CODEX_LOOM_GATEWAY_GENERATION", generation},
			[2]string{"CODEX_LOOM_GATEWAY_BUILD", descriptor.Build},
			[2]string{"CODEX_LOOM_GATEWAY_EXECUTABLE_DIGEST", descriptor.ExecutableDigest},
		)
	}
	switch descriptor.Manager {
	case gatewayServiceManagerLaunchd:
		var argsXML strings.Builder
		for _, argument := range args {
			argsXML.WriteString("      <string>" + html.EscapeString(argument) + "</string>\n")
		}
		var environmentXML strings.Builder
		for _, entry := range environment {
			environmentXML.WriteString("<key>" + html.EscapeString(entry[0]) + "</key><string>" + html.EscapeString(entry[1]) + "</string>")
		}
		content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key><string>%s</string>
    <key>ProgramArguments</key>
    <array>
%s    </array>
    <key>EnvironmentVariables</key>
    <dict>%s</dict>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
    <key>ProcessType</key><string>Background</string>
    <key>StandardOutPath</key><string>%s</string>
    <key>StandardErrorPath</key><string>%s</string>
  </dict>
</plist>
`, html.EscapeString(descriptor.ServiceID), argsXML.String(), environmentXML.String(), html.EscapeString(descriptor.LogPath), html.EscapeString(descriptor.LogPath))
		return []byte(content), nil
	case gatewayServiceManagerSystemd:
		quoted := make([]string, 0, len(args))
		for _, argument := range args {
			quoted = append(quoted, feishuSystemdQuote(argument))
		}
		var environmentLines strings.Builder
		for _, entry := range environment {
			environmentLines.WriteString("Environment=" + entry[0] + "=" + feishuSystemdQuote(entry[1]) + "\n")
		}
		content := fmt.Sprintf(`[Unit]
Description=CodexLoom native Feishu gateway (%s)
After=network-online.target

[Service]
Type=simple
ExecStart=%s
%sRestart=always
RestartSec=2
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, descriptor.ConnectionID, strings.Join(quoted, " "), environmentLines.String(), descriptor.LogPath, descriptor.LogPath)
		return []byte(content), nil
	case gatewayServiceManagerFake:
		return jsonCanonicalGatewayUnit(descriptor, attemptID, generation)
	default:
		return nil, fmt.Errorf("unsupported Gateway service manager %q", descriptor.Manager)
	}
}

func feishuSystemdQuote(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func jsonCanonicalGatewayUnit(descriptor gatewayLaunchDescriptor, attemptID, generation string) ([]byte, error) {
	type fakeUnit struct {
		Descriptor gatewayLaunchDescriptor `json:"descriptor"`
		AttemptID  string                  `json:"attemptId,omitempty"`
		Generation string                  `json:"generation"`
	}
	data, err := jsonMarshalNoEscape(fakeUnit{Descriptor: descriptor, AttemptID: attemptID, Generation: generation})
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func jsonMarshalNoEscape(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'}), nil
}

func writeLaunchdString(builder *strings.Builder, key, value string) {
	builder.WriteString("<key>")
	gatewayWriteXMLText(builder, key)
	builder.WriteString("</key><string>")
	gatewayWriteXMLText(builder, value)
	builder.WriteString("</string>\n")
}

func gatewayWriteXMLText(builder *strings.Builder, value string) {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;")
	builder.WriteString(replacer.Replace(value))
}

func systemdGatewayQuote(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	value = strings.ReplaceAll(value, "$", "$$")
	value = strings.ReplaceAll(value, "%", "%%")
	return "\"" + value + "\""
}

func replaceGatewayServiceUnit(path string, content []byte) (committed bool, err error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false, fmt.Errorf("Gateway service unit path is not absolute and canonical")
	}
	directory := filepath.Dir(path)
	if info, inspectErr := os.Lstat(path); inspectErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return false, fmt.Errorf("Gateway service unit is not a regular file")
		}
	} else if !os.IsNotExist(inspectErr) {
		return false, inspectErr
	}
	temporary, err := os.CreateTemp(directory, ".codexloom-gateway-unit-*")
	if err != nil {
		return false, err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return false, err
	}
	if _, err := temporary.Write(content); err != nil {
		return false, err
	}
	if err := temporary.Sync(); err != nil {
		return false, err
	}
	if err := temporary.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		// A failed replacement syscall is conservatively indeterminate: some
		// filesystems can expose the new name even when durability/reporting
		// fails. The coordinator must inspect instead of assuming no effect.
		return true, err
	}
	committed = true
	dir, err := os.Open(directory)
	if err != nil {
		return true, err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return true, err
	}
	return true, nil
}

func readGatewayServiceUnit(path string) ([]byte, error) {
	const maximum = 1 << 20
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 0 || info.Size() > maximum {
		return nil, fmt.Errorf("Gateway service unit is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("Gateway service unit changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || len(data) > maximum || int64(len(data)) != opened.Size() {
		return nil, fmt.Errorf("Gateway service unit changed or exceeded its bound")
	}
	after, err := file.Stat()
	current, pathErr := os.Stat(path)
	currentLstat, lstatErr := os.Lstat(path)
	if err != nil || pathErr != nil || !os.SameFile(opened, after) || !os.SameFile(opened, current) || after.Size() != opened.Size() {
		return nil, fmt.Errorf("Gateway service unit identity changed during validation")
	}
	if lstatErr != nil || !currentLstat.Mode().IsRegular() || currentLstat.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, currentLstat) {
		return nil, fmt.Errorf("Gateway service unit path changed during validation")
	}
	return data, nil
}

func restartGatewayServiceUnit(ctx context.Context, descriptor gatewayLaunchDescriptor) error {
	command := func(name string, arguments ...string) error {
		var output gatewayCommandOutput
		cmd := exec.CommandContext(ctx, name, arguments...)
		cmd.Stdout = &output
		cmd.Stderr = &output
		err := cmd.Run()
		if err != nil {
			message := output.String()
			if message == "" {
				message = err.Error()
			}
			return fmt.Errorf("%s: %s", name, message)
		}
		return nil
	}
	switch descriptor.Manager {
	case gatewayServiceManagerLaunchd:
		if goruntime.GOOS != "darwin" {
			return fmt.Errorf("launchd Gateway adapter is unsupported on %s", goruntime.GOOS)
		}
		current, err := user.Current()
		if err != nil || current.Uid == "" {
			return fmt.Errorf("resolve launchd user: %w", err)
		}
		domain := "gui/" + current.Uid
		target := domain + "/" + descriptor.ServiceID
		_ = command("launchctl", "bootout", target)
		if err := command("launchctl", "bootstrap", domain, descriptor.UnitPath); err != nil {
			return err
		}
		return command("launchctl", "kickstart", "-k", target)
	case gatewayServiceManagerSystemd:
		if goruntime.GOOS != "linux" {
			return fmt.Errorf("systemd Gateway adapter is unsupported on %s", goruntime.GOOS)
		}
		if err := command("systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		return command("systemctl", "--user", "restart", descriptor.ServiceID)
	default:
		return fmt.Errorf("Gateway service manager %q has no production adapter", descriptor.Manager)
	}
}

var _ gatewayServiceAdapter = (*platformGatewayServiceAdapter)(nil)
