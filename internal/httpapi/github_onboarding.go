package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/credentialstore"
	githubapi "github.com/yan5xu/codex-loom/internal/github"
	"github.com/yan5xu/codex-loom/internal/hub"
)

var githubCapabilities = []string{
	"trigger:pull-request",
	"trigger:workflow-run",
	"trigger:poll",
}

type githubTokenParams struct {
	Token         string `json:"token"`
	ResourceOwner string `json:"resourceOwner"`
}

type githubCredentialParams struct {
	CredentialRef string `json:"credentialRef"`
	ResourceOwner string `json:"resourceOwner"`
}

type githubDeviceParams struct {
	PublicOnly bool `json:"publicOnly"`
}

type githubDeviceFlow struct {
	ID              string
	ClientID        string
	DeviceCode      string
	UserCode        string
	VerificationURI string
	Scope           string
	ExpiresAt       time.Time
	Interval        time.Duration
	NextPollAt      time.Time
	Polling         bool
	AccessToken     string
	expiryTimer     *time.Timer
}

type githubDeviceView struct {
	ID              string                  `json:"id"`
	Status          string                  `json:"status"`
	UserCode        string                  `json:"userCode,omitempty"`
	VerificationURI string                  `json:"verificationUri,omitempty"`
	Scope           string                  `json:"scope,omitempty"`
	ExpiresAt       string                  `json:"expiresAt,omitempty"`
	PollAfter       int                     `json:"pollAfterSeconds,omitempty"`
	Connection      *hub.PlatformConnection `json:"connection,omitempty"`
	Login           string                  `json:"login,omitempty"`
	Error           string                  `json:"error,omitempty"`
}

func (s *Server) startGitHubDevice(ctx context.Context, params githubDeviceParams) (githubDeviceView, error) {
	clientID := strings.TrimSpace(os.Getenv("CODEX_LOOM_GITHUB_CLIENT_ID"))
	if clientID == "" {
		return githubDeviceView{}, &hub.HubError{Status: 409, Message: "GitHub Device Flow is not configured; set CODEX_LOOM_GITHUB_CLIENT_ID or import a Personal Access Token"}
	}
	scope := "repo"
	if params.PublicOnly {
		scope = "public_repo"
	}
	device, err := githubapi.StartDeviceFlow(ctx, &http.Client{Timeout: 15 * time.Second}, clientID, scope)
	if err != nil {
		return githubDeviceView{}, &hub.HubError{Status: 502, Message: "Start GitHub Device Flow: " + err.Error()}
	}
	flow := &githubDeviceFlow{
		ID: newGitHubDeviceID(), ClientID: clientID, DeviceCode: device.DeviceCode,
		UserCode: device.UserCode, VerificationURI: device.VerificationURI, Scope: scope,
		ExpiresAt: time.Now().UTC().Add(time.Duration(device.ExpiresIn) * time.Second),
		Interval:  time.Duration(device.Interval) * time.Second,
	}
	flow.NextPollAt = time.Now().UTC().Add(flow.Interval)
	s.githubMu.Lock()
	s.githubDevices[flow.ID] = flow
	s.scheduleGitHubDeviceExpiryLocked(flow)
	s.githubMu.Unlock()
	return githubDeviceViewFor(flow, "pending"), nil
}

func (s *Server) pollGitHubDevice(ctx context.Context, id string) (githubDeviceView, error) {
	id = strings.TrimSpace(id)
	s.githubMu.Lock()
	flow := s.githubDevices[id]
	if flow == nil {
		s.githubMu.Unlock()
		return githubDeviceView{}, &hub.HubError{Status: 404, Message: "GitHub Device Flow not found or expired"}
	}
	current := time.Now().UTC()
	if !flow.ExpiresAt.After(current) {
		s.removeGitHubDeviceLocked(id)
		s.githubMu.Unlock()
		return githubDeviceView{ID: id, Status: "expired", Error: "GitHub authorization expired"}, nil
	}
	if flow.Polling || flow.AccessToken == "" && current.Before(flow.NextPollAt) {
		view := githubDeviceViewFor(flow, "pending")
		s.githubMu.Unlock()
		return view, nil
	}
	flow.Polling = true
	flow.NextPollAt = current.Add(flow.Interval)
	clientID, deviceCode, accessToken := flow.ClientID, flow.DeviceCode, flow.AccessToken
	s.githubMu.Unlock()

	if accessToken == "" {
		result, err := githubapi.PollDeviceFlow(ctx, &http.Client{Timeout: 15 * time.Second}, clientID, deviceCode)
		if err != nil {
			s.githubMu.Lock()
			if flow = s.githubDevices[id]; flow != nil {
				flow.Polling = false
			}
			s.githubMu.Unlock()
			return githubDeviceView{}, &hub.HubError{Status: 502, Message: "Poll GitHub Device Flow: " + err.Error()}
		}
		if result.AccessToken == "" {
			s.githubMu.Lock()
			flow = s.githubDevices[id]
			if flow != nil {
				flow.Polling = false
			}
			switch result.Error {
			case "authorization_pending", "":
				view := githubDeviceViewFor(flow, "pending")
				s.githubMu.Unlock()
				return view, nil
			case "slow_down":
				if flow != nil {
					flow.Interval += 5 * time.Second
					flow.NextPollAt = time.Now().UTC().Add(flow.Interval)
				}
				view := githubDeviceViewFor(flow, "pending")
				s.githubMu.Unlock()
				return view, nil
			default:
				s.removeGitHubDeviceLocked(id)
				s.githubMu.Unlock()
				message := strings.TrimSpace(result.Description)
				if message == "" {
					message = result.Error
				}
				return githubDeviceView{ID: id, Status: "failed", Error: message}, nil
			}
		}
		accessToken = result.AccessToken
		s.githubMu.Lock()
		if flow = s.githubDevices[id]; flow != nil {
			flow.AccessToken = accessToken
		}
		s.githubMu.Unlock()
	}
	connection, login, err := s.connectGitHubToken(ctx, accessToken, "*")
	if err != nil {
		s.githubMu.Lock()
		if flow = s.githubDevices[id]; flow != nil {
			flow.Polling = false
		}
		s.githubMu.Unlock()
		return githubDeviceView{}, err
	}
	s.githubMu.Lock()
	s.removeGitHubDeviceLocked(id)
	s.githubMu.Unlock()
	return githubDeviceView{ID: id, Status: "connected", Connection: &connection, Login: login}, nil
}

func (s *Server) scheduleGitHubDeviceExpiryLocked(flow *githubDeviceFlow) {
	if flow == nil {
		return
	}
	delay := time.Until(flow.ExpiresAt)
	if delay < 0 {
		delay = 0
	}
	flow.expiryTimer = time.AfterFunc(delay, func() {
		s.githubMu.Lock()
		defer s.githubMu.Unlock()
		current := s.githubDevices[flow.ID]
		if current == flow && !flow.ExpiresAt.After(time.Now().UTC()) {
			s.removeGitHubDeviceLocked(flow.ID)
		}
	})
}

func (s *Server) removeGitHubDeviceLocked(id string) {
	flow := s.githubDevices[id]
	if flow != nil && flow.expiryTimer != nil {
		flow.expiryTimer.Stop()
	}
	delete(s.githubDevices, id)
}

func githubDeviceViewFor(flow *githubDeviceFlow, status string) githubDeviceView {
	if flow == nil {
		return githubDeviceView{Status: status}
	}
	wait := int(time.Until(flow.NextPollAt).Seconds())
	if wait < 1 {
		wait = 1
	}
	return githubDeviceView{
		ID: flow.ID, Status: status, UserCode: flow.UserCode, VerificationURI: flow.VerificationURI,
		Scope: flow.Scope, ExpiresAt: flow.ExpiresAt.Format(time.RFC3339Nano), PollAfter: wait,
	}
}

func (s *Server) connectGitHubToken(ctx context.Context, rawToken, rawResourceOwner string) (hub.PlatformConnection, string, error) {
	token := strings.TrimSpace(rawToken)
	if token == "" {
		return hub.PlatformConnection{}, "", &hub.HubError{Status: 400, Message: "GitHub token is required"}
	}
	user, err := validateGitHubCredential(ctx, token)
	if err != nil {
		return hub.PlatformConnection{}, "", err
	}
	resourceOwner, err := normalizeGitHubResourceOwner(rawResourceOwner)
	if err != nil {
		return hub.PlatformConnection{}, "", err
	}
	credentials, err := s.hub.CredentialStore()
	if err != nil {
		return hub.PlatformConnection{}, "", &hub.HubError{Status: 500, Message: "Open managed credential store: " + err.Error()}
	}
	previousConnection := findGitHubConnection(s.hub.ListConnections(), user.Login, resourceOwner)
	previousRef := previousConnection.CredentialRef
	previousToken, previousErr := githubapi.LoadCredentialFor(credentials, previousRef, user.Login, resourceOwner)
	credentialRef, err := githubapi.SaveManagedScopedToken(credentials, user.Login, resourceOwner, token)
	if err != nil {
		return hub.PlatformConnection{}, "", &hub.HubError{Status: 500, Message: "Save GitHub credential: " + err.Error()}
	}
	connection, err := s.upsertGitHubConnection(user.Login, resourceOwner, credentialRef)
	if err != nil {
		if strings.HasPrefix(previousRef, credentialstore.ManagedReferencePrefix) && previousErr == nil && previousToken != "" {
			_, _ = githubapi.SaveManagedScopedToken(credentials, user.Login, resourceOwner, previousToken)
		}
		return hub.PlatformConnection{}, "", err
	}
	return connection, user.Login, nil
}

func (s *Server) connectGitHubCredential(ctx context.Context, rawRef, rawResourceOwner string) (hub.PlatformConnection, string, error) {
	credentialRef := strings.TrimSpace(rawRef)
	if !strings.HasPrefix(credentialRef, "env:") && !strings.HasPrefix(credentialRef, "keychain:") && !strings.HasPrefix(credentialRef, credentialstore.ManagedReferencePrefix) {
		return hub.PlatformConnection{}, "", &hub.HubError{Status: 400, Message: "GitHub credentialRef must use env:, keychain:, or managed:"}
	}
	var credentials *credentialstore.Store
	var err error
	var token string
	if strings.HasPrefix(credentialRef, credentialstore.ManagedReferencePrefix) {
		credentials, err = s.hub.CredentialStore()
		if err == nil {
			token, err = githubapi.LoadManagedCredential(credentials, credentialRef)
		}
	} else {
		token, err = githubapi.LoadCredential(credentialRef)
	}
	if err != nil {
		return hub.PlatformConnection{}, "", &hub.HubError{Status: 400, Message: "Load GitHub credential: " + err.Error()}
	}
	user, err := validateGitHubCredential(ctx, token)
	if err != nil {
		return hub.PlatformConnection{}, "", err
	}
	resourceOwner, err := normalizeGitHubResourceOwner(rawResourceOwner)
	if err != nil {
		return hub.PlatformConnection{}, "", err
	}
	if strings.HasPrefix(credentialRef, credentialstore.ManagedReferencePrefix) {
		if err := credentials.ValidateBinding(credentialRef, githubapi.ManagedCredentialBinding(user.Login, resourceOwner)); err != nil {
			return hub.PlatformConnection{}, "", &hub.HubError{Status: 400, Message: "Validate GitHub managed credential: " + err.Error()}
		}
	}
	connection, err := s.upsertGitHubConnection(user.Login, resourceOwner, credentialRef)
	if err != nil {
		return hub.PlatformConnection{}, "", err
	}
	return connection, user.Login, nil
}

func validateGitHubCredential(ctx context.Context, token string) (githubapi.User, error) {
	client := githubapi.NewClient(token)
	if value := strings.TrimSpace(os.Getenv("CODEX_LOOM_GITHUB_API_URL")); value != "" {
		client.APIURL = value
	}
	user, err := client.GetUser(ctx)
	if err != nil {
		var apiErr *githubapi.APIError
		status := 502
		if errors.As(err, &apiErr) && (apiErr.Status == 401 || apiErr.Status == 403) {
			status = 400
		}
		return githubapi.User{}, &hub.HubError{Status: status, Message: "GitHub token verification failed: " + err.Error()}
	}
	return user, nil
}

func normalizeGitHubResourceOwner(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return "", &hub.HubError{Status: 400, Message: "GitHub resourceOwner is required for a Personal Access Token"}
	}
	if value == "*" {
		return value, nil
	}
	if strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") {
		return "", &hub.HubError{Status: 400, Message: "invalid GitHub resourceOwner"}
	}
	for _, char := range value {
		if char < 'a' || char > 'z' {
			if char < '0' || char > '9' {
				if char != '-' {
					return "", &hub.HubError{Status: 400, Message: "invalid GitHub resourceOwner"}
				}
			}
		}
	}
	return value, nil
}

func (s *Server) upsertGitHubConnection(login, resourceOwner, credentialRef string) (hub.PlatformConnection, error) {
	enabled := true
	var connection hub.PlatformConnection
	var legacy hub.PlatformConnection
	for _, candidate := range s.hub.ListConnections() {
		if candidate.Provider != "github" || !strings.EqualFold(candidate.AccountRef, login) || candidate.ArchivedAt != "" {
			continue
		}
		if strings.EqualFold(candidate.ScopeRef, resourceOwner) {
			connection = candidate
			break
		}
		if candidate.ScopeRef == "" && legacy.ID == "" {
			legacy = candidate
		}
	}
	if connection.ID == "" && strings.EqualFold(resourceOwner, login) {
		connection = legacy
	}
	var err error
	if connection.ID == "" {
		connection, err = s.hub.CreateConnection(hub.ConnectionParams{
			Provider: "github", AccountRef: login, ScopeRef: resourceOwner, CredentialRef: credentialRef,
			Capabilities: githubCapabilities, Enabled: &enabled,
		})
	} else {
		connection, err = s.hub.UpdateConnection(connection.ID, hub.ConnectionParams{
			AccountRef: login, ScopeRef: resourceOwner, CredentialRef: credentialRef, Capabilities: githubCapabilities, Enabled: &enabled,
		})
	}
	if err != nil {
		return hub.PlatformConnection{}, err
	}
	connection, err = s.hub.HeartbeatConnection(connection.ID, hub.ConnectionHeartbeatParams{Status: "connected", Capabilities: githubCapabilities})
	if err != nil {
		return hub.PlatformConnection{}, err
	}
	return connection, nil
}

func findGitHubConnection(connections []hub.PlatformConnection, login, resourceOwner string) hub.PlatformConnection {
	var legacy hub.PlatformConnection
	for _, candidate := range connections {
		if candidate.Provider != "github" || !strings.EqualFold(candidate.AccountRef, login) || candidate.ArchivedAt != "" {
			continue
		}
		if strings.EqualFold(candidate.ScopeRef, resourceOwner) {
			return candidate
		}
		if candidate.ScopeRef == "" && legacy.ID == "" {
			legacy = candidate
		}
	}
	if strings.EqualFold(resourceOwner, login) {
		return legacy
	}
	return hub.PlatformConnection{}
}

func newGitHubDeviceID() string {
	buffer := make([]byte, 8)
	_, _ = rand.Read(buffer)
	return "gdev_" + hex.EncodeToString(buffer)
}
