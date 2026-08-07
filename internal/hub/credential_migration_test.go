package hub

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/yan5xu/codex-loom/internal/credentialstore"
	"github.com/yan5xu/codex-loom/internal/store"
)

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

func randomHubCredential(t *testing.T) string {
	t.Helper()
	buffer := make([]byte, 48)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatal("random test credential generation failed")
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}
