package hub

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/codex"
)

type RemoteConfig struct {
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type RemoteStatus struct {
	State          string `json:"state"`
	ServerName     string `json:"serverName,omitempty"`
	SystemHostname string `json:"systemHostname"`
	InstallationID string `json:"installationId,omitempty"`
	EnvironmentID  string `json:"environmentId,omitempty"`
	CodexPath      string `json:"codexPath,omitempty"`
	LastError      string `json:"lastError,omitempty"`
	UpdatedAt      string `json:"updatedAt"`
}

type RemotePairing struct {
	PairingCode       string `json:"pairingCode"`
	ManualPairingCode string `json:"manualPairingCode,omitempty"`
	EnvironmentID     string `json:"environmentId"`
	ExpiresAt         int64  `json:"expiresAt"`
	Claimed           bool   `json:"claimed"`
}

type RemoteDevice struct {
	ClientID    string `json:"clientId"`
	DisplayName string `json:"displayName,omitempty"`
	DeviceType  string `json:"deviceType,omitempty"`
	Platform    string `json:"platform,omitempty"`
	OSVersion   string `json:"osVersion,omitempty"`
	DeviceModel string `json:"deviceModel,omitempty"`
	AppVersion  string `json:"appVersion,omitempty"`
	LastSeenAt  *int64 `json:"lastSeenAt,omitempty"`
}

type RemoteSnapshot struct {
	Config  RemoteConfig   `json:"config"`
	Status  RemoteStatus   `json:"status"`
	Pairing *RemotePairing `json:"pairing,omitempty"`
}

type remoteRuntime struct {
	client     *codex.Client
	generation uint64
}

func (h *Hub) loadRemoteLocked() error {
	hostname, _ := os.Hostname()
	h.remoteConfig = RemoteConfig{}
	if err := h.st.LoadRemote(&h.remoteConfig); err != nil {
		return err
	}
	h.remoteStatus = RemoteStatus{
		State:          "disabled",
		SystemHostname: hostname,
		UpdatedAt:      now(),
	}
	return nil
}

func (h *Hub) remoteLoop() {
	h.reconcileRemote()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			h.reconcileRemote()
		}
	}
}

func (h *Hub) reconcileRemote() {
	h.mu.Lock()
	enabled := h.remoteConfig.Enabled
	running := h.remoteRuntime != nil && h.codexHost != nil &&
		h.remoteRuntime.generation == h.codexHost.generation && !h.codexHost.client.Closed()
	h.mu.Unlock()
	if enabled && !running {
		if err := h.ensureRemoteRuntime(); err != nil {
			log.Printf("[codex-loom] start Remote controller: %v", err)
		}
	}
}

func (h *Hub) RemoteSnapshot() RemoteSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.remoteSnapshotLocked()
}

func (h *Hub) remoteSnapshotLocked() RemoteSnapshot {
	snapshot := RemoteSnapshot{Config: h.remoteConfig, Status: h.remoteStatus}
	if h.remotePairing != nil {
		pairing := *h.remotePairing
		snapshot.Pairing = &pairing
	}
	return snapshot
}

func (h *Hub) emitRemoteLocked() {
	h.emitGlobalLocked("loom/remote-status", map[string]any{"remote": h.remoteSnapshotLocked()})
}

func (h *Hub) EnableRemote() (RemoteSnapshot, error) {
	h.mu.Lock()
	h.remoteConfig.Enabled = true
	h.remoteConfig.UpdatedAt = now()
	h.remoteStatus.State = "starting"
	h.remoteStatus.LastError = ""
	h.remoteStatus.UpdatedAt = now()
	if err := h.st.SaveRemote(h.remoteConfig); err != nil {
		h.mu.Unlock()
		return RemoteSnapshot{}, errf(500, "save Remote config: %s", err)
	}
	h.emitRemoteLocked()
	h.mu.Unlock()

	if err := h.ensureRemoteRuntime(); err != nil {
		return h.RemoteSnapshot(), err
	}
	return h.RemoteSnapshot(), nil
}

func (h *Hub) DisableRemote() (RemoteSnapshot, error) {
	h.remoteStartMu.Lock()
	defer h.remoteStartMu.Unlock()

	h.mu.Lock()
	h.remoteConfig.Enabled = false
	h.remoteConfig.UpdatedAt = now()
	h.remoteRuntime = nil
	h.remoteEnabledGeneration = 0
	h.remotePairing = nil
	h.remoteStatus.State = "disabled"
	h.remoteStatus.LastError = ""
	h.remoteStatus.UpdatedAt = now()
	if err := h.st.SaveRemote(h.remoteConfig); err != nil {
		h.mu.Unlock()
		return RemoteSnapshot{}, errf(500, "save Remote config: %s", err)
	}
	h.emitRemoteLocked()
	snapshot := h.remoteSnapshotLocked()
	h.mu.Unlock()

	if client, _, err := h.currentHostClient(); err == nil {
		_, _ = client.Request("remoteControl/disable", map[string]any{"ephemeral": true}, 10*time.Second)
	}
	return snapshot, nil
}

func codexHostBin() string {
	if path := strings.TrimSpace(os.Getenv("CODEX_LOOM_CODEX_BIN")); path != "" {
		return path
	}
	if path := strings.TrimSpace(os.Getenv("CODEX_REMOTE_BIN")); path != "" {
		return path
	}
	return strings.TrimSpace(os.Getenv("CODEX_BIN"))
}

func (h *Hub) ensureRemoteRuntime() error {
	h.remoteStartMu.Lock()
	defer h.remoteStartMu.Unlock()

	h.mu.Lock()
	if !h.remoteConfig.Enabled {
		h.mu.Unlock()
		return errf(409, "Remote is disabled")
	}
	h.mu.Unlock()

	host, err := h.ensureCodexHost()
	if err != nil {
		h.setRemoteStartError(0, err)
		return err
	}
	client := host.client
	generation := host.generation

	h.mu.Lock()
	if h.remoteRuntime != nil && h.remoteRuntime.generation == generation &&
		h.remoteEnabledGeneration == generation && !client.Closed() {
		h.mu.Unlock()
		return nil
	}
	h.remoteRuntime = &remoteRuntime{client: client, generation: generation}
	h.remoteStatus.State = "starting"
	h.remoteStatus.ServerName = ""
	h.remoteStatus.CodexPath = host.bin
	h.remoteStatus.LastError = ""
	h.remoteStatus.UpdatedAt = now()
	h.emitRemoteLocked()
	h.mu.Unlock()

	_, err = readRemoteStatus(client)
	if err != nil {
		h.setRemoteStartError(generation, err)
		return errf(500, "read Remote status: %s", err)
	}
	result, err := client.Request("remoteControl/enable", map[string]any{"ephemeral": true}, 30*time.Second)
	if err != nil {
		h.setRemoteStartError(generation, err)
		return errf(500, "enable Remote: %s", err)
	}
	if err := h.applyRemoteStatusResult(generation, result); err != nil {
		h.setRemoteStartError(generation, err)
		return errf(500, "decode Remote status: %s", err)
	}
	h.mu.Lock()
	h.remoteEnabledGeneration = generation
	h.mu.Unlock()
	return nil
}

type remoteStatusResult struct {
	Status         string `json:"status"`
	ServerName     string `json:"serverName"`
	InstallationID string `json:"installationId"`
	EnvironmentID  string `json:"environmentId"`
}

func readRemoteStatus(client *codex.Client) (remoteStatusResult, error) {
	result, err := client.Request("remoteControl/status/read", map[string]any{}, 10*time.Second)
	if err != nil {
		return remoteStatusResult{}, err
	}
	var status remoteStatusResult
	if err := json.Unmarshal(result, &status); err != nil {
		return remoteStatusResult{}, err
	}
	return status, nil
}

func (h *Hub) applyRemoteStatusResult(generation uint64, raw json.RawMessage) error {
	var status remoteStatusResult
	if err := json.Unmarshal(raw, &status); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.remoteRuntime == nil || h.remoteRuntime.generation != generation {
		return nil
	}
	h.remoteStatus.State = status.Status
	h.remoteStatus.ServerName = status.ServerName
	h.remoteStatus.InstallationID = status.InstallationID
	h.remoteStatus.EnvironmentID = status.EnvironmentID
	h.remoteStatus.LastError = ""
	h.remoteStatus.UpdatedAt = now()
	h.emitRemoteLocked()
	return nil
}

func (h *Hub) onRemoteNotification(generation uint64, method string, params json.RawMessage) {
	if method != "remoteControl/status/changed" {
		return
	}
	if err := h.applyRemoteStatusResult(generation, params); err != nil {
		log.Printf("[codex-loom] decode Remote status notification: %v", err)
	}
}

func (h *Hub) setRemoteStartError(generation uint64, cause error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.remoteRuntime != nil && h.remoteRuntime.generation == generation {
		h.remoteRuntime = nil
	}
	h.remoteStatus.State = "error"
	h.remoteStatus.LastError = cause.Error()
	h.remoteStatus.UpdatedAt = now()
	h.emitRemoteLocked()
}

func (h *Hub) remoteClient() (*codex.Client, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.remoteRuntime == nil || h.remoteRuntime.client.Closed() {
		return nil, "", errf(409, "Remote is not active on the shared CodexHost")
	}
	return h.remoteRuntime.client, h.remoteStatus.EnvironmentID, nil
}

func (h *Hub) currentHostClient() (*codex.Client, uint64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.codexHost == nil || h.codexHost.client.Closed() {
		return nil, 0, errf(409, "CodexHost is not running")
	}
	return h.codexHost.client, h.codexHost.generation, nil
}

func (h *Hub) StartRemotePairing() (RemotePairing, error) {
	if err := h.ensureRemoteRuntime(); err != nil {
		return RemotePairing{}, err
	}
	client, _, err := h.remoteClient()
	if err != nil {
		return RemotePairing{}, err
	}
	result, err := client.Request("remoteControl/pairing/start", map[string]any{"manualCode": true}, 30*time.Second)
	if err != nil {
		return RemotePairing{}, errf(500, "start Remote pairing: %s", err)
	}
	var response struct {
		PairingCode       string `json:"pairingCode"`
		ManualPairingCode string `json:"manualPairingCode"`
		EnvironmentID     string `json:"environmentId"`
		ExpiresAt         int64  `json:"expiresAt"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return RemotePairing{}, errf(500, "decode Remote pairing: %s", err)
	}
	pairing := RemotePairing{
		PairingCode: response.PairingCode, ManualPairingCode: response.ManualPairingCode,
		EnvironmentID: response.EnvironmentID, ExpiresAt: response.ExpiresAt,
	}
	h.mu.Lock()
	h.remotePairing = &pairing
	h.emitRemoteLocked()
	h.mu.Unlock()
	return pairing, nil
}

func (h *Hub) ReadRemotePairing() (RemotePairing, error) {
	h.mu.Lock()
	if h.remotePairing == nil {
		h.mu.Unlock()
		return RemotePairing{}, errf(404, "no active Remote pairing")
	}
	pairing := *h.remotePairing
	runtime := h.remoteRuntime
	h.mu.Unlock()
	if runtime == nil || runtime.client.Closed() {
		return RemotePairing{}, errf(409, "Remote is not active on the shared CodexHost")
	}
	result, err := runtime.client.Request("remoteControl/pairing/status", map[string]any{
		"pairingCode": pairing.PairingCode, "manualPairingCode": pairing.ManualPairingCode,
	}, 15*time.Second)
	if err != nil {
		return RemotePairing{}, errf(500, "read Remote pairing: %s", err)
	}
	var response struct {
		Claimed bool `json:"claimed"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return RemotePairing{}, errf(500, "decode Remote pairing status: %s", err)
	}
	pairing.Claimed = response.Claimed
	h.mu.Lock()
	h.remotePairing = &pairing
	h.emitRemoteLocked()
	h.mu.Unlock()
	return pairing, nil
}

func (h *Hub) ListRemoteDevices() ([]RemoteDevice, error) {
	client, environmentID, err := h.remoteClient()
	if err != nil {
		return nil, err
	}
	if environmentID == "" {
		return []RemoteDevice{}, nil
	}
	result, err := client.Request("remoteControl/client/list", map[string]any{
		"environmentId": environmentID, "limit": 100, "order": "desc",
	}, 20*time.Second)
	if err != nil {
		return nil, errf(500, "list Remote devices: %s", err)
	}
	var response struct {
		Data []RemoteDevice `json:"data"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, errf(500, "decode Remote devices: %s", err)
	}
	if response.Data == nil {
		response.Data = []RemoteDevice{}
	}
	return response.Data, nil
}

func (h *Hub) RevokeRemoteDevice(clientID string) error {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return errf(400, "client id is required")
	}
	client, environmentID, err := h.remoteClient()
	if err != nil {
		return err
	}
	if environmentID == "" {
		return errf(409, "Remote environment is not enrolled")
	}
	_, err = client.Request("remoteControl/client/revoke", map[string]any{
		"environmentId": environmentID, "clientId": clientID,
	}, 20*time.Second)
	if err != nil {
		return errf(500, "revoke Remote device: %s", err)
	}
	return nil
}
