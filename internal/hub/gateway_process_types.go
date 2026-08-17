package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/credentials"
)

const (
	gatewayProcessStateVersion       = 2
	gatewayLaunchProofStateVersion   = 3
	gatewayProcessStringMax          = 4096
	gatewayProcessPlanMaxBytes       = 32 << 10
	gatewayProcessExecutableMaxBytes = 512 << 20
	gatewayProcessProofFreshness     = 2 * time.Minute
	gatewayProcessProofWait          = 30 * time.Second
	gatewayProcessEffectTimeout      = 30 * time.Second
)

type gatewayServiceManager string

const (
	gatewayServiceManagerLaunchd gatewayServiceManager = "launchd"
	gatewayServiceManagerSystemd gatewayServiceManager = "systemd"
	gatewayServiceManagerFake    gatewayServiceManager = "fake"
)

type gatewayAttemptKind string

const (
	gatewayAttemptAutomatic gatewayAttemptKind = "automatic_restart"
	gatewayAttemptManual    gatewayAttemptKind = "manual_repair"
	gatewayAttemptMigration gatewayAttemptKind = "migration_activation"
	gatewayAttemptRollback  gatewayAttemptKind = "rollback"
)

type gatewayAttemptPhase string

type gatewayEffectStage string

const (
	gatewayEffectTarget   gatewayEffectStage = "target"
	gatewayEffectRecovery gatewayEffectStage = "recovery"
)

const (
	gatewayAttemptTargetIntent           gatewayAttemptPhase = "target_intent"
	gatewayAttemptAwaitingTargetProof    gatewayAttemptPhase = "awaiting_target_proof"
	gatewayAttemptReconcileRequired      gatewayAttemptPhase = "reconcile_required"
	gatewayAttemptRecoveryIntent         gatewayAttemptPhase = "recovery_intent"
	gatewayAttemptAwaitingRecoveryProof  gatewayAttemptPhase = "awaiting_recovery_proof"
	gatewayAttemptSucceeded              gatewayAttemptPhase = "succeeded"
	gatewayAttemptRecovered              gatewayAttemptPhase = "recovered"
	gatewayAttemptManualRecoveryRequired gatewayAttemptPhase = "manual_recovery_required"
)

// gatewayLaunchDescriptor is deliberately closed and secret-free. The L2a
// fields are the minimum typed Feishu launch identity; arbitrary argv,
// environment, registration payloads, and provider secrets remain forbidden.
type gatewayLaunchDescriptor struct {
	Manager              gatewayServiceManager `json:"manager"`
	ConnectionID         string                `json:"connectionId"`
	Provider             string                `json:"provider,omitempty"`
	AddressID            string                `json:"addressId,omitempty"`
	AccountRef           string                `json:"accountRef,omitempty"`
	ManagedCredentialRef string                `json:"managedCredentialRef,omitempty"`
	ServiceID            string                `json:"serviceId"`
	UnitPath             string                `json:"unitPath"`
	Executable           string                `json:"executable"`
	WorkingDirectory     string                `json:"workingDirectory"`
	HubURL               string                `json:"hubUrl"`
	DataDir              string                `json:"dataDir"`
	LogPath              string                `json:"logPath"`
	Build                string                `json:"build"`
	ExecutableDigest     string                `json:"executableDigest"`
}

type gatewayRegistrationAnchor struct {
	Descriptor      gatewayLaunchDescriptor `json:"descriptor"`
	AttemptID       string                  `json:"attemptId,omitempty"`
	Generation      string                  `json:"generation,omitempty"`
	IntegritySHA256 string                  `json:"integritySha256"`
}

type gatewayLaunchPlan struct {
	ConnectionID    string                    `json:"connectionId"`
	Target          gatewayLaunchDescriptor   `json:"target"`
	Anchor          gatewayRegistrationAnchor `json:"anchor"`
	IntegritySHA256 string                    `json:"integritySha256,omitempty"`
}

type gatewayProcessProof struct {
	AttemptID        string `json:"attemptId"`
	Generation       string `json:"generation"`
	Build            string `json:"build"`
	ExecutableDigest string `json:"executableDigest"`
	ObservedAt       string `json:"observedAt"`
}

type gatewayTransitionAttempt struct {
	ID                 string                   `json:"id"`
	ConnectionID       string                   `json:"connectionId"`
	Kind               gatewayAttemptKind       `json:"kind"`
	Phase              gatewayAttemptPhase      `json:"phase"`
	BindingEpoch       uint64                   `json:"bindingEpoch"`
	Binding            gatewayConfiguredBinding `json:"binding"`
	Plan               gatewayLaunchPlan        `json:"plan"`
	TargetGeneration   string                   `json:"targetGeneration"`
	RecoveryGeneration string                   `json:"recoveryGeneration"`
	EffectStartedAt    string                   `json:"effectStartedAt"`
	RecoveryStartedAt  string                   `json:"recoveryStartedAt,omitempty"`
	ProofDeadline      string                   `json:"proofDeadline,omitempty"`
	UpdatedAt          string                   `json:"updatedAt"`
	CompletedAt        string                   `json:"completedAt,omitempty"`
	LastError          string                   `json:"lastError,omitempty"`
	ReconcileEffect    gatewayEffectStage       `json:"reconcileEffect,omitempty"`
	AcceptedProof      *gatewayProcessProof     `json:"acceptedProof,omitempty"`
}

func validGatewayServiceManager(value gatewayServiceManager) bool {
	return value == gatewayServiceManagerLaunchd || value == gatewayServiceManagerSystemd || value == gatewayServiceManagerFake
}

func validGatewayAttemptKind(value gatewayAttemptKind) bool {
	switch value {
	case gatewayAttemptAutomatic, gatewayAttemptManual, gatewayAttemptMigration, gatewayAttemptRollback:
		return true
	default:
		return false
	}
}

func validGatewayAttemptPhase(value gatewayAttemptPhase) bool {
	switch value {
	case gatewayAttemptTargetIntent, gatewayAttemptAwaitingTargetProof, gatewayAttemptReconcileRequired,
		gatewayAttemptRecoveryIntent, gatewayAttemptAwaitingRecoveryProof, gatewayAttemptSucceeded,
		gatewayAttemptRecovered, gatewayAttemptManualRecoveryRequired:
		return true
	default:
		return false
	}
}

func gatewayAttemptTerminal(value gatewayAttemptPhase) bool {
	return value == gatewayAttemptSucceeded || value == gatewayAttemptRecovered || value == gatewayAttemptManualRecoveryRequired
}

func newGatewayRegistrationAnchor(descriptor gatewayLaunchDescriptor) (gatewayRegistrationAnchor, error) {
	if err := validateGatewayLaunchDescriptor(descriptor); err != nil {
		return gatewayRegistrationAnchor{}, err
	}
	return gatewayRegistrationAnchor{Descriptor: descriptor, IntegritySHA256: gatewayAnchorIntegrity(descriptor)}, nil
}

func gatewayAnchorIntegrity(descriptor gatewayLaunchDescriptor) string {
	return gatewayAnchorIntegrityWithProcess(descriptor, "", "")
}

func gatewayAnchorIntegrityWithGeneration(descriptor gatewayLaunchDescriptor, generation string) string {
	return gatewayAnchorIntegrityWithProcess(descriptor, "", generation)
}

func gatewayAnchorIntegrityWithProcess(descriptor gatewayLaunchDescriptor, attemptID, generation string) string {
	payload := struct {
		Descriptor gatewayLaunchDescriptor `json:"descriptor"`
		AttemptID  string                  `json:"attemptId,omitempty"`
		Generation string                  `json:"generation,omitempty"`
	}{Descriptor: descriptor, AttemptID: attemptID, Generation: generation}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func validateGatewayLaunchPlan(plan gatewayLaunchPlan) error {
	if strings.TrimSpace(plan.ConnectionID) == "" || plan.ConnectionID != strings.TrimSpace(plan.ConnectionID) {
		return fmt.Errorf("Gateway launch plan Connection is required")
	}
	if err := validateGatewayLaunchDescriptor(plan.Target); err != nil {
		return fmt.Errorf("invalid target launch descriptor: %w", err)
	}
	if err := validateGatewayRegistrationAnchor(plan.Anchor); err != nil {
		return fmt.Errorf("invalid registration anchor: %w", err)
	}
	anchor := plan.Anchor.Descriptor
	if plan.Target.ConnectionID != plan.ConnectionID || anchor.ConnectionID != plan.ConnectionID ||
		plan.Target.Manager != anchor.Manager || plan.Target.ServiceID != anchor.ServiceID ||
		plan.Target.UnitPath != anchor.UnitPath || plan.Target.HubURL != anchor.HubURL ||
		plan.Target.DataDir != anchor.DataDir || plan.Target.LogPath != anchor.LogPath ||
		plan.Target.Provider != anchor.Provider || plan.Target.AddressID != anchor.AddressID ||
		plan.Target.AccountRef != anchor.AccountRef {
		return fmt.Errorf("Gateway target and registration anchor identify different services")
	}
	if plan.Target.Provider != "" {
		if !credentials.IsManagedRef(plan.Target.ManagedCredentialRef) {
			return fmt.Errorf("Lark Gateway target has no canonical managed credential reference")
		}
		if plan.IntegritySHA256 == "" || plan.IntegritySHA256 != gatewayLaunchPlanIntegrity(plan) {
			return fmt.Errorf("Gateway launch plan integrity does not match")
		}
	} else if plan.IntegritySHA256 != "" && plan.IntegritySHA256 != gatewayLaunchPlanIntegrity(plan) {
		return fmt.Errorf("Gateway launch plan integrity does not match")
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	if len(data) > gatewayProcessPlanMaxBytes {
		return fmt.Errorf("Gateway launch plan exceeds %d bytes", gatewayProcessPlanMaxBytes)
	}
	return nil
}

func validateGatewayRegistrationAnchor(anchor gatewayRegistrationAnchor) error {
	if err := validateGatewayLaunchDescriptor(anchor.Descriptor); err != nil {
		return err
	}
	if len(anchor.AttemptID) > gatewayProcessStringMax || strings.ContainsAny(anchor.AttemptID, "\r\n\x00") || gatewayStringMayContainSecret(anchor.AttemptID) ||
		len(anchor.Generation) > gatewayProcessStringMax || strings.ContainsAny(anchor.Generation, "\r\n\x00") || gatewayStringMayContainSecret(anchor.Generation) ||
		(anchor.Descriptor.Provider != "" && (anchor.AttemptID == "") != (anchor.Generation == "")) ||
		anchor.IntegritySHA256 == "" || anchor.IntegritySHA256 != gatewayAnchorIntegrityWithProcess(anchor.Descriptor, anchor.AttemptID, anchor.Generation) {
		return fmt.Errorf("registration anchor integrity does not match")
	}
	return nil
}

func validateGatewayLaunchDescriptor(value gatewayLaunchDescriptor) error {
	if !validGatewayServiceManager(value.Manager) {
		return fmt.Errorf("unsupported service manager %q", value.Manager)
	}
	fields := map[string]string{
		"connection ID": value.ConnectionID, "service ID": value.ServiceID, "unit path": value.UnitPath,
		"executable": value.Executable, "working directory": value.WorkingDirectory, "Hub URL": value.HubURL,
		"data directory": value.DataDir, "log path": value.LogPath, "build": value.Build,
	}
	for name, field := range fields {
		if field == "" || field != strings.TrimSpace(field) || len(field) > gatewayProcessStringMax || strings.ContainsAny(field, "\r\n\x00") {
			return fmt.Errorf("%s is missing, non-canonical, or unbounded", name)
		}
	}
	if !gatewayServiceIdentifierCanonical(value.ConnectionID) || !gatewayServiceIdentifierCanonical(value.ServiceID) {
		return fmt.Errorf("Gateway Connection/service identifier is not canonical")
	}
	hasTypedProvider := value.Provider != "" || value.AddressID != "" || value.AccountRef != "" || value.ManagedCredentialRef != ""
	if hasTypedProvider {
		provider := strings.ToLower(strings.TrimSpace(value.Provider))
		if (provider != "lark" && provider != "feishu") || value.Provider != provider ||
			!gatewayServiceIdentifierCanonical(value.AddressID) || value.AccountRef == "" || value.AccountRef != strings.TrimSpace(value.AccountRef) ||
			len(value.AccountRef) > gatewayProcessStringMax || strings.ContainsAny(value.AccountRef, "\r\n\x00") || gatewayStringMayContainSecret(value.AccountRef) {
			return fmt.Errorf("Gateway provider launch identity is not canonical")
		}
		if value.ManagedCredentialRef != "" && (value.ManagedCredentialRef != strings.TrimSpace(value.ManagedCredentialRef) || !credentials.IsManagedRef(value.ManagedCredentialRef)) {
			return fmt.Errorf("managed credential reference is not canonical")
		}
	}
	for _, path := range []string{value.UnitPath, value.Executable, value.WorkingDirectory, value.DataDir, value.LogPath} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("Gateway launch path is not absolute and canonical")
		}
	}
	if (value.Manager == gatewayServiceManagerLaunchd && filepath.Ext(value.UnitPath) != ".plist") ||
		(value.Manager == gatewayServiceManagerSystemd && filepath.Ext(value.UnitPath) != ".service") {
		return fmt.Errorf("Gateway service unit suffix does not match its manager")
	}
	parsed, err := url.Parse(value.HubURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("Hub URL must be a credential-free HTTP(S) origin")
	}
	if len(value.ExecutableDigest) != sha256.Size*2 || value.ExecutableDigest != strings.ToLower(value.ExecutableDigest) {
		return fmt.Errorf("executable digest must be lowercase SHA-256")
	}
	if _, err := hex.DecodeString(value.ExecutableDigest); err != nil {
		return fmt.Errorf("executable digest must be lowercase SHA-256")
	}
	for name, field := range fields {
		if gatewayStringMayContainSecret(field) {
			return fmt.Errorf("%s may contain secret material", name)
		}
	}
	return nil
}

func gatewayLaunchPlanIntegrity(plan gatewayLaunchPlan) string {
	payload := struct {
		ConnectionID string                    `json:"connectionId"`
		Target       gatewayLaunchDescriptor   `json:"target"`
		Anchor       gatewayRegistrationAnchor `json:"anchor"`
	}{ConnectionID: plan.ConnectionID, Target: plan.Target, Anchor: plan.Anchor}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func gatewayServiceIdentifierCanonical(value string) bool {
	if value == "" || len(value) > 255 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') ||
			char == '-' || char == '_' || char == '.' || char == ':' || char == '@' {
			continue
		}
		return false
	}
	return true
}

func verifyGatewayLaunchPlanExecutables(plan gatewayLaunchPlan) error {
	if err := verifyGatewayExecutable(plan.Target); err != nil {
		return fmt.Errorf("target executable integrity: %w", err)
	}
	if err := verifyGatewayExecutable(plan.Anchor.Descriptor); err != nil {
		return fmt.Errorf("registration anchor executable integrity: %w", err)
	}
	return nil
}

func verifyGatewayExecutable(descriptor gatewayLaunchDescriptor) error {
	digest, err := verifyGatewayExecutablePath(descriptor.Executable)
	if err != nil {
		return err
	}
	if digest != descriptor.ExecutableDigest {
		return fmt.Errorf("Gateway executable digest mismatch")
	}
	return nil
}

func verifyGatewayExecutablePath(path string) (string, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !pathInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 || pathInfo.Size() <= 0 || pathInfo.Size() > gatewayProcessExecutableMaxBytes {
		return "", fmt.Errorf("Gateway executable is not a bounded regular file")
	}
	if goruntime.GOOS != "windows" && pathInfo.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("Gateway executable is not executable")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(pathInfo, openedInfo) {
		return "", fmt.Errorf("Gateway executable changed while opening")
	}
	digest := sha256.New()
	written, err := io.Copy(digest, io.LimitReader(file, gatewayProcessExecutableMaxBytes+1))
	if err != nil || written != pathInfo.Size() || written > gatewayProcessExecutableMaxBytes {
		return "", fmt.Errorf("Gateway executable changed or exceeded integrity bound")
	}
	afterInfo, err := file.Stat()
	currentInfo, pathErr := os.Stat(path)
	currentLstat, lstatErr := os.Lstat(path)
	if err != nil || pathErr != nil || !os.SameFile(openedInfo, afterInfo) || !os.SameFile(openedInfo, currentInfo) || afterInfo.Size() != openedInfo.Size() {
		return "", fmt.Errorf("Gateway executable identity changed during verification")
	}
	if lstatErr != nil || !currentLstat.Mode().IsRegular() || currentLstat.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedInfo, currentLstat) {
		return "", fmt.Errorf("Gateway executable path changed during verification")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func gatewayStringMayContainSecret(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"authorization:", "bearer ", "password=", "passwd=", "secret=", "token=", "api_key=", "apikey="} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func gatewaySafeEffectError(err error) string {
	if err == nil {
		return "Gateway service effect failed without detail"
	}
	// Adapter/service output is outside the secret-free Runtime foundation
	// trust boundary. Persist only a correlation digest, never raw stderr.
	digest := sha256.Sum256([]byte(err.Error()))
	return "Gateway service effect detail sha256=" + hex.EncodeToString(digest[:8])
}

func validateGatewayTransitionAttempt(attempt gatewayTransitionAttempt) error {
	if attempt.ID == "" || attempt.ConnectionID == "" || !validGatewayAttemptKind(attempt.Kind) || !validGatewayAttemptPhase(attempt.Phase) ||
		attempt.BindingEpoch == 0 || attempt.Binding.Connection.ID != attempt.ConnectionID ||
		attempt.TargetGeneration == "" || attempt.RecoveryGeneration == "" || attempt.TargetGeneration == attempt.RecoveryGeneration ||
		attempt.EffectStartedAt == "" || attempt.UpdatedAt == "" {
		return fmt.Errorf("invalid Gateway transition attempt")
	}
	for _, value := range []string{attempt.ID, attempt.ConnectionID, attempt.TargetGeneration, attempt.RecoveryGeneration} {
		if len(value) > gatewayProcessStringMax || strings.ContainsAny(value, "\r\n\x00") || gatewayStringMayContainSecret(value) {
			return fmt.Errorf("Gateway transition identity is unbounded or sensitive")
		}
	}
	if !gatewayBindingCanonical(attempt.Binding) || attempt.Plan.ConnectionID != attempt.ConnectionID {
		return fmt.Errorf("invalid Gateway transition binding")
	}
	if err := validateGatewayLaunchPlan(attempt.Plan); err != nil {
		return err
	}
	effectStartedAt, err := time.Parse(time.RFC3339Nano, attempt.EffectStartedAt)
	if err != nil {
		return fmt.Errorf("invalid Gateway transition effect time")
	}
	var recoveryStartedAt time.Time
	if attempt.RecoveryStartedAt != "" {
		recoveryStartedAt, err = time.Parse(time.RFC3339Nano, attempt.RecoveryStartedAt)
		if err != nil {
			return fmt.Errorf("invalid Gateway recovery effect time")
		}
	}
	awaitingProof := attempt.Phase == gatewayAttemptAwaitingTargetProof || attempt.Phase == gatewayAttemptAwaitingRecoveryProof
	if awaitingProof != (attempt.ProofDeadline != "") {
		return fmt.Errorf("Gateway proof deadline does not match attempt phase")
	}
	if attempt.ProofDeadline != "" {
		deadline, err := time.Parse(time.RFC3339Nano, attempt.ProofDeadline)
		if err != nil {
			return fmt.Errorf("invalid Gateway proof deadline")
		}
		base := effectStartedAt
		if attempt.Phase == gatewayAttemptAwaitingRecoveryProof {
			if recoveryStartedAt.IsZero() {
				return fmt.Errorf("Gateway recovery proof has no recovery start")
			}
			base = recoveryStartedAt
		}
		if !deadline.After(base) || deadline.Sub(base) > gatewayProcessProofWait {
			return fmt.Errorf("Gateway proof deadline is outside the bounded window")
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, attempt.UpdatedAt); err != nil {
		return fmt.Errorf("invalid Gateway transition update time")
	}
	if len(attempt.LastError) > 1024 || gatewayStringMayContainSecret(attempt.LastError) {
		return fmt.Errorf("Gateway transition error detail is unbounded or sensitive")
	}
	if gatewayAttemptTerminal(attempt.Phase) {
		if attempt.CompletedAt == "" {
			return fmt.Errorf("terminal Gateway attempt has no completion time")
		}
		if _, err := time.Parse(time.RFC3339Nano, attempt.CompletedAt); err != nil {
			return fmt.Errorf("invalid Gateway transition completion time")
		}
	} else if attempt.CompletedAt != "" || attempt.AcceptedProof != nil {
		return fmt.Errorf("active Gateway attempt carries terminal proof")
	}
	if !gatewayAttemptTerminal(attempt.Phase) && attempt.Plan.Anchor.Generation != "" &&
		(attempt.TargetGeneration == attempt.Plan.Anchor.Generation || attempt.RecoveryGeneration == attempt.Plan.Anchor.Generation) {
		return fmt.Errorf("active Gateway generation reuses its registration anchor")
	}
	if attempt.Phase == gatewayAttemptReconcileRequired {
		if attempt.ReconcileEffect != gatewayEffectTarget && attempt.ReconcileEffect != gatewayEffectRecovery {
			return fmt.Errorf("Gateway reconcile attempt has no effect stage")
		}
	} else if attempt.ReconcileEffect != "" {
		return fmt.Errorf("Gateway attempt carries stale reconcile stage")
	}
	if attempt.AcceptedProof != nil {
		if err := validateGatewayProcessProofShape(*attempt.AcceptedProof); err != nil {
			return err
		}
		if attempt.Phase != gatewayAttemptSucceeded && attempt.Phase != gatewayAttemptRecovered {
			return fmt.Errorf("manual Gateway terminal cannot carry an accepted proof")
		}
		expectedGeneration, expectedBuild, expectedDigest := attempt.TargetGeneration, attempt.Plan.Target.Build, attempt.Plan.Target.ExecutableDigest
		if attempt.Phase == gatewayAttemptRecovered {
			expectedGeneration, expectedBuild, expectedDigest = attempt.RecoveryGeneration, attempt.Plan.Anchor.Descriptor.Build, attempt.Plan.Anchor.Descriptor.ExecutableDigest
		}
		if attempt.AcceptedProof.AttemptID != attempt.ID || attempt.AcceptedProof.Generation != expectedGeneration ||
			attempt.AcceptedProof.Build != expectedBuild || attempt.AcceptedProof.ExecutableDigest != expectedDigest {
			return fmt.Errorf("Gateway terminal proof does not match the accepted process")
		}
	}
	return nil
}

func gatewayLaunchPlansEqual(left, right gatewayLaunchPlan) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func gatewayLaunchDescriptorsEqual(left, right gatewayLaunchDescriptor) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func validateGatewayProcessProofShape(proof gatewayProcessProof) error {
	if proof.AttemptID == "" || proof.Generation == "" || proof.Build == "" || proof.ObservedAt == "" ||
		len(proof.ExecutableDigest) != sha256.Size*2 || proof.ExecutableDigest != strings.ToLower(proof.ExecutableDigest) {
		return fmt.Errorf("incomplete Gateway process proof")
	}
	for _, value := range []string{proof.AttemptID, proof.Generation, proof.Build} {
		if len(value) > gatewayProcessStringMax || strings.ContainsAny(value, "\r\n\x00") || gatewayStringMayContainSecret(value) {
			return fmt.Errorf("Gateway process proof contains unbounded or sensitive identity")
		}
	}
	if _, err := hex.DecodeString(proof.ExecutableDigest); err != nil {
		return fmt.Errorf("invalid Gateway process proof digest")
	}
	if _, err := time.Parse(time.RFC3339Nano, proof.ObservedAt); err != nil {
		return fmt.Errorf("invalid Gateway process proof time")
	}
	return nil
}
