package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const foundationFileName = "runtime-foundation.json"

const runtimeFoundationMaxBytes = 1 << 20

const (
	runtimeFoundationSchemaVersion   = 1
	runtimeWriterFloorS0             = 1
	runtimeWriterFloorGatewayState   = 2
	runtimeWriterFloorGatewayProcess = 3
	runtimeWriterFloorCredential     = 4
	runtimeWriterFloorGatewayProof   = 5
)

// runtimeFoundationEnvelope is private Runtime persistence shared by Store
// and Hub. It is not an API or provider wire contract. S0 recognizes version
// 1 as the empty foundation. Foundation state version 2 contains Gateway state:
// Gateway v1/floor 2 is R0b control, while Gateway v2/floor 3 adds an explicit
// R1 launch plan or attempt. Foundation v3/floor 4 marks managed credentials;
// v4/floor 5 freezes the typed Lark launch/proof descriptor. Each first write
// raises its floor atomically.
type runtimeFoundationEnvelope struct {
	SchemaVersion int             `json:"schemaVersion"`
	MinimumWriter int             `json:"minimumWriter"`
	State         json.RawMessage `json:"state"`
}

type foundationState struct {
	Version           int             `json:"version"`
	GatewayState      json.RawMessage `json:"gatewayState,omitempty"`
	CredentialManaged bool            `json:"credentialManaged,omitempty"`
}

type gatewayFoundationStateShape struct {
	Version      int                                           `json:"version"`
	Controls     map[string]*gatewayFoundationControlShape     `json:"controls"`
	Observations map[string]*gatewayFoundationObservationShape `json:"observations"`
	LaunchPlans  map[string]*gatewayFoundationLaunchPlanShape  `json:"launchPlans,omitempty"`
	Attempts     map[string]*gatewayFoundationAttemptShape     `json:"attempts,omitempty"`
}

type gatewayFoundationControlShape struct {
	ConnectionID    string                        `json:"connectionId"`
	Epoch           uint64                        `json:"epoch"`
	Lifecycle       string                        `json:"lifecycle"`
	Recovery        string                        `json:"recovery"`
	Reason          string                        `json:"reason,omitempty"`
	Binding         gatewayFoundationBindingShape `json:"binding"`
	ActiveAttemptID string                        `json:"activeAttemptId,omitempty"`
	UpdatedAt       string                        `json:"updatedAt"`
}

type gatewayFoundationLaunchDescriptorShape struct {
	Manager              string `json:"manager"`
	ConnectionID         string `json:"connectionId"`
	Provider             string `json:"provider,omitempty"`
	AddressID            string `json:"addressId,omitempty"`
	AccountRef           string `json:"accountRef,omitempty"`
	ManagedCredentialRef string `json:"managedCredentialRef,omitempty"`
	ServiceID            string `json:"serviceId"`
	UnitPath             string `json:"unitPath"`
	Executable           string `json:"executable"`
	WorkingDirectory     string `json:"workingDirectory"`
	HubURL               string `json:"hubUrl"`
	DataDir              string `json:"dataDir"`
	LogPath              string `json:"logPath"`
	Build                string `json:"build"`
	ExecutableDigest     string `json:"executableDigest"`
}

type gatewayFoundationAnchorShape struct {
	Descriptor      gatewayFoundationLaunchDescriptorShape `json:"descriptor"`
	AttemptID       string                                 `json:"attemptId,omitempty"`
	Generation      string                                 `json:"generation,omitempty"`
	IntegritySHA256 string                                 `json:"integritySha256"`
}

type gatewayFoundationLaunchPlanShape struct {
	ConnectionID    string                                 `json:"connectionId"`
	Target          gatewayFoundationLaunchDescriptorShape `json:"target"`
	Anchor          gatewayFoundationAnchorShape           `json:"anchor"`
	IntegritySHA256 string                                 `json:"integritySha256,omitempty"`
}

type gatewayFoundationProofShape struct {
	AttemptID        string `json:"attemptId"`
	Generation       string `json:"generation"`
	Build            string `json:"build"`
	ExecutableDigest string `json:"executableDigest"`
	ObservedAt       string `json:"observedAt"`
}

type gatewayFoundationAttemptShape struct {
	ID                 string                           `json:"id"`
	ConnectionID       string                           `json:"connectionId"`
	Kind               string                           `json:"kind"`
	Phase              string                           `json:"phase"`
	BindingEpoch       uint64                           `json:"bindingEpoch"`
	Binding            gatewayFoundationBindingShape    `json:"binding"`
	Plan               gatewayFoundationLaunchPlanShape `json:"plan"`
	TargetGeneration   string                           `json:"targetGeneration"`
	RecoveryGeneration string                           `json:"recoveryGeneration"`
	EffectStartedAt    string                           `json:"effectStartedAt"`
	RecoveryStartedAt  string                           `json:"recoveryStartedAt,omitempty"`
	ProofDeadline      string                           `json:"proofDeadline,omitempty"`
	UpdatedAt          string                           `json:"updatedAt"`
	CompletedAt        string                           `json:"completedAt,omitempty"`
	LastError          string                           `json:"lastError,omitempty"`
	ReconcileEffect    string                           `json:"reconcileEffect,omitempty"`
	AcceptedProof      *gatewayFoundationProofShape     `json:"acceptedProof,omitempty"`
}

type gatewayFoundationBindingShape struct {
	Connection gatewayFoundationConnectionShape `json:"connection"`
	Addresses  []gatewayFoundationAddressShape  `json:"addresses"`
}

type gatewayFoundationConnectionShape struct {
	ID            string   `json:"id"`
	Provider      string   `json:"provider"`
	AccountRef    string   `json:"accountRef,omitempty"`
	ScopeRef      string   `json:"scopeRef,omitempty"`
	CredentialRef string   `json:"credentialRef,omitempty"`
	Capabilities  []string `json:"capabilities,omitempty"`
	Enabled       bool     `json:"enabled"`
	SupersededBy  string   `json:"supersededBy,omitempty"`
	ArchivedAt    string   `json:"archivedAt,omitempty"`
	CreatedAt     string   `json:"createdAt"`
}

type gatewayFoundationAddressShape struct {
	ID                 string   `json:"id"`
	AgentID            string   `json:"agentId"`
	ConnectionID       string   `json:"connectionId"`
	ExternalIdentity   string   `json:"externalIdentity"`
	DisplayName        string   `json:"displayName,omitempty"`
	TriggerPolicy      string   `json:"triggerPolicy"`
	ReplyPolicy        string   `json:"replyPolicy"`
	DMPolicy           string   `json:"dmPolicy,omitempty"`
	TrustDomain        string   `json:"trustDomain"`
	AllowActors        []string `json:"allowActors,omitempty"`
	AllowConversations []string `json:"allowConversations,omitempty"`
	BlockActors        []string `json:"blockActors,omitempty"`
	BlockConversations []string `json:"blockConversations,omitempty"`
	Enabled            bool     `json:"enabled"`
	SupersededBy       string   `json:"supersededBy,omitempty"`
	ArchivedAt         string   `json:"archivedAt,omitempty"`
	DeletedAt          string   `json:"deletedAt,omitempty"`
	Version            int      `json:"version"`
	CreatedAt          string   `json:"createdAt"`
}

type gatewayFoundationObservationShape struct {
	ConnectionID         string   `json:"connectionId"`
	Sequence             uint64   `json:"sequence"`
	Status               string   `json:"status"`
	Error                string   `json:"error,omitempty"`
	Cursor               string   `json:"cursor,omitempty"`
	LastEventAt          string   `json:"lastEventAt,omitempty"`
	ObservedCapabilities []string `json:"observedCapabilities,omitempty"`
	HeartbeatAt          string   `json:"heartbeatAt,omitempty"`
	ObservedAt           string   `json:"observedAt"`
}

type stableDataDir struct {
	requested string
	canonical string
	identity  string
	info      os.FileInfo
	handle    *os.File
	root      *os.Root
	lock      *os.File
	once      sync.Once
	err       error
}

var processWriters = struct {
	sync.Mutex
	held map[string]struct{}
}{held: map[string]struct{}{}}

func openStableDataDir(path string, readOnly bool) (_ *stableDataDir, err error) {
	return openStableDataDirWithClaimHook(path, readOnly, nil)
}

// openStableDataDirWithClaimHook keeps the production open path identical
// while allowing a deterministic identity change immediately after the OS
// writer lock is acquired. Production always passes a nil hook.
func openStableDataDirWithClaimHook(path string, readOnly bool, afterWriterLock func()) (_ *stableDataDir, err error) {
	requested, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("resolve data directory: %w", err)
	}
	canonical, exists, err := resolveDataDir(requested)
	if err != nil {
		return nil, err
	}
	if !exists {
		if readOnly {
			return nil, os.ErrNotExist
		}
		if err := createDataDirFromStableParent(canonical); err != nil {
			return nil, err
		}
	}

	handle, err := os.Open(canonical)
	if err != nil {
		return nil, fmt.Errorf("open stable data directory handle: %w", err)
	}
	defer func() {
		if err != nil {
			_ = handle.Close()
		}
	}()
	info, err := handle.Stat()
	if err != nil || !info.IsDir() {
		if err == nil {
			err = fmt.Errorf("data directory is not a directory: %s", canonical)
		}
		return nil, err
	}
	if err = verifySupportedFilesystem(handle); err != nil {
		return nil, err
	}
	identity, err := stableFileIdentity(handle, info)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		return nil, fmt.Errorf("open stable data directory root: %w", err)
	}
	defer func() {
		if err != nil {
			_ = root.Close()
		}
	}()
	rootInfo, err := root.Stat(".")
	if err != nil || !os.SameFile(info, rootInfo) {
		return nil, fmt.Errorf("data directory changed while opening stable root")
	}
	d := &stableDataDir{requested: requested, canonical: canonical, identity: identity, info: info, handle: handle, root: root}
	if err = d.verifyIdentity(); err != nil {
		return nil, err
	}
	// Validation deliberately precedes lockfile/events/migration creation.
	if err = validateFoundation(root); err != nil {
		return nil, err
	}
	if !readOnly {
		if err = d.claimWriter(afterWriterLock); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func resolveDataDir(requested string) (canonical string, exists bool, err error) {
	if _, err = os.Lstat(requested); err == nil {
		canonical, err = filepath.EvalSymlinks(requested)
		if err != nil {
			return "", false, fmt.Errorf("resolve data directory: %w", err)
		}
		return filepath.Clean(canonical), true, nil
	} else if !os.IsNotExist(err) {
		return "", false, fmt.Errorf("inspect data directory: %w", err)
	}
	missing := []string{}
	current := requested
	for {
		if _, statErr := os.Lstat(current); statErr == nil {
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil {
				return "", false, fmt.Errorf("resolve data directory parent: %w", resolveErr)
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), false, nil
		} else if !os.IsNotExist(statErr) {
			return "", false, fmt.Errorf("inspect data directory parent: %w", statErr)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false, fmt.Errorf("data directory has no stable parent: %s", requested)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func createDataDirFromStableParent(path string) error {
	parent := filepath.Dir(path)
	for {
		if info, err := os.Stat(parent); err == nil {
			if !info.IsDir() {
				return fmt.Errorf("data directory parent is not a directory: %s", parent)
			}
			break
		} else if !os.IsNotExist(err) {
			return err
		}
		next := filepath.Dir(parent)
		if next == parent {
			return fmt.Errorf("data directory has no stable parent: %s", path)
		}
		parent = next
	}
	handle, err := os.Open(parent)
	if err != nil {
		return err
	}
	defer handle.Close()
	if err := verifySupportedFilesystem(handle); err != nil {
		return err
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer root.Close()
	handleInfo, err := handle.Stat()
	if err != nil {
		return err
	}
	rootInfo, err := root.Stat(".")
	if err != nil || !os.SameFile(handleInfo, rootInfo) {
		return fmt.Errorf("data directory parent changed while opening stable root")
	}
	rel, err := filepath.Rel(parent, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("data directory escaped stable parent")
	}
	return root.MkdirAll(rel, 0o755)
}

func validateFoundation(root *os.Root) error {
	f, err := root.Open(foundationFileName)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open Runtime foundation: %w", err)
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, runtimeFoundationMaxBytes))
	dec.DisallowUnknownFields()
	var envelope runtimeFoundationEnvelope
	if err := dec.Decode(&envelope); err != nil {
		return fmt.Errorf("decode Runtime foundation: %w", err)
	}
	if err := requireJSONEOF(dec); err != nil {
		return fmt.Errorf("decode Runtime foundation: %w", err)
	}
	if envelope.SchemaVersion != runtimeFoundationSchemaVersion {
		return fmt.Errorf("unsupported Runtime foundation schema/floor: schema=%d floor=%d", envelope.SchemaVersion, envelope.MinimumWriter)
	}
	stateDec := json.NewDecoder(strings.NewReader(string(envelope.State)))
	stateDec.DisallowUnknownFields()
	var state foundationState
	if err := stateDec.Decode(&state); err != nil {
		return fmt.Errorf("invalid Runtime foundation state")
	}
	if err := requireJSONEOF(stateDec); err != nil {
		return fmt.Errorf("invalid Runtime foundation state: %w", err)
	}
	switch state.Version {
	case 1:
		if envelope.MinimumWriter < 0 || envelope.MinimumWriter > runtimeWriterFloorS0 || len(state.GatewayState) != 0 || state.CredentialManaged {
			return fmt.Errorf("unsupported Runtime foundation schema/floor: schema=%d floor=%d", envelope.SchemaVersion, envelope.MinimumWriter)
		}
	case 2:
		if len(state.GatewayState) == 0 || state.CredentialManaged {
			return fmt.Errorf("unsupported Runtime foundation schema/floor: schema=%d floor=%d", envelope.SchemaVersion, envelope.MinimumWriter)
		}
		gatewayVersion, err := validateGatewayFoundationState(state.GatewayState)
		if err != nil {
			return err
		}
		if gatewayVersion == 3 {
			return fmt.Errorf("L2a Gateway launch proof requires the credential and launch-proof floors")
		}
		expectedFloor := runtimeWriterFloorGatewayState
		if gatewayVersion == 2 {
			expectedFloor = runtimeWriterFloorGatewayProcess
		}
		if envelope.MinimumWriter != expectedFloor {
			return fmt.Errorf("unsupported Runtime foundation schema/floor: schema=%d floor=%d", envelope.SchemaVersion, envelope.MinimumWriter)
		}
	case 3:
		if envelope.MinimumWriter != runtimeWriterFloorCredential || !state.CredentialManaged {
			return fmt.Errorf("unsupported Runtime foundation schema/floor: schema=%d floor=%d", envelope.SchemaVersion, envelope.MinimumWriter)
		}
		if len(state.GatewayState) != 0 {
			gatewayVersion, err := validateGatewayFoundationState(state.GatewayState)
			if err != nil {
				return err
			}
			if gatewayVersion == 3 {
				return fmt.Errorf("L2a Gateway launch proof requires the launch-proof floor")
			}
		}
	case 4:
		if envelope.MinimumWriter != runtimeWriterFloorGatewayProof || !state.CredentialManaged || len(state.GatewayState) == 0 {
			return fmt.Errorf("unsupported Runtime foundation schema/floor: schema=%d floor=%d", envelope.SchemaVersion, envelope.MinimumWriter)
		}
		gatewayVersion, err := validateGatewayFoundationState(state.GatewayState)
		if err != nil || gatewayVersion != 3 {
			return fmt.Errorf("unsupported Runtime Gateway launch-proof foundation")
		}
	default:
		return fmt.Errorf("invalid Runtime foundation state version %d", state.Version)
	}
	return nil
}

func validateGatewayFoundationState(raw json.RawMessage) (int, error) {
	gatewayDec := json.NewDecoder(strings.NewReader(string(raw)))
	gatewayDec.DisallowUnknownFields()
	var gateway gatewayFoundationStateShape
	if err := gatewayDec.Decode(&gateway); err != nil {
		return 0, fmt.Errorf("invalid Runtime Gateway foundation state")
	}
	if err := requireJSONEOF(gatewayDec); err != nil {
		return 0, fmt.Errorf("invalid Runtime Gateway foundation state: %w", err)
	}
	if (gateway.Version != 1 && gateway.Version != 2 && gateway.Version != 3) || gateway.Controls == nil || gateway.Observations == nil {
		return 0, fmt.Errorf("invalid Runtime Gateway foundation state version")
	}
	if gateway.Version == 1 && (len(gateway.LaunchPlans) != 0 || len(gateway.Attempts) != 0) {
		return 0, fmt.Errorf("R0b Gateway foundation contains R1 process state")
	}
	if gateway.Version == 2 && len(gateway.LaunchPlans) == 0 {
		return 0, fmt.Errorf("R1 Gateway process foundation has no explicit launch plan")
	}
	for id, control := range gateway.Controls {
		if id == "" || control == nil || control.ConnectionID != id || control.Epoch == 0 ||
			(control.Lifecycle != "provisioning" && control.Lifecycle != "adopted") ||
			(control.Recovery != "none" && control.Recovery != "needs_reconcile" && control.Recovery != "manual_recovery_required") ||
			!validateGatewayFoundationBinding(id, control.Binding) {
			return 0, fmt.Errorf("invalid Runtime Gateway control %q", id)
		}
	}
	for id, observation := range gateway.Observations {
		if id == "" || observation == nil || observation.ConnectionID != id || observation.Sequence == 0 || gateway.Controls[id] == nil ||
			(observation.Status != "disconnected" && observation.Status != "connecting" && observation.Status != "connected" && observation.Status != "degraded") ||
			!foundationStringsCanonical(observation.ObservedCapabilities, true) {
			return 0, fmt.Errorf("invalid Runtime Gateway observation %q", id)
		}
	}
	typedPlans := 0
	for id, plan := range gateway.LaunchPlans {
		if plan == nil || plan.ConnectionID != id || gateway.Controls[id] == nil {
			return 0, fmt.Errorf("invalid Runtime Gateway launch plan %q", id)
		}
		if err := validateGatewayFoundationLaunchPlan(*plan); err != nil {
			return 0, fmt.Errorf("invalid Runtime Gateway launch plan %q: %w", id, err)
		}
		if plan.Target.Provider != "" {
			typedPlans++
		}
	}
	if gateway.Version != 3 && typedPlans != 0 {
		return 0, fmt.Errorf("typed Gateway launch plan requires L2a foundation")
	}
	for id, attempt := range gateway.Attempts {
		if attempt == nil || attempt.ConnectionID != id || gateway.Controls[id] == nil || gateway.LaunchPlans[id] == nil ||
			attempt.ID == "" || attempt.BindingEpoch == 0 || !validateGatewayFoundationBinding(id, attempt.Binding) ||
			attempt.TargetGeneration == "" || attempt.RecoveryGeneration == "" || attempt.TargetGeneration == attempt.RecoveryGeneration ||
			!foundationAttemptKind(attempt.Kind) || !foundationAttemptPhase(attempt.Phase) {
			return 0, fmt.Errorf("invalid Runtime Gateway attempt %q", id)
		}
		for _, value := range []string{attempt.ID, attempt.ConnectionID, attempt.TargetGeneration, attempt.RecoveryGeneration} {
			if len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") || foundationStringMayContainSecret(value) {
				return 0, fmt.Errorf("unbounded Runtime Gateway attempt %q", id)
			}
		}
		if err := validateGatewayFoundationLaunchPlan(attempt.Plan); err != nil {
			return 0, fmt.Errorf("invalid Runtime Gateway attempt plan %q", id)
		}
		effectStartedAt, err := time.Parse(time.RFC3339Nano, attempt.EffectStartedAt)
		if err != nil {
			return 0, fmt.Errorf("invalid Runtime Gateway attempt time %q", id)
		}
		if _, err := time.Parse(time.RFC3339Nano, attempt.UpdatedAt); err != nil || len(attempt.LastError) > 1024 || foundationStringMayContainSecret(attempt.LastError) {
			return 0, fmt.Errorf("invalid Runtime Gateway attempt update %q", id)
		}
		var recoveryStartedAt time.Time
		if attempt.RecoveryStartedAt != "" {
			recoveryStartedAt, err = time.Parse(time.RFC3339Nano, attempt.RecoveryStartedAt)
			if err != nil {
				return 0, fmt.Errorf("invalid Runtime Gateway recovery time %q", id)
			}
		}
		awaitingProof := attempt.Phase == "awaiting_target_proof" || attempt.Phase == "awaiting_recovery_proof"
		if awaitingProof != (attempt.ProofDeadline != "") {
			return 0, fmt.Errorf("invalid Runtime Gateway proof deadline %q", id)
		}
		if attempt.ProofDeadline != "" {
			deadline, err := time.Parse(time.RFC3339Nano, attempt.ProofDeadline)
			if err != nil {
				return 0, fmt.Errorf("invalid Runtime Gateway proof deadline time %q", id)
			}
			base := effectStartedAt
			if attempt.Phase == "awaiting_recovery_proof" {
				if recoveryStartedAt.IsZero() {
					return 0, fmt.Errorf("Runtime Gateway recovery proof has no start %q", id)
				}
				base = recoveryStartedAt
			}
			if !deadline.After(base) || deadline.Sub(base) > 30*time.Second {
				return 0, fmt.Errorf("Runtime Gateway proof deadline is unbounded %q", id)
			}
		}
		terminal := attempt.Phase == "succeeded" || attempt.Phase == "recovered" || attempt.Phase == "manual_recovery_required"
		if !terminal && attempt.Plan.Anchor.Generation != "" &&
			(attempt.TargetGeneration == attempt.Plan.Anchor.Generation || attempt.RecoveryGeneration == attempt.Plan.Anchor.Generation) {
			return 0, fmt.Errorf("active Runtime Gateway attempt reuses its anchor generation %q", id)
		}
		if terminal != (attempt.CompletedAt != "") || (!terminal && attempt.AcceptedProof != nil) {
			return 0, fmt.Errorf("invalid Runtime Gateway terminal attempt %q", id)
		}
		if terminal {
			if _, err := time.Parse(time.RFC3339Nano, attempt.CompletedAt); err != nil {
				return 0, fmt.Errorf("invalid Runtime Gateway completion time %q", id)
			}
		}
		if (attempt.Phase == "reconcile_required") != (attempt.ReconcileEffect == "target" || attempt.ReconcileEffect == "recovery") {
			return 0, fmt.Errorf("invalid Runtime Gateway reconcile effect %q", id)
		}
		if attempt.AcceptedProof != nil {
			if attempt.AcceptedProof.AttemptID != attempt.ID || attempt.AcceptedProof.Generation == "" || attempt.AcceptedProof.Build == "" ||
				!foundationSHA256(attempt.AcceptedProof.ExecutableDigest) {
				return 0, fmt.Errorf("invalid Runtime Gateway proof %q", id)
			}
			for _, value := range []string{attempt.AcceptedProof.AttemptID, attempt.AcceptedProof.Generation, attempt.AcceptedProof.Build} {
				if len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") || foundationStringMayContainSecret(value) {
					return 0, fmt.Errorf("unbounded Runtime Gateway proof %q", id)
				}
			}
			if _, err := time.Parse(time.RFC3339Nano, attempt.AcceptedProof.ObservedAt); err != nil {
				return 0, fmt.Errorf("invalid Runtime Gateway proof time %q", id)
			}
			expectedGeneration, expectedBuild, expectedDigest := attempt.TargetGeneration, attempt.Plan.Target.Build, attempt.Plan.Target.ExecutableDigest
			if attempt.Phase == "recovered" {
				expectedGeneration, expectedBuild, expectedDigest = attempt.RecoveryGeneration, attempt.Plan.Anchor.Descriptor.Build, attempt.Plan.Anchor.Descriptor.ExecutableDigest
			}
			if attempt.AcceptedProof.Generation != expectedGeneration || attempt.AcceptedProof.Build != expectedBuild || attempt.AcceptedProof.ExecutableDigest != expectedDigest {
				return 0, fmt.Errorf("mismatched Runtime Gateway proof %q", id)
			}
		}
		control := gateway.Controls[id]
		attemptPlanJSON, _ := json.Marshal(attempt.Plan)
		configuredPlanJSON, _ := json.Marshal(gateway.LaunchPlans[id])
		if terminal {
			if control.ActiveAttemptID != "" {
				return 0, fmt.Errorf("terminal Runtime Gateway attempt remains active %q", id)
			}
		} else if !bytes.Equal(attemptPlanJSON, configuredPlanJSON) {
			return 0, fmt.Errorf("active Runtime Gateway attempt plan drifted %q", id)
		} else if control.ActiveAttemptID != attempt.ID || control.Recovery == "none" {
			return 0, fmt.Errorf("active Runtime Gateway attempt is not fenced %q", id)
		} else {
			bindingJSON, _ := json.Marshal(attempt.Binding)
			controlBindingJSON, _ := json.Marshal(control.Binding)
			if control.Epoch != attempt.BindingEpoch || !bytes.Equal(bindingJSON, controlBindingJSON) || !bytes.Equal(attemptPlanJSON, configuredPlanJSON) {
				return 0, fmt.Errorf("active Runtime Gateway attempt drifted %q", id)
			}
		}
	}
	for id, control := range gateway.Controls {
		if control.ActiveAttemptID == "" {
			continue
		}
		attempt := gateway.Attempts[id]
		if attempt == nil || attempt.ID != control.ActiveAttemptID {
			return 0, fmt.Errorf("Runtime Gateway control lost active attempt %q", id)
		}
	}
	return gateway.Version, nil
}

func gatewayFoundationHasTypedLaunchPlan(raw json.RawMessage) bool {
	var gateway gatewayFoundationStateShape
	if err := json.Unmarshal(raw, &gateway); err != nil {
		return false
	}
	for _, plan := range gateway.LaunchPlans {
		if plan != nil && plan.Target.Provider != "" {
			return true
		}
	}
	return false
}

func validateGatewayFoundationBinding(id string, binding gatewayFoundationBindingShape) bool {
	if binding.Connection.ID != id || binding.Connection.Provider == "" || !foundationStringsCanonical(binding.Connection.Capabilities, true) {
		return false
	}
	previousAddressID := ""
	for _, address := range binding.Addresses {
		if address.ID == "" || address.ID <= previousAddressID || address.ConnectionID != id || address.AgentID == "" || address.ExternalIdentity == "" || address.Version < 1 ||
			!foundationStringsCanonical(address.AllowActors, false) || !foundationStringsCanonical(address.AllowConversations, false) ||
			!foundationStringsCanonical(address.BlockActors, false) || !foundationStringsCanonical(address.BlockConversations, false) {
			return false
		}
		previousAddressID = address.ID
	}
	return true
}

func validateGatewayFoundationLaunchPlan(plan gatewayFoundationLaunchPlanShape) error {
	if plan.ConnectionID == "" || plan.Target.ConnectionID != plan.ConnectionID || plan.Anchor.Descriptor.ConnectionID != plan.ConnectionID {
		return fmt.Errorf("launch plan Connection mismatch")
	}
	if err := validateGatewayFoundationLaunchDescriptor(plan.Target); err != nil {
		return err
	}
	if err := validateGatewayFoundationLaunchDescriptor(plan.Anchor.Descriptor); err != nil {
		return err
	}
	if len(plan.Anchor.AttemptID) > 4096 || strings.ContainsAny(plan.Anchor.AttemptID, "\r\n\x00") || foundationStringMayContainSecret(plan.Anchor.AttemptID) ||
		len(plan.Anchor.Generation) > 4096 || strings.ContainsAny(plan.Anchor.Generation, "\r\n\x00") || foundationStringMayContainSecret(plan.Anchor.Generation) ||
		(plan.Anchor.Descriptor.Provider != "" && (plan.Anchor.AttemptID == "") != (plan.Anchor.Generation == "")) {
		return fmt.Errorf("invalid registration anchor process identity")
	}
	anchor := plan.Anchor.Descriptor
	if plan.Target.Manager != anchor.Manager || plan.Target.ServiceID != anchor.ServiceID || plan.Target.UnitPath != anchor.UnitPath ||
		plan.Target.HubURL != anchor.HubURL || plan.Target.DataDir != anchor.DataDir || plan.Target.LogPath != anchor.LogPath ||
		plan.Target.Provider != anchor.Provider || plan.Target.AddressID != anchor.AddressID || plan.Target.AccountRef != anchor.AccountRef {
		return fmt.Errorf("launch plan registration mismatch")
	}
	if plan.Target.Provider != "" && !foundationManagedCredentialRef(plan.Target.ManagedCredentialRef) {
		return fmt.Errorf("Lark Gateway target has no managed credential reference")
	}
	payload := struct {
		Descriptor gatewayFoundationLaunchDescriptorShape `json:"descriptor"`
		AttemptID  string                                 `json:"attemptId,omitempty"`
		Generation string                                 `json:"generation,omitempty"`
	}{Descriptor: anchor, AttemptID: plan.Anchor.AttemptID, Generation: plan.Anchor.Generation}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if plan.Anchor.IntegritySHA256 != hex.EncodeToString(digest[:]) {
		return fmt.Errorf("registration anchor integrity mismatch")
	}
	if plan.Target.Provider != "" {
		integrityPayload := struct {
			ConnectionID string                                 `json:"connectionId"`
			Target       gatewayFoundationLaunchDescriptorShape `json:"target"`
			Anchor       gatewayFoundationAnchorShape           `json:"anchor"`
		}{ConnectionID: plan.ConnectionID, Target: plan.Target, Anchor: plan.Anchor}
		integrityData, err := json.Marshal(integrityPayload)
		if err != nil {
			return err
		}
		integrityDigest := sha256.Sum256(integrityData)
		if plan.IntegritySHA256 != hex.EncodeToString(integrityDigest[:]) {
			return fmt.Errorf("launch plan integrity mismatch")
		}
	} else if plan.IntegritySHA256 != "" && !foundationSHA256(plan.IntegritySHA256) {
		return fmt.Errorf("invalid launch plan integrity")
	}
	planJSON, err := json.Marshal(plan)
	if err != nil || len(planJSON) > 32<<10 {
		return fmt.Errorf("registration plan is unbounded")
	}
	return nil
}

func validateGatewayFoundationLaunchDescriptor(value gatewayFoundationLaunchDescriptorShape) error {
	if value.Manager != "launchd" && value.Manager != "systemd" && value.Manager != "fake" {
		return fmt.Errorf("unsupported service manager")
	}
	fields := []string{value.ConnectionID, value.ServiceID, value.UnitPath, value.Executable, value.WorkingDirectory, value.HubURL, value.DataDir, value.LogPath, value.Build}
	for _, field := range fields {
		if field == "" || field != strings.TrimSpace(field) || len(field) > 4096 || strings.ContainsAny(field, "\r\n\x00") {
			return fmt.Errorf("invalid launch descriptor field")
		}
	}
	if !foundationGatewayServiceIdentifier(value.ConnectionID) || !foundationGatewayServiceIdentifier(value.ServiceID) {
		return fmt.Errorf("invalid launch descriptor identifier")
	}
	hasTypedProvider := value.Provider != "" || value.AddressID != "" || value.AccountRef != "" || value.ManagedCredentialRef != ""
	if hasTypedProvider {
		if (value.Provider != "lark" && value.Provider != "feishu") || !foundationGatewayServiceIdentifier(value.AddressID) ||
			value.AccountRef == "" || value.AccountRef != strings.TrimSpace(value.AccountRef) || len(value.AccountRef) > 4096 || strings.ContainsAny(value.AccountRef, "\r\n\x00") {
			return fmt.Errorf("invalid provider launch identity")
		}
		if foundationStringMayContainSecret(value.AccountRef) {
			return fmt.Errorf("provider launch identity may contain secret material")
		}
		if value.ManagedCredentialRef != "" && !foundationManagedCredentialRef(value.ManagedCredentialRef) {
			return fmt.Errorf("invalid managed credential reference")
		}
	}
	for _, path := range []string{value.UnitPath, value.Executable, value.WorkingDirectory, value.DataDir, value.LogPath} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("invalid launch descriptor path")
		}
	}
	if (value.Manager == "launchd" && filepath.Ext(value.UnitPath) != ".plist") || (value.Manager == "systemd" && filepath.Ext(value.UnitPath) != ".service") {
		return fmt.Errorf("launch descriptor unit suffix mismatch")
	}
	parsed, err := url.Parse(value.HubURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("invalid launch descriptor Hub URL")
	}
	if !foundationSHA256(value.ExecutableDigest) {
		return fmt.Errorf("invalid launch descriptor digest")
	}
	for _, field := range fields {
		if foundationStringMayContainSecret(field) {
			return fmt.Errorf("launch descriptor may contain secret material")
		}
	}
	return nil
}

func foundationManagedCredentialRef(value string) bool {
	const prefix = "managed:"
	return value == strings.TrimSpace(value) && strings.HasPrefix(value, prefix) && foundationSHA256(strings.TrimPrefix(value, prefix))
}

func foundationGatewayServiceIdentifier(value string) bool {
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

func foundationStringMayContainSecret(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"authorization:", "bearer ", "password=", "passwd=", "secret=", "token=", "api_key=", "apikey="} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func foundationSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func foundationAttemptKind(value string) bool {
	return value == "automatic_restart" || value == "manual_repair" || value == "migration_activation" || value == "rollback"
}

func foundationAttemptPhase(value string) bool {
	switch value {
	case "target_intent", "awaiting_target_proof", "reconcile_required", "recovery_intent", "awaiting_recovery_proof", "succeeded", "recovered", "manual_recovery_required":
		return true
	default:
		return false
	}
}

func foundationStringsCanonical(values []string, lower bool) bool {
	previous := ""
	for _, value := range values {
		normalized := strings.TrimSpace(value)
		if lower {
			normalized = strings.ToLower(normalized)
		}
		if value != normalized || value == "" || value <= previous {
			return false
		}
		previous = value
	}
	return true
}

func requireJSONEOF(dec *json.Decoder) error {
	var extra any
	err := dec.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return fmt.Errorf("multiple JSON values")
	}
	return err
}

func (d *stableDataDir) claimWriter(afterWriterLock func()) (err error) {
	processWriters.Lock()
	if _, exists := processWriters.held[d.identity]; exists {
		processWriters.Unlock()
		return fmt.Errorf("data directory already has a writable CodexLoom process: %s", d.canonical)
	}
	processWriters.held[d.identity] = struct{}{}
	processWriters.Unlock()
	claimComplete := false
	var lock *os.File
	lockHeld := false
	defer func() {
		if claimComplete {
			return
		}
		var cleanupErr error
		if lockHeld {
			cleanupErr = unlockWriterFile(lock)
		}
		if lock != nil {
			cleanupErr = errors.Join(cleanupErr, lock.Close())
		}
		d.lock = nil
		processWriters.Lock()
		delete(processWriters.held, d.identity)
		processWriters.Unlock()
		if cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("release failed data directory writer claim: %w", cleanupErr))
		}
	}()
	if info, err := d.root.Lstat(".codex-loom-writer.lock"); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("data directory writer lease is not a regular file")
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect data directory writer lease: %w", err)
	}
	lock, err = d.root.OpenFile(".codex-loom-writer.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open data directory writer lease: %w", err)
	}
	if err := lockWriterFile(lock); err != nil {
		return fmt.Errorf("data directory already has a writable CodexLoom process: %s: %w", d.canonical, err)
	}
	lockHeld = true
	d.lock = lock
	if afterWriterLock != nil {
		afterWriterLock()
	}
	if err := d.verifyIdentity(); err != nil {
		return err
	}
	claimComplete = true
	return nil
}

func (d *stableDataDir) verifyIdentity() error {
	if d == nil || d.handle == nil || d.root == nil || d.info == nil {
		return fmt.Errorf("stable data directory handle is unavailable")
	}
	handleInfo, err := d.handle.Stat()
	if err != nil || !os.SameFile(d.info, handleInfo) {
		return fmt.Errorf("data directory handle identity changed")
	}
	current, err := os.Stat(d.canonical)
	if err != nil || !os.SameFile(d.info, current) {
		return fmt.Errorf("data directory canonical identity changed: %s", d.canonical)
	}
	resolved, _, err := resolveDataDir(d.requested)
	if err != nil {
		return fmt.Errorf("data directory bootstrap path changed: %w", err)
	}
	requestedInfo, err := os.Stat(resolved)
	if err != nil || !os.SameFile(d.info, requestedInfo) {
		return fmt.Errorf("data directory bootstrap path identity changed: %s", d.requested)
	}
	if err := verifySupportedFilesystem(d.handle); err != nil {
		return err
	}
	identity, err := stableFileIdentity(d.handle, handleInfo)
	if err != nil || identity != d.identity {
		return fmt.Errorf("data directory filesystem identity changed")
	}
	return nil
}

func (d *stableDataDir) close() error {
	if d == nil {
		return nil
	}
	d.once.Do(func() {
		if d.lock != nil {
			if err := unlockWriterFile(d.lock); err != nil {
				d.err = err
			}
			if err := d.lock.Close(); d.err == nil {
				d.err = err
			}
			processWriters.Lock()
			delete(processWriters.held, d.identity)
			processWriters.Unlock()
		}
		if err := d.root.Close(); d.err == nil {
			d.err = err
		}
		if err := d.handle.Close(); d.err == nil {
			d.err = err
		}
	})
	return d.err
}
