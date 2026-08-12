package hub

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/modelcatalog"
)

type providerErrorNotification struct {
	WillRetry bool `json:"willRetry"`
	Error     struct {
		Message           string `json:"message"`
		AdditionalDetails string `json:"additionalDetails"`
	} `json:"error"`
}

// customProviderModelRouteFailure separates a permanent model binding error
// from a transient Provider outage. Custom model IDs remain opaque and
// case-sensitive: this function never normalizes or rejects one before the
// Provider has explicitly reported that the exact configured ID has no route.
func customProviderModelRouteFailure(providerID, model string, params json.RawMessage) (string, bool) {
	providerID = strings.TrimSpace(providerID)
	model = strings.TrimSpace(model)
	if model == "" {
		return "", false
	}
	var event providerErrorNotification
	if json.Unmarshal(params, &event) != nil {
		return "", false
	}
	detail := strings.TrimSpace(strings.Join([]string{event.Error.Message, event.Error.AdditionalDetails}, " "))
	failure := customProviderModelRouteFailureDetail(providerID, model, detail)
	if failure == "" {
		return "", false
	}
	return failure, event.WillRetry
}

func customProviderModelRouteFailureDetail(providerID, model, detail string) string {
	providerID = strings.TrimSpace(providerID)
	model = strings.TrimSpace(model)
	if !isCustomProviderModelRouteScope(providerID) || model == "" || !isModelRouteFailure(detail, model) {
		return ""
	}
	failure := fmt.Sprintf("custom Provider %q has no route for model ID %q; model IDs are case-sensitive", providerID, model)
	return failure + "; verify the exact canonical model ID configured by the Provider"
}

func (h *Hub) scheduleModelRouteInterruptLocked(agentID string, turn *turnState, failure string) {
	if turn == nil {
		return
	}
	h.startWorkerLocked(func() {
		if _, err := h.interruptTurnIfActive(agentID, turn, failure); err != nil {
			log.Printf("[codex-loom] stop permanent Provider model routing retry for %s: %v", agentID, err)
		}
	})
}

// interruptTurnIfActive is intentionally narrower than the public Interrupt
// operation. It may only target the turnState that produced the routing error;
// if that Turn has finished or been superseded, the delayed stop is a no-op.
// In particular, it never follows an app-server active-Turn mismatch to a
// successor Turn.
func (h *Hub) interruptTurnIfActive(agentID string, expected *turnState, reason string) (bool, error) {
	h.mu.Lock()
	meta := h.agents[agentID]
	rt := h.runtimes[agentID]
	if meta == nil || rt == nil || expected == nil || expected.finished || rt.activeTurn != expected {
		h.mu.Unlock()
		return false, nil
	}
	threadID := meta.ThreadID
	turnID := expected.turnID
	client := rt.client
	h.mu.Unlock()

	if turnID == "" {
		return false, fmt.Errorf("model routing error Turn is still starting")
	}
	_, err := client.Request("turn/interrupt", map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
	}, 10*time.Second)
	if err != nil {
		// The target can finish while the turn-scoped request is in flight. That
		// is already the desired terminal condition; never retry against whatever
		// Turn is active now.
		h.mu.Lock()
		stillActive := h.runtimes[agentID] == rt && rt.activeTurn == expected && !expected.finished
		h.mu.Unlock()
		if !stillActive {
			return false, nil
		}
		return false, err
	}

	// Codex should follow up with turn/completed(status=interrupted). Preserve
	// the public Interrupt fallback, fenced to the same turnState pointer.
	h.startWorker(func() {
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		select {
		case <-expected.stopWatchdog:
			return
		case <-timer.C:
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if current := h.runtimes[agentID]; current == rt && rt.activeTurn == expected && !expected.finished {
			if currentMeta := h.agents[agentID]; currentMeta != nil {
				h.finishTurnLocked(currentMeta, rt, "interrupted", reason)
			}
		}
	})
	return true, nil
}

// isCustomProviderModelRouteScope fails closed whenever provider ownership is
// unclear. Any Provider represented by Loom's active managed model catalog is
// outside this custom-Provider error projection, including builtin OpenAI,
// managed DeepSeek, and future managed Providers added to the catalog.
func isCustomProviderModelRouteScope(providerID string) bool {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return false
	}
	snapshot, err := modelcatalog.Describe(os.Getenv("CODEX_LOOM_MODEL_CATALOG"))
	if err != nil {
		return false
	}
	providerID = normalizePublicProviderID(providerID)
	for _, candidate := range snapshot.PublicModels() {
		if candidate.ProviderID == providerID {
			return false
		}
	}
	return true
}

func isModelRouteFailure(detail, model string) bool {
	detail = strings.ToLower(strings.TrimSpace(detail))
	model = strings.ToLower(strings.TrimSpace(model))
	if detail == "" || model == "" {
		return false
	}
	routeFailure := strings.Contains(detail, "no available channel for model") ||
		strings.Contains(detail, "no available channels for model") ||
		strings.Contains(detail, "model_not_found")
	return routeFailure && mentionsConfiguredModel(detail, model)
}

func mentionsConfiguredModel(detail, model string) bool {
	for _, marker := range []string{"for model", `"model":`, "'model':", "model=", "model "} {
		remaining := detail
		for {
			index := strings.Index(remaining, marker)
			if index < 0 {
				break
			}
			candidate := strings.TrimLeft(remaining[index+len(marker):], " \t\r\n\"'`")
			if strings.HasPrefix(candidate, model) && modelReferenceBoundary(candidate[len(model):]) {
				return true
			}
			remaining = remaining[index+len(marker):]
		}
	}
	return false
}

func modelReferenceBoundary(suffix string) bool {
	if suffix == "" {
		return true
	}
	return strings.ContainsRune(" \t\r\n\"'`,;:)}]", rune(suffix[0]))
}
