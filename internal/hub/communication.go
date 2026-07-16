package hub

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/codex"
)

func (h *Hub) ListComms(agent, status string) []AgentMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	agent = strings.TrimSpace(agent)
	agentID := ""
	if meta := h.resolveLocked(agent); meta != nil {
		agentID = meta.ID
	}
	status = strings.TrimSpace(status)
	out := []AgentMessage{}
	for i := len(h.commOrder) - 1; i >= 0; i-- {
		msg := h.comms[h.commOrder[i]]
		if msg == nil {
			continue
		}
		if agent != "" && msg.From != agent && msg.To != agent && msg.FromAgentID != agent && msg.ToAgentID != agent && msg.FromAgentID != agentID && msg.ToAgentID != agentID {
			continue
		}
		if status != "" && msg.Status != status {
			continue
		}
		out = append(out, *msg)
	}
	return out
}

func (h *Hub) GetAgentMessage(id string) (AgentMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	msg := h.comms[strings.TrimSpace(id)]
	if msg == nil {
		return AgentMessage{}, errf(404, "message not found: %s", id)
	}
	return *msg, nil
}

func (h *Hub) CancelAgentMessage(id string) (AgentMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	msg := h.comms[strings.TrimSpace(id)]
	if msg == nil {
		return AgentMessage{}, errf(404, "message not found: %s", id)
	}
	if msg.Resolution == "cancelled" {
		return *msg, nil
	}
	if msg.Status == "answered" || msg.Resolution == "reply" {
		return AgentMessage{}, errf(409, "message %s already has a reply", msg.ID)
	}
	next := *msg
	if next.DeliveryStatus == "queued" || next.DeliveryStatus == "delivering" {
		next.DeliveryStatus = "cancelled"
	} else if msg.Status != "open" || msg.Response != "required" {
		return AgentMessage{}, errf(409, "message %s is already resolved", msg.ID)
	}
	next.Status = "closed"
	next.Resolution = "cancelled"
	next.ResolvedBy = next.From
	next.ResolvedAt = now()
	next.UpdatedAt = next.ResolvedAt
	if err := h.commitAgentMessageLocked(next); err != nil {
		return AgentMessage{}, errf(500, "save cancelled message: %s", err)
	}
	return next, nil
}

// RetryAgentMessage resets one unresolved internal message to the delivery
// queue without creating a second request or changing its reply chain.
func (h *Hub) RetryAgentMessage(id string) (AgentMessage, error) {
	h.mu.Lock()
	msg := h.comms[strings.TrimSpace(id)]
	if msg == nil {
		h.mu.Unlock()
		return AgentMessage{}, errf(404, "message not found: %s", id)
	}
	if msg.Response != "required" || msg.Status != "open" || msg.ReplyTo != "" {
		h.mu.Unlock()
		return AgentMessage{}, errf(409, "message %s is not an unresolved required request", msg.ID)
	}
	if rt := h.runtimes[msg.ToAgentID]; rt != nil && rt.activeTurn != nil && !rt.activeTurn.finished && rt.activeTurn.turnID == msg.DeliveredTurnID {
		h.mu.Unlock()
		return AgentMessage{}, errf(409, "message %s is still active in Turn %s", msg.ID, msg.DeliveredTurnID)
	}
	if msg.DeliveryStatus == "queued" || msg.DeliveryStatus == "delivering" {
		copy := *msg
		h.mu.Unlock()
		return copy, nil
	}
	if msg.DeliveryStatus != "delivered" && msg.DeliveryStatus != "failed" {
		h.mu.Unlock()
		return AgentMessage{}, errf(409, "message %s cannot be retried from %s", msg.ID, msg.DeliveryStatus)
	}
	next := *msg
	next.HandlingAttempts = cloneAgentMessageHandlingAttempts(msg.HandlingAttempts)
	next.DeliveryStatus = "queued"
	next.DeliveryMode = ""
	next.DeliveredAt = ""
	next.DeliveredAgentID = ""
	next.DeliveredSessionID = ""
	next.DeliveredTurnID = ""
	next.LastDeliveryError = ""
	next.HandlingStatus = "pending"
	next.ActiveHandlingID = ""
	next.LastHandlingError = ""
	next.UpdatedAt = now()
	if err := h.commitAgentMessageLocked(next); err != nil {
		h.mu.Unlock()
		return AgentMessage{}, errf(500, "save retried message: %s", err)
	}
	targetID := next.ToAgentID
	h.mu.Unlock()

	h.startWorker(func() { h.deliverNextQueuedForTarget(targetID, defaultInactivity) })
	return next, nil
}

func cloneAgentMessageHandlingAttempts(attempts []AgentMessageHandlingAttempt) []AgentMessageHandlingAttempt {
	if len(attempts) == 0 {
		return nil
	}
	return append([]AgentMessageHandlingAttempt(nil), attempts...)
}

// markAgentMessageHandlingRunningLocked crosses the delivery boundary. Once
// Codex has accepted turn/start, the message is delivered and all later
// outcomes belong to this handling attempt rather than the delivery queue.
func (h *Hub) markAgentMessageHandlingRunningLocked(turn *turnState, agentID string) error {
	if turn == nil || turn.agentMessageID == "" || turn.turnID == "" {
		return nil
	}
	current := h.comms[turn.agentMessageID]
	if current == nil || (current.DeliveryStatus != "delivering" && current.DeliveryStatus != "delivered") {
		return nil
	}

	next := *current
	next.HandlingAttempts = cloneAgentMessageHandlingAttempts(current.HandlingAttempts)
	deliveredNow := next.DeliveryStatus == "delivering"
	timestamp := now()
	if deliveredNow {
		next.DeliveryStatus = "delivered"
		next.DeliveryMode = "turn_start"
		next.DeliveredAgentID = agentID
		next.DeliveredSessionID = agentID
		next.DeliveredTurnID = turn.turnID
		next.DeliveredAt = timestamp
		next.LastDeliveryError = ""
	}

	attemptIndex := -1
	if turn.handlingAttemptID != "" {
		for i := range next.HandlingAttempts {
			if next.HandlingAttempts[i].ID == turn.handlingAttemptID {
				attemptIndex = i
				break
			}
		}
	}
	if attemptIndex < 0 && next.ActiveHandlingID != "" {
		for i := range next.HandlingAttempts {
			if next.HandlingAttempts[i].ID == next.ActiveHandlingID && next.HandlingAttempts[i].Status == "running" {
				attemptIndex = i
				turn.handlingAttemptID = next.ActiveHandlingID
				break
			}
		}
	}
	if attemptIndex < 0 {
		attempt := AgentMessageHandlingAttempt{
			ID: newIntegrationID("matt"), TurnID: turn.turnID, Status: "running", StartedAt: timestamp,
		}
		next.HandlingAttempts = append(next.HandlingAttempts, attempt)
		attemptIndex = len(next.HandlingAttempts) - 1
		turn.handlingAttemptID = attempt.ID
	} else if next.HandlingAttempts[attemptIndex].TurnID == "" {
		next.HandlingAttempts[attemptIndex].TurnID = turn.turnID
	}

	next.HandlingStatus = "running"
	next.ActiveHandlingID = next.HandlingAttempts[attemptIndex].ID
	next.LastHandlingError = ""
	next.UpdatedAt = timestamp
	if err := h.commitAgentMessageLocked(next); err != nil {
		return err
	}
	if deliveredNow {
		if err := h.markOriginalAnsweredLocked(&next); err != nil {
			return err
		}
	}
	return nil
}

type ResolveAgentMessageParams struct {
	From       string `json:"from"`
	Resolution string `json:"resolution"`
	Reason     string `json:"reason"`
}

// ResolveAgentMessage lets the original sender explicitly close a required
// request whose work completed outside the reply thread or was superseded.
// It never infers a relationship from timing or subject text.
func (h *Hub) ResolveAgentMessage(id string, p ResolveAgentMessageParams) (AgentMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	msg := h.comms[strings.TrimSpace(id)]
	if msg == nil {
		return AgentMessage{}, errf(404, "message not found: %s", id)
	}
	resolution := strings.TrimSpace(p.Resolution)
	if resolution != "completed_elsewhere" && resolution != "superseded" {
		return AgentMessage{}, errf(400, "resolution must be completed_elsewhere or superseded")
	}
	reason := strings.TrimSpace(p.Reason)
	if reason == "" {
		return AgentMessage{}, errf(400, "reason is required")
	}
	from := h.resolveLocked(strings.TrimSpace(p.From))
	if from == nil || from.ID != msg.FromAgentID {
		return AgentMessage{}, errf(403, "only %s can resolve message %s", msg.From, msg.ID)
	}
	if msg.Resolution == resolution {
		return *msg, nil
	}
	if msg.ReplyTo != "" || msg.Response != "required" {
		return AgentMessage{}, errf(409, "message %s is not an open required request", msg.ID)
	}
	if msg.Status != "open" {
		return AgentMessage{}, errf(409, "message %s is already resolved", msg.ID)
	}
	next := *msg
	if next.DeliveryStatus == "queued" || next.DeliveryStatus == "delivering" {
		next.DeliveryStatus = "cancelled"
	}
	next.Status = "closed"
	next.Resolution = resolution
	next.ResolutionReason = reason
	next.ResolvedBy = from.Name
	next.ResolvedAt = now()
	next.UpdatedAt = next.ResolvedAt
	if err := h.commitAgentMessageLocked(next); err != nil {
		return AgentMessage{}, errf(500, "save resolved message: %s", err)
	}
	return next, nil
}

// NoReplyAgentMessage explicitly closes a response-required internal message.
// It is idempotent for the same recipient and mutually exclusive with a reply.
func (h *Hub) NoReplyAgentMessage(id, fromKey string) (AgentMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	msg := h.comms[strings.TrimSpace(id)]
	if msg == nil {
		return AgentMessage{}, errf(404, "message not found: %s", id)
	}
	from := h.resolveLocked(strings.TrimSpace(fromKey))
	if from == nil || from.ID != msg.ToAgentID {
		return AgentMessage{}, errf(403, "only %s can close message %s", msg.To, msg.ID)
	}
	if msg.Resolution == "no_reply" {
		return *msg, nil
	}
	if msg.Status == "answered" || msg.Resolution == "reply" {
		return AgentMessage{}, errf(409, "message %s already has a reply", msg.ID)
	}
	if msg.Response != "required" {
		return AgentMessage{}, errf(409, "message %s does not require a response", msg.ID)
	}
	next := *msg
	next.Status = "closed"
	next.Resolution = "no_reply"
	next.ResolvedBy = from.Name
	next.ResolvedAt = now()
	next.UpdatedAt = next.ResolvedAt
	if err := h.commitAgentMessageLocked(next); err != nil {
		return AgentMessage{}, errf(500, "save no-reply decision: %s", err)
	}
	return next, nil
}

func (h *Hub) SendAgentMessage(p CommParams) (CommResult, error) {
	if p.Timeout == 0 && p.TimeoutSec > 0 {
		p.Timeout = time.Duration(p.TimeoutSec) * time.Second
	}
	if p.ReplyTo != "" {
		return h.replyAgentMessage(p)
	}
	return h.createAgentMessage(p)
}

func (h *Hub) createAgentMessage(p CommParams) (CommResult, error) {
	from, to, err := h.validateCommEndpoints(p.From, p.To, p.System && p.From == schedulerIdentity)
	if err != nil {
		return CommResult{}, err
	}
	subject := strings.TrimSpace(p.Subject)
	body := strings.TrimSpace(p.Body)
	if subject == "" {
		return CommResult{}, errf(400, "subject is required")
	}
	if body == "" {
		return CommResult{}, errf(400, "body is required")
	}
	response := strings.TrimSpace(p.Response)
	if response == "" {
		response = "required"
	}
	if response != "required" && response != "none" {
		return CommResult{}, errf(400, "response must be required or none")
	}
	id := newMessageID()
	status := "closed"
	if response == "required" {
		status = "open"
	}
	msg := &AgentMessage{
		ID:             id,
		FromAgentID:    endpointID(from, p.From),
		ToAgentID:      to.ID,
		From:           endpointName(from, p.From),
		To:             to.Name,
		Subject:        subject,
		Body:           body,
		Response:       response,
		Status:         status,
		DeliveryStatus: "queued",
		HandlingStatus: "pending",
		CreatedAt:      now(),
		UpdatedAt:      now(),
	}
	h.mu.Lock()
	h.captureMessageSourceTurnLocked(msg)
	if err := h.commitAgentMessageLocked(*msg); err != nil {
		h.mu.Unlock()
		return CommResult{}, errf(500, "save message: %s", err)
	}
	h.mu.Unlock()

	delivered, _ := h.deliverNextQueuedForTarget(to.ID, p.Timeout)
	current, err := h.GetAgentMessage(id)
	if err != nil {
		return CommResult{}, err
	}
	result := CommResult{Message: &current}
	if delivered != nil && delivered.ID == id {
		result.TurnID = delivered.DeliveredTurnID
	}
	return result, nil
}

func (h *Hub) replyAgentMessage(p CommParams) (CommResult, error) {
	fromName := strings.TrimSpace(p.From)
	body := strings.TrimSpace(p.Body)
	if fromName == "" {
		return CommResult{}, errf(400, "from is required")
	}
	if body == "" {
		return CommResult{}, errf(400, "body is required")
	}

	h.mu.Lock()
	storedOriginal := h.comms[p.ReplyTo]
	if storedOriginal == nil {
		h.mu.Unlock()
		return CommResult{}, errf(404, "message not found: %s", p.ReplyTo)
	}
	origCopy := *storedOriginal
	orig := &origCopy
	if orig.Response != "required" {
		h.mu.Unlock()
		return CommResult{}, errf(409, "message %s does not require a response", orig.ID)
	}
	if orig.Status != "open" || orig.Resolution == "no_reply" {
		h.mu.Unlock()
		return CommResult{}, errf(409, "message %s is already resolved", orig.ID)
	}
	if orig.FromAgentID == "" {
		if orig.From == schedulerIdentity {
			orig.FromAgentID = schedulerAgentID
		} else if sender := h.resolveLocked(orig.From); sender != nil {
			orig.FromAgentID = sender.ID
		}
	}
	if orig.ToAgentID == "" {
		if target := h.resolveLocked(orig.To); target != nil {
			orig.ToAgentID = target.ID
		}
	}
	origID := orig.ID
	origFrom := orig.From
	origSubject := orig.Subject
	from := h.resolveLocked(fromName)
	if from == nil {
		h.mu.Unlock()
		return CommResult{}, errf(404, "from agent not found: %s", fromName)
	}
	if from.ID != orig.ToAgentID {
		h.mu.Unlock()
		return CommResult{}, errf(400, "message %s expects replies from %s", origID, orig.To)
	}
	for _, existingID := range h.commOrder {
		existing := h.comms[existingID]
		if existing != nil && existing.ReplyTo == origID && existing.FromAgentID == from.ID {
			copy := *existing
			h.mu.Unlock()
			return CommResult{Message: &copy, TurnID: copy.DeliveredTurnID}, nil
		}
	}
	subject := strings.TrimSpace(p.Subject)
	if subject == "" {
		subject = "Re: " + origSubject
	}
	toName := origFrom
	var to *Agent
	if orig.FromAgentID != schedulerAgentID {
		to = h.resolveLocked(orig.FromAgentID)
		if to == nil {
			h.mu.Unlock()
			return CommResult{}, errf(404, "original sender agent not found: %s", origFrom)
		}
		toName = to.Name
	}
	msg := &AgentMessage{
		ID:             newMessageID(),
		FromAgentID:    from.ID,
		ToAgentID:      orig.FromAgentID,
		From:           from.Name,
		To:             toName,
		Subject:        subject,
		Body:           body,
		Response:       "none",
		ReplyTo:        origID,
		Status:         "closed",
		Resolution:     "reply",
		DeliveryStatus: "queued",
		HandlingStatus: "pending",
		CreatedAt:      now(),
		UpdatedAt:      now(),
	}
	if orig.FromAgentID == schedulerAgentID {
		msg.DeliveryStatus = "delivered"
		msg.DeliveredAt = msg.CreatedAt
		msg.UpdatedAt = msg.CreatedAt
		if err := h.commitAgentMessageLocked(*msg); err != nil {
			h.mu.Unlock()
			return CommResult{}, errf(500, "save reply: %s", err)
		}
		if err := h.markOriginalAnsweredLocked(msg); err != nil {
			h.mu.Unlock()
			return CommResult{}, errf(500, "save replied request: %s", err)
		}
		cp := *msg
		h.mu.Unlock()
		return CommResult{Message: &cp}, nil
	}
	h.captureMessageSourceTurnLocked(msg)
	if err := h.commitAgentMessageLocked(*msg); err != nil {
		h.mu.Unlock()
		return CommResult{}, errf(500, "save reply: %s", err)
	}
	h.mu.Unlock()

	delivered, _ := h.deliverNextQueuedForTarget(to.ID, p.Timeout)
	current, err := h.GetAgentMessage(msg.ID)
	if err != nil {
		return CommResult{}, err
	}
	result := CommResult{Message: &current}
	if delivered != nil && delivered.ID == msg.ID {
		result.TurnID = delivered.DeliveredTurnID
	}
	return result, nil
}

func (h *Hub) validateCommEndpoints(fromKey, toKey string, allowSystemFrom bool) (*Agent, *Agent, error) {
	fromKey = strings.TrimSpace(fromKey)
	toKey = strings.TrimSpace(toKey)
	if fromKey == "" {
		return nil, nil, errf(400, "from is required")
	}
	if toKey == "" {
		return nil, nil, errf(400, "to is required")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	from := h.resolveLocked(fromKey)
	if from == nil && !(allowSystemFrom && fromKey == schedulerIdentity) {
		return nil, nil, errf(404, "from agent not found: %s", fromKey)
	}
	to := h.resolveLocked(toKey)
	if to == nil {
		return nil, nil, errf(404, "to agent not found: %s", toKey)
	}
	return from, to, nil
}

func endpointName(s *Agent, fallback string) string {
	if s != nil {
		return s.Name
	}
	return strings.TrimSpace(fallback)
}

func endpointID(s *Agent, fallback string) string {
	if s != nil {
		return s.ID
	}
	if strings.TrimSpace(fallback) == schedulerIdentity {
		return schedulerAgentID
	}
	return legacyAgentID(fallback)
}

func (h *Hub) deliveryLoop() {
	h.drainQueuedAll()
	h.drainHumanAnswers()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			h.drainQueuedAll()
			h.drainHumanAnswers()
		case <-h.stop:
			return
		}
	}
}

func (h *Hub) drainQueuedAll() {
	targets := h.queuedTargets()
	for _, target := range targets {
		h.deliverNextQueuedForTarget(target, defaultInactivity)
	}
}

func (h *Hub) queuedTargets() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := map[string]bool{}
	out := []string{}
	for _, id := range h.commOrder {
		msg := h.comms[id]
		if msg == nil || msg.DeliveryStatus != "queued" || seen[msg.ToAgentID] {
			continue
		}
		seen[msg.ToAgentID] = true
		out = append(out, msg.ToAgentID)
	}
	return out
}

func (h *Hub) deliverNextQueuedForTarget(target string, timeout time.Duration) (*AgentMessage, bool) {
	if steered, delivered := h.tryDeliverReplyToActiveTurn(target, timeout); delivered {
		return steered, true
	}
	view, err := h.GetAgent(target)
	if err != nil {
		if isNotFoundErr(err) {
			h.failQueuedForTarget(target, err)
		}
		return nil, false
	}
	if view.Status == "running" {
		return nil, false
	}

	h.mu.Lock()
	if h.isDrainingLocked() {
		h.mu.Unlock()
		return nil, false
	}
	targetMeta := h.resolveLocked(target)
	if targetMeta == nil {
		h.mu.Unlock()
		h.failQueuedForTarget(target, errf(404, "agent not found: %s", target))
		return nil, false
	}
	if rt := h.runtimes[targetMeta.ID]; rt != nil && rt.activeTurn != nil && !rt.activeTurn.finished {
		h.mu.Unlock()
		return nil, false
	}
	goalReserved := h.activeGoalReservesThreadLocked(targetMeta.ID)
	var snapshot *AgentMessage
	for _, id := range h.commOrder {
		msg := h.comms[id]
		if msg == nil || msg.ToAgentID != targetMeta.ID || msg.DeliveryStatus != "queued" {
			continue
		}
		if goalReserved && !h.isCausalReplyForAgentLocked(msg, targetMeta.ID) {
			continue
		}
		next := *msg
		next.DeliveryStatus = "delivering"
		next.LastDeliveryError = ""
		next.UpdatedAt = now()
		if err := h.commitAgentMessageLocked(next); err != nil {
			log.Printf("[codex-loom] claim queued message %s: %v", next.ID, err)
			break
		}
		snapshot = &next
		break
	}
	h.mu.Unlock()
	if snapshot == nil {
		return nil, false
	}

	result, err := h.sendTask(snapshot.ToAgentID, formatAgentEnvelope(snapshot), timeout, "", "", "", snapshot.ID)
	h.mu.Lock()
	defer h.mu.Unlock()
	current := h.comms[snapshot.ID]
	if current == nil {
		return nil, false
	}
	if err != nil {
		next := *current
		next.UpdatedAt = now()
		next.LastDeliveryError = err.Error()
		if isBusyErr(err) {
			next.DeliveryStatus = "queued"
		} else {
			next.DeliveryStatus = "failed"
		}
		if commitErr := h.commitAgentMessageLocked(next); commitErr != nil {
			log.Printf("[codex-loom] save failed message delivery %s: %v", next.ID, commitErr)
			copy := *current
			return &copy, false
		}
		return &next, false
	}
	if current.DeliveryStatus == "queued" || current.DeliveryStatus == "failed" {
		cp := *current
		return &cp, false
	}
	if current.DeliveryStatus == "delivered" {
		cp := *current
		return &cp, true
	}
	next := *current
	next.DeliveryStatus = "delivered"
	next.DeliveredAgentID = result.AgentID
	next.DeliveredSessionID = result.AgentID
	next.DeliveredTurnID = result.TurnID
	next.DeliveryMode = "turn_start"
	next.DeliveredAt = now()
	next.UpdatedAt = next.DeliveredAt
	next.LastDeliveryError = ""
	if commitErr := h.commitAgentMessageLocked(next); commitErr != nil {
		log.Printf("[codex-loom] save delivered message %s: %v", next.ID, commitErr)
		copy := *current
		return &copy, false
	}
	if commitErr := h.markOriginalAnsweredLocked(&next); commitErr != nil {
		log.Printf("[codex-loom] save answered request for %s: %v", next.ID, commitErr)
	}
	return &next, true
}

func (h *Hub) isCausalReplyForAgentLocked(message *AgentMessage, agentID string) bool {
	if message == nil || message.ReplyTo == "" {
		return false
	}
	root := h.comms[message.ReplyTo]
	return root != nil && root.FromAgentID == agentID && root.SourceTurnID != ""
}

func (h *Hub) captureMessageSourceTurnLocked(msg *AgentMessage) {
	if msg == nil || msg.FromAgentID == "" || msg.FromAgentID == schedulerAgentID {
		return
	}
	rt := h.runtimes[msg.FromAgentID]
	if rt == nil || rt.activeTurn == nil || rt.activeTurn.finished {
		return
	}
	msg.SourceTurnID = rt.activeTurn.turnID
}

// tryDeliverReplyToActiveTurn injects only a causally linked reply into the
// exact Turn that created the original required message. Ordinary queued work
// never interrupts a running Turn, and a failed steer remains queued.
func (h *Hub) tryDeliverReplyToActiveTurn(target string, timeout time.Duration) (*AgentMessage, bool) {
	h.mu.Lock()
	targetMeta := h.resolveLocked(target)
	if targetMeta == nil {
		h.mu.Unlock()
		return nil, false
	}
	rt := h.runtimes[targetMeta.ID]
	if rt == nil || rt.activeTurn == nil || rt.activeTurn.finished || rt.activeTurn.turnID == "" {
		h.mu.Unlock()
		return nil, false
	}
	activeTurnID := rt.activeTurn.turnID
	var snapshot *AgentMessage
	for _, id := range h.commOrder {
		msg := h.comms[id]
		if msg == nil || msg.ToAgentID != targetMeta.ID || msg.DeliveryStatus != "queued" || msg.ReplyTo == "" {
			continue
		}
		root := h.comms[msg.ReplyTo]
		if root == nil || root.FromAgentID != targetMeta.ID || root.SourceTurnID == "" || root.SourceTurnID != activeTurnID {
			continue
		}
		next := *msg
		next.DeliveryStatus = "delivering"
		next.LastDeliveryError = ""
		next.UpdatedAt = now()
		if err := h.commitAgentMessageLocked(next); err != nil {
			log.Printf("[codex-loom] claim active-Turn reply %s: %v", next.ID, err)
			break
		}
		snapshot = &next
		break
	}
	threadID := targetMeta.ThreadID
	client := rt.client
	h.mu.Unlock()
	if snapshot == nil {
		return nil, false
	}

	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	input := formatAgentEnvelope(snapshot)
	acceptedTurnID, err := h.requestTurnSteer(client, threadID, activeTurnID, input, timeout)

	h.mu.Lock()
	defer h.mu.Unlock()
	current := h.comms[snapshot.ID]
	if current == nil {
		return nil, false
	}
	if err != nil {
		next := *current
		next.DeliveryStatus = "queued"
		next.LastDeliveryError = "mid-turn delivery deferred: " + err.Error()
		next.UpdatedAt = now()
		if commitErr := h.commitAgentMessageLocked(next); commitErr != nil {
			log.Printf("[codex-loom] save deferred active-Turn reply %s: %v", next.ID, commitErr)
			copy := *current
			return &copy, false
		}
		return &next, false
	}
	next := *current
	next.DeliveryStatus = "delivered"
	next.DeliveryMode = "turn_steer"
	next.DeliveredAgentID = targetMeta.ID
	next.DeliveredSessionID = targetMeta.ID
	next.DeliveredTurnID = acceptedTurnID
	next.DeliveredAt = now()
	next.UpdatedAt = next.DeliveredAt
	next.LastDeliveryError = ""
	if commitErr := h.commitAgentMessageLocked(next); commitErr != nil {
		log.Printf("[codex-loom] save active-Turn reply %s: %v", next.ID, commitErr)
		copy := *current
		return &copy, false
	}
	if commitErr := h.markOriginalAnsweredLocked(&next); commitErr != nil {
		log.Printf("[codex-loom] save answered request for %s: %v", next.ID, commitErr)
	}
	return &next, true
}

func (h *Hub) requestTurnSteer(client *codex.Client, threadID, expectedTurnID, input string, timeout time.Duration) (string, error) {
	if h.steerTurn != nil {
		return h.steerTurn(threadID, expectedTurnID, input, timeout)
	}
	if client == nil {
		return "", errors.New("Codex runtime is unavailable")
	}
	result, err := client.Request("turn/steer", map[string]any{
		"threadId":       threadID,
		"expectedTurnId": expectedTurnID,
		"input":          []map[string]any{{"type": "text", "text": input}},
	}, timeout)
	if err != nil {
		return "", err
	}
	var response struct {
		TurnID string `json:"turnId"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return "", fmt.Errorf("decode turn/steer response: %w", err)
	}
	if response.TurnID == "" {
		return "", errors.New("turn/steer returned no turnId")
	}
	if response.TurnID != expectedTurnID {
		return "", fmt.Errorf("turn/steer accepted %s, expected %s", response.TurnID, expectedTurnID)
	}
	return response.TurnID, nil
}

func (h *Hub) failQueuedForTarget(target string, cause error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, id := range h.commOrder {
		msg := h.comms[id]
		if msg == nil || msg.ToAgentID != target || msg.DeliveryStatus != "queued" {
			continue
		}
		next := *msg
		next.DeliveryStatus = "failed"
		next.LastDeliveryError = cause.Error()
		next.UpdatedAt = now()
		if err := h.commitAgentMessageLocked(next); err != nil {
			log.Printf("[codex-loom] fail queued message %s: %v", next.ID, err)
		}
	}
}

func (h *Hub) markOriginalAnsweredLocked(reply *AgentMessage) error {
	if reply.ReplyTo == "" || reply.DeliveryStatus != "delivered" {
		return nil
	}
	orig := h.comms[reply.ReplyTo]
	if orig == nil || orig.Status != "open" {
		return nil
	}
	next := *orig
	next.Status = "answered"
	next.Resolution = "reply"
	next.ResolvedBy = reply.From
	next.ResolvedAt = now()
	next.UpdatedAt = next.ResolvedAt
	return h.commitAgentMessageLocked(next)
}

func isBusyErr(err error) bool {
	var hubErr *HubError
	if ok := errors.As(err, &hubErr); ok && hubErr.Status == 409 {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already running") || strings.Contains(msg, " is running")
}

func isNotFoundErr(err error) bool {
	var hubErr *HubError
	return errors.As(err, &hubErr) && hubErr.Status == 404
}

func newMessageID() string {
	idBytes := make([]byte, 8)
	_, _ = rand.Read(idBytes)
	return "msg_" + hex.EncodeToString(idBytes)
}

func formatAgentEnvelope(msg *AgentMessage) string {
	return formatAgentEnvelopeAt(msg, now())
}

func formatAgentEnvelopeAt(msg *AgentMessage, currentTime string) string {
	var b strings.Builder
	b.WriteString(`<agent_message version="1" id="`)
	b.WriteString(xmlEscape(msg.ID))
	b.WriteString(`" response="`)
	b.WriteString(xmlEscape(msg.Response))
	b.WriteString(`" status="`)
	b.WriteString(xmlEscape(msg.Status))
	b.WriteString(`">` + "\n")
	b.WriteString("  <timing")
	writeXMLAttribute(&b, "sent_at", msg.CreatedAt)
	writeXMLAttribute(&b, "current_time", currentTime)
	b.WriteString(" />\n")
	writeXMLText(&b, "from", msg.From)
	writeXMLText(&b, "to", msg.To)
	writeXMLText(&b, "subject", msg.Subject)
	if msg.ReplyTo != "" {
		writeXMLText(&b, "reply_to", msg.ReplyTo)
	}
	if msg.Response == "required" {
		writeXMLText(&b, "reply_command", "loom msg --reply-to "+msg.ID+" --from "+msg.To+" --body \"...\"")
	}
	writeXMLCDATA(&b, "body", msg.Body)
	b.WriteString("</agent_message>")
	return b.String()
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func writeXMLText(b *strings.Builder, tag, value string) {
	b.WriteString("  <")
	b.WriteString(tag)
	b.WriteString(">")
	b.WriteString(xmlEscape(value))
	b.WriteString("</")
	b.WriteString(tag)
	b.WriteString(">\n")
}

func writeXMLCDATA(b *strings.Builder, tag, value string) {
	b.WriteString("  <")
	b.WriteString(tag)
	b.WriteString("><![CDATA[")
	b.WriteString(strings.ReplaceAll(value, "]]>", "]]]]><![CDATA[>"))
	b.WriteString("]]></")
	b.WriteString(tag)
	b.WriteString(">\n")
}
