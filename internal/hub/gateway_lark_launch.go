package hub

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/credentials"
)

// larkGatewayExecutableSpec is a private, secret-free description of one
// verified Gateway executable. It is deliberately narrower than arbitrary
// argv/environment input.
type larkGatewayExecutableSpec struct {
	Executable       string
	Build            string
	ExecutableDigest string
}

// larkGatewayLaunchSpec is the private hand-off from the Lark vertical slice
// into the shared Runtime coordinator. Provider identity and the target
// managed ref are not trusted from this value: they are re-derived from the
// frozen R0b binding before the plan is committed.
type larkGatewayLaunchSpec struct {
	ConnectionID     string
	AddressID        string
	Manager          gatewayServiceManager
	ServiceID        string
	UnitPath         string
	WorkingDirectory string
	HubURL           string
	DataDir          string
	LogPath          string
	Target           larkGatewayExecutableSpec
	Anchor           larkGatewayExecutableSpec
	AnchorManagedRef string
	AnchorAttemptID  string
	AnchorGeneration string
}

// startLarkGatewayLaunch is the production Runtime caller for the L2a chain:
// typed LaunchSpec -> frozen R1 plan -> one coordinated process attempt. It
// has no HTTP/CLI surface; the provider-specific phase supplies the spec from
// its already-authorized local operator flow.
func (h *Hub) startLarkGatewayLaunch(ctx context.Context, spec larkGatewayLaunchSpec, kind gatewayAttemptKind) (gatewayTransitionAttempt, error) {
	if kind != gatewayAttemptMigration && kind != gatewayAttemptManual && kind != gatewayAttemptRollback {
		return gatewayTransitionAttempt{}, errf(400, "unsupported Lark Gateway launch intent")
	}
	if _, err := h.configureLarkGatewayLaunch(spec); err != nil {
		return gatewayTransitionAttempt{}, err
	}
	connectionID := strings.TrimSpace(spec.ConnectionID)
	attempt, beginErr := h.beginGatewayProcessAttempt(ctx, connectionID, kind)
	if attempt.ID == "" || (attempt.Phase != gatewayAttemptAwaitingTargetProof && attempt.Phase != gatewayAttemptAwaitingRecoveryProof) {
		return attempt, beginErr
	}
	terminal, waitErr := h.waitLarkGatewayLaunch(ctx, connectionID, attempt.ID)
	if waitErr == nil && terminal.Phase != gatewayAttemptSucceeded {
		waitErr = fmt.Errorf("Lark Gateway target did not reach accepted proof (terminal phase %s)", terminal.Phase)
	}
	return terminal, errors.Join(beginErr, waitErr)
}

// configureLarkGatewayLaunch is the maintenance-safe half of the production
// chain. It commits the typed, binding-frozen plan without a service effect;
// the already-wired Hub startup caller then consumes it through the same R1
// coordinator after HTTP heartbeat handling is available.
func (h *Hub) configureLarkGatewayLaunch(spec larkGatewayLaunchSpec) (gatewayControl, error) {
	connectionID := strings.TrimSpace(spec.ConnectionID)
	if connectionID == "" {
		return gatewayControl{}, errf(400, "Gateway launch Connection is required")
	}
	h.mu.Lock()
	control := h.gatewayState.Controls[connectionID]
	h.mu.Unlock()
	if control == nil {
		if _, err := h.initializeGatewayControl(connectionID, gatewayBindingAdopted, gatewayRecoveryNone, ""); err != nil {
			return gatewayControl{}, fmt.Errorf("adopt Lark Gateway binding: %w", err)
		}
	}

	snapshot, err := h.snapshotGatewayBindingForProcessPlan(connectionID)
	if err != nil {
		return gatewayControl{}, err
	}
	plan, err := buildLarkGatewayLaunchPlan(snapshot.Binding, spec)
	if err != nil {
		return gatewayControl{}, errf(400, "invalid Lark Gateway launch specification: %s", err)
	}
	return h.configureGatewayLaunchPlan(snapshot, plan)
}

// waitLarkGatewayLaunch keeps the in-process maintenance Hub alive until the
// existing private heartbeat route commits exact proof or the R1 attempt
// reaches another durable stop. It performs no effect and never clears a
// latch itself.
func (h *Hub) waitLarkGatewayLaunch(ctx context.Context, connectionID, attemptID string) (gatewayTransitionAttempt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		h.mu.Lock()
		attempt := h.gatewayState.Attempts[connectionID]
		poisoned := h.gatewayFoundationPoisoned
		if attempt == nil || attempt.ID != attemptID {
			h.mu.Unlock()
			return gatewayTransitionAttempt{}, errf(409, "Gateway process attempt changed while awaiting proof")
		}
		current := *attempt
		if attempt.AcceptedProof != nil {
			proof := *attempt.AcceptedProof
			current.AcceptedProof = &proof
		}
		h.mu.Unlock()
		if poisoned {
			return current, errf(503, "Gateway foundation requires reopen/reconciliation")
		}
		if gatewayAttemptTerminal(current.Phase) {
			return current, nil
		}
		if current.Phase != gatewayAttemptAwaitingTargetProof && current.Phase != gatewayAttemptAwaitingRecoveryProof {
			return current, errf(409, "Gateway process stopped in %s while awaiting proof", current.Phase)
		}
		select {
		case <-ctx.Done():
			return current, ctx.Err()
		case <-h.stop:
			return current, errf(503, "Hub stopped while awaiting Gateway process proof")
		case <-ticker.C:
		}
	}
}

func buildLarkGatewayLaunchPlan(binding gatewayConfiguredBinding, spec larkGatewayLaunchSpec) (gatewayLaunchPlan, error) {
	connection := binding.Connection
	provider := strings.ToLower(strings.TrimSpace(connection.Provider))
	if provider != "lark" && provider != "feishu" {
		return gatewayLaunchPlan{}, fmt.Errorf("Connection provider is not Lark/Feishu")
	}
	if spec.ConnectionID != strings.TrimSpace(spec.ConnectionID) || spec.ConnectionID != connection.ID {
		return gatewayLaunchPlan{}, fmt.Errorf("Connection identity does not match the frozen binding")
	}
	if !credentials.IsManagedRef(connection.CredentialRef) || connection.CredentialRef != strings.TrimSpace(connection.CredentialRef) {
		return gatewayLaunchPlan{}, fmt.Errorf("frozen binding does not carry a canonical managed credential ref")
	}
	if connection.AccountRef == "" || connection.AccountRef != strings.TrimSpace(connection.AccountRef) {
		return gatewayLaunchPlan{}, fmt.Errorf("frozen binding has no canonical Lark App ID")
	}
	foundAddress := false
	for _, address := range binding.Addresses {
		if address.ID == spec.AddressID && address.ConnectionID == connection.ID && address.Enabled &&
			address.ArchivedAt == "" && address.DeletedAt == "" && address.SupersededBy == "" {
			foundAddress = true
			break
		}
	}
	if !foundAddress {
		return gatewayLaunchPlan{}, fmt.Errorf("launch Address is not active in the frozen binding")
	}

	base := gatewayLaunchDescriptor{
		Manager: spec.Manager, ConnectionID: connection.ID, Provider: provider,
		AddressID: spec.AddressID, AccountRef: connection.AccountRef, Domain: connection.Domain,
		ServiceID: spec.ServiceID, UnitPath: spec.UnitPath, WorkingDirectory: spec.WorkingDirectory,
		HubURL: spec.HubURL, DataDir: spec.DataDir, LogPath: spec.LogPath,
	}
	target := base
	target.Executable, target.Build, target.ExecutableDigest = spec.Target.Executable, spec.Target.Build, spec.Target.ExecutableDigest
	// The target ref is always derived from, and therefore exactly equal to, the
	// frozen configured binding. A caller cannot substitute another credential.
	target.ManagedCredentialRef = connection.CredentialRef
	anchorDescriptor := base
	anchorDescriptor.Executable, anchorDescriptor.Build, anchorDescriptor.ExecutableDigest = spec.Anchor.Executable, spec.Anchor.Build, spec.Anchor.ExecutableDigest
	anchorDescriptor.ManagedCredentialRef = strings.TrimSpace(spec.AnchorManagedRef)
	anchor, err := newGatewayRegistrationAnchor(anchorDescriptor)
	if err != nil {
		return gatewayLaunchPlan{}, err
	}
	anchor.AttemptID = strings.TrimSpace(spec.AnchorAttemptID)
	anchor.Generation = strings.TrimSpace(spec.AnchorGeneration)
	anchor.IntegritySHA256 = gatewayAnchorIntegrityWithProcess(anchor.Descriptor, anchor.AttemptID, anchor.Generation)
	plan := gatewayLaunchPlan{ConnectionID: connection.ID, Target: target, Anchor: anchor}
	plan.IntegritySHA256 = gatewayLaunchPlanIntegrity(plan)
	if err := validateGatewayLaunchPlanForBinding(plan, binding); err != nil {
		return gatewayLaunchPlan{}, err
	}
	return plan, nil
}

func validateGatewayLaunchPlanForBinding(plan gatewayLaunchPlan, binding gatewayConfiguredBinding) error {
	if err := validateGatewayLaunchPlan(plan); err != nil {
		return err
	}
	connection := binding.Connection
	if plan.ConnectionID != connection.ID || plan.Target.Provider != strings.ToLower(strings.TrimSpace(connection.Provider)) ||
		plan.Target.AccountRef != connection.AccountRef || plan.Target.Domain != connection.Domain || plan.Target.ManagedCredentialRef != connection.CredentialRef {
		return fmt.Errorf("Gateway launch plan does not match its frozen configured binding")
	}
	for _, address := range binding.Addresses {
		if address.ID == plan.Target.AddressID && address.ConnectionID == connection.ID && address.Enabled &&
			address.ArchivedAt == "" && address.DeletedAt == "" && address.SupersededBy == "" {
			return nil
		}
	}
	return fmt.Errorf("Gateway launch plan Address is not active in its frozen configured binding")
}
