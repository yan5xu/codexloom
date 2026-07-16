package hub

import (
	"sort"
	"time"
)

// ActiveOperation is durable non-Turn work that should finish its current
// provider round trip before an operator-requested restart proceeds.
type ActiveOperation struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	AgentID   string `json:"agentId,omitempty"`
	Provider  string `json:"provider,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

type DrainStatus struct {
	Agents     []ActiveAgent     `json:"agents,omitempty"`
	Operations []ActiveOperation `json:"operations,omitempty"`
}

func (status DrainStatus) Busy() bool {
	return len(status.Agents) > 0 || len(status.Operations) > 0
}

// BeginDrain stops the Hub from claiming durable queued work while allowing
// active Turns and Connector operations to finish. Causal Agent replies may
// still steer the active Turn that requested them.
func (h *Hub) BeginDrain() {
	h.mu.Lock()
	h.draining = true
	h.mu.Unlock()
}

// CancelDrain restores normal queue admission after restart preparation fails.
// The periodic loops would eventually resume delivery, but an immediate drain
// keeps a failed restart from leaving avoidable latency behind.
func (h *Hub) CancelDrain() {
	h.mu.Lock()
	if h.stopping || !h.draining {
		h.mu.Unlock()
		return
	}
	h.draining = false
	h.mu.Unlock()
	h.startWorker(func() {
		h.drainQueuedAll()
		h.drainInbox()
		h.drainHumanAnswers()
	})
}

func (h *Hub) isDrainingLocked() bool {
	return h.draining
}

// DrainStatus reports work that is unsafe to interrupt at the current instant.
// Queued messages and schedules are already durable and do not block restart.
func (h *Hub) DrainStatus() DrainStatus {
	status := DrainStatus{Agents: h.ActiveAgents()}
	h.mu.Lock()
	for _, item := range h.outbox {
		if item == nil || item.State != "sending" || outboxClaimExpired(item, time.Now().UTC()) {
			continue
		}
		provider := ""
		if address := h.addresses[item.AddressID]; address != nil {
			if connection := h.connections[address.ConnectionID]; connection != nil {
				provider = connection.Provider
			}
		}
		status.Operations = append(status.Operations, ActiveOperation{
			Kind: "outbox", ID: item.ID, AgentID: item.AgentID, Provider: provider, ExpiresAt: item.ClaimExpiresAt,
		})
	}
	for _, operation := range h.providerOperations {
		if operation == nil || operation.State != "running" || providerOperationClaimExpired(operation, time.Now().UTC()) {
			continue
		}
		status.Operations = append(status.Operations, ActiveOperation{
			Kind: "provider_operation", ID: operation.ID, AgentID: operation.AgentID,
			Provider: operation.Provider, ExpiresAt: operation.ClaimExpiresAt,
		})
	}
	h.mu.Unlock()
	sort.Slice(status.Operations, func(i, j int) bool {
		if status.Operations[i].Kind != status.Operations[j].Kind {
			return status.Operations[i].Kind < status.Operations[j].Kind
		}
		return status.Operations[i].ID < status.Operations[j].ID
	})
	return status
}
