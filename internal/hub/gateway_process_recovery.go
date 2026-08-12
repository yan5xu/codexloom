package hub

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// configureGatewayLaunchPlan is private R1 plumbing consumed only by typed
// in-process Runtime callers. The first explicit plan upgrades the private
// foundation and writer floor atomically; ordinary open never does.
func (h *Hub) configureGatewayLaunchPlan(snapshot gatewayBindingSnapshot, plan gatewayLaunchPlan) (gatewayControl, error) {
	if err := validateGatewayLaunchPlan(plan); err != nil {
		return gatewayControl{}, errf(400, "invalid Gateway launch plan: %s", err)
	}
	if err := verifyGatewayLaunchPlanExecutables(plan); err != nil {
		return gatewayControl{}, errf(400, "invalid Gateway launch plan integrity: %s", err)
	}
	adapter, err := h.gatewayServiceAdapter(plan)
	if err != nil {
		return gatewayControl{}, errf(409, "Gateway service adapter is unavailable: %s", err)
	}
	if err := adapter.ValidateAnchor(context.Background(), plan); err != nil {
		return gatewayControl{}, errf(409, "Gateway registration anchor is not authoritative: %s", err)
	}
	connectionID := strings.TrimSpace(snapshot.Binding.Connection.ID)
	if connectionID == "" || plan.ConnectionID != connectionID {
		return gatewayControl{}, errf(409, "Gateway launch plan does not match binding snapshot")
	}
	unlock := h.gatewayCoordinatorForUse().lock(connectionID)
	defer unlock()
	if err := adapter.ValidateAnchor(context.Background(), plan); err != nil {
		return gatewayControl{}, errf(409, "Gateway registration anchor changed before plan commit: %s", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return gatewayControl{}, err
	}
	if h.stopping {
		return gatewayControl{}, errf(503, "Hub is stopping")
	}
	if h.st == nil || filepath.Clean(plan.Target.DataDir) != filepath.Clean(h.st.Dir()) {
		return gatewayControl{}, errf(409, "Gateway launch plan targets another Runtime data directory")
	}
	if snapshot.OpenGeneration == "" || snapshot.OpenGeneration != h.gatewayOpenGeneration {
		return gatewayControl{}, errf(409, "Gateway binding snapshot belongs to another Hub generation")
	}
	control := h.gatewayState.Controls[connectionID]
	if control == nil || control.Epoch != snapshot.ControlEpoch || control.Lifecycle != gatewayBindingAdopted || control.ActiveAttemptID != "" {
		return gatewayControl{}, errf(409, "Gateway control is not eligible for a launch plan")
	}
	current, err := h.gatewayBindingLocked(connectionID)
	if err != nil || !gatewayBindingsEqual(current, snapshot.Binding) || !gatewayBindingsEqual(current, control.Binding) {
		return gatewayControl{}, errf(409, "Gateway configured binding changed")
	}
	if plan.Target.Provider != "" {
		if err := validateGatewayLaunchPlanForBinding(plan, current); err != nil {
			return gatewayControl{}, errf(409, "Gateway launch plan does not match configured binding: %s", err)
		}
	}
	next := upgradeGatewayStateForProcess(h.gatewayState)
	if plan.Target.Provider != "" {
		next = upgradeGatewayStateForLaunchProof(h.gatewayState)
	}
	nextControl := next.Controls[connectionID]
	nextEpoch, err := advanceGatewayEpoch(nextControl.Epoch)
	if err != nil {
		return gatewayControl{}, err
	}
	nextControl.Epoch = nextEpoch
	nextControl.Binding = current
	nextControl.UpdatedAt = now()
	copy := plan
	next.LaunchPlans[connectionID] = &copy
	delete(next.Attempts, connectionID)
	if err := h.saveGatewayStateLocked(next); err != nil {
		return gatewayControl{}, fmt.Errorf("persist Gateway launch plan: %w", err)
	}
	return *nextControl, nil
}

// snapshotGatewayBindingForProcessPlan preserves an existing recovery latch.
// It authorizes only a private plan commit; automatic effects still require
// recovery=none, while a future explicit repair may use the plan and clear the
// latch only through an accepted exact proof.
func (h *Hub) snapshotGatewayBindingForProcessPlan(connectionID string) (gatewayBindingSnapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return gatewayBindingSnapshot{}, err
	}
	connectionID = strings.TrimSpace(connectionID)
	control := h.gatewayState.Controls[connectionID]
	if control == nil || control.Lifecycle != gatewayBindingAdopted || control.ActiveAttemptID != "" {
		return gatewayBindingSnapshot{}, errf(409, "Gateway binding is not eligible for a process plan")
	}
	binding, err := h.gatewayBindingLocked(connectionID)
	if err != nil || !gatewayBindingsEqual(binding, control.Binding) {
		return gatewayBindingSnapshot{}, errf(409, "Gateway configured binding changed")
	}
	return gatewayBindingSnapshot{OpenGeneration: h.gatewayOpenGeneration, ControlEpoch: control.Epoch, Binding: binding}, nil
}

func (h *Hub) gatewayServiceAdapter(plan gatewayLaunchPlan) (gatewayServiceAdapter, error) {
	if h.gatewayServiceAdapterForTest != nil {
		return h.gatewayServiceAdapterForTest(plan)
	}
	return defaultGatewayServiceAdapter(plan)
}

func (h *Hub) beginGatewayProcessAttempt(ctx context.Context, connectionID string, kind gatewayAttemptKind) (gatewayTransitionAttempt, error) {
	if !validGatewayAttemptKind(kind) {
		return gatewayTransitionAttempt{}, errf(400, "invalid Gateway transition kind")
	}
	connectionID = strings.TrimSpace(connectionID)
	if ctx == nil {
		ctx = context.Background()
	}
	unlock := h.gatewayCoordinatorForUse().lock(connectionID)
	defer unlock()
	h.mu.Lock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		h.mu.Unlock()
		return gatewayTransitionAttempt{}, err
	}
	if h.stopping {
		h.mu.Unlock()
		return gatewayTransitionAttempt{}, errf(503, "Hub is stopping")
	}
	control := h.gatewayState.Controls[connectionID]
	plan := h.gatewayState.LaunchPlans[connectionID]
	if control == nil || plan == nil || control.Lifecycle != gatewayBindingAdopted || control.ActiveAttemptID != "" {
		h.mu.Unlock()
		return gatewayTransitionAttempt{}, errf(409, "Gateway process is not eligible")
	}
	if err := gatewayAttemptAllowedForControl(control, kind); err != nil {
		h.mu.Unlock()
		return gatewayTransitionAttempt{}, err
	}
	current, err := h.gatewayBindingLocked(connectionID)
	if err != nil || !gatewayBindingsEqual(current, control.Binding) || !gatewayBindingEligible(current) {
		h.mu.Unlock()
		return gatewayTransitionAttempt{}, errf(409, "Gateway configured binding is incomplete or changed")
	}
	planCopy := *plan
	if err := validateGatewayLaunchPlan(planCopy); err != nil {
		h.mu.Unlock()
		return gatewayTransitionAttempt{}, errf(409, "Gateway launch plan is invalid: %s", err)
	}
	preparedEpoch := control.Epoch
	preparedBinding := cloneGatewayBinding(current)
	h.mu.Unlock()
	if err := verifyGatewayLaunchPlanExecutables(planCopy); err != nil {
		reason := "Gateway launch executable integrity failed: " + gatewaySafeEffectError(err)
		latchErr := h.latchGatewayProcessPrecondition(connectionID, preparedEpoch, reason)
		return gatewayTransitionAttempt{}, errors.Join(errf(409, "%s", reason), latchErr)
	}
	adapter, err := h.gatewayServiceAdapter(planCopy)
	if err != nil {
		reason := "Gateway service adapter is unavailable: " + gatewaySafeEffectError(err)
		latchErr := h.latchGatewayProcessPrecondition(connectionID, preparedEpoch, reason)
		return gatewayTransitionAttempt{}, errors.Join(errf(409, "%s", reason), latchErr)
	}
	if err := adapter.ValidateAnchor(ctx, planCopy); err != nil {
		reason := "Gateway registration anchor changed before effect intent: " + gatewaySafeEffectError(err)
		latchErr := h.latchGatewayProcessPrecondition(connectionID, preparedEpoch, reason)
		return gatewayTransitionAttempt{}, errors.Join(errf(409, "%s", reason), latchErr)
	}
	h.mu.Lock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		h.mu.Unlock()
		return gatewayTransitionAttempt{}, err
	}
	if h.stopping {
		h.mu.Unlock()
		return gatewayTransitionAttempt{}, errf(503, "Hub is stopping")
	}
	control = h.gatewayState.Controls[connectionID]
	plan = h.gatewayState.LaunchPlans[connectionID]
	if control == nil || plan == nil || control.Epoch != preparedEpoch || control.Lifecycle != gatewayBindingAdopted || control.ActiveAttemptID != "" ||
		gatewayAttemptAllowedForControl(control, kind) != nil || !gatewayLaunchPlansEqual(*plan, planCopy) {
		h.mu.Unlock()
		return gatewayTransitionAttempt{}, errf(409, "Gateway process control changed during executable verification")
	}
	current, err = h.gatewayBindingLocked(connectionID)
	if err != nil || !gatewayBindingsEqual(current, preparedBinding) || !gatewayBindingsEqual(current, control.Binding) || !gatewayBindingEligible(current) {
		h.mu.Unlock()
		return gatewayTransitionAttempt{}, errf(409, "Gateway configured binding changed during executable verification")
	}
	previousAttempt := h.gatewayState.Attempts[connectionID]
	attemptID := newIntegrationID("gattempt")
	for (previousAttempt != nil && attemptID == previousAttempt.ID) || attemptID == planCopy.Anchor.AttemptID {
		attemptID = newIntegrationID("gattempt")
	}
	targetGeneration := newIntegrationID("ggen")
	for targetGeneration == planCopy.Anchor.Generation {
		targetGeneration = newIntegrationID("ggen")
	}
	recoveryGeneration := newIntegrationID("grecover")
	for recoveryGeneration == targetGeneration || recoveryGeneration == planCopy.Anchor.Generation {
		recoveryGeneration = newIntegrationID("grecover")
	}
	timestamp := now()
	next := upgradeGatewayStateForProcess(h.gatewayState)
	nextControl := next.Controls[connectionID]
	nextEpoch, err := advanceGatewayEpoch(nextControl.Epoch)
	if err != nil {
		h.mu.Unlock()
		return gatewayTransitionAttempt{}, err
	}
	attempt := &gatewayTransitionAttempt{
		ID: attemptID, ConnectionID: connectionID, Kind: kind, Phase: gatewayAttemptTargetIntent,
		BindingEpoch: nextEpoch, Binding: cloneGatewayBinding(current), Plan: planCopy,
		TargetGeneration: targetGeneration, RecoveryGeneration: recoveryGeneration,
		EffectStartedAt: timestamp, UpdatedAt: timestamp,
	}
	nextControl.Epoch = nextEpoch
	nextControl.ActiveAttemptID = attempt.ID
	if nextControl.Recovery != gatewayRecoveryManual {
		nextControl.Recovery = gatewayRecoveryReconcile
	}
	nextControl.Reason = "Gateway process transition is awaiting exact proof"
	nextControl.UpdatedAt = timestamp
	next.Attempts[connectionID] = attempt
	if err := h.saveGatewayStateLocked(next); err != nil {
		h.mu.Unlock()
		return gatewayTransitionAttempt{}, fmt.Errorf("persist Gateway process effect intent: %w", err)
	}
	h.applyGatewayHealthProjectionLocked(connectionID)
	h.mu.Unlock()

	effectContext, cancelEffect := gatewayBoundedEffectContext(ctx)
	result := invokeGatewayServiceEffect(func() gatewayServiceEffectResult {
		return adapter.Apply(effectContext, gatewayServiceEffect{AttemptID: attempt.ID, ConnectionID: connectionID, Generation: targetGeneration, Descriptor: planCopy.Target, Plan: planCopy})
	})
	cancelEffect()
	return h.finishGatewayTargetEffect(ctx, connectionID, attempt.ID, adapter, result)
}

func gatewayAttemptAllowedForControl(control *gatewayControl, kind gatewayAttemptKind) error {
	if control == nil {
		return errf(409, "Gateway process control is unavailable")
	}
	if kind != gatewayAttemptManual && control.Recovery != gatewayRecoveryNone {
		return errf(409, "Gateway process requires explicit repair")
	}
	if kind == gatewayAttemptManual && control.Recovery != gatewayRecoveryNone && control.Recovery != gatewayRecoveryReconcile && control.Recovery != gatewayRecoveryManual {
		return errf(409, "Gateway process recovery state is invalid")
	}
	return nil
}

// latchGatewayProcessPrecondition is called while the per-Connection
// coordinator is held. It records a zero-effect fail-closed stop so automatic
// startup cannot repeatedly retry an unverifiable plan.
func (h *Hub) latchGatewayProcessPrecondition(connectionID string, expectedEpoch uint64, reason string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return err
	}
	control := h.gatewayState.Controls[connectionID]
	if control == nil || control.Epoch != expectedEpoch || control.ActiveAttemptID != "" {
		return errf(409, "Gateway process control changed before precondition stop")
	}
	next := cloneGatewayState(h.gatewayState)
	nextControl := next.Controls[connectionID]
	nextEpoch, err := advanceGatewayEpoch(nextControl.Epoch)
	if err != nil {
		return err
	}
	nextControl.Epoch = nextEpoch
	nextControl.Recovery = gatewayRecoveryManual
	nextControl.Reason = strings.TrimSpace(reason)
	nextControl.UpdatedAt = now()
	if err := h.saveGatewayStateLocked(next); err != nil {
		return fmt.Errorf("persist Gateway process precondition stop: %w", err)
	}
	h.applyGatewayHealthProjectionLocked(connectionID)
	return nil
}

func (h *Hub) finishGatewayTargetEffect(ctx context.Context, connectionID, attemptID string, adapter gatewayServiceAdapter, result gatewayServiceEffectResult) (gatewayTransitionAttempt, error) {
	h.mu.Lock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		h.mu.Unlock()
		return gatewayTransitionAttempt{}, err
	}
	attempt := h.gatewayState.Attempts[connectionID]
	control := h.gatewayState.Controls[connectionID]
	if attempt == nil || control == nil || attempt.ID != attemptID || control.ActiveAttemptID != attemptID || attempt.Phase != gatewayAttemptTargetIntent {
		h.mu.Unlock()
		return gatewayTransitionAttempt{}, errf(409, "Gateway process attempt changed during target effect")
	}
	next := cloneGatewayState(h.gatewayState)
	nextAttempt := next.Attempts[connectionID]
	timestamp := now()
	nextAttempt.UpdatedAt = timestamp
	switch result.Outcome {
	case gatewayServiceEffectApplied:
		nextAttempt.Phase = gatewayAttemptAwaitingTargetProof
		nextAttempt.ReconcileEffect = ""
		nextAttempt.ProofDeadline = h.gatewayProofDeadlineLocked(nextAttempt.EffectStartedAt)
		if err := h.saveGatewayStateLocked(next); err != nil {
			h.mu.Unlock()
			return *nextAttempt, fmt.Errorf("persist Gateway target effect result: %w", err)
		}
		h.startGatewayProofDeadlineLocked(connectionID, attemptID, gatewayAttemptAwaitingTargetProof, nextAttempt.ProofDeadline)
		h.mu.Unlock()
		return *nextAttempt, nil
	case gatewayServiceEffectIndeterminate:
		nextAttempt.Phase = gatewayAttemptReconcileRequired
		nextAttempt.ReconcileEffect = gatewayEffectTarget
		nextAttempt.ProofDeadline = ""
		nextAttempt.LastError = result.Err.Error()
		next.Controls[connectionID].Recovery = gatewayRecoveryReconcile
		next.Controls[connectionID].Reason = "Gateway target effect outcome is indeterminate"
		next.Controls[connectionID].UpdatedAt = timestamp
		if err := h.saveGatewayStateLocked(next); err != nil {
			h.mu.Unlock()
			return *nextAttempt, fmt.Errorf("persist indeterminate Gateway target result: %w", err)
		}
		h.applyGatewayHealthProjectionLocked(connectionID)
		h.mu.Unlock()
		return *nextAttempt, fmt.Errorf("Gateway target effect is indeterminate: %w", result.Err)
	case gatewayServiceEffectRejected:
		nextAttempt.Phase = gatewayAttemptManualRecoveryRequired
		nextAttempt.ReconcileEffect = ""
		nextAttempt.LastError = result.Err.Error()
		nextAttempt.CompletedAt = timestamp
		nextAttempt.ProofDeadline = ""
		nextControl := next.Controls[connectionID]
		nextControl.ActiveAttemptID = ""
		nextControl.Recovery = gatewayRecoveryManual
		nextControl.Reason = "Gateway target precondition changed before any service mutation: " + result.Err.Error()
		nextEpoch, err := advanceGatewayEpoch(nextControl.Epoch)
		if err != nil {
			h.mu.Unlock()
			return gatewayTransitionAttempt{}, err
		}
		nextControl.Epoch = nextEpoch
		nextControl.UpdatedAt = timestamp
		if err := h.saveGatewayStateLocked(next); err != nil {
			h.mu.Unlock()
			return *nextAttempt, fmt.Errorf("persist rejected Gateway target precondition: %w", err)
		}
		h.applyGatewayHealthProjectionLocked(connectionID)
		h.mu.Unlock()
		return *nextAttempt, fmt.Errorf("Gateway target precondition changed: %w", result.Err)
	case gatewayServiceEffectFailed:
		nextAttempt.Phase = gatewayAttemptRecoveryIntent
		nextAttempt.ReconcileEffect = ""
		nextAttempt.ProofDeadline = ""
		nextAttempt.RecoveryStartedAt = timestamp
		nextAttempt.LastError = result.Err.Error()
		next.Controls[connectionID].Recovery = gatewayRecoveryReconcile
		next.Controls[connectionID].Reason = "Gateway target failed; restoring prior registration"
		next.Controls[connectionID].UpdatedAt = timestamp
		if err := h.saveGatewayStateLocked(next); err != nil {
			h.mu.Unlock()
			return *nextAttempt, fmt.Errorf("persist Gateway recovery intent: %w", err)
		}
		h.mu.Unlock()
		recoveryContext, cancelRecovery := gatewayBoundedEffectContext(ctx)
		recoveryResult := invokeGatewayServiceEffect(func() gatewayServiceEffectResult {
			return adapter.Restore(recoveryContext, gatewayServiceEffect{AttemptID: attemptID, ConnectionID: connectionID,
				Generation: nextAttempt.RecoveryGeneration, Descriptor: nextAttempt.Plan.Anchor.Descriptor, Plan: nextAttempt.Plan})
		})
		cancelRecovery()
		finished, finishErr := h.finishGatewayRecoveryEffect(connectionID, attemptID, recoveryResult)
		return finished, errors.Join(fmt.Errorf("Gateway target effect failed: %w", result.Err), finishErr)
	default:
		h.mu.Unlock()
		return gatewayTransitionAttempt{}, errf(500, "invalid Gateway service effect outcome")
	}
}

func (h *Hub) finishGatewayRecoveryEffect(connectionID, attemptID string, result gatewayServiceEffectResult) (gatewayTransitionAttempt, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return gatewayTransitionAttempt{}, err
	}
	attempt := h.gatewayState.Attempts[connectionID]
	control := h.gatewayState.Controls[connectionID]
	if attempt == nil || control == nil || attempt.ID != attemptID || control.ActiveAttemptID != attemptID || attempt.Phase != gatewayAttemptRecoveryIntent {
		return gatewayTransitionAttempt{}, errf(409, "Gateway process attempt changed during recovery effect")
	}
	next := cloneGatewayState(h.gatewayState)
	nextAttempt := next.Attempts[connectionID]
	nextControl := next.Controls[connectionID]
	timestamp := now()
	nextAttempt.UpdatedAt = timestamp
	switch result.Outcome {
	case gatewayServiceEffectApplied:
		nextAttempt.Phase = gatewayAttemptAwaitingRecoveryProof
		nextAttempt.ReconcileEffect = ""
		if nextAttempt.RecoveryStartedAt == "" {
			nextAttempt.RecoveryStartedAt = timestamp
		}
		nextAttempt.ProofDeadline = h.gatewayProofDeadlineLocked(nextAttempt.RecoveryStartedAt)
		nextControl.Recovery = gatewayRecoveryReconcile
		nextControl.Reason = "Gateway registration recovery is awaiting exact proof"
	case gatewayServiceEffectIndeterminate:
		nextAttempt.Phase = gatewayAttemptReconcileRequired
		nextAttempt.ReconcileEffect = gatewayEffectRecovery
		nextAttempt.ProofDeadline = ""
		nextAttempt.LastError = result.Err.Error()
		nextControl.Recovery = gatewayRecoveryReconcile
		nextControl.Reason = "Gateway registration recovery outcome is indeterminate"
	case gatewayServiceEffectRejected, gatewayServiceEffectFailed:
		nextAttempt.Phase = gatewayAttemptManualRecoveryRequired
		nextAttempt.ReconcileEffect = ""
		nextAttempt.LastError = result.Err.Error()
		nextAttempt.CompletedAt = timestamp
		nextAttempt.ProofDeadline = ""
		nextControl.ActiveAttemptID = ""
		nextControl.Recovery = gatewayRecoveryManual
		nextControl.Reason = "Gateway registration recovery failed: " + result.Err.Error()
		nextEpoch, err := advanceGatewayEpoch(nextControl.Epoch)
		if err != nil {
			return gatewayTransitionAttempt{}, err
		}
		nextControl.Epoch = nextEpoch
	default:
		return gatewayTransitionAttempt{}, errf(500, "invalid Gateway recovery effect outcome")
	}
	nextControl.UpdatedAt = timestamp
	if err := h.saveGatewayStateLocked(next); err != nil {
		return *nextAttempt, fmt.Errorf("persist Gateway recovery result: %w", err)
	}
	if result.Outcome == gatewayServiceEffectApplied {
		h.startGatewayProofDeadlineLocked(connectionID, attemptID, gatewayAttemptAwaitingRecoveryProof, nextAttempt.ProofDeadline)
	}
	h.applyGatewayHealthProjectionLocked(connectionID)
	if result.Outcome == gatewayServiceEffectApplied {
		return *nextAttempt, nil
	}
	return *nextAttempt, fmt.Errorf("Gateway registration recovery %s: %w", result.Outcome, result.Err)
}

func (h *Hub) acceptGatewayProcessProof(connectionID string, proof gatewayProcessProof, observedNow time.Time) (gatewayTransitionAttempt, error) {
	connectionID = strings.TrimSpace(connectionID)
	unlock := h.gatewayCoordinatorForUse().lock(connectionID)
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.acceptGatewayProcessProofLocked(connectionID, proof, observedNow, "", nil)
}

// acceptGatewayProcessProofLocked requires both the per-Connection coordinator
// and h.mu. The private heartbeat path uses it so exact proof, terminal control,
// latch clear, and the connected observation remain one durable commit.
func (h *Hub) acceptGatewayProcessProofLocked(connectionID string, proof gatewayProcessProof, observedNow time.Time, cursor string, capabilities []string) (gatewayTransitionAttempt, error) {
	if err := validateGatewayProcessProofShape(proof); err != nil {
		return gatewayTransitionAttempt{}, errf(400, "%s", err)
	}
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return gatewayTransitionAttempt{}, err
	}
	observedAt, _ := time.Parse(time.RFC3339Nano, proof.ObservedAt)
	if observedNow.IsZero() {
		observedNow = time.Now().UTC()
	}
	if observedAt.After(observedNow.Add(5*time.Second)) || observedNow.Sub(observedAt) > gatewayProcessProofFreshness {
		return gatewayTransitionAttempt{}, errf(409, "Gateway process proof is not fresh")
	}
	attempt := h.gatewayState.Attempts[connectionID]
	control := h.gatewayState.Controls[connectionID]
	if attempt != nil && control != nil && control.ActiveAttemptID == "" && gatewayAttemptTerminal(attempt.Phase) &&
		attempt.AcceptedProof != nil && gatewayProcessProofIdentityEqual(*attempt.AcceptedProof, proof) {
		next := cloneGatewayState(h.gatewayState)
		timestamp := now()
		if nextAttempt := next.Attempts[connectionID]; nextAttempt != nil && nextAttempt.AcceptedProof != nil {
			nextAttempt.AcceptedProof.ObservedAt = timestamp
			nextAttempt.UpdatedAt = timestamp
		}
		next.Observations[connectionID] = gatewayObservationForAcceptedProof(next.Observations[connectionID], connectionID, proof, timestamp, cursor, capabilities)
		if err := h.saveGatewayStateLocked(next); err != nil {
			return *attempt, fmt.Errorf("persist repeated Gateway process proof heartbeat: %w", err)
		}
		h.applyGatewayHealthProjectionLocked(connectionID)
		return *attempt, nil
	}
	if attempt == nil || control == nil || control.ActiveAttemptID != attempt.ID || proof.AttemptID != attempt.ID {
		return gatewayTransitionAttempt{}, errf(409, "Gateway process proof does not match the active attempt")
	}
	effectStartedAt, _ := time.Parse(time.RFC3339Nano, attempt.EffectStartedAt)
	if observedAt.Before(effectStartedAt) {
		return gatewayTransitionAttempt{}, errf(409, "Gateway process proof is not fresh for the active attempt")
	}
	deadline, deadlineErr := time.Parse(time.RFC3339Nano, attempt.ProofDeadline)
	if deadlineErr != nil || observedNow.After(deadline) {
		return gatewayTransitionAttempt{}, errf(409, "Gateway process proof arrived after its bounded deadline")
	}
	expectedGeneration, expectedBuild, expectedDigest := "", "", ""
	terminalPhase := gatewayAttemptSucceeded
	switch attempt.Phase {
	case gatewayAttemptAwaitingTargetProof:
		expectedGeneration, expectedBuild, expectedDigest = attempt.TargetGeneration, attempt.Plan.Target.Build, attempt.Plan.Target.ExecutableDigest
	case gatewayAttemptAwaitingRecoveryProof:
		expectedGeneration, expectedBuild, expectedDigest = attempt.RecoveryGeneration, attempt.Plan.Anchor.Descriptor.Build, attempt.Plan.Anchor.Descriptor.ExecutableDigest
		terminalPhase = gatewayAttemptRecovered
	default:
		return gatewayTransitionAttempt{}, errf(409, "Gateway process attempt is not awaiting proof")
	}
	if proof.Generation != expectedGeneration || proof.Build != expectedBuild || proof.ExecutableDigest != expectedDigest {
		return gatewayTransitionAttempt{}, errf(409, "Gateway process proof does not match generation/build/digest")
	}
	next := cloneGatewayState(h.gatewayState)
	nextAttempt := next.Attempts[connectionID]
	nextControl := next.Controls[connectionID]
	timestamp := now()
	proofCopy := proof
	promotedPlan := *next.LaunchPlans[connectionID]
	promotedDescriptor, promotedGeneration := promotedPlan.Target, nextAttempt.TargetGeneration
	if terminalPhase == gatewayAttemptRecovered {
		promotedDescriptor, promotedGeneration = promotedPlan.Anchor.Descriptor, nextAttempt.RecoveryGeneration
	}
	promotedAnchor, anchorErr := newGatewayRegistrationAnchor(promotedDescriptor)
	if anchorErr != nil {
		return gatewayTransitionAttempt{}, errf(409, "accepted Gateway proof cannot promote its registration anchor: %s", anchorErr)
	}
	promotedAnchor.AttemptID = nextAttempt.ID
	promotedAnchor.Generation = promotedGeneration
	promotedAnchor.IntegritySHA256 = gatewayAnchorIntegrityWithProcess(promotedAnchor.Descriptor, promotedAnchor.AttemptID, promotedAnchor.Generation)
	promotedPlan.Anchor = promotedAnchor
	if promotedPlan.Target.Provider != "" {
		promotedPlan.IntegritySHA256 = gatewayLaunchPlanIntegrity(promotedPlan)
	}
	next.LaunchPlans[connectionID] = &promotedPlan
	nextAttempt.Phase = terminalPhase
	nextAttempt.ReconcileEffect = ""
	nextAttempt.AcceptedProof = &proofCopy
	nextAttempt.CompletedAt = timestamp
	nextAttempt.UpdatedAt = timestamp
	nextAttempt.LastError = ""
	nextAttempt.ProofDeadline = ""
	nextEpoch, err := advanceGatewayEpoch(nextControl.Epoch)
	if err != nil {
		return gatewayTransitionAttempt{}, err
	}
	nextControl.Epoch = nextEpoch
	nextControl.ActiveAttemptID = ""
	nextControl.Recovery = gatewayRecoveryNone
	nextControl.Reason = ""
	nextControl.UpdatedAt = timestamp
	next.Observations[connectionID] = gatewayObservationForAcceptedProof(next.Observations[connectionID], connectionID, proof, timestamp, cursor, capabilities)
	// The accepted proof and latch clear are one private foundation commit.
	if err := h.saveGatewayStateLocked(next); err != nil {
		return *nextAttempt, fmt.Errorf("persist accepted Gateway process proof: %w", err)
	}
	h.applyGatewayHealthProjectionLocked(connectionID)
	return *nextAttempt, nil
}

func gatewayProcessProofIdentityEqual(left, right gatewayProcessProof) bool {
	return left.AttemptID == right.AttemptID && left.Generation == right.Generation && left.Build == right.Build && left.ExecutableDigest == right.ExecutableDigest
}

func gatewayObservationForAcceptedProof(previous *gatewayProcessObservation, connectionID string, proof gatewayProcessProof, timestamp, cursor string, capabilities []string) *gatewayProcessObservation {
	sequence := uint64(1)
	observation := &gatewayProcessObservation{ConnectionID: connectionID, Status: "connected", Cursor: strings.TrimSpace(cursor), HeartbeatAt: proof.ObservedAt, ObservedAt: timestamp}
	if previous != nil {
		sequence = previous.Sequence + 1
		if observation.Cursor == "" {
			observation.Cursor = previous.Cursor
		}
		observation.LastEventAt = previous.LastEventAt
		if capabilities == nil {
			observation.ObservedCapabilities = append([]string(nil), previous.ObservedCapabilities...)
		}
	}
	if capabilities != nil {
		observation.ObservedCapabilities = normalizeCapabilities(capabilities)
	}
	observation.Sequence = sequence
	return observation
}

func (h *Hub) gatewayProcessEligibleConnectionIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.gatewayFoundationPoisoned || (h.gatewayState.Version != gatewayProcessStateVersion && h.gatewayState.Version != gatewayLaunchProofStateVersion) {
		return nil
	}
	result := []string{}
	for id, control := range h.gatewayState.Controls {
		connection := h.connections[id]
		plan := h.gatewayState.LaunchPlans[id]
		if connection == nil || plan == nil || control.Lifecycle != gatewayBindingAdopted || control.Recovery != gatewayRecoveryNone ||
			control.ActiveAttemptID != "" || !connection.Enabled || connection.ArchivedAt != "" || connection.SupersededBy != "" ||
			validateGatewayLaunchPlan(*plan) != nil {
			continue
		}
		binding, err := h.gatewayBindingLocked(id)
		if err == nil && gatewayBindingsEqual(binding, control.Binding) && gatewayBindingEligible(binding) {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}

// RestartGatewayProcesses is an internal-package entry used by Hub startup.
// It has no HTTP/CLI route. It first reconciles durable attempts and only then
// begins automatic attempts for adopted controls with a complete typed plan.
func (h *Hub) RestartGatewayProcesses() error {
	ctx := context.Background()
	result := h.reconcileGatewayProcessAttempts(ctx)
	for _, connectionID := range h.gatewayProcessEligibleConnectionIDs() {
		if _, err := h.beginGatewayProcessAttempt(ctx, connectionID, gatewayAttemptAutomatic); err != nil {
			result = errors.Join(result, fmt.Errorf("restart Gateway %s: %w", connectionID, err))
		}
	}
	return result
}

func (h *Hub) reconcileGatewayProcessAttempts(ctx context.Context) error {
	h.mu.Lock()
	ids := []string{}
	for id, control := range h.gatewayState.Controls {
		if control != nil && control.ActiveAttemptID != "" {
			ids = append(ids, id)
		}
	}
	h.mu.Unlock()
	sort.Strings(ids)
	var result error
	for _, id := range ids {
		if err := h.reconcileGatewayProcessAttempt(ctx, id); err != nil {
			result = errors.Join(result, fmt.Errorf("reconcile Gateway %s: %w", id, err))
		}
	}
	return result
}

func (h *Hub) reconcileGatewayProcessAttempt(ctx context.Context, connectionID string) error {
	unlock := h.gatewayCoordinatorForUse().lock(connectionID)
	defer unlock()
	h.mu.Lock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		h.mu.Unlock()
		return err
	}
	if h.stopping {
		h.mu.Unlock()
		return nil
	}
	control := h.gatewayState.Controls[connectionID]
	attempt := h.gatewayState.Attempts[connectionID]
	plan := h.gatewayState.LaunchPlans[connectionID]
	if control == nil || attempt == nil || plan == nil || control.ActiveAttemptID != attempt.ID || gatewayAttemptTerminal(attempt.Phase) {
		h.mu.Unlock()
		return nil
	}
	connection := h.connections[connectionID]
	binding, bindingErr := h.gatewayBindingLocked(connectionID)
	if control.Lifecycle != gatewayBindingAdopted || connection == nil || !connection.Enabled || connection.ArchivedAt != "" || connection.SupersededBy != "" ||
		bindingErr != nil || !gatewayBindingsEqual(binding, control.Binding) || !gatewayBindingsEqual(binding, attempt.Binding) || !gatewayBindingEligible(binding) {
		markErr := h.markGatewayAttemptManualLocked(connectionID, attempt.ID, "Gateway process control is no longer eligible for reconciliation")
		h.mu.Unlock()
		return markErr
	}
	adapter, err := h.gatewayServiceAdapter(*plan)
	if err != nil {
		markErr := h.markGatewayAttemptManualLocked(connectionID, attempt.ID, "Gateway service adapter is unavailable: "+gatewaySafeEffectError(err))
		h.mu.Unlock()
		return markErr
	}
	request := gatewayServiceInspectionRequest{AttemptID: attempt.ID, ConnectionID: connectionID, Plan: attempt.Plan,
		TargetGeneration: attempt.TargetGeneration, RecoveryGeneration: attempt.RecoveryGeneration}
	h.mu.Unlock()
	inspectionContext, cancelInspection := gatewayBoundedEffectContext(ctx)
	inspection, inspectErr := invokeGatewayServiceInspection(func() (gatewayServiceInspection, error) { return adapter.Inspect(inspectionContext, request) })
	cancelInspection()
	if inspectErr != nil || inspection.State == gatewayServiceObservedUnknown {
		reason := "Gateway service state cannot be reconciled"
		if inspectErr != nil {
			reason += ": " + gatewaySafeEffectError(inspectErr)
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.markGatewayAttemptManualLocked(connectionID, attempt.ID, reason)
	}
	if inspectionMatchesTarget(inspection, *attempt) && attempt.ReconcileEffect != gatewayEffectRecovery && attempt.Phase != gatewayAttemptRecoveryIntent && attempt.Phase != gatewayAttemptAwaitingRecoveryProof {
		return h.persistGatewayAwaitingProof(connectionID, attempt.ID, gatewayAttemptAwaitingTargetProof)
	}
	if inspectionMatchesRecovery(inspection, *attempt) {
		return h.persistGatewayAwaitingProof(connectionID, attempt.ID, gatewayAttemptAwaitingRecoveryProof)
	}
	// A known target/anchor/absent registration after inspection is safe to
	// drive only toward the prior anchor. R1 never replays the target effect.
	if inspection.State == gatewayServiceObservedTarget || inspection.State == gatewayServiceObservedAnchor || inspection.State == gatewayServiceObservedAbsent {
		if err := h.persistGatewayRecoveryIntent(connectionID, attempt.ID, "startup reconciliation is restoring the prior registration"); err != nil {
			return err
		}
		recoveryContext, cancelRecovery := gatewayBoundedEffectContext(ctx)
		result := invokeGatewayServiceEffect(func() gatewayServiceEffectResult {
			return adapter.Restore(recoveryContext, gatewayServiceEffect{AttemptID: attempt.ID, ConnectionID: connectionID,
				Generation: attempt.RecoveryGeneration, Descriptor: attempt.Plan.Anchor.Descriptor, Plan: attempt.Plan})
		})
		cancelRecovery()
		_, err := h.finishGatewayRecoveryEffect(connectionID, attempt.ID, result)
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.markGatewayAttemptManualLocked(connectionID, attempt.ID, "Gateway service inspection did not prove target or recovery registration")
}

func invokeGatewayServiceInspection(fn func() (gatewayServiceInspection, error)) (inspection gatewayServiceInspection, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			inspection = gatewayServiceInspection{State: gatewayServiceObservedUnknown}
			err = fmt.Errorf("Gateway service inspection panicked: %v", recovered)
		}
	}()
	return fn()
}

func gatewayBoundedEffectContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, gatewayProcessEffectTimeout)
}

func inspectionMatchesTarget(value gatewayServiceInspection, attempt gatewayTransitionAttempt) bool {
	return value.State == gatewayServiceObservedTarget && value.Generation == attempt.TargetGeneration &&
		value.Build == attempt.Plan.Target.Build && value.ExecutableDigest == attempt.Plan.Target.ExecutableDigest
}

func inspectionMatchesRecovery(value gatewayServiceInspection, attempt gatewayTransitionAttempt) bool {
	return value.State == gatewayServiceObservedRecovery && value.Generation == attempt.RecoveryGeneration &&
		value.Build == attempt.Plan.Anchor.Descriptor.Build && value.ExecutableDigest == attempt.Plan.Anchor.Descriptor.ExecutableDigest
}

func (h *Hub) persistGatewayAwaitingProof(connectionID, attemptID string, phase gatewayAttemptPhase) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return err
	}
	attempt := h.gatewayState.Attempts[connectionID]
	control := h.gatewayState.Controls[connectionID]
	if attempt == nil || control == nil || attempt.ID != attemptID || control.ActiveAttemptID != attemptID {
		return errf(409, "Gateway process attempt changed during reconciliation")
	}
	next := cloneGatewayState(h.gatewayState)
	nextAttempt := next.Attempts[connectionID]
	nextAttempt.Phase = phase
	nextAttempt.ReconcileEffect = ""
	base := nextAttempt.EffectStartedAt
	if phase == gatewayAttemptAwaitingRecoveryProof {
		base = nextAttempt.RecoveryStartedAt
		if base == "" {
			base = nextAttempt.UpdatedAt
		}
	}
	nextAttempt.ProofDeadline = h.gatewayProofDeadlineLocked(base)
	nextAttempt.UpdatedAt = now()
	if err := h.saveGatewayStateLocked(next); err != nil {
		return err
	}
	h.startGatewayProofDeadlineLocked(connectionID, attemptID, phase, nextAttempt.ProofDeadline)
	return nil
}

func (h *Hub) persistGatewayRecoveryIntent(connectionID, attemptID, reason string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return err
	}
	attempt := h.gatewayState.Attempts[connectionID]
	control := h.gatewayState.Controls[connectionID]
	if attempt == nil || control == nil || attempt.ID != attemptID || control.ActiveAttemptID != attemptID {
		return errf(409, "Gateway process attempt changed during reconciliation")
	}
	next := cloneGatewayState(h.gatewayState)
	next.Attempts[connectionID].Phase = gatewayAttemptRecoveryIntent
	next.Attempts[connectionID].ReconcileEffect = ""
	next.Attempts[connectionID].ProofDeadline = ""
	next.Attempts[connectionID].RecoveryStartedAt = now()
	next.Attempts[connectionID].UpdatedAt = next.Attempts[connectionID].RecoveryStartedAt
	next.Controls[connectionID].Recovery = gatewayRecoveryReconcile
	next.Controls[connectionID].Reason = strings.TrimSpace(reason)
	next.Controls[connectionID].UpdatedAt = now()
	return h.saveGatewayStateLocked(next)
}

// markGatewayAttemptManualLocked requires h.mu and never clears a latch.
func (h *Hub) markGatewayAttemptManualLocked(connectionID, attemptID, reason string) error {
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return err
	}
	attempt := h.gatewayState.Attempts[connectionID]
	control := h.gatewayState.Controls[connectionID]
	if attempt == nil || control == nil || attempt.ID != attemptID || control.ActiveAttemptID != attemptID {
		return errf(409, "Gateway process attempt changed during reconciliation")
	}
	next := cloneGatewayState(h.gatewayState)
	nextAttempt := next.Attempts[connectionID]
	nextControl := next.Controls[connectionID]
	timestamp := now()
	nextAttempt.Phase = gatewayAttemptManualRecoveryRequired
	nextAttempt.ReconcileEffect = ""
	nextAttempt.LastError = strings.TrimSpace(reason)
	nextAttempt.CompletedAt = timestamp
	nextAttempt.ProofDeadline = ""
	nextAttempt.UpdatedAt = timestamp
	nextControl.ActiveAttemptID = ""
	nextControl.Recovery = gatewayRecoveryManual
	nextControl.Reason = strings.TrimSpace(reason)
	nextControl.UpdatedAt = timestamp
	nextEpoch, err := advanceGatewayEpoch(nextControl.Epoch)
	if err != nil {
		return err
	}
	nextControl.Epoch = nextEpoch
	if err := h.saveGatewayStateLocked(next); err != nil {
		return err
	}
	h.applyGatewayHealthProjectionLocked(connectionID)
	return fmt.Errorf("%s", reason)
}

func (h *Hub) gatewayProofDeadlineLocked(base string) string {
	wait := gatewayProcessProofWait
	if h.gatewayProofWaitForTest > 0 {
		wait = h.gatewayProofWaitForTest
	}
	started, err := time.Parse(time.RFC3339Nano, base)
	if err != nil {
		started = time.Now().UTC()
	}
	return started.Add(wait).UTC().Format(time.RFC3339Nano)
}

// startGatewayProofDeadlineLocked requires h.mu. The finite worker is part of
// Hub shutdown, and a stale timer can only observe state; the coordinator and
// durable attempt ID make duplicate recovery effects impossible.
func (h *Hub) startGatewayProofDeadlineLocked(connectionID, attemptID string, phase gatewayAttemptPhase, deadline string) {
	deadlineAt, err := time.Parse(time.RFC3339Nano, deadline)
	if err != nil {
		return
	}
	h.startWorkerLocked(func() {
		delay := time.Until(deadlineAt)
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-h.stop:
			return
		case <-timer.C:
		}
		h.expireGatewayProcessProof(connectionID, attemptID, phase, deadline)
	})
}

func (h *Hub) expireGatewayProcessProof(connectionID, attemptID string, phase gatewayAttemptPhase, deadline string) {
	unlock := h.gatewayCoordinatorForUse().lock(connectionID)
	defer unlock()
	h.mu.Lock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		h.mu.Unlock()
		return
	}
	if h.stopping {
		h.mu.Unlock()
		return
	}
	attempt := h.gatewayState.Attempts[connectionID]
	control := h.gatewayState.Controls[connectionID]
	if attempt == nil || control == nil || attempt.ID != attemptID || control.ActiveAttemptID != attemptID ||
		attempt.Phase != phase || attempt.ProofDeadline != deadline {
		h.mu.Unlock()
		return
	}
	if phase == gatewayAttemptAwaitingRecoveryProof {
		_ = h.markGatewayAttemptManualLocked(connectionID, attemptID, "Gateway registration recovery proof deadline expired")
		h.mu.Unlock()
		return
	}
	if phase != gatewayAttemptAwaitingTargetProof {
		h.mu.Unlock()
		return
	}
	adapter, err := h.gatewayServiceAdapter(attempt.Plan)
	if err != nil {
		_ = h.markGatewayAttemptManualLocked(connectionID, attemptID, "Gateway service adapter is unavailable after proof timeout: "+gatewaySafeEffectError(err))
		h.mu.Unlock()
		return
	}
	attemptCopy := *attempt
	request := gatewayServiceInspectionRequest{AttemptID: attempt.ID, ConnectionID: connectionID, Plan: attempt.Plan,
		TargetGeneration: attempt.TargetGeneration, RecoveryGeneration: attempt.RecoveryGeneration}
	h.mu.Unlock()
	inspectionContext, cancelInspection := gatewayBoundedEffectContext(context.Background())
	inspection, inspectErr := invokeGatewayServiceInspection(func() (gatewayServiceInspection, error) {
		return adapter.Inspect(inspectionContext, request)
	})
	cancelInspection()
	h.mu.Lock()
	attempt = h.gatewayState.Attempts[connectionID]
	control = h.gatewayState.Controls[connectionID]
	if attempt == nil || control == nil || attempt.ID != attemptID || control.ActiveAttemptID != attemptID ||
		attempt.Phase != phase || attempt.ProofDeadline != deadline {
		h.mu.Unlock()
		return
	}
	if inspectErr != nil || inspection.State == gatewayServiceObservedUnknown ||
		(inspection.State == gatewayServiceObservedTarget && !inspectionMatchesTarget(inspection, attemptCopy)) {
		reason := "Gateway target proof expired and service registration cannot be reconciled"
		if inspectErr != nil {
			reason += ": " + gatewaySafeEffectError(inspectErr)
		}
		_ = h.markGatewayAttemptManualLocked(connectionID, attemptID, reason)
		h.mu.Unlock()
		return
	}
	if inspectionMatchesRecovery(inspection, attemptCopy) {
		h.mu.Unlock()
		_ = h.persistGatewayAwaitingProof(connectionID, attemptID, gatewayAttemptAwaitingRecoveryProof)
		return
	}
	if inspection.State != gatewayServiceObservedTarget && inspection.State != gatewayServiceObservedAnchor && inspection.State != gatewayServiceObservedAbsent {
		_ = h.markGatewayAttemptManualLocked(connectionID, attemptID, "Gateway target proof expired without a known registration anchor")
		h.mu.Unlock()
		return
	}
	next := cloneGatewayState(h.gatewayState)
	nextAttempt := next.Attempts[connectionID]
	timestamp := now()
	nextAttempt.Phase = gatewayAttemptRecoveryIntent
	nextAttempt.ReconcileEffect = ""
	nextAttempt.ProofDeadline = ""
	nextAttempt.RecoveryStartedAt = timestamp
	nextAttempt.UpdatedAt = timestamp
	nextAttempt.LastError = "Gateway target proof deadline expired"
	next.Controls[connectionID].Recovery = gatewayRecoveryReconcile
	next.Controls[connectionID].Reason = "Gateway target proof deadline expired; restoring prior registration"
	next.Controls[connectionID].UpdatedAt = timestamp
	if err := h.saveGatewayStateLocked(next); err != nil {
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()
	recoveryContext, cancelRecovery := gatewayBoundedEffectContext(context.Background())
	result := invokeGatewayServiceEffect(func() gatewayServiceEffectResult {
		return adapter.Restore(recoveryContext, gatewayServiceEffect{AttemptID: attemptID, ConnectionID: connectionID,
			Generation: nextAttempt.RecoveryGeneration, Descriptor: nextAttempt.Plan.Anchor.Descriptor, Plan: nextAttempt.Plan})
	})
	cancelRecovery()
	_, _ = h.finishGatewayRecoveryEffect(connectionID, attemptID, result)
}
