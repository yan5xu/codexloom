package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yan5xu/codex-loom/internal/credentialstore"
	"github.com/yan5xu/codex-loom/internal/feishu"
	"github.com/yan5xu/codex-loom/internal/hub"
	loomslack "github.com/yan5xu/codex-loom/internal/slack"
	"github.com/yan5xu/codex-loom/internal/store"
)

type credentialMigrationFixture struct {
	server     *Server
	hub        *hub.Hub
	connection hub.PlatformConnection
	address    hub.AgentAddress
	membership hub.ConversationMembership
	inbox      hub.InboxItem
	outbox     hub.OutboxItem
}

type credentialMigrationFake struct {
	sourceCalls   int
	providerCalls int
	activateCalls int
	rollbackCalls int
	providerErr   error
	activateErr   error
	rollbackErr   error
}

func TestCredentialMigrationIsIdempotentAndPreservesIntegrationHistory(t *testing.T) {
	fixture := newCredentialMigrationFixture(t)
	secret := randomTestCredential(t)
	fake := installCredentialMigrationFake(t, fixture.connection, secret)

	beforeConnection := fixture.connection
	beforeAddress := fixture.address
	beforeMembership := fixture.membership
	beforeInbox := fixture.inbox
	beforeOutbox := fixture.outbox

	result, err := fixture.server.migrateCredential(t.Context(), fixture.connection.ID, credentialMigrationRequest{Confirm: fixture.connection.ID}, "http://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if result["state"] != hub.CredentialMigrationCompleted {
		t.Fatalf("migration state = %v", result["state"])
	}
	receiptID, _ := result["id"].(string)
	targetRef, _ := result["targetCredentialRef"].(string)
	if receiptID == "" || !strings.HasPrefix(targetRef, credentialstore.ManagedReferencePrefix) {
		t.Fatal("migration did not return opaque receipt and managed references")
	}
	if _, exposed := result["previousCredentialRef"]; exposed {
		t.Fatal("migration response exposed the previous credential reference")
	}
	publicReceipt, err := json.Marshal(result)
	if err != nil || bytes.Contains(publicReceipt, []byte(secret)) {
		t.Fatal("migration response exposed credential material")
	}
	for _, name := range []string{"credential-migrations.json", "integrations.json"} {
		payload, readErr := os.ReadFile(filepath.Join(fixture.server.st.Dir(), name))
		if readErr != nil || bytes.Contains(payload, []byte(secret)) {
			t.Fatal("non-secret durable state exposed credential material")
		}
	}
	afterConnection := credentialMigrationConnection(t, fixture.hub, fixture.connection.ID)
	if afterConnection.CredentialRef != targetRef {
		t.Fatal("canonical credential reference did not switch to the managed entry")
	}
	assertConnectionIdentityUnchanged(t, beforeConnection, afterConnection)
	assertCredentialMigrationObjectsUnchanged(t, fixture, beforeAddress, beforeMembership, beforeInbox, beforeOutbox)
	if fake.sourceCalls != 1 || fake.providerCalls != 1 || fake.activateCalls != 1 || fake.rollbackCalls != 0 {
		t.Fatalf("migration hook counts = source %d provider %d activate %d rollback %d", fake.sourceCalls, fake.providerCalls, fake.activateCalls, fake.rollbackCalls)
	}

	repeated, err := fixture.server.migrateCredential(t.Context(), fixture.connection.ID, credentialMigrationRequest{Confirm: fixture.connection.ID}, "http://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if repeated["id"] != receiptID || repeated["state"] != hub.CredentialMigrationCompleted {
		t.Fatalf("idempotent migration result = %#v", repeated)
	}
	if fake.sourceCalls != 1 || fake.providerCalls != 1 || fake.activateCalls != 1 {
		t.Fatal("idempotent retry repeated migration side effects")
	}

	dryRollback, err := fixture.server.rollbackCredentialMigration(t.Context(), receiptID, credentialMigrationRequest{DryRun: true})
	if err != nil || dryRollback["dryRun"] != true {
		t.Fatalf("rollback dry-run = %#v, err = %v", dryRollback, err)
	}
	if credentialMigrationConnection(t, fixture.hub, fixture.connection.ID).CredentialRef != targetRef {
		t.Fatal("rollback dry-run changed the canonical reference")
	}
	rolledBack, err := fixture.server.rollbackCredentialMigration(t.Context(), receiptID, credentialMigrationRequest{Confirm: receiptID})
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack["state"] != hub.CredentialMigrationRolledBack || fake.rollbackCalls != 1 {
		t.Fatalf("rollback result = %#v", rolledBack)
	}
	afterRollback := credentialMigrationConnection(t, fixture.hub, fixture.connection.ID)
	if afterRollback.CredentialRef != beforeConnection.CredentialRef {
		t.Fatal("rollback did not restore the previous canonical reference")
	}
	assertConnectionIdentityUnchanged(t, beforeConnection, afterRollback)
	assertCredentialMigrationObjectsUnchanged(t, fixture, beforeAddress, beforeMembership, beforeInbox, beforeOutbox)
	credentials, err := fixture.hub.CredentialStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := credentials.Resolve(targetRef); err != nil {
		t.Fatal("rollback unexpectedly deleted the managed credential rollback endpoint")
	}
}

func TestCredentialMigrationRetriesProviderFailureWithSameReceipt(t *testing.T) {
	fixture := newCredentialMigrationFixture(t)
	fake := installCredentialMigrationFake(t, fixture.connection, randomTestCredential(t))
	fake.providerErr = errors.New("provider unavailable")

	failed, err := fixture.server.migrateCredential(t.Context(), fixture.connection.ID, credentialMigrationRequest{Confirm: fixture.connection.ID}, "http://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if failed["state"] != hub.CredentialMigrationFailed {
		t.Fatalf("failed migration state = %v", failed["state"])
	}
	receiptID := failed["id"]
	if credentialMigrationConnection(t, fixture.hub, fixture.connection.ID).CredentialRef != fixture.connection.CredentialRef {
		t.Fatal("provider failure changed the canonical reference")
	}

	fake.providerErr = nil
	recovered, err := fixture.server.migrateCredential(t.Context(), fixture.connection.ID, credentialMigrationRequest{Confirm: fixture.connection.ID}, "http://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if recovered["id"] != receiptID || recovered["state"] != hub.CredentialMigrationCompleted {
		t.Fatalf("recovered migration = %#v", recovered)
	}
	if fake.sourceCalls != 1 || fake.providerCalls != 2 || fake.activateCalls != 1 {
		t.Fatalf("retry hook counts = source %d provider %d activate %d", fake.sourceCalls, fake.providerCalls, fake.activateCalls)
	}
}

func TestCredentialMigrationGatewayFailureRollsBackOrFailsClosed(t *testing.T) {
	t.Run("automatic rollback", func(t *testing.T) {
		fixture := newCredentialMigrationFixture(t)
		fake := installCredentialMigrationFake(t, fixture.connection, randomTestCredential(t))
		fake.activateErr = errors.New("gateway activation failed")

		result, err := fixture.server.migrateCredential(t.Context(), fixture.connection.ID, credentialMigrationRequest{Confirm: fixture.connection.ID}, "http://127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		if result["state"] != hub.CredentialMigrationRolledBack || fake.rollbackCalls != 1 {
			t.Fatalf("automatic rollback result = %#v", result)
		}
		if credentialMigrationConnection(t, fixture.hub, fixture.connection.ID).CredentialRef != fixture.connection.CredentialRef {
			t.Fatal("automatic rollback changed the previous canonical reference")
		}
		fake.activateErr = nil
		retried, err := fixture.server.migrateCredential(t.Context(), fixture.connection.ID, credentialMigrationRequest{Confirm: fixture.connection.ID}, "http://127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		if retried["id"] != result["id"] || retried["state"] != hub.CredentialMigrationCompleted || fake.activateCalls != 2 {
			t.Fatalf("same-receipt retry after rollback = %#v", retried)
		}
	})

	t.Run("rollback failure", func(t *testing.T) {
		fixture := newCredentialMigrationFixture(t)
		fake := installCredentialMigrationFake(t, fixture.connection, randomTestCredential(t))
		fake.activateErr = errors.New("gateway activation failed")
		fake.rollbackErr = errors.New("gateway rollback failed")

		result, err := fixture.server.migrateCredential(t.Context(), fixture.connection.ID, credentialMigrationRequest{Confirm: fixture.connection.ID}, "http://127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		if result["state"] != hub.CredentialMigrationManualRecoveryRequired || fake.rollbackCalls != 1 {
			t.Fatalf("fail-closed result = %#v", result)
		}
		receipt, err := fixture.hub.GetCredentialMigration(result["id"].(string))
		if err != nil || receipt.State != hub.CredentialMigrationManualRecoveryRequired || receipt.Error == nil || receipt.Error.Code != "gateway_restore_failed" {
			t.Fatalf("durable fail-closed receipt = %#v, err = %v", receipt, err)
		}
	})
}

func TestCredentialMigrationRecoversCanonicalSwitchWindow(t *testing.T) {
	fixture := newCredentialMigrationFixture(t)
	fake := installCredentialMigrationFake(t, fixture.connection, randomTestCredential(t))
	credentials, err := fixture.hub.CredentialStore()
	if err != nil {
		t.Fatal(err)
	}
	targetRef, err := feishu.SaveManagedAppSecret(credentials, fixture.connection.AccountRef, randomTestCredential(t))
	if err != nil {
		t.Fatal(err)
	}
	receipt, _, err := fixture.hub.BeginCredentialMigration(fixture.connection)
	if err != nil {
		t.Fatal(err)
	}
	receipt.TargetCredentialRef = targetRef
	receipt.ProviderReceipt = &hub.CredentialMigrationProviderReceipt{Status: "verified"}
	receipt.GatewayReceipt = &hub.CredentialMigrationGatewayReceipt{Status: "verified", AnchorID: receipt.ID}
	receipt.State = hub.CredentialMigrationSwitchingReference
	receipt, err = fixture.hub.SaveCredentialMigration(receipt, receipt.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.hub.CompareAndSwapConnectionCredentialForMigration(receipt.ID, fixture.connection.CredentialRef, targetRef); err != nil {
		t.Fatal(err)
	}

	recovered, err := fixture.server.migrateCredential(t.Context(), fixture.connection.ID, credentialMigrationRequest{Confirm: fixture.connection.ID}, "http://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if recovered["id"] != receipt.ID || recovered["state"] != hub.CredentialMigrationCompleted {
		t.Fatalf("switch-window recovery = %#v", recovered)
	}
	if fake.sourceCalls != 0 || fake.providerCalls != 0 || fake.activateCalls != 0 {
		t.Fatal("switch-window recovery repeated completed side effects")
	}
}

func TestCredentialMigrationReconcilesPreparedEffectFromGenerationHeartbeat(t *testing.T) {
	fixture := newCredentialMigrationFixture(t)
	credentials, err := fixture.hub.CredentialStore()
	if err != nil {
		t.Fatal(err)
	}
	targetRef, err := feishu.SaveManagedAppSecret(credentials, fixture.connection.AccountRef, randomTestCredential(t))
	if err != nil {
		t.Fatal(err)
	}
	receipt, _, err := fixture.hub.BeginCredentialMigration(fixture.connection)
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("b", 64)
	receipt.TargetCredentialRef = targetRef
	receipt.ProviderReceipt = &hub.CredentialMigrationProviderReceipt{Status: "verified"}
	receipt.GatewayReceipt = &hub.CredentialMigrationGatewayReceipt{
		Status: "activation_prepared", AnchorID: receipt.ID, Generation: "ggen_reconcile",
		Build: "build-reconcile", ExecutableSHA256: digest,
	}
	receipt.GatewayEffectID = "geff_reconcile"
	receipt.GatewayEffectAttempt = 1
	receipt.GatewayEffectState = "activation_prepared"
	receipt.State = hub.CredentialMigrationGatewayActivating
	receipt, err = fixture.hub.SaveCredentialMigration(receipt, receipt.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.hub.HeartbeatConnection(fixture.connection.ID, hub.ConnectionHeartbeatParams{
		Status: "connected", GatewayGeneration: "ggen_reconcile", GatewayBuild: "build-reconcile",
		GatewayExecutableSHA256: digest,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.server.migrateCredential(t.Context(), fixture.connection.ID, credentialMigrationRequest{Confirm: fixture.connection.ID}, "http://127.0.0.1")
	if err != nil || result["state"] != hub.CredentialMigrationCompleted {
		t.Fatalf("generation reconciliation = %#v, err=%v", result, err)
	}
	loaded, err := fixture.hub.GetCredentialMigration(receipt.ID)
	if err != nil || loaded.GatewayReceipt == nil || loaded.GatewayReceipt.Status != "verified" || loaded.GatewayReceipt.HeartbeatAt == "" || loaded.GatewayEffectState != "activation_applied" {
		t.Fatalf("reconciled receipt = %#v, err=%v", loaded, err)
	}
}

func TestGatewayProcessEvidenceRequiresExactGenerationForMigrationAcceptance(t *testing.T) {
	digest := strings.Repeat("d", 64)
	expected := hub.CredentialMigrationGatewayReceipt{Build: "build-optional", ExecutableSHA256: digest}
	connection := hub.PlatformConnection{GatewayBuild: expected.Build, GatewayExecutableSHA256: digest}
	if gatewayProcessEvidenceMatches(connection, expected) {
		t.Fatal("generation-less heartbeat satisfied a new migration effect")
	}
	expected.Generation = "ggen_expected"
	connection.GatewayGeneration = expected.Generation
	if !gatewayProcessEvidenceMatches(connection, expected) {
		t.Fatal("matching generation, build, and digest were rejected")
	}
	connection.GatewayGeneration = "ggen_unexpected"
	if gatewayProcessEvidenceMatches(connection, expected) {
		t.Fatal("an unexpected generation matched generation-less evidence")
	}
}

func TestCredentialMigrationReopenDoesNotRepeatIndeterminateGatewayEffects(t *testing.T) {
	t.Run("activation persist failure", func(t *testing.T) {
		fixture := newCredentialMigrationFixture(t)
		fake := installCredentialMigrationFake(t, fixture.connection, randomTestCredential(t))
		persist := fixture.server.credentialMigrationSave
		failed := false
		fixture.server.credentialMigrationSave = func(receipt hub.CredentialMigrationReceipt, version int) (hub.CredentialMigrationReceipt, error) {
			if receipt.State == hub.CredentialMigrationGatewayVerified && !failed {
				failed = true
				return hub.CredentialMigrationReceipt{}, errors.New("injected receipt persistence failure")
			}
			return persist(receipt, version)
		}
		result, err := fixture.server.migrateCredential(t.Context(), fixture.connection.ID, credentialMigrationRequest{Confirm: fixture.connection.ID}, "http://127.0.0.1")
		if err != nil || result["state"] != hub.CredentialMigrationManualRecoveryRequired || fake.activateCalls != 1 {
			t.Fatalf("first attempt = %#v, err=%v, calls=%#v", result, err, fake)
		}
		durable, err := fixture.hub.GetCredentialMigration(result["id"].(string))
		if err != nil || durable.State != hub.CredentialMigrationGatewayActivating || durable.GatewayEffectState != "activation_prepared" {
			t.Fatalf("durable pre-reopen receipt = %#v, err=%v", durable, err)
		}
		fixture.hub.Shutdown()
		reopened, err := hub.OpenWithOptions(fixture.server.st, hub.OpenOptions{Passive: true})
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Shutdown()
		recovered, err := New(reopened, fixture.server.st, fstest.MapFS{"index.html": {Data: []byte("app")}}).migrateCredential(t.Context(), fixture.connection.ID, credentialMigrationRequest{Confirm: fixture.connection.ID}, "http://127.0.0.1")
		if err != nil || recovered["state"] != hub.CredentialMigrationManualRecoveryRequired || fake.activateCalls != 1 {
			t.Fatalf("reopen result = %#v, err=%v, calls=%#v", recovered, err, fake)
		}
		durable, err = reopened.GetCredentialMigration(result["id"].(string))
		if err != nil || durable.State != hub.CredentialMigrationManualRecoveryRequired || durable.Error == nil || durable.Error.Code != "gateway_effect_indeterminate" {
			t.Fatalf("durable reopen receipt = %#v, err=%v", durable, err)
		}
	})

	t.Run("rollback persist failure", func(t *testing.T) {
		fixture := newCredentialMigrationFixture(t)
		fake := installCredentialMigrationFake(t, fixture.connection, randomTestCredential(t))
		completed, err := fixture.server.migrateCredential(t.Context(), fixture.connection.ID, credentialMigrationRequest{Confirm: fixture.connection.ID}, "http://127.0.0.1")
		if err != nil || completed["state"] != hub.CredentialMigrationCompleted {
			t.Fatalf("completed migration = %#v, err=%v", completed, err)
		}
		persist := fixture.server.credentialMigrationSave
		failed := false
		fixture.server.credentialMigrationSave = func(receipt hub.CredentialMigrationReceipt, version int) (hub.CredentialMigrationReceipt, error) {
			if receipt.State == hub.CredentialMigrationRolledBack && !failed {
				failed = true
				return hub.CredentialMigrationReceipt{}, errors.New("injected rollback receipt persistence failure")
			}
			return persist(receipt, version)
		}
		result, err := fixture.server.rollbackCredentialMigration(t.Context(), completed["id"].(string), credentialMigrationRequest{Confirm: completed["id"].(string)})
		if err != nil || result["state"] != hub.CredentialMigrationManualRecoveryRequired || fake.rollbackCalls != 1 {
			t.Fatalf("rollback = %#v, err=%v, calls=%#v", result, err, fake)
		}
		fixture.hub.Shutdown()
		reopened, err := hub.OpenWithOptions(fixture.server.st, hub.OpenOptions{Passive: true})
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Shutdown()
		recovered, err := New(reopened, fixture.server.st, fstest.MapFS{"index.html": {Data: []byte("app")}}).migrateCredential(t.Context(), fixture.connection.ID, credentialMigrationRequest{Confirm: fixture.connection.ID}, "http://127.0.0.1")
		if err != nil || recovered["state"] != hub.CredentialMigrationManualRecoveryRequired || fake.rollbackCalls != 1 {
			t.Fatalf("rollback reopen = %#v, err=%v, calls=%#v", recovered, err, fake)
		}
	})

	t.Run("rollback persist failure reconciles proven effect", func(t *testing.T) {
		fixture := newCredentialMigrationFixture(t)
		fake := installCredentialMigrationFake(t, fixture.connection, randomTestCredential(t))
		completed, err := fixture.server.migrateCredential(t.Context(), fixture.connection.ID, credentialMigrationRequest{Confirm: fixture.connection.ID}, "http://127.0.0.1")
		if err != nil || completed["state"] != hub.CredentialMigrationCompleted {
			t.Fatalf("completed migration = %#v, err=%v", completed, err)
		}
		reconcileCredentialMigrationGatewayEffect = func(_ context.Context, _ *Server, _ hub.PlatformConnection, receipt hub.CredentialMigrationReceipt, rollback bool) (hub.CredentialMigrationGatewayReceipt, bool, error) {
			if !rollback || receipt.RollbackGatewayReceipt == nil {
				return hub.CredentialMigrationGatewayReceipt{}, false, nil
			}
			result := *receipt.RollbackGatewayReceipt
			result.Status = "restored"
			return result, true, nil
		}
		persist := fixture.server.credentialMigrationSave
		failed := false
		fixture.server.credentialMigrationSave = func(receipt hub.CredentialMigrationReceipt, version int) (hub.CredentialMigrationReceipt, error) {
			if receipt.State == hub.CredentialMigrationRolledBack && !failed {
				failed = true
				return hub.CredentialMigrationReceipt{}, errors.New("injected rollback receipt persistence failure")
			}
			return persist(receipt, version)
		}
		result, err := fixture.server.rollbackCredentialMigration(t.Context(), completed["id"].(string), credentialMigrationRequest{Confirm: completed["id"].(string)})
		if err != nil || result["state"] != hub.CredentialMigrationManualRecoveryRequired || fake.rollbackCalls != 1 {
			t.Fatalf("rollback = %#v, err=%v, calls=%#v", result, err, fake)
		}
		fixture.hub.Shutdown()
		reopened, err := hub.OpenWithOptions(fixture.server.st, hub.OpenOptions{Passive: true})
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Shutdown()
		recovered, err := New(reopened, fixture.server.st, fstest.MapFS{"index.html": {Data: []byte("app")}}).migrateCredential(t.Context(), fixture.connection.ID, credentialMigrationRequest{Confirm: fixture.connection.ID}, "http://127.0.0.1")
		if err != nil || recovered["state"] != hub.CredentialMigrationRolledBack || fake.rollbackCalls != 1 {
			t.Fatalf("proven rollback reopen = %#v, err=%v, calls=%#v", recovered, err, fake)
		}
	})
}

func TestCredentialMigrationPreflightEnumeratesOnlyEnabledKeychainConnections(t *testing.T) {
	oldFloor := verifyManagedCredentialWriteFloor
	verifyManagedCredentialWriteFloor = func(*Server) error { return nil }
	t.Cleanup(func() { verifyManagedCredentialWriteFloor = oldFloor })
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.OpenWithOptions(st, hub.OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Shutdown)
	server := New(h, st, fstest.MapFS{"index.html": {Data: []byte("app")}})
	oldSource := loadCredentialMigrationSource
	oldGatewayPreflight := preflightCredentialMigrationGateway
	loadCredentialMigrationSource = func(_ *Server, connection hub.PlatformConnection) (credentialMigrationMaterial, error) {
		return credentialMigrationMaterial{Subject: connection.ID}, nil
	}
	preflightCredentialMigrationGateway = func(_ *Server, _ hub.PlatformConnection) error { return nil }
	t.Cleanup(func() {
		loadCredentialMigrationSource = oldSource
		preflightCredentialMigrationGateway = oldGatewayPreflight
	})

	enabled := true
	disabled := false
	larkConnection, err := h.CreateConnection(hub.ConnectionParams{Provider: "lark", AccountRef: "app-preflight", CredentialRef: "keychain:preflight-lark", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	githubConnection, err := h.CreateConnection(hub.ConnectionParams{Provider: "github", AccountRef: "owner", ScopeRef: "scope", CredentialRef: "keychain:preflight-github", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	envConnection, err := h.CreateConnection(hub.ConnectionParams{Provider: "github", AccountRef: "owner", ScopeRef: "scope", CredentialRef: "env:TEST_CREDENTIAL", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	disabledConnection, err := h.CreateConnection(hub.ConnectionParams{Provider: "slack", CredentialRef: "keychain:preflight-slack", Enabled: &disabled})
	if err != nil {
		t.Fatal(err)
	}

	items, err := server.preflightCredentialMigrations(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ConnectionID != larkConnection.ID || items[1].ConnectionID != githubConnection.ID || !items[0].Eligible || !items[1].Eligible {
		t.Fatalf("enumerated preflight = %#v", items)
	}
	envItems, err := server.preflightCredentialMigrations(t.Context(), envConnection.ID)
	if err != nil || len(envItems) != 1 || envItems[0].Status != "environment_reference_not_auto_migrated" {
		t.Fatalf("environment preflight = %#v, err = %v", envItems, err)
	}
	disabledItems, err := server.preflightCredentialMigrations(t.Context(), disabledConnection.ID)
	if err != nil || len(disabledItems) != 1 || disabledItems[0].Status != "disabled" {
		t.Fatalf("disabled preflight = %#v, err = %v", disabledItems, err)
	}
}

func TestSlackMigrationVerificationRequiresAppAndTeamIdentity(t *testing.T) {
	oldDiscover := discoverSlackClient
	t.Cleanup(func() { discoverSlackClient = oldDiscover })
	material := credentialMigrationMaterial{Payload: credentialstore.Payload{
		Provider: "slack", Kind: "tokens", Values: map[string]string{
			"appID": "A_EXPECTED", "teamID": "T_EXPECTED",
			"botToken": randomTestCredential(t), "appToken": randomTestCredential(t),
		},
	}}
	connection := hub.PlatformConnection{Provider: "slack", AccountRef: "T_EXPECTED"}
	discoverSlackClient = func(context.Context, string, string) (loomslack.Discovery, error) {
		return loomslack.Discovery{Identity: loomslack.Identity{AppID: "A_EXPECTED", TeamID: "T_OTHER"}}, nil
	}
	if _, err := (&Server{}).verifyMigrationProvider(t.Context(), connection, material); err == nil {
		t.Fatal("Slack migration accepted credentials from a different workspace")
	}
	discoverSlackClient = func(context.Context, string, string) (loomslack.Discovery, error) {
		return loomslack.Discovery{Identity: loomslack.Identity{AppID: "A_EXPECTED", TeamID: "T_EXPECTED"}}, nil
	}
	if _, err := (&Server{}).verifyMigrationProvider(t.Context(), connection, material); err != nil {
		t.Fatalf("matching Slack App and Team identity failed: %v", err)
	}
}

func TestCredentialMigrationHTTPContract(t *testing.T) {
	fixture := newCredentialMigrationFixture(t)
	installCredentialMigrationFake(t, fixture.connection, randomTestCredential(t))
	handler := fixture.server.Handler()

	dryRun := integrationAPIRequest(t, handler, http.MethodPost, "/api/integrations/connections/"+fixture.connection.ID+"/credential-migration", map[string]any{"dryRun": true}, http.StatusOK)
	if dryRun["dryRun"] != true || dryRun["credentialsIncluded"] != false || dryRun["runnableRestore"] != false || len(fixture.hub.ListCredentialMigrations()) != 0 {
		t.Fatalf("migration dry-run response = %#v", dryRun)
	}
	integrationAPIRequest(t, handler, http.MethodPost, "/api/integrations/connections/"+fixture.connection.ID+"/credential-migration", map[string]any{"confirm": "wrong"}, http.StatusBadRequest)
	completed := integrationAPIRequest(t, handler, http.MethodPost, "/api/integrations/connections/"+fixture.connection.ID+"/credential-migration", map[string]any{"confirm": fixture.connection.ID}, http.StatusOK)
	if completed["state"] != hub.CredentialMigrationCompleted || completed["credentialsIncluded"] != false || completed["runnableRestore"] != false {
		t.Fatalf("migration API response = %#v", completed)
	}
	receiptID := completed["id"].(string)
	loaded := integrationAPIRequest(t, handler, http.MethodGet, "/api/integrations/credential-migrations/"+receiptID, nil, http.StatusOK)
	if loaded["id"] != receiptID || loaded["state"] != hub.CredentialMigrationCompleted {
		t.Fatalf("migration receipt API response = %#v", loaded)
	}
	integrationAPIRequest(t, handler, http.MethodPost, "/api/integrations/credential-migrations/"+receiptID+"/rollback", map[string]any{"confirm": "wrong"}, http.StatusBadRequest)
	rolledBack := integrationAPIRequest(t, handler, http.MethodPost, "/api/integrations/credential-migrations/"+receiptID+"/rollback", map[string]any{"confirm": receiptID}, http.StatusOK)
	if rolledBack["state"] != hub.CredentialMigrationRolledBack {
		t.Fatalf("rollback API response = %#v", rolledBack)
	}
}

func TestCredentialMigrationReservationReturnsConflictForIdentityWrites(t *testing.T) {
	fixture := newCredentialMigrationFixture(t)
	if _, _, err := fixture.hub.BeginCredentialMigration(fixture.connection); err != nil {
		t.Fatal(err)
	}
	handler := fixture.server.Handler()
	connectionResult := integrationAPIRequest(t, handler, http.MethodPatch,
		"/api/integrations/connections/"+fixture.connection.ID,
		map[string]any{"accountRef": "changed-account"}, http.StatusConflict)
	if connectionResult["error"] != "credential_migration_in_progress" {
		t.Fatalf("Connection conflict response = %#v", connectionResult)
	}
	addressResult := integrationAPIRequest(t, handler, http.MethodPatch,
		"/api/integrations/addresses/"+fixture.address.ID,
		map[string]any{"externalIdentity": "changed-identity"}, http.StatusConflict)
	if addressResult["error"] != "credential_migration_in_progress" {
		t.Fatalf("Address conflict response = %#v", addressResult)
	}
}

func TestProviderSetupSharesMigrationReservationBeforeCredentialOrProviderHooks(t *testing.T) {
	fixture := newCredentialMigrationFixture(t)
	if _, _, err := fixture.hub.BeginCredentialMigration(fixture.connection); err != nil {
		t.Fatal(err)
	}
	oldDiscover := discoverFeishu
	providerCalls := 0
	discoverFeishu = func(context.Context, string, string) (feishu.Discovery, error) {
		providerCalls++
		return feishu.Discovery{}, errors.New("provider hook must not run")
	}
	t.Cleanup(func() { discoverFeishu = oldDiscover })
	_, err := fixture.server.setupLark(t.Context(), larkSetupParams{
		Agent: "migration-agent", AppID: fixture.connection.AccountRef,
	}, "http://127.0.0.1")
	var hubErr *hub.HubError
	if !errors.As(err, &hubErr) || hubErr.Status != http.StatusConflict || hubErr.Message != "credential_migration_in_progress" {
		t.Fatalf("provider setup migration fence = %v", err)
	}
	if providerCalls != 0 {
		t.Fatalf("provider setup crossed migration fence: calls=%d", providerCalls)
	}
}

func TestCredentialMigrationRollbackDryRunSharesFailClosedValidator(t *testing.T) {
	t.Run("receipt phase", func(t *testing.T) {
		fixture := newCredentialMigrationFixture(t)
		receipt, _, err := fixture.hub.BeginCredentialMigration(fixture.connection)
		if err != nil {
			t.Fatal(err)
		}
		result, err := fixture.server.rollbackCredentialMigration(t.Context(), receipt.ID, credentialMigrationRequest{DryRun: true})
		if err != nil || result["rollbackStatus"] != "blocked" || result["rollbackReason"] != "receipt_phase_not_rollbackable" {
			t.Fatalf("preparing rollback dry-run = %#v, err=%v", result, err)
		}
	})

	t.Run("anchor proof", func(t *testing.T) {
		fixture := newCredentialMigrationFixture(t)
		fake := installCredentialMigrationFake(t, fixture.connection, randomTestCredential(t))
		completed, err := fixture.server.migrateCredential(t.Context(), fixture.connection.ID, credentialMigrationRequest{Confirm: fixture.connection.ID}, "http://127.0.0.1")
		if err != nil || completed["state"] != hub.CredentialMigrationCompleted {
			t.Fatalf("completed migration = %#v, err=%v", completed, err)
		}
		preflightCredentialMigrationRollback = func(_ *Server, _ hub.PlatformConnection, _ hub.CredentialMigrationReceipt) error {
			return errors.New("anchor unavailable")
		}
		result, err := fixture.server.rollbackCredentialMigration(t.Context(), completed["id"].(string), credentialMigrationRequest{DryRun: true})
		if err != nil || result["rollbackStatus"] != "blocked" || result["rollbackReason"] != "gateway_anchor_or_platform_unverified" {
			t.Fatalf("unverified anchor dry-run = %#v, err=%v", result, err)
		}
		if _, err := fixture.server.rollbackCredentialMigration(t.Context(), completed["id"].(string), credentialMigrationRequest{Confirm: completed["id"].(string)}); err == nil {
			t.Fatal("actual rollback bypassed the shared validator")
		}
		if fake.rollbackCalls != 0 {
			t.Fatal("blocked rollback invoked the gateway effect")
		}
	})
}

func TestCredentialRoutesRequireExplicitTokenAndRejectCrossOriginBeforeHooks(t *testing.T) {
	fixture := newCredentialMigrationFixture(t)
	fake := installCredentialMigrationFake(t, fixture.connection, randomTestCredential(t))
	handler := fixture.server.Handler()

	tests := []struct {
		name   string
		method string
		path   string
		token  string
		origin string
	}{
		{name: "preflight without token", method: http.MethodGet, path: "/api/integrations/credentials/preflight"},
		{name: "migration without token", method: http.MethodPost, path: "/api/integrations/connections/" + fixture.connection.ID + "/credential-migration"},
		{name: "receipt without token", method: http.MethodGet, path: "/api/integrations/credential-migrations/cmig_unknown"},
		{name: "rollback without token", method: http.MethodPost, path: "/api/integrations/credential-migrations/cmig_unknown/rollback"},
		{name: "github token without token", method: http.MethodPost, path: "/api/integrations/providers/github/token"},
		{name: "github credential without token", method: http.MethodPost, path: "/api/integrations/providers/github/credential"},
		{name: "github device start without token", method: http.MethodPost, path: "/api/integrations/providers/github/device"},
		{name: "github device poll without token", method: http.MethodGet, path: "/api/integrations/providers/github/device/device_unknown"},
		{name: "lark credentials without token", method: http.MethodPost, path: "/api/integrations/providers/lark/credentials"},
		{name: "lark setup without token", method: http.MethodPost, path: "/api/integrations/providers/lark/setup"},
		{name: "slack credentials without token", method: http.MethodPost, path: "/api/integrations/providers/slack/credentials"},
		{name: "slack setup without token", method: http.MethodPost, path: "/api/integrations/providers/slack/setup"},
		{name: "parall credentials without token", method: http.MethodPost, path: "/api/integrations/providers/parall/credentials"},
		{name: "parall agent credentials without token", method: http.MethodPost, path: "/api/integrations/providers/parall/agent-credentials"},
		{name: "parall import without token", method: http.MethodPost, path: "/api/integrations/providers/parall/import"},
		{name: "parall setup without token", method: http.MethodPost, path: "/api/integrations/providers/parall/setup"},
		{name: "parall gateway without token", method: http.MethodPost, path: "/api/integrations/providers/parall/gateway"},
		{name: "cross origin with token", method: http.MethodGet, path: "/api/integrations/credentials/preflight", token: "credential-test-admin-token", origin: "https://attacker.example"},
		{name: "cross scheme with token", method: http.MethodGet, path: "/api/integrations/credentials/preflight", token: "credential-test-admin-token", origin: "https://loom.test"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "http://loom.test"+test.path, strings.NewReader(`{"confirm":"`+fixture.connection.ID+`"}`))
			if test.token != "" {
				request.Header.Set("X-Codex-Loom-Admin-Token", test.token)
			}
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
		})
	}
	if fake.sourceCalls != 0 || fake.providerCalls != 0 || fake.activateCalls != 0 || fake.rollbackCalls != 0 || len(fixture.hub.ListCredentialMigrations()) != 0 {
		t.Fatalf("denied credential requests reached hooks: fake=%#v receipts=%d", fake, len(fixture.hub.ListCredentialMigrations()))
	}

	request := httptest.NewRequest(http.MethodGet, "http://loom.test/api/integrations/credentials/preflight", nil)
	request.Header.Set("X-Codex-Loom-Admin-Token", "credential-test-admin-token")
	request.Header.Set("Origin", "http://loom.test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || fake.sourceCalls != 1 {
		t.Fatalf("same-origin authorized preflight = %d, hooks=%#v: %s", response.Code, fake, response.Body.String())
	}
}

func TestCredentialRoutesFailClosedInCanaryBeforeHooks(t *testing.T) {
	fixture := newCredentialMigrationFixture(t)
	fake := installCredentialMigrationFake(t, fixture.connection, randomTestCredential(t))
	handler := NewWithOptions(fixture.hub, fixture.server.st, fstest.MapFS{"index.html": {Data: []byte("app")}}, Options{Mode: "canary", ReadOnly: true}).Handler()

	for _, requestSpec := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/integrations/credentials/preflight"},
		{method: http.MethodGet, path: "/api/integrations/credential-migrations/cmig_unknown"},
		{method: http.MethodGet, path: "/api/integrations/providers/github/device/device_unknown"},
		{method: http.MethodPost, path: "/api/integrations/providers/lark/credentials"},
		{method: http.MethodPost, path: "/api/integrations/providers/slack/setup"},
		{method: http.MethodPost, path: "/api/integrations/providers/parall/gateway"},
	} {
		request := httptest.NewRequest(requestSpec.method, requestSpec.path, strings.NewReader(`{}`))
		request.Header.Set("X-Codex-Loom-Admin-Token", "credential-test-admin-token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s %s = %d: %s", requestSpec.method, requestSpec.path, response.Code, response.Body.String())
		}
	}
	if fake.sourceCalls != 0 || fake.providerCalls != 0 || fake.activateCalls != 0 || fake.rollbackCalls != 0 || len(fixture.hub.ListCredentialMigrations()) != 0 {
		t.Fatalf("canary credential request reached hooks: fake=%#v receipts=%d", fake, len(fixture.hub.ListCredentialMigrations()))
	}
}

func newCredentialMigrationFixture(t *testing.T) credentialMigrationFixture {
	t.Helper()
	t.Setenv("CODEX_LOOM_ADMIN_TOKEN", "credential-test-admin-token")
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAgents(map[string]*hub.Agent{
		"agent-migration": {ID: "agent-migration", Name: "migration-agent", Status: "idle", CreatedAt: "2026-08-07T00:00:00Z", UpdatedAt: "2026-08-07T00:00:00Z"},
	}); err != nil {
		t.Fatal(err)
	}
	h, err := hub.OpenWithOptions(st, hub.OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Shutdown)
	enabled := true
	appID := "app-migration-fixture"
	connection, err := h.CreateConnection(hub.ConnectionParams{
		Provider: "lark", AccountRef: appID, CredentialRef: "keychain:" + feishu.CredentialService(appID),
		Capabilities: []string{"receive_events", "proactive_send"}, Enabled: &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(hub.AddressParams{
		Agent: "migration-agent", ConnectionID: connection.ID, ExternalIdentity: "bot-migration",
		TriggerPolicy: "mention", ReplyPolicy: "final_answer", TrustDomain: "migration-fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	conversationType := "group"
	membership, _, err := h.UpsertConversationMembership(hub.ConversationMembershipParams{
		AddressID: address.ID, ConversationID: "conversation-migration", ConversationType: &conversationType,
	})
	if err != nil {
		t.Fatal(err)
	}
	ingress, err := h.IngestMessage(hub.IngressParams{
		ConnectionID: connection.ID, AddressID: address.ID, ExternalEventID: "event-migration", ExternalMessageID: "message-migration",
		Sender: hub.ActorRef{ExternalID: "actor-migration"}, Conversation: hub.ConversationRef{ConversationID: membership.ConversationID, ConversationType: "group"},
		Content: hub.MessageContent{Text: "Migration history fixture."}, Trigger: hub.TriggerEvidence{Mentioned: true},
	})
	if err != nil || ingress.InboxItem == nil {
		t.Fatalf("seed migration Inbox: %v", err)
	}
	outbox, err := h.CreateOutbox(hub.OutboxParams{
		Agent: "migration-agent", AddressID: address.ID, Conversation: hub.ConversationRef{ConversationID: membership.ConversationID},
		Content: hub.MessageContent{Text: "Migration outbox fixture."},
	})
	if err != nil {
		t.Fatal(err)
	}
	connection = credentialMigrationConnection(t, h, connection.ID)
	return credentialMigrationFixture{
		server: New(h, st, fstest.MapFS{"index.html": {Data: []byte("app")}}), hub: h,
		connection: connection, address: address, membership: membership, inbox: *ingress.InboxItem, outbox: outbox,
	}
}

func installCredentialMigrationFake(t *testing.T, connection hub.PlatformConnection, secret string) *credentialMigrationFake {
	t.Helper()
	fake := &credentialMigrationFake{}
	oldSource := loadCredentialMigrationSource
	oldVerify := verifyCredentialMigrationProvider
	oldActivate := activateCredentialMigrationGateway
	oldRollback := rollbackCredentialMigrationGateway
	oldGatewayPreflight := preflightCredentialMigrationGateway
	oldPrepare := prepareCredentialMigrationGatewayEffect
	oldPrepareRollback := prepareCredentialMigrationRollbackEffect
	oldReconcile := reconcileCredentialMigrationGatewayEffect
	oldRollbackPreflight := preflightCredentialMigrationRollback
	oldFloor := verifyManagedCredentialWriteFloor
	verifyManagedCredentialWriteFloor = func(*Server) error { return nil }
	loadCredentialMigrationSource = func(_ *Server, candidate hub.PlatformConnection) (credentialMigrationMaterial, error) {
		fake.sourceCalls++
		if candidate.ID != connection.ID {
			return credentialMigrationMaterial{}, errors.New("unexpected migration source")
		}
		return credentialMigrationMaterial{
			Binding: credentialstoreBinding(feishu.ManagedCredentialBinding(candidate.AccountRef)), Subject: candidate.AccountRef,
			Payload: credentialstore.Payload{Provider: "lark", Kind: "app-secret", Values: map[string]string{"appID": candidate.AccountRef, "appSecret": secret}},
		}, nil
	}
	verifyCredentialMigrationProvider = func(_ context.Context, _ *Server, candidate hub.PlatformConnection, _ credentialMigrationMaterial) (hub.CredentialMigrationProviderReceipt, error) {
		fake.providerCalls++
		return hub.CredentialMigrationProviderReceipt{Status: "verified", Subject: candidate.AccountRef}, fake.providerErr
	}
	prepareCredentialMigrationGatewayEffect = func(_ *Server, _ hub.PlatformConnection, receipt hub.CredentialMigrationReceipt) (string, hub.CredentialMigrationGatewayReceipt, error) {
		return fmt.Sprintf("geff_fixture_%d", receipt.GatewayEffectAttempt+1), hub.CredentialMigrationGatewayReceipt{
			Status: "activation_prepared", AnchorID: receipt.ID, Generation: "ggen_fixture",
			Build: "fake-build", ExecutableSHA256: strings.Repeat("a", 64),
		}, nil
	}
	prepareCredentialMigrationRollbackEffect = func(_ *Server, _ hub.PlatformConnection, receipt hub.CredentialMigrationReceipt) (string, hub.CredentialMigrationGatewayReceipt, error) {
		return fmt.Sprintf("geff_rollback_fixture_%d", receipt.RollbackEffectAttempt+1), hub.CredentialMigrationGatewayReceipt{
			Status: "rollback_prepared", AnchorID: receipt.ID, Generation: "ggen_rollback_fixture",
			Build: "fake-build", ExecutableSHA256: strings.Repeat("b", 64),
		}, nil
	}
	reconcileCredentialMigrationGatewayEffect = func(_ context.Context, _ *Server, _ hub.PlatformConnection, _ hub.CredentialMigrationReceipt, _ bool) (hub.CredentialMigrationGatewayReceipt, bool, error) {
		return hub.CredentialMigrationGatewayReceipt{}, false, nil
	}
	activateCredentialMigrationGateway = func(_ context.Context, _ *Server, _ hub.PlatformConnection, _, receiptID, _ string, prepared hub.CredentialMigrationGatewayReceipt) (hub.CredentialMigrationGatewayReceipt, error) {
		fake.activateCalls++
		status := "verified"
		if fake.activateErr != nil {
			status = "activation_failed"
		}
		prepared.Status = status
		prepared.AnchorID = receiptID
		return prepared, fake.activateErr
	}
	rollbackCredentialMigrationGateway = func(_ context.Context, _ *Server, _ hub.PlatformConnection, receipt hub.CredentialMigrationReceipt) (hub.CredentialMigrationGatewayReceipt, error) {
		fake.rollbackCalls++
		prepared := *receipt.RollbackGatewayReceipt
		prepared.Status = "restored"
		return prepared, fake.rollbackErr
	}
	preflightCredentialMigrationGateway = func(_ *Server, _ hub.PlatformConnection) error { return nil }
	preflightCredentialMigrationRollback = func(_ *Server, _ hub.PlatformConnection, _ hub.CredentialMigrationReceipt) error { return nil }
	t.Cleanup(func() {
		loadCredentialMigrationSource = oldSource
		verifyCredentialMigrationProvider = oldVerify
		activateCredentialMigrationGateway = oldActivate
		rollbackCredentialMigrationGateway = oldRollback
		preflightCredentialMigrationGateway = oldGatewayPreflight
		prepareCredentialMigrationGatewayEffect = oldPrepare
		prepareCredentialMigrationRollbackEffect = oldPrepareRollback
		reconcileCredentialMigrationGatewayEffect = oldReconcile
		preflightCredentialMigrationRollback = oldRollbackPreflight
		verifyManagedCredentialWriteFloor = oldFloor
	})
	return fake
}

func credentialMigrationConnection(t *testing.T, h *hub.Hub, connectionID string) hub.PlatformConnection {
	t.Helper()
	for _, connection := range h.ListConnections() {
		if connection.ID == connectionID {
			return connection
		}
	}
	t.Fatal("migration Connection disappeared")
	return hub.PlatformConnection{}
}

func assertConnectionIdentityUnchanged(t *testing.T, before, after hub.PlatformConnection) {
	t.Helper()
	before.CredentialRef, after.CredentialRef = "", ""
	before.UpdatedAt, after.UpdatedAt = "", ""
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("migration changed Connection fields beyond credentialRef/updatedAt: before=%#v after=%#v", before, after)
	}
}

func assertCredentialMigrationObjectsUnchanged(t *testing.T, fixture credentialMigrationFixture, address hub.AgentAddress, membership hub.ConversationMembership, inbox hub.InboxItem, outbox hub.OutboxItem) {
	t.Helper()
	addresses, err := fixture.hub.ListAddresses("")
	if err != nil {
		t.Fatal(err)
	}
	var currentAddress hub.AgentAddress
	for _, candidate := range addresses {
		if candidate.ID == address.ID {
			currentAddress = candidate
		}
	}
	currentMembership, err := fixture.hub.GetConversationMembership(membership.ID)
	if err != nil {
		t.Fatal(err)
	}
	currentInbox, _, err := fixture.hub.GetInboxItem(inbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	currentOutbox, err := fixture.hub.GetOutbox(outbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(currentAddress, address) || !reflect.DeepEqual(currentMembership, membership) || !reflect.DeepEqual(currentInbox, inbox) || !reflect.DeepEqual(currentOutbox, outbox) {
		t.Fatal("credential migration changed Address, Membership, Inbox, or Outbox identity/history")
	}
}
