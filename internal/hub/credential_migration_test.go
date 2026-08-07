package hub

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/credentialstore"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestMigrationControlEpochDoesNotRevertAfterCompensatedPersistFailure(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := OpenWithOptions(st, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	connection, err := h.CreateConnection(ConnectionParams{Provider: "lark", CredentialRef: "keychain:previous"})
	if err != nil {
		t.Fatal(err)
	}
	receipt, _, err := h.BeginCredentialMigration(connection)
	if err != nil {
		t.Fatal(err)
	}
	receipt.State = CredentialMigrationSwitchingReference
	receipt, err = h.SaveCredentialMigrationControlled(receipt, receipt.Version, receipt.PreviousCredentialRef)
	if err != nil {
		t.Fatal(err)
	}
	before, err := h.SnapshotConnectionControl(connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	h.saveIntegrations = func(config integrationConfig) error {
		calls++
		if calls == 1 {
			return errors.New("fixture one-shot integrations persist failure")
		}
		return st.SaveIntegrations(config)
	}
	if _, _, err := h.CompareAndSwapConnectionCredentialForMigration(receipt.ID, receipt.PreviousCredentialRef, "keychain:target"); err == nil {
		t.Fatal("migration CAS succeeded despite one-shot integrations persist failure")
	}
	h.saveIntegrations = nil
	after, err := h.SnapshotConnectionControl(connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.CredentialRef != before.CredentialRef || after.Revision <= before.Revision {
		t.Fatalf("compensated control = %#v, want previous business value with epoch > %d", after, before.Revision)
	}
	if err := h.MatchConnectionControl(before); err == nil {
		t.Fatal("pre-migration snapshot matched after a durable epoch was allocated")
	}
	currentReceipt, err := h.GetCredentialMigration(receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if currentReceipt.ConnectionRevision != after.Revision || currentReceipt.Version <= receipt.Version {
		t.Fatalf("active reconcile receipt = %#v, control=%#v", currentReceipt, after)
	}
	if err := h.MatchCredentialMigrationIdentity(currentReceipt, receipt.PreviousCredentialRef); err != nil {
		t.Fatalf("compensated active reconcile identity does not match: %v", err)
	}
}

func TestCredentialMigrationReceiptPersistsAndFencesOnePerConnection(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := OpenWithOptions(st, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	connection, err := h.CreateConnection(ConnectionParams{
		Provider: "lark", AccountRef: "app-receipt", CredentialRef: "keychain:receipt-source", Enabled: &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, created, err := h.BeginCredentialMigration(connection)
	if err != nil || !created {
		t.Fatalf("begin receipt = %#v, created = %v, err = %v", receipt, created, err)
	}
	receipt.State = CredentialMigrationCredentialStored
	receipt.TargetCredentialRef = "managed:crd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	saved, err := h.SaveCredentialMigration(receipt, receipt.Version)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != receipt.Version+1 || saved.CredentialsIncluded || saved.RunnableRestore {
		t.Fatalf("saved receipt = %#v", saved)
	}
	if _, err := h.SaveCredentialMigration(saved, receipt.Version); err == nil {
		t.Fatal("stale receipt version unexpectedly overwrote durable state")
	}
	h.Shutdown()

	reloaded, err := OpenWithOptions(st, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Shutdown()
	loaded, err := reloaded.GetCredentialMigration(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != saved.ID || loaded.ConnectionID != connection.ID || loaded.State != CredentialMigrationCredentialStored || loaded.TargetCredentialRef != saved.TargetCredentialRef || loaded.Version != saved.Version {
		t.Fatalf("reloaded receipt = %#v", loaded)
	}
	repeated, created, err := reloaded.BeginCredentialMigration(connection)
	if err != nil || created || repeated.ID != saved.ID || repeated.Version != saved.Version {
		t.Fatalf("repeated begin = %#v, created = %v, err = %v", repeated, created, err)
	}
	if receipts := reloaded.ListCredentialMigrations(); len(receipts) != 1 || receipts[0].ID != saved.ID {
		t.Fatalf("receipt list = %#v", receipts)
	}
}

func TestConnectionAcceptsOnlyExistingProviderMatchedManagedReference(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := OpenWithOptions(st, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	credentials, err := h.CredentialStore()
	if err != nil {
		t.Fatal(err)
	}
	reference, _, err := credentials.PutBound("test/provider-binding", credentialstore.Payload{
		Provider: "lark", Kind: "app-secret", Values: map[string]string{"appSecret": randomHubCredential(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := h.CreateConnection(ConnectionParams{Provider: "lark", AccountRef: "app-managed", CredentialRef: reference})
	if err != nil || connection.CredentialRef != reference {
		t.Fatalf("managed Connection creation failed: %v", err)
	}
	if _, err := h.CreateConnection(ConnectionParams{Provider: "slack", CredentialRef: reference}); err == nil {
		t.Fatal("managed credential was bound to the wrong provider")
	}
	if _, err := h.CreateConnection(ConnectionParams{Provider: "lark", CredentialRef: "managed:crd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err == nil {
		t.Fatal("missing managed credential was accepted")
	}
	if _, err := h.UpdateConnection(connection.ID, ConnectionParams{CredentialRef: "managed:../../outside"}); err == nil {
		t.Fatal("path-like managed credential reference was accepted")
	}
	if _, err := h.UpdateConnection(connection.ID, ConnectionParams{Provider: "slack"}); err == nil {
		t.Fatal("provider-only update left a managed credential bound to the wrong provider")
	}
}

func TestCredentialMigrationReservationFencesConnectionAndAddressIdentity(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAgents(map[string]*Agent{
		"agent-fence": {ID: "agent-fence", Name: "agent-fence", Status: "idle", CreatedAt: "2026-08-07T00:00:00Z", UpdatedAt: "2026-08-07T00:00:00Z"},
	}); err != nil {
		t.Fatal(err)
	}
	h, err := OpenWithOptions(st, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	enabled := true
	connection, err := h.CreateConnection(ConnectionParams{Provider: "lark", AccountRef: "A_FENCE", CredentialRef: "keychain:fence", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(AddressParams{
		Agent: "agent-fence", ConnectionID: connection.ID, ExternalIdentity: "bot-fence",
		TriggerPolicy: "mention", ReplyPolicy: "final_answer", TrustDomain: "fence",
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, created, err := h.BeginCredentialMigration(connection)
	if err != nil || !created || receipt.ConnectionRevision < 1 || len(receipt.AddressSnapshots) != 1 || receipt.AddressSnapshots[0].ID != address.ID {
		t.Fatalf("migration identity snapshot = %#v, created=%v, err=%v", receipt, created, err)
	}
	if _, err := h.UpdateConnection(connection.ID, ConnectionParams{AccountRef: "A_CHANGED"}); err == nil || err.Error() != "credential_migration_in_progress" {
		t.Fatalf("Connection identity mutation error = %v", err)
	}
	if _, err := h.UpdateAddress(address.ID, AddressParams{ExternalIdentity: "bot-changed"}); err == nil || err.Error() != "credential_migration_in_progress" {
		t.Fatalf("Address identity mutation error = %v", err)
	}
	if _, err := h.CreateAddress(AddressParams{
		Agent: "agent-fence", ConnectionID: connection.ID, ExternalIdentity: "bot-new",
		TriggerPolicy: "mention", ReplyPolicy: "final_answer", TrustDomain: "fence",
	}); err == nil || err.Error() != "credential_migration_in_progress" {
		t.Fatalf("new Address binding error = %v", err)
	}
	if _, err := h.HeartbeatConnection(connection.ID, ConnectionHeartbeatParams{Status: "connected"}); err != nil {
		t.Fatalf("heartbeat was incorrectly fenced: %v", err)
	}
	if err := h.MatchCredentialMigrationIdentity(receipt, receipt.PreviousCredentialRef); err != nil {
		t.Fatalf("heartbeat changed the frozen migration identity: %v", err)
	}
}

func TestCredentialMigrationEffectAttemptFreezesTargetProof(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := OpenWithOptions(st, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	connection, err := h.CreateConnection(ConnectionParams{Provider: "lark", AccountRef: "A_EFFECT", CredentialRef: "keychain:effect"})
	if err != nil {
		t.Fatal(err)
	}
	receipt, _, err := h.BeginCredentialMigration(connection)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := h.CredentialStore()
	if err != nil {
		t.Fatal(err)
	}
	targetRef, _, err := credentials.PutBound("lark/app-secret/A_EFFECT", credentialstore.Payload{
		Provider: "lark", Kind: "app-secret", Values: map[string]string{"appID": "A_EFFECT", "appSecret": randomHubCredential(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt.TargetCredentialRef = targetRef
	receipt.State = CredentialMigrationGatewayActivating
	receipt.GatewayEffectID = "geff_attempt_one"
	receipt.GatewayEffectAttempt = 1
	receipt.GatewayEffectState = "activation_prepared"
	receipt.GatewayReceipt = &CredentialMigrationGatewayReceipt{
		Status: "activation_prepared", Manager: "launchd", Service: "svc-effect", AnchorID: receipt.ID,
		Build: "build-effect", ExecutableSHA256: strings.Repeat("a", 64), Generation: "ggen-effect-1",
	}
	receipt, err = h.SaveCredentialMigrationControlled(receipt, receipt.Version, receipt.PreviousCredentialRef)
	if err != nil {
		t.Fatal(err)
	}
	tampered := cloneCredentialMigrationReceipt(receipt)
	tampered.GatewayReceipt.Build = "build-tampered"
	if _, err := h.SaveCredentialMigration(tampered, tampered.Version); err == nil {
		t.Fatal("same activation attempt changed its target proof")
	}
	tampered = cloneCredentialMigrationReceipt(receipt)
	tampered.GatewayEffectAttempt++
	if _, err := h.SaveCredentialMigration(tampered, tampered.Version); err == nil {
		t.Fatal("new activation attempt reused the prior effect identity")
	}
}

func TestConnectionControlSnapshotIgnoresHeartbeatButDetectsIdentityDrift(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := OpenWithOptions(st, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	connection, err := h.CreateConnection(ConnectionParams{Provider: "lark", AccountRef: "app-a", ScopeRef: "scope-a", CredentialRef: "keychain:test"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := h.SnapshotConnectionControl(connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.HeartbeatConnection(connection.ID, ConnectionHeartbeatParams{Status: "connected", Cursor: "cursor-2"}); err != nil {
		t.Fatal(err)
	}
	if err := h.MatchConnectionControl(snapshot); err != nil {
		t.Fatalf("heartbeat invalidated control snapshot: %v", err)
	}
	disabled := false
	if _, err := h.UpdateConnection(connection.ID, ConnectionParams{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if err := h.MatchConnectionControl(snapshot); err == nil {
		t.Fatal("enabled-state drift matched the frozen control snapshot")
	}
	enabled := true
	if _, err := h.UpdateConnection(connection.ID, ConnectionParams{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if err := h.MatchConnectionControl(snapshot); err == nil {
		t.Fatal("control revision allowed enabled-state ABA to match the frozen snapshot")
	}
	if _, err := h.UpdateConnection(connection.ID, ConnectionParams{AccountRef: "app-b"}); err != nil {
		t.Fatal(err)
	}
	if err := h.MatchConnectionControl(snapshot); err == nil {
		t.Fatal("identity drift matched the frozen control snapshot")
	}
}

func TestMigrationCredentialCASAdvancesControlEpochAcrossRollback(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := OpenWithOptions(st, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	connection, err := h.CreateConnection(ConnectionParams{Provider: "lark", AccountRef: "app-epoch", CredentialRef: "keychain:epoch"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := h.SnapshotConnectionControl(connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _, err := h.BeginCredentialMigration(connection)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := h.CredentialStore()
	if err != nil {
		t.Fatal(err)
	}
	targetRef, _, err := credentials.PutBound("lark/app-secret/app-epoch", credentialstore.Payload{
		Provider: "lark", Kind: "app-secret", Values: map[string]string{"appID": "app-epoch", "appSecret": randomHubCredential(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt.TargetCredentialRef = targetRef
	receipt.State = CredentialMigrationSwitchingReference
	receipt, err = h.SaveCredentialMigrationControlled(receipt, receipt.Version, receipt.PreviousCredentialRef)
	if err != nil {
		t.Fatal(err)
	}
	_, afterMigration, err := h.CompareAndSwapConnectionCredentialForMigration(receipt.ID, receipt.PreviousCredentialRef, targetRef)
	if err != nil {
		t.Fatal(err)
	}
	if afterMigration.ConnectionRevision <= before.Revision {
		t.Fatalf("migration revision = %d, before=%d", afterMigration.ConnectionRevision, before.Revision)
	}
	_, afterRollback, err := h.CompareAndSwapConnectionCredentialForMigration(receipt.ID, targetRef, receipt.PreviousCredentialRef)
	if err != nil {
		t.Fatal(err)
	}
	if afterRollback.ConnectionRevision <= afterMigration.ConnectionRevision {
		t.Fatalf("rollback revision = %d, migration=%d", afterRollback.ConnectionRevision, afterMigration.ConnectionRevision)
	}
	if err := h.MatchConnectionControl(before); err == nil {
		t.Fatal("pre-migration snapshot matched after migrate and rollback ABA")
	}
	current, err := h.SnapshotConnectionControl(connection.ID)
	if err != nil || current.CredentialRef != receipt.PreviousCredentialRef || current.Revision != afterRollback.ConnectionRevision {
		t.Fatalf("rollback control = %#v, receipt=%#v, err=%v", current, afterRollback, err)
	}
}

func TestMigrationCredentialCASFailsClosedWhenIntegrationPersistCannotRecover(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := OpenWithOptions(st, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	connection, err := h.CreateConnection(ConnectionParams{Provider: "lark", AccountRef: "app-persist", CredentialRef: "keychain:persist"})
	if err != nil {
		t.Fatal(err)
	}
	receipt, _, err := h.BeginCredentialMigration(connection)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := h.CredentialStore()
	if err != nil {
		t.Fatal(err)
	}
	targetRef, _, err := credentials.PutBound("lark/app-secret/app-persist", credentialstore.Payload{
		Provider: "lark", Kind: "app-secret", Values: map[string]string{"appID": "app-persist", "appSecret": randomHubCredential(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt.TargetCredentialRef = targetRef
	receipt.State = CredentialMigrationSwitchingReference
	receipt, err = h.SaveCredentialMigrationControlled(receipt, receipt.Version, receipt.PreviousCredentialRef)
	if err != nil {
		t.Fatal(err)
	}
	before, err := h.SnapshotConnectionControl(connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	integrationsPath := filepath.Join(st.Dir(), "integrations.json")
	if err := os.Remove(integrationsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(integrationsPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.CompareAndSwapConnectionCredentialForMigration(receipt.ID, receipt.PreviousCredentialRef, targetRef); err == nil {
		t.Fatal("migration CAS succeeded despite integration persistence failure")
	}
	after, err := h.SnapshotConnectionControl(connection.ID)
	if err != nil || after.CredentialRef != before.CredentialRef || after.Revision <= before.Revision {
		t.Fatalf("control after failed persist = %#v, want previous business value with epoch > %d, err=%v", after, before.Revision, err)
	}
	if err := h.MatchConnectionControl(before); err == nil {
		t.Fatal("indeterminate integration persistence allowed the old control snapshot to match")
	}
	currentReceipt, err := h.GetCredentialMigration(receipt.ID)
	if err != nil || currentReceipt.ConnectionRevision <= receipt.ConnectionRevision || currentReceipt.Version <= receipt.Version {
		t.Fatalf("fail-closed receipt after persist failure = %#v, prior revision=%d version=%d, err=%v", currentReceipt, receipt.ConnectionRevision, receipt.Version, err)
	}
	if err := h.MatchCredentialMigrationIdentity(currentReceipt, receipt.PreviousCredentialRef); err == nil {
		t.Fatal("indeterminate persisted epoch matched the restored control snapshot")
	}
	if err := h.RequireCredentialMigrationsIdle(connection.ID); err == nil {
		t.Fatal("indeterminate persisted epoch released the active migration reservation")
	}
}

func TestPassiveOpenProjectsLegacyCredentialStateWithoutWritingFiles(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	integrationsPath := filepath.Join(dir, "integrations.json")
	integrations := []byte(`{
  "connections": {
    "conn_legacy": {
      "id": "conn_legacy",
      "provider": "lark",
      "accountRef": "app-legacy",
      "credentialRef": "keychain:legacy",
      "status": "disconnected",
      "enabled": true,
      "createdAt": "2026-08-07T00:00:00Z",
      "updatedAt": "2026-08-07T00:00:00Z"
    }
  }
}`)
	if err := os.WriteFile(integrationsPath, integrations, 0o600); err != nil {
		t.Fatal(err)
	}
	migrationsPath := filepath.Join(dir, "credential-migrations.json")
	migrations := []byte(`{
  "cmig_legacy_passive": {
    "id": "cmig_legacy_passive",
    "connectionId": "conn_legacy",
    "provider": "lark",
    "state": "gateway_activating",
    "previousCredentialRef": "keychain:legacy",
    "gatewayEffectId": "geff_legacy_passive",
    "gatewayEffectState": "activation_prepared",
    "credentialsIncluded": false,
    "runnableRestore": false,
    "createdAt": "2026-08-07T00:00:00Z",
    "updatedAt": "2026-08-07T00:00:00Z",
    "version": 1
  }
}`)
	if err := os.WriteFile(migrationsPath, migrations, 0o600); err != nil {
		t.Fatal(err)
	}
	integrationsHash, migrationsHash := sha256.Sum256(integrations), sha256.Sum256(migrations)
	h, err := OpenWithOptions(st, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := h.SnapshotConnectionControl("conn_legacy")
	if err != nil || snapshot.Revision != 1 {
		t.Fatalf("passive integration projection = %#v, err=%v", snapshot, err)
	}
	receipt, err := h.GetCredentialMigration("cmig_legacy_passive")
	if err != nil || receipt.State != CredentialMigrationManualRecoveryRequired || receipt.GatewayEffectAttempt != 1 {
		t.Fatalf("passive migration projection = %#v, err=%v", receipt, err)
	}
	h.Shutdown()
	afterIntegrations, err := os.ReadFile(integrationsPath)
	if err != nil {
		t.Fatal(err)
	}
	afterMigrations, err := os.ReadFile(migrationsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterIntegrations, integrations) || sha256.Sum256(afterIntegrations) != integrationsHash {
		t.Fatal("passive open rewrote legacy integrations bytes")
	}
	if !bytes.Equal(afterMigrations, migrations) || sha256.Sum256(afterMigrations) != migrationsHash {
		t.Fatal("passive open rewrote legacy migration receipt bytes")
	}
}

func TestLegacyCandidateMigrationReceiptFailsClosedWithoutIdentitySnapshot(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := OpenWithOptions(st, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	connection, err := h.CreateConnection(ConnectionParams{Provider: "lark", AccountRef: "A_LEGACY", CredentialRef: "keychain:legacy", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	h.Shutdown()
	receiptID := "cmig_legacy_candidate"
	if err := st.SaveCredentialMigrations(map[string]*CredentialMigrationReceipt{
		receiptID: {
			ID: receiptID, ConnectionID: connection.ID, Provider: connection.Provider,
			State: CredentialMigrationGatewayActivating, PreviousCredentialRef: connection.CredentialRef,
			GatewayEffectID: "geff_legacy_candidate", GatewayEffectState: "activation_prepared",
			CreatedAt: "2026-08-07T00:00:00Z", UpdatedAt: "2026-08-07T00:00:00Z", Version: 1,
		},
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithOptions(st, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Shutdown()
	receipt, err := reopened.GetCredentialMigration(receiptID)
	if err != nil || receipt.State != CredentialMigrationManualRecoveryRequired || receipt.Error == nil || receipt.Error.Code != "migration_identity_snapshot_unavailable" {
		t.Fatalf("legacy candidate receipt = %#v, err=%v", receipt, err)
	}
}

func TestConnectionHeartbeatPersistsAndClearsOptionalGatewayEvidence(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := OpenWithOptions(st, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	connection, err := h.CreateConnection(ConnectionParams{Provider: "lark", CredentialRef: "keychain:test"})
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	observed, err := h.HeartbeatConnection(connection.ID, ConnectionHeartbeatParams{
		Status: "connected", GatewayGeneration: "ggen_test", GatewayBuild: "build-test",
		GatewayExecutableSHA256: digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.GatewayGeneration != "ggen_test" || observed.GatewayBuild != "build-test" || observed.GatewayExecutableSHA256 != digest {
		t.Fatalf("observed gateway evidence = %#v", observed)
	}
	legacy, err := h.HeartbeatConnection(connection.ID, ConnectionHeartbeatParams{Status: "connected"})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.GatewayGeneration != "" || legacy.GatewayBuild != "" || legacy.GatewayExecutableSHA256 != "" {
		t.Fatalf("legacy heartbeat retained stale evidence = %#v", legacy)
	}
	if _, err := h.HeartbeatConnection(connection.ID, ConnectionHeartbeatParams{GatewayExecutableSHA256: "BAD"}); err == nil {
		t.Fatal("invalid gateway digest accepted")
	}
	if _, err := h.HeartbeatConnection(connection.ID, ConnectionHeartbeatParams{GatewayBuild: "unknown"}); err == nil {
		t.Fatal("placeholder gateway build accepted")
	}
	if _, err := h.HeartbeatConnection(connection.ID, ConnectionHeartbeatParams{GatewayGeneration: "bad\ngeneration"}); err == nil {
		t.Fatal("control character in gateway generation accepted")
	}
}

func TestConnectionManualRecoveryLatchSurvivesOrdinaryHeartbeat(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := OpenWithOptions(st, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	connection, err := h.CreateConnection(ConnectionParams{Provider: "lark", CredentialRef: "keychain:test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.MarkConnectionManualRecovery(connection.ID, "manual_recovery_required: fixture"); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("d", 64)
	started := time.Now().UTC().Add(-time.Second)
	observed, err := h.HeartbeatConnection(connection.ID, ConnectionHeartbeatParams{
		Status: "connected", GatewayGeneration: "ggen_late", GatewayBuild: "build-test",
		GatewayExecutableSHA256: digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Status != "disconnected" || observed.LastError != "manual_recovery_required: fixture" {
		t.Fatalf("ordinary heartbeat cleared durable manual recovery latch: %#v", observed)
	}
	if observed.GatewayGeneration != "ggen_late" || observed.GatewayBuild != "build-test" || observed.GatewayExecutableSHA256 != digest {
		t.Fatalf("latched heartbeat did not retain reconciliation proof: %#v", observed)
	}
	reconciled, err := h.ClearConnectionManualRecovery(connection.ID, started, CredentialMigrationGatewayReceipt{
		Generation: "ggen_late", Build: "build-test", ExecutableSHA256: digest,
	})
	if err != nil || reconciled.Status != "connected" || reconciled.LastError != "" {
		t.Fatalf("explicit exact recovery proof did not clear latch: %#v, err=%v", reconciled, err)
	}
}

func TestConnectionManualRecoveryClearPersistFailureStaysLatched(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := OpenWithOptions(st, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := h.CreateConnection(ConnectionParams{Provider: "lark", CredentialRef: "keychain:test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.MarkConnectionManualRecovery(connection.ID, "manual_recovery_required: fixture"); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("f", 64)
	started := time.Now().UTC().Add(-time.Second)
	if _, err := h.HeartbeatConnection(connection.ID, ConnectionHeartbeatParams{
		Status: "connected", GatewayGeneration: "ggen_reconcile", GatewayBuild: "build-test", GatewayExecutableSHA256: digest,
	}); err != nil {
		t.Fatal(err)
	}
	integrationsPath := filepath.Join(st.Dir(), "integrations.json")
	backupPath := integrationsPath + ".fixture"
	if err := os.Rename(integrationsPath, backupPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(integrationsPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ClearConnectionManualRecovery(connection.ID, started, CredentialMigrationGatewayReceipt{
		Generation: "ggen_reconcile", Build: "build-test", ExecutableSHA256: digest,
	}); err == nil {
		t.Fatal("manual recovery latch cleared despite persistence failure")
	}
	if err := os.Remove(integrationsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backupPath, integrationsPath); err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	var current PlatformConnection
	for _, candidate := range h.ListConnections() {
		if candidate.ID == connection.ID {
			current = candidate
		}
	}
	if !ConnectionManualRecoveryRequired(current) || current.Status != "disconnected" {
		t.Fatalf("latch persistence failure left connection clear: %#v", current)
	}
}

func TestTransientConnectionDisconnectStillClearsOnHeartbeat(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := OpenWithOptions(st, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	connection, err := h.CreateConnection(ConnectionParams{Provider: "lark", CredentialRef: "keychain:test"})
	if err != nil {
		t.Fatal(err)
	}
	h.MarkConnectionDisconnected(connection.ID, "connector command stream closed")
	observed, err := h.HeartbeatConnection(connection.ID, ConnectionHeartbeatParams{Status: "connected"})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Status != "connected" || observed.LastError != "" || ConnectionManualRecoveryRequired(observed) {
		t.Fatalf("ordinary reconnect remained latched: %#v", observed)
	}
}

func randomHubCredential(t *testing.T) string {
	t.Helper()
	buffer := make([]byte, 48)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatal("random test credential generation failed")
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}
