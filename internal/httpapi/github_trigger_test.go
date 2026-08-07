package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/yan5xu/codex-loom/internal/credentialstore"
	githubapi "github.com/yan5xu/codex-loom/internal/github"
	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
	"github.com/zalando/go-keyring"
)

func TestGitHubDeviceFlowRetriesConnectionWithGrantedToken(t *testing.T) {
	allowManagedCredentialWritesForTest(t)
	keyring.MockInit()
	grantedCredential := randomTestCredential(t)
	var tokenRequests atomic.Int32
	var userRequests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oauth/token":
			if count := tokenRequests.Add(1); count > 1 {
				t.Fatalf("device code was exchanged %d times", count)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": grantedCredential, "token_type": "bearer", "scope": "repo"})
		case "/user":
			if r.Header.Get("Authorization") != "Bearer "+grantedCredential {
				t.Fatal("provider received an unexpected authorization credential")
			}
			if userRequests.Add(1) == 1 {
				http.Error(w, "temporary failure", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"login":"loom-owner","id":7}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	t.Setenv("CODEX_LOOM_GITHUB_TOKEN_URL", provider.URL+"/oauth/token")
	t.Setenv("CODEX_LOOM_GITHUB_API_URL", provider.URL)
	t.Setenv("PINIX_EDGE_NAMES", t.TempDir()+"/missing.json")

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.OpenWithOptions(st, hub.OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	server := New(h, st, fstest.MapFS{"index.html": {Data: []byte("ok")}})
	server.githubDevices["flow-1"] = &githubDeviceFlow{
		ID: "flow-1", ClientID: "client-id", DeviceCode: "device-code", Scope: "repo",
		ExpiresAt: time.Now().UTC().Add(time.Minute), Interval: 5 * time.Second,
	}

	if _, err := server.pollGitHubDevice(t.Context(), "flow-1"); err == nil {
		t.Fatal("first connection attempt unexpectedly succeeded")
	}
	flow := server.githubDevices["flow-1"]
	if flow == nil || flow.AccessToken != grantedCredential || flow.Polling {
		t.Fatal("retryable flow did not retain the granted credential safely")
	}
	result, err := server.pollGitHubDevice(t.Context(), "flow-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "connected" || result.Connection == nil || result.Connection.ScopeRef != "*" {
		t.Fatalf("connected result = %#v", result)
	}
	if tokenRequests.Load() != 1 || userRequests.Load() != 2 {
		t.Fatalf("requests token=%d user=%d", tokenRequests.Load(), userRequests.Load())
	}
	if server.githubDevices["flow-1"] != nil {
		t.Fatal("completed device flow was not removed")
	}
}

func TestGitHubDeviceFlowExpiresWithoutAnotherPoll(t *testing.T) {
	server := &Server{githubDevices: map[string]*githubDeviceFlow{}}
	flow := &githubDeviceFlow{ID: "flow-expiring", ExpiresAt: time.Now().UTC().Add(25 * time.Millisecond)}
	server.githubMu.Lock()
	server.githubDevices[flow.ID] = flow
	server.scheduleGitHubDeviceExpiryLocked(flow)
	server.githubMu.Unlock()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		server.githubMu.Lock()
		_, exists := server.githubDevices[flow.ID]
		server.githubMu.Unlock()
		if !exists {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("abandoned GitHub Device Flow was not removed at expiry")
}

func TestGitHubCredentialAndTriggerHTTPFlow(t *testing.T) {
	t.Setenv("CODEX_LOOM_ADMIN_TOKEN", "github-http-test-admin-token")
	keyring.MockInit()
	ownerCredential := randomTestCredential(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+ownerCredential {
			t.Fatal("provider received an unexpected authorization credential")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user":
			_, _ = w.Write([]byte(`{"login":"loom-owner","id":7}`))
		case "/repos/acme/widget/pulls/7":
			_, _ = w.Write([]byte(`{"number":7,"state":"open","merged":false,"head":{"sha":"abc"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	t.Setenv("CODEX_LOOM_GITHUB_API_URL", provider.URL)
	t.Setenv("LOOM_TEST_GITHUB_TOKEN", ownerCredential)
	t.Setenv("PINIX_EDGE_NAMES", t.TempDir()+"/missing.json")

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAgents(map[string]*hub.Agent{
		"agent-1": {ID: "agent-1", Name: "lead", ThreadID: "thread-1", Status: "idle", CreatedAt: "2026-07-19T00:00:00Z", UpdatedAt: "2026-07-19T00:00:00Z"},
	}); err != nil {
		t.Fatal(err)
	}
	h, err := hub.OpenWithOptions(st, hub.OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(h, st, fstest.MapFS{"index.html": {Data: []byte("ok")}}).Handler()

	credentialBody := `{"credentialRef":"env:LOOM_TEST_GITHUB_TOKEN","resourceOwner":"acme"}`
	credential := serveJSON(t, handler, http.MethodPost, "/api/integrations/providers/github/credential", credentialBody, http.StatusOK)
	connection := credential["connection"].(map[string]any)
	connectionID := connection["id"].(string)
	if credential["login"] != "loom-owner" || connection["scopeRef"] != "acme" || connection["credentialRef"] != "env:LOOM_TEST_GITHUB_TOKEN" || connection["status"] != "connected" {
		t.Fatalf("credential response = %#v", credential)
	}

	triggerBody, _ := json.Marshal(map[string]any{
		"agent": "lead", "connectionId": connectionID, "provider": "github", "resourceKind": "pull-request",
		"subject":    map[string]string{"owner": "acme", "repo": "widget", "number": "7"},
		"conditions": []map[string]string{{"event": "merged"}}, "resumeInstruction": "Re-read the pull request.", "expiresAt": "1d",
	})
	created := serveJSON(t, handler, http.MethodPost, "/api/triggers", string(triggerBody), http.StatusCreated)
	trigger := created["trigger"].(map[string]any)
	if trigger["connectionId"] != connectionID {
		t.Fatalf("Trigger response = %#v", trigger)
	}
	triggerID := trigger["id"].(string)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		stored, getErr := h.GetTrigger(triggerID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if stored.State == "armed" && stored.LastObservedAt != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	stored, err := h.GetTrigger(triggerID)
	if err != nil || stored.State != "armed" || stored.LastObservedAt == "" {
		t.Fatalf("observed Trigger = %#v, err = %v", stored, err)
	}

	credentialValue, err := githubapi.LoadCredential("env:LOOM_TEST_GITHUB_TOKEN")
	if err != nil || credentialValue != ownerCredential {
		t.Fatal("managed environment credential did not round-trip")
	}
}

func TestGitHubTokenConnectionsMigrateLegacyAndSeparateResourceOwners(t *testing.T) {
	allowManagedCredentialWritesForTest(t)
	t.Setenv("CODEX_LOOM_ADMIN_TOKEN", "github-http-test-admin-token")
	keyring.MockInit()
	parallCredential := randomTestCredential(t)
	personalCredential := randomTestCredential(t)
	updatedCredential := randomTestCredential(t)
	accepted := map[string]bool{
		"Bearer " + parallCredential: true, "Bearer " + personalCredential: true, "Bearer " + updatedCredential: true,
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !accepted[r.Header.Get("Authorization")] {
			t.Fatal("provider received an unexpected authorization credential")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/user" {
			_, _ = w.Write([]byte(`{"login":"yan5xu","id":7}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer provider.Close()
	t.Setenv("CODEX_LOOM_GITHUB_API_URL", provider.URL)
	t.Setenv("PINIX_EDGE_NAMES", t.TempDir()+"/missing.json")

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.OpenWithOptions(st, hub.OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	legacy, err := h.CreateConnection(hub.ConnectionParams{
		Provider: "github", AccountRef: "yan5xu", CredentialRef: "keychain:com.codexloom.github.yan5xu",
		Capabilities: githubCapabilities, Enabled: &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(h, st, fstest.MapFS{"index.html": {Data: []byte("ok")}}).Handler()

	first := serveJSON(t, handler, http.MethodPost, "/api/integrations/providers/github/token", fmt.Sprintf(`{"token":%q,"resourceOwner":"parall-hq"}`, parallCredential), http.StatusOK)
	parall := first["connection"].(map[string]any)
	if parall["id"] == legacy.ID || parall["scopeRef"] != "parall-hq" {
		t.Fatalf("organization source replaced legacy connection: %#v, legacy ID = %s", parall, legacy.ID)
	}
	second := serveJSON(t, handler, http.MethodPost, "/api/integrations/providers/github/token", fmt.Sprintf(`{"token":%q,"resourceOwner":"yan5xu"}`, personalCredential), http.StatusOK)
	personal := second["connection"].(map[string]any)
	if personal["id"] != legacy.ID || personal["scopeRef"] != "yan5xu" {
		t.Fatalf("second resource owner = %#v", personal)
	}
	third := serveJSON(t, handler, http.MethodPost, "/api/integrations/providers/github/token", fmt.Sprintf(`{"token":%q,"resourceOwner":"yan5xu"}`, updatedCredential), http.StatusOK)
	if third["connection"].(map[string]any)["id"] != personal["id"] {
		t.Fatalf("same owner did not update in place: second=%#v third=%#v", second, third)
	}
	connections := h.ListConnections()
	if len(connections) != 2 {
		t.Fatalf("connections = %#v", connections)
	}
	credentials, err := h.CredentialStore()
	if err != nil {
		t.Fatal(err)
	}
	var parallConnection, personalConnection hub.PlatformConnection
	for _, connection := range connections {
		switch connection.ScopeRef {
		case "parall-hq":
			parallConnection = connection
		case "yan5xu":
			personalConnection = connection
		}
	}
	if !strings.HasPrefix(parallConnection.CredentialRef, credentialstore.ManagedReferencePrefix) || !strings.HasPrefix(personalConnection.CredentialRef, credentialstore.ManagedReferencePrefix) {
		t.Fatal("GitHub onboarding did not persist managed references")
	}
	parallToken, err := githubapi.LoadCredentialFor(credentials, parallConnection.CredentialRef, "yan5xu", "parall-hq")
	if err != nil || parallToken != parallCredential {
		t.Fatal("managed organization credential did not round-trip")
	}
	personalToken, err := githubapi.LoadCredentialFor(credentials, personalConnection.CredentialRef, "yan5xu", "yan5xu")
	if err != nil || personalToken != updatedCredential {
		t.Fatal("managed personal credential did not update in place")
	}
}

func serveJSON(t *testing.T, handler http.Handler, method, path, body string, status int) map[string]any {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if token := os.Getenv("CODEX_LOOM_ADMIN_TOKEN"); token != "" {
		req.Header.Set("X-Codex-Loom-Admin-Token", token)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != status {
		t.Fatalf("%s %s = %d: %s", method, path, res.Code, res.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}
