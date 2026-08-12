package hub

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"time"
)

// larkGatewayHubURL is the standard CodexLoom Hub URL used when freezing a
// maintenance launch plan. The Hub startup bridge serves the same URL after
// restart; the plan does not carry a secret.
const larkGatewayHubURL = "http://127.0.0.1:4870"

// ConfigureLarkGatewayLaunch is the production maintenance entry used by the
// existing lark-migrate CLI. After a managed credential reference is committed,
// it freezes a durable, binding-frozen typed launch plan for one Lark
// Connection with zero service effect. The Hub startup bridge
// (RestartManagedGateways) later consumes the plan through the shared R1
// coordinator. It never opens a provider socket and never reads a secret.
func (h *Hub) ConfigureLarkGatewayLaunch(connectionID, executable string) error {
	launchSpec, err := h.larkGatewayLaunchSpec(connectionID, executable)
	if err != nil {
		return err
	}
	if h.larkGatewayLaunchCutoverReady(connectionID) {
		return nil
	}
	if _, err := h.configureLarkGatewayLaunch(launchSpec); err != nil {
		return err
	}
	return nil
}

// larkGatewayLaunchCutoverReady reports whether the durable typed plan is
// already frozen with the current managed reference and a fresh accepted
// target proof exists. Preflight and the maintenance configure use it to no-op
// after a successful cutover instead of re-checking the legacy anchor or
// re-committing a plan.
func (h *Hub) larkGatewayLaunchCutoverReady(connectionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	connection := h.connections[connectionID]
	plan := h.gatewayState.LaunchPlans[connectionID]
	if connection == nil || plan == nil || plan.Target.Provider == "" ||
		plan.Target.ManagedCredentialRef != strings.TrimSpace(connection.CredentialRef) || plan.Target.Domain != strings.TrimSpace(connection.Domain) {
		return false
	}
	attempt := h.gatewayState.Attempts[connectionID]
	if attempt == nil || attempt.AcceptedProof == nil || attempt.Phase != gatewayAttemptSucceeded {
		return false
	}
	proof := attempt.AcceptedProof
	if proof.Generation != attempt.TargetGeneration || proof.Build != attempt.Plan.Target.Build ||
		proof.ExecutableDigest != attempt.Plan.Target.ExecutableDigest {
		return false
	}
	observedAt, err := time.Parse(time.RFC3339Nano, proof.ObservedAt)
	return err == nil && time.Since(observedAt) <= gatewayProcessProofFreshness
}

func (h *Hub) larkGatewayLaunchSpec(connectionID, executable string) (larkGatewayLaunchSpec, error) {
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" {
		return larkGatewayLaunchSpec{}, errf(400, "Gateway launch Connection is required")
	}
	manager, err := larkGatewayServiceManager()
	if err != nil {
		return larkGatewayLaunchSpec{}, err
	}
	executable, err = larkGatewayCanonicalExecutable(executable)
	if err != nil {
		return larkGatewayLaunchSpec{}, err
	}
	digest, err := verifyGatewayExecutablePath(executable)
	if err != nil {
		return larkGatewayLaunchSpec{}, err
	}
	addressID, err := h.activeLarkAddressID(connectionID)
	if err != nil {
		return larkGatewayLaunchSpec{}, err
	}
	h.mu.Lock()
	connection := h.connections[connectionID]
	h.mu.Unlock()
	if connection == nil {
		return larkGatewayLaunchSpec{}, errf(404, "connection not found: %s", connectionID)
	}
	accountRef := strings.TrimSpace(connection.AccountRef)
	if accountRef == "" {
		return larkGatewayLaunchSpec{}, errf(409, "Connection has no canonical Lark App ID")
	}
	spec := larkGatewayExecutableSpec{
		Executable: executable, Build: "sha256:" + digest[:16], ExecutableDigest: digest,
	}
	serviceID, unitPath, err := larkGatewayServiceIdentity(manager, connectionID)
	if err != nil {
		return larkGatewayLaunchSpec{}, err
	}
	launchSpec := larkGatewayLaunchSpec{
		ConnectionID: connectionID, AddressID: addressID, Manager: manager,
		ServiceID: serviceID, UnitPath: unitPath, WorkingDirectory: filepath.Dir(executable),
		HubURL: larkGatewayHubURL, DataDir: h.st.Dir(),
		LogPath: filepath.Join(h.st.Dir(), "gateway", "feishu-"+connectionID+".log"),
		Target:  spec, Anchor: spec,
	}
	return launchSpec, nil
}

// PreflightLarkGatewayLaunch is the read-only companion used by lark-migrate
// preflight/dry-run. It proves the launch plan prerequisites without writing
// any state: supported service manager, canonical executable with a computable
// digest, exactly one active launch Address, and a legacy registration anchor
// whose installed unit matches the typed Runtime render.
func (h *Hub) PreflightLarkGatewayLaunch(connectionID, executable string) error {
	launchSpec, err := h.larkGatewayLaunchSpec(connectionID, executable)
	if err != nil {
		return err
	}
	if h.larkGatewayLaunchCutoverReady(connectionID) {
		return nil
	}
	h.mu.Lock()
	connection := h.connections[launchSpec.ConnectionID]
	h.mu.Unlock()
	if connection == nil {
		return errf(404, "connection not found: %s", launchSpec.ConnectionID)
	}
	anchorDescriptor := gatewayLaunchDescriptor{
		Manager: launchSpec.Manager, ConnectionID: launchSpec.ConnectionID,
		Provider:  strings.ToLower(strings.TrimSpace(connection.Provider)),
		AddressID: launchSpec.AddressID, AccountRef: strings.TrimSpace(connection.AccountRef), Domain: strings.TrimSpace(connection.Domain),
		ServiceID: launchSpec.ServiceID, UnitPath: launchSpec.UnitPath,
		Executable: launchSpec.Anchor.Executable, WorkingDirectory: launchSpec.WorkingDirectory,
		HubURL: launchSpec.HubURL, DataDir: launchSpec.DataDir, LogPath: launchSpec.LogPath,
		Build: launchSpec.Anchor.Build, ExecutableDigest: launchSpec.Anchor.ExecutableDigest,
	}
	want, err := renderGatewayServiceUnitForAttempt(anchorDescriptor, "", "")
	if err != nil {
		return err
	}
	got, err := readGatewayServiceUnit(launchSpec.UnitPath)
	if err != nil {
		return fmt.Errorf("Gateway registration anchor is not configurable: %w", err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("Gateway registration anchor does not match the installed legacy unit")
	}
	return nil
}

// LarkGatewayLaunchPlanRef reports the managed credential reference frozen in
// the durable typed launch plan for one Connection, or "" when no typed plan
// exists. It is a read-only maintenance diagnostic and never exposes a secret.
func (h *Hub) LarkGatewayLaunchPlanRef(connectionID string) string {
	connectionID = strings.TrimSpace(connectionID)
	h.mu.Lock()
	defer h.mu.Unlock()
	plan := h.gatewayState.LaunchPlans[connectionID]
	if plan == nil || plan.Target.Provider == "" {
		return ""
	}
	return plan.Target.ManagedCredentialRef
}

// RevokeLarkGatewayLaunch removes a durable typed Lark launch plan (and any
// attempt record) for one Connection so the next Hub startup returns to the
// legacy Keychain path. It performs no service effect. An active process
// attempt is an indeterminate state and fails closed for manual reconcile.
func (h *Hub) RevokeLarkGatewayLaunch(connectionID string) error {
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" {
		return errf(400, "Gateway launch Connection is required")
	}
	unlock := h.gatewayCoordinatorForUse().lock(connectionID)
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return err
	}
	control := h.gatewayState.Controls[connectionID]
	if control == nil {
		return nil
	}
	if control.ActiveAttemptID != "" {
		return errf(409, "Gateway has an active process attempt; manual reconcile required")
	}
	plan := h.gatewayState.LaunchPlans[connectionID]
	if plan == nil || plan.Target.Provider == "" {
		return nil
	}
	control = h.gatewayState.Controls[connectionID]
	if control != nil && control.Recovery != gatewayRecoveryNone && control.Recovery != gatewayRecoveryReconcile {
		return errf(409, "Gateway recovery is %q; manual reconcile required", control.Recovery)
	}
	next := cloneGatewayState(h.gatewayState)
	delete(next.LaunchPlans, connectionID)
	delete(next.Attempts, connectionID)
	if nextControl := next.Controls[connectionID]; nextControl != nil {
		nextControl.Recovery = gatewayRecoveryNone
		nextControl.Reason = ""
		nextControl.ActiveAttemptID = ""
		nextControl.UpdatedAt = now()
	}
	if err := h.saveGatewayStateLocked(next); err != nil {
		return fmt.Errorf("persist Gateway launch plan revocation: %w", err)
	}
	return nil
}

func larkGatewayServiceManager() (gatewayServiceManager, error) {
	switch goruntime.GOOS {
	case "darwin":
		return gatewayServiceManagerLaunchd, nil
	case "linux":
		return gatewayServiceManagerSystemd, nil
	default:
		return "", fmt.Errorf("typed Lark Gateway launch is unsupported on %s", goruntime.GOOS)
	}
}

func larkGatewayCanonicalExecutable(executable string) (string, error) {
	executable = strings.TrimSpace(executable)
	if executable == "" || !filepath.IsAbs(executable) || filepath.Clean(executable) != executable {
		return "", fmt.Errorf("Gateway executable must be an absolute canonical path")
	}
	info, err := os.Stat(executable)
	if err != nil {
		return "", fmt.Errorf("Gateway executable is unavailable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("Gateway executable is not a regular executable file")
	}
	return executable, nil
}

func larkGatewayServiceIdentity(manager gatewayServiceManager, connectionID string) (serviceID, unitPath string, err error) {
	safe := safeLarkServicePart(connectionID)
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("resolve user home directory: %w", err)
	}
	switch manager {
	case gatewayServiceManagerLaunchd:
		serviceID = "com.codexloom.feishu." + safe
		unitPath = filepath.Join(home, "Library", "LaunchAgents", serviceID+".plist")
	case gatewayServiceManagerSystemd:
		serviceID = "codexloom-feishu-" + safe + ".service"
		unitPath = filepath.Join(home, ".config", "systemd", "user", serviceID)
	default:
		return "", "", fmt.Errorf("unsupported Gateway service manager %q", manager)
	}
	return serviceID, unitPath, nil
}

func safeLarkServicePart(value string) string {
	var result strings.Builder
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			result.WriteRune(r)
		} else {
			result.WriteByte('-')
		}
	}
	return strings.Trim(result.String(), "-")
}

func (h *Hub) activeLarkAddressID(connectionID string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	connection := h.connections[connectionID]
	if connection == nil {
		return "", errf(404, "connection not found: %s", connectionID)
	}
	provider := strings.ToLower(strings.TrimSpace(connection.Provider))
	if provider != "lark" && provider != "feishu" {
		return "", errf(409, "Connection provider is not Lark/Feishu")
	}
	var matches []string
	for _, address := range h.addresses {
		if address == nil || address.ConnectionID != connectionID || !address.Enabled ||
			address.ArchivedAt != "" || address.DeletedAt != "" || address.SupersededBy != "" {
			continue
		}
		matches = append(matches, address.ID)
	}
	sort.Strings(matches)
	if len(matches) != 1 {
		return "", errf(409, "Connection must have exactly one active launch Address")
	}
	return matches[0], nil
}
