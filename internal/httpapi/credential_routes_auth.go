package httpapi

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
)

// allowCredentialOperatorRequest is stricter than the legacy admin helper:
// credential operations always require an explicitly configured token, even
// on loopback. Browser requests must additionally be same-origin so possession
// of an ambient browser session cannot be used as a CSRF capability.
func (s *Server) allowCredentialOperatorRequest(w http.ResponseWriter, r *http.Request) bool {
	if s.readOnly {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "credential operations are disabled in a read-only CodexLoom canary"})
		return false
	}
	want := strings.TrimSpace(envCompat("CODEX_LOOM_ADMIN_TOKEN", "CODEX_HUB_ADMIN_TOKEN"))
	if want == "" {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "credential operations require CODEX_LOOM_ADMIN_TOKEN"})
		return false
	}
	got := strings.TrimSpace(r.Header.Get("X-Codex-Loom-Admin-Token"))
	if got == "" {
		got = strings.TrimSpace(r.Header.Get("X-Codex-Hub-Admin-Token"))
	}
	if got == "" {
		got = strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}
	if len(got) != len(want) || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "credential operator token is required"})
		return false
	}
	if site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); site == "cross-site" {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "cross-origin credential operation denied"})
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true // CLI and other non-browser clients still require the token.
	}
	parsed, err := url.Parse(origin)
	requestScheme := "http"
	if r.TLS != nil {
		requestScheme = "https"
	}
	if err != nil || parsed.Scheme != requestScheme || parsed.User != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || !strings.EqualFold(parsed.Host, r.Host) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "cross-origin credential operation denied"})
		return false
	}
	return true
}
