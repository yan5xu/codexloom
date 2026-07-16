package hub

import "fmt"

// commitAgentMessageLocked is the single projection boundary for internal
// Agent communication. Durable append happens before memory replacement and
// observer publication, so callers never see an uncommitted transition.
func (h *Hub) commitAgentMessageLocked(next AgentMessage) error {
	if next.ID == "" {
		return fmt.Errorf("message id is required")
	}
	if err := h.st.AppendComm(commRecord{Message: next}); err != nil {
		return err
	}
	if _, exists := h.comms[next.ID]; !exists {
		h.commOrder = append(h.commOrder, next.ID)
	}
	copy := next
	h.comms[next.ID] = &copy
	h.emitGlobalLocked("loom/comms-message", map[string]any{"message": copy})
	return nil
}

func (h *Hub) commitAgentMessageUpdateLocked(current *AgentMessage, mutate func(*AgentMessage)) (AgentMessage, error) {
	if current == nil {
		return AgentMessage{}, fmt.Errorf("message is required")
	}
	next := *current
	mutate(&next)
	if err := h.commitAgentMessageLocked(next); err != nil {
		return AgentMessage{}, err
	}
	return next, nil
}
