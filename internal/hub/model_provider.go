package hub

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/codex"
	"github.com/yan5xu/codex-loom/internal/modelcatalog"
)

const (
	deepSeekProviderID = "deepseek"
	deepSeekModel      = "deepseek-v4-flash"
)

// ModelProvider is a deliberately secret-free projection of one Codex
// model_providers entry. Raw config/read responses never leave the Hub because
// Codex may return literal bearer tokens and static HTTP headers unchanged.
type ModelProvider struct {
	ID                   string                     `json:"id"`
	Name                 string                     `json:"name"`
	BaseURL              string                     `json:"baseUrl,omitempty"`
	WireAPI              string                     `json:"wireApi,omitempty"`
	Source               string                     `json:"source"`
	Configured           bool                       `json:"configured"`
	CredentialSource     string                     `json:"credentialSource"`
	CredentialConfigured bool                       `json:"credentialConfigured"`
	Models               []string                   `json:"models"`
	ModelDetails         []modelcatalog.PublicModel `json:"modelDetails"`
	BoundAgentCount      int                        `json:"boundAgentCount"`
	PublicBeta           bool                       `json:"publicBeta,omitempty"`
	TextOnly             bool                       `json:"textOnly,omitempty"`
	Limitations          []string                   `json:"limitations,omitempty"`
}

type ModelProviderUpsertParams struct {
	Name            string `json:"name"`
	BaseURL         string `json:"baseUrl"`
	WireAPI         string `json:"wireApi"`
	APIKey          string `json:"apiKey"`
	EnvKey          string `json:"envKey"`
	ClearCredential bool   `json:"clearCredential"`
}

type ModelProviderVerification struct {
	ProviderID       string `json:"providerId"`
	Config           string `json:"config"`
	Authentication   string `json:"authentication"`
	MinimalRequest   string `json:"minimalRequest"`
	CredentialSource string `json:"credentialSource"`
	Message          string `json:"message,omitempty"`
}

type ModelProviderVerifyParams struct {
	Model string `json:"model"`
}

type providerTurnResult struct {
	Status  string
	Message string
}

type codexConfigSnapshot struct {
	Config   map[string]any
	Version  string
	FilePath string
}

func (h *Hub) ListModelProviders() ([]ModelProvider, error) {
	h.modelProviderMu.Lock()
	defer h.modelProviderMu.Unlock()
	return h.listModelProvidersLocked()
}

func (h *Hub) listModelProvidersLocked() ([]ModelProvider, error) {
	catalog, err := modelcatalog.Describe(os.Getenv("CODEX_LOOM_MODEL_CATALOG"))
	if err != nil {
		return nil, errf(500, "read Codex model catalog: %s", err)
	}
	snapshot, err := h.readCodexConfig()
	if err != nil {
		return nil, err
	}

	bound := h.modelProviderBindings()
	openAIAuthConfigured := h.codexAuthConfigured()
	providers := []ModelProvider{{
		ID: "openai", Name: "OpenAI / ChatGPT login", Source: "builtin",
		Configured: true, CredentialSource: "codex-auth", CredentialConfigured: openAIAuthConfigured,
		Models: catalogModelIDs(catalog, "openai"), ModelDetails: catalogModels(catalog, "openai"), BoundAgentCount: bound[""],
	}}
	seen := map[string]bool{"openai": true}
	for id, raw := range anyMap(snapshot.Config["model_providers"]) {
		if !nameRe.MatchString(id) || seen[id] {
			continue
		}
		definition := anyMap(raw)
		provider := sanitizeModelProvider(id, definition, catalog)
		provider.BoundAgentCount = bound[id]
		providers = append(providers, provider)
		seen[id] = true
	}
	for id, count := range bound {
		if id == "" || count == 0 || seen[id] {
			continue
		}
		providers = append(providers, ModelProvider{
			ID: id, Name: id, Source: "missing", Configured: false,
			CredentialSource: "missing", CredentialConfigured: false,
			Models: []string{}, BoundAgentCount: count,
		})
	}
	sort.Slice(providers[1:], func(i, j int) bool { return providers[i+1].ID < providers[j+1].ID })
	return providers, nil
}

func (h *Hub) ModelCatalogStatus() (modelcatalog.Status, error) {
	snapshot, err := modelcatalog.Describe(os.Getenv("CODEX_LOOM_MODEL_CATALOG"))
	if err != nil {
		return modelcatalog.Status{}, errf(500, "read Codex model catalog: %s", err)
	}
	version, _ := codex.Version(codexHostBin())
	h.mu.Lock()
	host := h.codexHost
	applied := host != nil && !host.client.Closed() && host.catalogSHA == snapshot.SHA256
	restartRequired := host != nil && !host.client.Closed() && host.catalogSHA != snapshot.SHA256
	h.mu.Unlock()
	return modelcatalog.Status{
		Source: snapshot.Source, Version: snapshot.Version, SHA256: snapshot.SHA256,
		CodexBaseline: modelcatalog.CodexBaseline, CodexVersion: version,
		Compatibility: modelcatalog.Compatibility(version), ModelCount: len(snapshot.Catalog.Models),
		Models: snapshot.PublicModels(), Applied: applied, RestartRequired: restartRequired,
	}, nil
}

func (h *Hub) codexAuthConfigured() bool {
	host, err := h.ensureCodexHost()
	if err != nil {
		return false
	}
	raw, err := host.client.Request("account/read", map[string]any{"refreshToken": false}, 20*time.Second)
	if err != nil {
		return false
	}
	var response struct {
		Account json.RawMessage `json:"account"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return false
	}
	account := strings.TrimSpace(string(response.Account))
	return account != "" && account != "null"
}

func (h *Hub) GetModelProvider(id string) (ModelProvider, error) {
	id = normalizePublicProviderID(id)
	providers, err := h.ListModelProviders()
	if err != nil {
		return ModelProvider{}, err
	}
	for _, provider := range providers {
		if provider.ID == id {
			return provider, nil
		}
	}
	return ModelProvider{}, errf(404, "model Provider not found: %s", id)
}

func (h *Hub) UpsertModelProvider(id string, params ModelProviderUpsertParams) (ModelProvider, error) {
	id = strings.TrimSpace(id)
	if id == "" || !nameRe.MatchString(id) {
		return ModelProvider{}, errf(400, "Provider id must match [a-zA-Z0-9_-]+")
	}
	if id == "openai" {
		return ModelProvider{}, errf(400, "the built-in OpenAI Provider is managed by Codex authentication")
	}
	params.APIKey = strings.TrimSpace(params.APIKey)
	params.EnvKey = strings.TrimSpace(params.EnvKey)
	if params.APIKey != "" && params.EnvKey != "" {
		return ModelProvider{}, errf(400, "apiKey and envKey are mutually exclusive")
	}
	if params.ClearCredential && (params.APIKey != "" || params.EnvKey != "") {
		return ModelProvider{}, errf(400, "clearCredential cannot be combined with apiKey or envKey")
	}
	if id == deepSeekProviderID {
		if strings.TrimSpace(params.Name) == "" {
			params.Name = "DeepSeek"
		}
		if strings.TrimSpace(params.BaseURL) == "" {
			params.BaseURL = "https://api.deepseek.com"
		}
		if strings.TrimSpace(params.WireAPI) == "" {
			params.WireAPI = "responses"
		}
	}
	params.Name = strings.TrimSpace(params.Name)
	params.BaseURL = strings.TrimRight(strings.TrimSpace(params.BaseURL), "/")
	params.WireAPI = strings.TrimSpace(params.WireAPI)
	if params.Name == "" || params.BaseURL == "" || params.WireAPI == "" {
		return ModelProvider{}, errf(400, "name, baseUrl, and wireApi are required")
	}

	h.modelProviderMu.Lock()
	defer h.modelProviderMu.Unlock()
	snapshot, err := h.readCodexConfig()
	if err != nil {
		return ModelProvider{}, err
	}
	prefix := "model_providers." + id + "."
	existing := anyMap(anyMap(snapshot.Config["model_providers"])[id])
	edits := []map[string]any{
		configEdit(prefix+"name", params.Name),
		configEdit(prefix+"base_url", params.BaseURL),
		configEdit(prefix+"wire_api", params.WireAPI),
		configEdit(prefix+"requires_openai_auth", false),
	}
	if params.APIKey != "" {
		edits = append(edits, credentialResetEdits(prefix, existing)...)
		edits = append(edits,
			configEdit(prefix+"experimental_bearer_token", params.APIKey),
		)
	} else if params.EnvKey != "" {
		edits = append(edits, credentialResetEdits(prefix, existing)...)
		edits = append(edits,
			configEdit(prefix+"env_key", params.EnvKey),
		)
	} else if params.ClearCredential {
		edits = append(edits, credentialResetEdits(prefix, existing)...)
	}
	if err := h.writeCodexConfig(snapshot, edits); err != nil {
		return ModelProvider{}, err
	}
	providers, err := h.listModelProvidersLocked()
	if err != nil {
		return ModelProvider{}, err
	}
	for _, provider := range providers {
		if provider.ID == id {
			return provider, nil
		}
	}
	return ModelProvider{}, errf(500, "Provider %s was written but is not visible in effective Codex config", id)
}

func (h *Hub) DeleteModelProvider(id string) error {
	id = strings.TrimSpace(id)
	if id == "" || id == "openai" || !nameRe.MatchString(id) {
		return errf(400, "only custom Providers can be disabled")
	}
	if count := h.modelProviderBindings()[id]; count > 0 {
		return errf(409, "Provider %s is bound to %d Agent(s); create replacement Agents before disabling it", id, count)
	}
	h.modelProviderMu.Lock()
	defer h.modelProviderMu.Unlock()
	snapshot, err := h.readCodexConfig()
	if err != nil {
		return err
	}
	return h.writeCodexConfig(snapshot, []map[string]any{configEdit("model_providers."+id, nil)})
}

func (h *Hub) VerifyModelProvider(id, model string) (ModelProviderVerification, error) {
	provider, err := h.GetModelProvider(id)
	if err != nil {
		return ModelProviderVerification{}, err
	}
	verification := ModelProviderVerification{
		ProviderID: provider.ID, Config: "valid", Authentication: "not_run",
		MinimalRequest: "not_run", CredentialSource: provider.CredentialSource,
	}
	if !provider.Configured {
		verification.Config = "missing"
		verification.Message = "Provider is not present in the effective Codex config"
		return verification, nil
	}
	if provider.CredentialConfigured {
		verification.Authentication = "configured"
	} else {
		verification.Authentication = "missing"
		verification.Message = "No usable credential source is configured"
		return verification, nil
	}
	model = strings.TrimSpace(model)
	if model == "" && len(provider.Models) > 0 {
		model = provider.Models[0]
	}
	requestStatus, authStatus, message := h.verifyProviderRequest(provider.ID, model)
	verification.MinimalRequest = requestStatus
	verification.Authentication = authStatus
	verification.Message = message
	return verification, nil
}

func (h *Hub) verifyProviderRequest(providerID, model string) (requestStatus, authStatus, message string) {
	if providerID != "openai" && strings.TrimSpace(model) == "" {
		return "not_run", "configured", "A model id is required to run a custom Provider canary"
	}
	cwd, err := os.MkdirTemp("", "codexloom-provider-verify-")
	if err != nil {
		return "failed", "configured", "Create verification workspace: " + err.Error()
	}
	defer os.RemoveAll(cwd)

	catalog, err := h.materializeModelCatalog()
	if err != nil {
		return "failed", "configured", "Prepare model catalog: " + err.Error()
	}
	client, err := codex.SpawnWithOptions(codex.SpawnOptions{
		Bin: codexHostBin(), Env: codexHostEnv(), Args: modelcatalog.SpawnArgs(catalog.Path),
	})
	if err != nil {
		return "failed", "configured", "Start Codex verification runtime: " + err.Error()
	}
	defer client.Close()
	completion := make(chan providerTurnResult, 1)
	client.OnNotification = func(method string, params json.RawMessage) {
		if method != "turn/completed" {
			return
		}
		var event struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				Status string `json:"status"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"turn"`
		}
		if json.Unmarshal(params, &event) == nil {
			result := providerTurnResult{Status: event.Turn.Status}
			if event.Turn.Error != nil {
				result.Message = event.Turn.Error.Message
			}
			select {
			case completion <- result:
			default:
			}
		}
	}
	client.OnServerRequest = func(requestID json.RawMessage, _ string, _ json.RawMessage) {
		_ = client.RespondError(requestID, -32000, "Provider verification does not approve tool use")
	}
	if err := client.InitializeAs(codex.ClientInfo{Name: "codexloom-provider-verify", Title: "CodexLoom Provider Verify", Version: "0.1.0"}); err != nil {
		return "failed", authFailureStatus(err), safeProviderError(err)
	}
	bindingID := providerID
	if bindingID == "openai" {
		bindingID = ""
	}
	startParams := threadBindingParams("read-only", cwd, bindingID, model, nil)
	raw, err := client.Request("thread/start", startParams, 30*time.Second)
	if err != nil {
		return "failed", authFailureStatus(err), safeProviderError(err)
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &started) != nil || started.Thread.ID == "" {
		return "failed", "configured", "Codex returned no verification Thread id"
	}
	defer func() {
		_, _ = client.Request("thread/archive", map[string]any{"threadId": started.Thread.ID}, 10*time.Second)
	}()
	turnParams := map[string]any{
		"threadId":       started.Thread.ID,
		"input":          []map[string]any{{"type": "text", "text": "Reply with exactly OK."}},
		"approvalPolicy": "never", "sandboxPolicy": codexSandboxPolicy("read-only"),
	}
	if model != "" {
		turnParams["model"] = model
	}
	if _, err := client.Request("turn/start", turnParams, 30*time.Second); err != nil {
		return "failed", authFailureStatus(err), safeProviderError(err)
	}
	select {
	case result := <-completion:
		if result.Status == "completed" {
			return "success", "accepted", ""
		}
		authStatus := authFailureStatusText(result.Message)
		return "failed", authStatus, safeProviderMessage(result.Message, "Verification Turn ended with status "+result.Status)
	case <-time.After(90 * time.Second):
		return "timed_out", "configured", "Verification Turn did not complete within 90 seconds"
	}
}

func authFailureStatus(err error) string {
	return authFailureStatusText(err.Error())
}

func authFailureStatusText(value string) string {
	message := strings.ToLower(value)
	if strings.Contains(message, "401") || strings.Contains(message, "403") || strings.Contains(message, "unauthorized") || strings.Contains(message, "authentication") || strings.Contains(message, "api key") {
		return "failed"
	}
	return "configured"
}

func safeProviderError(err error) string {
	return safeProviderMessage(err.Error(), "Provider verification request failed")
}

func safeProviderMessage(value, fallback string) string {
	message := strings.ToLower(strings.TrimSpace(value))
	switch {
	case message == "":
		return fallback
	case strings.Contains(message, "401"), strings.Contains(message, "403"), strings.Contains(message, "unauthorized"), strings.Contains(message, "authentication"), strings.Contains(message, "api key"):
		return "Provider authentication failed"
	case strings.Contains(message, "429"), strings.Contains(message, "rate limit"):
		return "Provider rate limit was reached"
	case strings.Contains(message, "timeout"), strings.Contains(message, "timed out"), strings.Contains(message, "deadline exceeded"):
		return "Provider request timed out"
	case strings.Contains(message, "model") && (strings.Contains(message, "not found") || strings.Contains(message, "invalid") || strings.Contains(message, "unsupported")):
		return "Provider rejected the selected model"
	case strings.Contains(message, "connection refused"), strings.Contains(message, "dns"), strings.Contains(message, "network"):
		return "Provider network request failed"
	default:
		return fallback
	}
}

func (h *Hub) readCodexConfig() (codexConfigSnapshot, error) {
	host, err := h.ensureCodexHost()
	if err != nil {
		return codexConfigSnapshot{}, err
	}
	raw, err := host.client.Request("config/read", map[string]any{"includeLayers": true}, 20*time.Second)
	if err != nil {
		return codexConfigSnapshot{}, errf(500, "read Codex Provider configuration failed")
	}
	var response struct {
		Config map[string]any `json:"config"`
		Layers []struct {
			Name struct {
				Type string `json:"type"`
				File string `json:"file"`
			} `json:"name"`
			Version string `json:"version"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return codexConfigSnapshot{}, errf(500, "decode Codex Provider configuration failed")
	}
	if response.Config == nil {
		response.Config = map[string]any{}
	}
	snapshot := codexConfigSnapshot{Config: response.Config}
	for _, layer := range response.Layers {
		if layer.Name.Type == "user" {
			snapshot.Version = layer.Version
			snapshot.FilePath = layer.Name.File
			break
		}
	}
	if snapshot.Version == "" {
		return codexConfigSnapshot{}, errf(500, "Codex config/read returned no writable user layer")
	}
	return snapshot, nil
}

func (h *Hub) writeCodexConfig(snapshot codexConfigSnapshot, edits []map[string]any) error {
	host, err := h.ensureCodexHost()
	if err != nil {
		return err
	}
	params := map[string]any{
		"edits": edits, "expectedVersion": snapshot.Version, "reloadUserConfig": false,
	}
	if snapshot.FilePath != "" {
		params["filePath"] = snapshot.FilePath
	}
	if _, err := host.client.Request("config/batchWrite", params, 20*time.Second); err != nil {
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "configversionconflict") || strings.Contains(message, "version conflict") {
			return errf(409, "Codex Provider configuration changed; reload and retry")
		}
		return errf(500, "write Codex Provider configuration failed")
	}
	return nil
}

func (h *Hub) modelProviderBindings() map[string]int {
	h.mu.Lock()
	defer h.mu.Unlock()
	bound := map[string]int{}
	for _, agent := range h.agents {
		bound[agent.ProviderID]++
	}
	return bound
}

func sanitizeModelProvider(id string, definition map[string]any, catalog modelcatalog.Snapshot) ModelProvider {
	provider := ModelProvider{
		ID: id, Name: stringValue(definition["name"]), BaseURL: stringValue(definition["base_url"]),
		WireAPI: stringValue(definition["wire_api"]), Source: "custom", Configured: true,
		Models: catalogModelIDs(catalog, id), ModelDetails: catalogModels(catalog, id), CredentialSource: "missing",
	}
	if provider.Name == "" {
		provider.Name = id
	}
	switch {
	case boolValue(definition["requires_openai_auth"]):
		provider.CredentialSource, provider.CredentialConfigured = "codex-auth", true
	case strings.TrimSpace(stringValue(definition["experimental_bearer_token"])) != "":
		provider.CredentialSource, provider.CredentialConfigured = "toml", true
	case strings.TrimSpace(stringValue(definition["env_key"])) != "":
		provider.CredentialSource = "environment"
		_, provider.CredentialConfigured = os.LookupEnv(strings.TrimSpace(stringValue(definition["env_key"])))
	case len(anyMap(definition["auth"])) > 0:
		provider.CredentialSource, provider.CredentialConfigured = "command", true
	case hasAuthorizationHeader(anyMap(definition["http_headers"])):
		provider.CredentialSource, provider.CredentialConfigured = "toml", true
	case hasAuthorizationHeader(anyMap(definition["env_http_headers"])):
		provider.CredentialSource = "environment"
		for key, raw := range anyMap(definition["env_http_headers"]) {
			if strings.EqualFold(key, "authorization") {
				_, provider.CredentialConfigured = os.LookupEnv(stringValue(raw))
				break
			}
		}
	}
	if id == deepSeekProviderID {
		provider.PublicBeta = true
		provider.TextOnly = true
		provider.Limitations = []string{
			"Responses API is public beta",
			"image and file input are not supported",
			"previous_response_id, conversation, store, and background are not supported",
		}
	}
	return provider
}

func catalogModels(catalog modelcatalog.Snapshot, providerID string) []modelcatalog.PublicModel {
	models := make([]modelcatalog.PublicModel, 0)
	for _, model := range catalog.PublicModels() {
		if model.ProviderID == providerID && model.Visible {
			models = append(models, model)
		}
	}
	return models
}

func catalogModelIDs(catalog modelcatalog.Snapshot, providerID string) []string {
	models := catalogModels(catalog, providerID)
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

func configEdit(keyPath string, value any) map[string]any {
	return map[string]any{"keyPath": keyPath, "value": value, "mergeStrategy": "replace"}
}

func credentialResetEdits(prefix string, definition map[string]any) []map[string]any {
	edits := []map[string]any{
		configEdit(prefix+"env_key", nil),
		configEdit(prefix+"experimental_bearer_token", nil),
		configEdit(prefix+"auth", nil),
	}
	for _, table := range []string{"env_http_headers", "http_headers"} {
		headers := anyMap(definition[table])
		found := false
		for key := range headers {
			if strings.EqualFold(key, "authorization") {
				edits = append(edits, configEdit(prefix+table+"."+key, nil))
				found = true
			}
		}
		if !found {
			edits = append(edits, configEdit(prefix+table+".Authorization", nil))
		}
	}
	return edits
}

func normalizePublicProviderID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "openai"
	}
	return id
}

func anyMap(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if result, ok := value.(map[string]any); ok {
		return result
	}
	return map[string]any{}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}

func hasAuthorizationHeader(headers map[string]any) bool {
	for key, value := range headers {
		if strings.EqualFold(key, "authorization") && strings.TrimSpace(stringValue(value)) != "" {
			return true
		}
	}
	return false
}
