package hub

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	HumanRequestRequired = "required"
	HumanRequestOptional = "optional"
)

type HumanRequestOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// HumanRequest is a durable request from an Agent to its human operator. It
// is deliberately separate from Inbox, which contains work addressed to an
// Agent. Answer delivery resumes the same Agent Thread in a new Turn.
type HumanRequest struct {
	ID             string               `json:"id"`
	AgentID        string               `json:"agentId"`
	AgentName      string               `json:"agentName"`
	ThreadID       string               `json:"threadId,omitempty"`
	SourceTurnID   string               `json:"sourceTurnId,omitempty"`
	SourceTask     string               `json:"sourceTask,omitempty"`
	Expectation    string               `json:"expectation"`
	Question       string               `json:"question"`
	Context        string               `json:"context,omitempty"`
	BlockedWork    string               `json:"blockedWork,omitempty"`
	Options        []HumanRequestOption `json:"options,omitempty"`
	State          string               `json:"state"` // open, answered, cancelled
	Answer         string               `json:"answer,omitempty"`
	DeliveryStatus string               `json:"deliveryStatus"` // waiting, queued, delivering, delivered, failed, cancelled
	ResumedTurnID  string               `json:"resumedTurnId,omitempty"`
	LastError      string               `json:"lastError,omitempty"`
	CreatedAt      string               `json:"createdAt"`
	UpdatedAt      string               `json:"updatedAt"`
	AnsweredAt     string               `json:"answeredAt,omitempty"`
	DeliveredAt    string               `json:"deliveredAt,omitempty"`
}

type CreateHumanRequestParams struct {
	Agent       string               `json:"agent"`
	Expectation string               `json:"expectation"`
	Question    string               `json:"question"`
	Context     string               `json:"context"`
	BlockedWork string               `json:"blockedWork"`
	Options     []HumanRequestOption `json:"options"`
}

type AnswerHumanRequestParams struct {
	Answer string `json:"answer"`
}

func (h *Hub) loadHumanRequests() error {
	if h.humanRequests == nil {
		h.humanRequests = map[string]*HumanRequest{}
	}
	return h.st.ReadHumanRequests(func(raw json.RawMessage) {
		var request HumanRequest
		if err := json.Unmarshal(raw, &request); err != nil || request.ID == "" {
			return
		}
		normalizeHumanRequest(&request)
		if _, exists := h.humanRequests[request.ID]; !exists {
			h.humanRequestOrder = append(h.humanRequestOrder, request.ID)
		}
		copy := cloneHumanRequest(request)
		h.humanRequests[request.ID] = &copy
	})
}

func normalizeHumanRequest(request *HumanRequest) {
	if request.Expectation != HumanRequestOptional {
		request.Expectation = HumanRequestRequired
	}
	if request.State == "" {
		request.State = "open"
	}
	if request.DeliveryStatus == "" {
		if request.State == "open" {
			request.DeliveryStatus = "waiting"
		} else if request.ResumedTurnID != "" {
			request.DeliveryStatus = "delivered"
		} else if request.State == "cancelled" {
			request.DeliveryStatus = "cancelled"
		} else {
			request.DeliveryStatus = "queued"
		}
	}
	if request.DeliveryStatus == "delivering" {
		request.DeliveryStatus = "queued"
		request.LastError = "recovered from interrupted delivery"
	}
}

func cloneHumanRequest(request HumanRequest) HumanRequest {
	request.Options = append([]HumanRequestOption(nil), request.Options...)
	return request
}

func (h *Hub) appendHumanRequestLocked(request HumanRequest) error {
	request = cloneHumanRequest(request)
	if err := h.st.AppendHumanRequest(request); err != nil {
		return err
	}
	if h.humanRequests == nil {
		h.humanRequests = map[string]*HumanRequest{}
	}
	if _, exists := h.humanRequests[request.ID]; !exists {
		h.humanRequestOrder = append(h.humanRequestOrder, request.ID)
	}
	h.humanRequests[request.ID] = &request
	h.emitGlobalLocked("loom/human-request", map[string]any{"request": request})
	return nil
}

func (h *Hub) CreateHumanRequest(params CreateHumanRequestParams) (HumanRequest, error) {
	params.Agent = strings.TrimSpace(params.Agent)
	params.Question = strings.TrimSpace(params.Question)
	params.Context = strings.TrimSpace(params.Context)
	params.BlockedWork = strings.TrimSpace(params.BlockedWork)
	params.Expectation = strings.ToLower(strings.TrimSpace(params.Expectation))
	if params.Agent == "" {
		return HumanRequest{}, errf(400, "agent is required")
	}
	if params.Question == "" {
		return HumanRequest{}, errf(400, "question is required")
	}
	if params.Expectation == "" {
		params.Expectation = HumanRequestRequired
	}
	if params.Expectation != HumanRequestRequired && params.Expectation != HumanRequestOptional {
		return HumanRequest{}, errf(400, "expectation must be required or optional")
	}
	if len(params.Options) > 8 {
		return HumanRequest{}, errf(400, "at most 8 options are allowed")
	}
	options := make([]HumanRequestOption, 0, len(params.Options))
	for _, option := range params.Options {
		option.Label = strings.TrimSpace(option.Label)
		option.Description = strings.TrimSpace(option.Description)
		if option.Label == "" {
			return HumanRequest{}, errf(400, "option label is required")
		}
		options = append(options, option)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	agent := h.resolveLocked(params.Agent)
	if agent == nil {
		return HumanRequest{}, errf(404, "agent not found: %s", params.Agent)
	}
	stamp := now()
	request := HumanRequest{
		ID:             newIntegrationID("hrq"),
		AgentID:        agent.ID,
		AgentName:      agent.Name,
		ThreadID:       agent.ThreadID,
		SourceTurnID:   agent.CurrentTurnID,
		SourceTask:     agent.CurrentTask,
		Expectation:    params.Expectation,
		Question:       params.Question,
		Context:        params.Context,
		BlockedWork:    params.BlockedWork,
		Options:        options,
		State:          "open",
		DeliveryStatus: "waiting",
		CreatedAt:      stamp,
		UpdatedAt:      stamp,
	}
	if request.SourceTurnID == "" {
		if rt := h.runtimes[agent.ID]; rt != nil && rt.activeTurn != nil && !rt.activeTurn.finished {
			request.SourceTurnID = rt.activeTurn.turnID
			request.SourceTask = rt.activeTurn.task
		}
	}
	if request.Expectation == HumanRequestRequired && request.BlockedWork == "" {
		request.BlockedWork = request.SourceTask
	}
	if err := h.appendHumanRequestLocked(request); err != nil {
		return HumanRequest{}, errf(500, "persist human request: %s", err)
	}
	return cloneHumanRequest(request), nil
}

func (h *Hub) ListHumanRequests(agentKey, state string) ([]HumanRequest, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	agentID := ""
	if strings.TrimSpace(agentKey) != "" {
		agent := h.resolveLocked(strings.TrimSpace(agentKey))
		if agent == nil {
			return nil, errf(404, "agent not found: %s", agentKey)
		}
		agentID = agent.ID
	}
	state = strings.ToLower(strings.TrimSpace(state))
	out := make([]HumanRequest, 0, len(h.humanRequestOrder))
	for _, id := range h.humanRequestOrder {
		request := h.humanRequests[id]
		if request == nil || agentID != "" && request.AgentID != agentID || state != "" && state != "all" && request.State != state {
			continue
		}
		out = append(out, cloneHumanRequest(*request))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out, nil
}

func (h *Hub) GetHumanRequest(id string) (HumanRequest, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	request := h.humanRequests[strings.TrimSpace(id)]
	if request == nil {
		return HumanRequest{}, errf(404, "human request not found: %s", id)
	}
	return cloneHumanRequest(*request), nil
}

func (h *Hub) AnswerHumanRequest(id string, params AnswerHumanRequestParams) (HumanRequest, error) {
	answer := strings.TrimSpace(params.Answer)
	if answer == "" {
		return HumanRequest{}, errf(400, "answer is required")
	}
	h.mu.Lock()
	current := h.humanRequests[strings.TrimSpace(id)]
	if current == nil {
		h.mu.Unlock()
		return HumanRequest{}, errf(404, "human request not found: %s", id)
	}
	if current.State != "open" {
		h.mu.Unlock()
		return HumanRequest{}, errf(409, "human request %s is already %s", id, current.State)
	}
	request := cloneHumanRequest(*current)
	request.State = "answered"
	request.Answer = answer
	request.DeliveryStatus = "queued"
	request.LastError = ""
	request.AnsweredAt = now()
	request.UpdatedAt = request.AnsweredAt
	if err := h.appendHumanRequestLocked(request); err != nil {
		h.mu.Unlock()
		return HumanRequest{}, errf(500, "persist human answer: %s", err)
	}
	h.mu.Unlock()
	h.startWorker(func() { h.deliverAnsweredHumanRequest(request.AgentID) })
	return cloneHumanRequest(request), nil
}

func (h *Hub) CancelHumanRequest(id string) (HumanRequest, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	current := h.humanRequests[strings.TrimSpace(id)]
	if current == nil {
		return HumanRequest{}, errf(404, "human request not found: %s", id)
	}
	if current.State != "open" {
		return HumanRequest{}, errf(409, "only an open human request can be cancelled")
	}
	request := cloneHumanRequest(*current)
	request.State = "cancelled"
	request.DeliveryStatus = "cancelled"
	request.UpdatedAt = now()
	if err := h.appendHumanRequestLocked(request); err != nil {
		return HumanRequest{}, errf(500, "persist human request cancellation: %s", err)
	}
	return cloneHumanRequest(request), nil
}

func (h *Hub) RetryHumanRequest(id string) (HumanRequest, error) {
	h.mu.Lock()
	current := h.humanRequests[strings.TrimSpace(id)]
	if current == nil {
		h.mu.Unlock()
		return HumanRequest{}, errf(404, "human request not found: %s", id)
	}
	if current.State != "answered" || current.DeliveryStatus != "failed" {
		h.mu.Unlock()
		return HumanRequest{}, errf(409, "only a failed human answer can be retried")
	}
	request := cloneHumanRequest(*current)
	request.DeliveryStatus = "queued"
	request.LastError = ""
	request.UpdatedAt = now()
	if err := h.appendHumanRequestLocked(request); err != nil {
		h.mu.Unlock()
		return HumanRequest{}, errf(500, "persist human answer retry: %s", err)
	}
	h.mu.Unlock()
	h.startWorker(func() { h.deliverAnsweredHumanRequest(request.AgentID) })
	return cloneHumanRequest(request), nil
}

func (h *Hub) drainHumanAnswers() {
	h.mu.Lock()
	seen := map[string]bool{}
	targets := []string{}
	for _, id := range h.humanRequestOrder {
		request := h.humanRequests[id]
		if request == nil || request.State != "answered" || request.DeliveryStatus != "queued" || seen[request.AgentID] {
			continue
		}
		seen[request.AgentID] = true
		targets = append(targets, request.AgentID)
	}
	h.mu.Unlock()
	for _, target := range targets {
		h.deliverAnsweredHumanRequest(target)
	}
}

func (h *Hub) deliverAnsweredHumanRequest(agentID string) (HumanRequest, bool) {
	h.mu.Lock()
	if h.isDrainingLocked() {
		h.mu.Unlock()
		return HumanRequest{}, false
	}
	agent := h.resolveLocked(agentID)
	if agent == nil {
		h.mu.Unlock()
		return HumanRequest{}, false
	}
	if agent.Status == "running" {
		h.mu.Unlock()
		return HumanRequest{}, false
	}
	if rt := h.runtimes[agent.ID]; rt != nil && rt.activeTurn != nil && !rt.activeTurn.finished {
		h.mu.Unlock()
		return HumanRequest{}, false
	}
	var request HumanRequest
	found := false
	for _, id := range h.humanRequestOrder {
		candidate := h.humanRequests[id]
		if candidate == nil || candidate.AgentID != agent.ID || candidate.State != "answered" || candidate.DeliveryStatus != "queued" {
			continue
		}
		request = cloneHumanRequest(*candidate)
		request.DeliveryStatus = "delivering"
		request.LastError = ""
		request.UpdatedAt = now()
		if err := h.appendHumanRequestLocked(request); err != nil {
			h.mu.Unlock()
			return HumanRequest{}, false
		}
		found = true
		break
	}
	dispatch := h.dispatchHumanAnswer
	h.mu.Unlock()
	if !found {
		return HumanRequest{}, false
	}

	var result SendResult
	var err error
	if dispatch != nil {
		result, err = dispatch(request.AgentID, formatHumanInputResponse(request))
	} else {
		result, err = h.SendTask(request.AgentID, formatHumanInputResponse(request), defaultInactivity)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	current := h.humanRequests[request.ID]
	if current == nil {
		return HumanRequest{}, false
	}
	updated := cloneHumanRequest(*current)
	updated.UpdatedAt = now()
	if err != nil {
		updated.LastError = err.Error()
		if isBusyErr(err) {
			updated.DeliveryStatus = "queued"
		} else {
			updated.DeliveryStatus = "failed"
		}
	} else {
		updated.DeliveryStatus = "delivered"
		updated.ResumedTurnID = result.TurnID
		updated.DeliveredAt = updated.UpdatedAt
		updated.LastError = ""
	}
	if persistErr := h.appendHumanRequestLocked(updated); persistErr != nil {
		updated.LastError = fmt.Sprintf("persist delivery state: %v", persistErr)
		return updated, false
	}
	return cloneHumanRequest(updated), err == nil
}

func formatHumanInputResponse(request HumanRequest) string {
	var b strings.Builder
	b.WriteString(`<human_input_response version="1" request_id="`)
	b.WriteString(xmlEscape(request.ID))
	b.WriteString(`" source_turn_id="`)
	b.WriteString(xmlEscape(request.SourceTurnID))
	b.WriteString(`" expectation="`)
	b.WriteString(xmlEscape(request.Expectation))
	b.WriteString("\">\n")
	writeXMLCDATA(&b, "question", request.Question)
	writeXMLCDATA(&b, "answer", request.Answer)
	if request.BlockedWork != "" {
		writeXMLCDATA(&b, "blocked_work", request.BlockedWork)
	}
	b.WriteString("  <instruction>Use this answer to continue the related work if it is still relevant. Do not ask the same question again unless the answer is materially ambiguous.</instruction>\n")
	b.WriteString("</human_input_response>")
	return b.String()
}
