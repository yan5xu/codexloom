package hub

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

type addressLifecycleFixture struct {
	h          *Hub
	st         *store.Store
	connection PlatformConnection
	address    AgentAddress
	membership ConversationMembership
}

func newAddressLifecycleFixture(t *testing.T) addressLifecycleFixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := New(st)
	seedInboxAgent(t, h, "agent-a", "alpha")
	seedInboxAgent(t, h, "agent-b", "beta")
	connection, err := h.CreateConnection(ConnectionParams{Provider: "parall", Capabilities: []string{"receive_events", "provider_native_read"}})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(AddressParams{
		Agent: "alpha", ConnectionID: connection.ID, ExternalIdentity: "prll://usr_alpha",
		TriggerPolicy: "explicit_dispatch", ReplyPolicy: "final_answer", DMPolicy: "open", TrustDomain: "workspace-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	membership, _, err := h.UpsertConversationMembership(ConversationMembershipParams{
		AddressID: address.ID, ConversationID: "chat-1", DisplayName: stringPointer("Product group"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return addressLifecycleFixture{h: h, st: st, connection: connection, address: address, membership: membership}
}

func TestManagedAddressArchiveAndRestorePreserveMembershipAndAudit(t *testing.T) {
	fixture := newAddressLifecycleFixture(t)
	if _, err := fixture.h.ReplaceConversationCandidates(fixture.address.ID, ConversationCandidateSnapshotParams{Conversations: []ConversationCandidateParams{{
		ConversationID: "chat-1", ConversationType: "group", DisplayName: "Product group",
	}}}); err != nil {
		t.Fatal(err)
	}

	plan, err := fixture.h.PreflightAddressLifecycle(fixture.address.ID, AddressLifecycleParams{Action: AddressLifecycleArchive})
	if err != nil || !plan.Allowed || plan.CurrentVersion != 1 || plan.EnabledMembershipCount != 1 {
		t.Fatalf("archive preflight = %#v, err=%v", plan, err)
	}
	stalePlan, err := fixture.h.PreflightAddressLifecycle(fixture.address.ID, AddressLifecycleParams{
		Action: AddressLifecycleArchive, ExpectedVersion: intPointer(0),
	})
	if err != nil || stalePlan.Allowed || !hasLifecycleBlocker(stalePlan, "expected_version", fixture.address.ID) {
		t.Fatalf("stale archive preflight = %#v, err=%v", stalePlan, err)
	}
	if _, err := fixture.h.ApplyAddressLifecycle(fixture.address.ID, AddressLifecycleParams{
		Action: AddressLifecycleArchive, ExpectedVersion: intPointer(1),
	}); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("archive without confirmation error = %v", err)
	}

	archived, err := fixture.h.ApplyAddressLifecycle(fixture.address.ID, AddressLifecycleParams{
		Action: AddressLifecycleArchive, ExpectedVersion: intPointer(1), Confirm: fixture.address.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if archived.Address.Enabled || archived.Address.ArchivedAt == "" || archived.Address.DeletedAt != "" || archived.Address.Version != 2 {
		t.Fatalf("archived address = %#v", archived.Address)
	}
	if archived.Operation == nil || archived.Operation.Action != AddressLifecycleArchive || len(archived.Operation.MembershipsBefore) != 1 {
		t.Fatalf("archive receipt = %#v", archived.Operation)
	}
	membership, err := fixture.h.GetConversationMembership(fixture.membership.ID)
	if err != nil || membership.Enabled || membership.ArchivedAt == "" || membership.Version != 2 {
		t.Fatalf("archived membership = %#v, err=%v", membership, err)
	}
	candidates, err := fixture.h.ListConversationCandidates("", fixture.address.ID, false)
	if err != nil || len(candidates) != 1 || candidates[0].Available {
		t.Fatalf("archived candidates = %#v, err=%v", candidates, err)
	}
	if _, err := fixture.h.UpdateAddress(fixture.address.ID, AddressParams{Enabled: boolPointer(true)}); err == nil {
		t.Fatal("ordinary enable restored an archived address")
	}

	fixture.h.Shutdown()
	reloaded := New(fixture.st)
	defer reloaded.Shutdown()
	operations := reloaded.ListAddressLifecycleOperations(fixture.address.ID)
	if len(operations) != 1 || operations[0].ID != archived.Operation.ID {
		t.Fatalf("persisted operations = %#v", operations)
	}
	restorePlan, err := reloaded.PreflightAddressLifecycle(fixture.address.ID, AddressLifecycleParams{Action: AddressLifecycleRestore})
	if err != nil || !restorePlan.Allowed || restorePlan.SourceOperationID != archived.Operation.ID {
		t.Fatalf("restore preflight = %#v, err=%v", restorePlan, err)
	}
	restored, err := reloaded.ApplyAddressLifecycle(fixture.address.ID, AddressLifecycleParams{
		Action: AddressLifecycleRestore, ExpectedVersion: intPointer(2), Confirm: fixture.address.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Address.Enabled || restored.Address.ArchivedAt != "" || restored.Address.Version != 3 {
		t.Fatalf("restored address = %#v", restored.Address)
	}
	membership, err = reloaded.GetConversationMembership(fixture.membership.ID)
	if err != nil || !membership.Enabled || membership.ArchivedAt != "" || membership.Version != 3 {
		t.Fatalf("restored membership = %#v, err=%v", membership, err)
	}
	archiveReceipt, err := reloaded.GetAddressLifecycleOperation(archived.Operation.ID)
	if err != nil || archiveReceipt.ReversedBy != restored.Operation.ID {
		t.Fatalf("reversed archive receipt = %#v, err=%v", archiveReceipt, err)
	}
}

func TestAddressLifecyclePreflightBlocksActiveWork(t *testing.T) {
	fixture := newAddressLifecycleFixture(t)
	ingress, err := fixture.h.IngestMessage(lifecycleIngress(fixture, "msg-active"))
	if err != nil {
		t.Fatal(err)
	}
	failedInbox := seedFailedLifecycleInbox(t, fixture, "msg-failed-blocker")
	outbox, err := fixture.h.CreateOutbox(OutboxParams{
		Agent: "alpha", AddressID: fixture.address.ID,
		Conversation: ConversationRef{ConversationID: "chat-1"}, Content: MessageContent{Text: "pending"},
	})
	if err != nil {
		t.Fatal(err)
	}
	providerOperation, err := fixture.h.CreateProviderOperation(ProviderOperationParams{
		Provider: "parall", AddressID: fixture.address.ID, Resource: "chats", Action: "list",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := fixture.h.PreflightAddressLifecycle(fixture.address.ID, AddressLifecycleParams{Action: AddressLifecycleTransfer, TargetAgent: "beta"})
	if err != nil || plan.Allowed {
		t.Fatalf("blocked transfer preflight = %#v, err=%v", plan, err)
	}
	for _, want := range []struct{ kind, id string }{{"inbox", ingress.InboxItem.ID}, {"inbox", failedInbox.ID}, {"outbox", outbox.ID}, {"provider_operation", providerOperation.ID}} {
		if !hasLifecycleBlocker(plan, want.kind, want.id) {
			t.Fatalf("preflight missing %s %s: %#v", want.kind, want.id, plan.Blockers)
		}
	}
	if _, err := fixture.h.ApplyAddressLifecycle(fixture.address.ID, AddressLifecycleParams{
		Action: AddressLifecycleTransfer, TargetAgent: "beta", ExpectedVersion: intPointer(1), Confirm: fixture.address.ID,
	}); err == nil {
		t.Fatal("transfer with active work succeeded")
	}
	addresses, err := fixture.h.ListAddresses("alpha")
	if err != nil || len(addresses) != 1 || addresses[0].AgentID != "agent-a" || addresses[0].Version != 1 {
		t.Fatalf("blocked transfer changed address: %#v, err=%v", addresses, err)
	}
}

func TestManagedAddressRestorePreservesPreviouslyDisabledState(t *testing.T) {
	fixture := newAddressLifecycleFixture(t)
	disabled := false
	address, err := fixture.h.UpdateAddress(fixture.address.ID, AddressParams{Enabled: &disabled})
	if err != nil || address.Enabled || address.Version != 2 {
		t.Fatalf("disabled address = %#v, err=%v", address, err)
	}
	archived, err := fixture.h.ApplyAddressLifecycle(fixture.address.ID, AddressLifecycleParams{
		Action: AddressLifecycleArchive, ExpectedVersion: intPointer(2), Confirm: fixture.address.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := fixture.h.ApplyAddressLifecycle(fixture.address.ID, AddressLifecycleParams{
		Action: AddressLifecycleRestore, ExpectedVersion: intPointer(3), Confirm: fixture.address.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Address.Enabled || restored.Address.ArchivedAt != "" || restored.Address.Version != 4 {
		t.Fatalf("restore did not preserve disabled state: %#v (archive=%#v)", restored.Address, archived.Address)
	}
}

func TestManagedAddressDeleteIsTerminalButReleasesIdentity(t *testing.T) {
	fixture := newAddressLifecycleFixture(t)
	failedInbox := seedFailedLifecycleInbox(t, fixture, "msg-failed")
	failedOutbox := seedFailedLifecycleOutbox(t, fixture)
	if _, err := fixture.h.NoReplyInboxItem(failedInbox.ID, InboxActionParams{Agent: "alpha", Reason: "terminal before delete"}); err != nil {
		t.Fatal(err)
	}

	plan, err := fixture.h.PreflightAddressLifecycle(fixture.address.ID, AddressLifecycleParams{Action: AddressLifecycleDelete})
	if err != nil || !plan.Allowed || len(plan.Warnings) != 1 {
		t.Fatalf("delete preflight = %#v, err=%v", plan, err)
	}
	deleted, err := fixture.h.ApplyAddressLifecycle(fixture.address.ID, AddressLifecycleParams{
		Action: AddressLifecycleDelete, ExpectedVersion: intPointer(1), Confirm: fixture.address.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Address.DeletedAt == "" || deleted.Address.ArchivedAt == "" || deleted.Address.Enabled || deleted.Address.Version != 2 {
		t.Fatalf("deleted address = %#v", deleted.Address)
	}
	restorePlan, err := fixture.h.PreflightAddressLifecycle(fixture.address.ID, AddressLifecycleParams{Action: AddressLifecycleRestore})
	if err != nil || restorePlan.Allowed || !hasLifecycleBlocker(restorePlan, "deleted", fixture.address.ID) {
		t.Fatalf("deleted restore preflight = %#v, err=%v", restorePlan, err)
	}
	if _, err := fixture.h.RetryOutboxItem(failedOutbox.ID); err == nil {
		t.Fatal("failed Outbox item was retryable after delete")
	}
	if _, _, err := fixture.h.GetInboxItem(failedInbox.ID); err != nil {
		t.Fatalf("deleted address lost Inbox history: %v", err)
	}
	if _, err := fixture.h.GetOutbox(failedOutbox.ID); err != nil {
		t.Fatalf("deleted address lost Outbox history: %v", err)
	}
	rebound, err := fixture.h.CreateAddress(AddressParams{
		Agent: "beta", ConnectionID: fixture.connection.ID, ExternalIdentity: fixture.address.ExternalIdentity,
		TrustDomain: fixture.address.TrustDomain,
	})
	if err != nil || rebound.ID == fixture.address.ID {
		t.Fatalf("identity was not released by terminal delete: %#v, err=%v", rebound, err)
	}
	redelivery := lifecycleIngress(fixture, "msg-failed")
	redelivery.AddressID = rebound.ID
	duplicate, err := fixture.h.IngestMessage(redelivery)
	if err != nil || !duplicate.Duplicate || duplicate.InboxItem == nil || duplicate.InboxItem.ID != failedInbox.ID {
		t.Fatalf("rebound identity duplicated historical ingress: %#v, err=%v", duplicate, err)
	}
}

func TestAddressTransferPreservesStableReferencesAndSupportsCleanRollback(t *testing.T) {
	fixture := newAddressLifecycleFixture(t)
	handled, err := fixture.h.IngestMessage(lifecycleIngress(fixture, "msg-handled"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.NoReplyInboxItem(handled.InboxItem.ID, InboxActionParams{Agent: "alpha", Reason: "historical"}); err != nil {
		t.Fatal(err)
	}
	transferred, err := fixture.h.ApplyAddressLifecycle(fixture.address.ID, AddressLifecycleParams{
		Action: AddressLifecycleTransfer, TargetAgent: "beta", ExpectedVersion: intPointer(1), Confirm: fixture.address.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if transferred.Address.ID != fixture.address.ID || transferred.Address.AgentID != "agent-b" || transferred.Address.Version != 2 {
		t.Fatalf("transferred address = %#v", transferred.Address)
	}
	membership, err := fixture.h.GetConversationMembership(fixture.membership.ID)
	if err != nil || membership.AddressID != fixture.address.ID || !membership.Enabled || membership.Version != fixture.membership.Version {
		t.Fatalf("membership changed during transfer: %#v, err=%v", membership, err)
	}
	history, err := fixture.h.ListInbox("alpha", "handled", "")
	if err != nil || len(history) != 1 || history[0].ID != handled.InboxItem.ID || history[0].AddressID != fixture.address.ID {
		t.Fatalf("source history changed = %#v, err=%v", history, err)
	}
	redelivered, err := fixture.h.IngestMessage(lifecycleIngress(fixture, "msg-handled"))
	if err != nil || !redelivered.Duplicate || redelivered.InboxItem == nil || redelivered.InboxItem.ID != handled.InboxItem.ID || redelivered.InboxItem.AgentID != "agent-a" {
		t.Fatalf("post-transfer duplicate changed ownership: %#v, err=%v", redelivered, err)
	}

	fixture.h.Shutdown()
	reloaded := New(fixture.st)
	defer reloaded.Shutdown()
	rollbackPlan, err := reloaded.PreflightAddressTransferRollback(transferred.Operation.ID)
	if err != nil || !rollbackPlan.Allowed {
		t.Fatalf("clean rollback preflight = %#v, err=%v", rollbackPlan, err)
	}
	staleRollback, err := reloaded.RollbackAddressTransfer(transferred.Operation.ID, AddressTransferRollbackParams{
		ExpectedVersion: intPointer(1), DryRun: true,
	})
	if err != nil || staleRollback.Preflight.Allowed || !hasLifecycleBlocker(staleRollback.Preflight, "expected_version", fixture.address.ID) {
		t.Fatalf("stale rollback preflight = %#v, err=%v", staleRollback.Preflight, err)
	}
	rolledBack, err := reloaded.RollbackAddressTransfer(transferred.Operation.ID, AddressTransferRollbackParams{
		ExpectedVersion: intPointer(2), Confirm: transferred.Operation.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Address.AgentID != "agent-a" || rolledBack.Address.Version != 3 || rolledBack.Operation.SourceOperationID != transferred.Operation.ID {
		t.Fatalf("rolled back address = %#v operation=%#v", rolledBack.Address, rolledBack.Operation)
	}
	transferReceipt, err := reloaded.GetAddressLifecycleOperation(transferred.Operation.ID)
	if err != nil || transferReceipt.ReversedBy != rolledBack.Operation.ID {
		t.Fatalf("transfer receipt after rollback = %#v, err=%v", transferReceipt, err)
	}
}

func TestAddressTransferAllowsTargetDisabledPlaceholderInSameTrustDomain(t *testing.T) {
	fixture := newAddressLifecycleFixture(t)
	disabled := false
	placeholder, err := fixture.h.CreateAddress(AddressParams{
		Agent: "beta", ConnectionID: fixture.connection.ID, ExternalIdentity: "usr_alpha",
		TrustDomain: fixture.address.TrustDomain, Enabled: &disabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := fixture.h.PreflightAddressLifecycle(fixture.address.ID, AddressLifecycleParams{
		Action: AddressLifecycleTransfer, TargetAgent: "beta",
	})
	if err != nil || !plan.Allowed || plan.ToAgentID != "agent-b" {
		t.Fatalf("placeholder transfer preflight = %#v, err=%v", plan, err)
	}
	transferred, err := fixture.h.ApplyAddressLifecycle(fixture.address.ID, AddressLifecycleParams{
		Action: AddressLifecycleTransfer, TargetAgent: "beta", ExpectedVersion: intPointer(1), Confirm: fixture.address.ID,
	})
	if err != nil || transferred.Address.AgentID != "agent-b" || transferred.Address.ExternalIdentity != "prll://usr_alpha" {
		t.Fatalf("placeholder transfer = %#v, err=%v", transferred, err)
	}
	addresses, err := fixture.h.ListAddresses("beta")
	if err != nil || len(addresses) != 2 {
		t.Fatalf("target addresses = %#v, err=%v", addresses, err)
	}
	for _, address := range addresses {
		if address.ID == placeholder.ID && (address.Enabled || address.ExternalIdentity != "usr_alpha") {
			t.Fatalf("placeholder changed during canonical transfer: %#v", address)
		}
	}
}

func TestAddressLifecycleNormalizesLegacyVersionsOnce(t *testing.T) {
	fixture := newAddressLifecycleFixture(t)
	fixture.h.mu.Lock()
	fixture.h.addresses[fixture.address.ID].Version = 0
	fixture.h.memberships[fixture.membership.ID].Version = 0
	err := fixture.h.persistIntegrationsLocked()
	fixture.h.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	fixture.h.Shutdown()
	reloaded := New(fixture.st)
	defer reloaded.Shutdown()
	addresses, err := reloaded.ListAddresses("alpha")
	if err != nil || len(addresses) != 1 || addresses[0].Version != 1 {
		t.Fatalf("normalized addresses = %#v, err=%v", addresses, err)
	}
	membership, err := reloaded.GetConversationMembership(fixture.membership.ID)
	if err != nil || membership.Version != 1 {
		t.Fatalf("normalized membership = %#v, err=%v", membership, err)
	}
	if operations := reloaded.ListAddressLifecycleOperations(fixture.address.ID); len(operations) != 0 {
		t.Fatalf("normalization fabricated lifecycle receipt: %#v", operations)
	}

	reloaded.Shutdown()
	reloadedAgain := New(fixture.st)
	reloadedAgain.Shutdown()
	addresses, err = reloadedAgain.ListAddresses("alpha")
	if err != nil || len(addresses) != 1 || addresses[0].Version != 1 {
		t.Fatalf("second normalization changed version: %#v, err=%v", addresses, err)
	}
}

func TestAddressTransferRollbackRejectsPostTransferActivity(t *testing.T) {
	fixture := newAddressLifecycleFixture(t)
	transferred, err := fixture.h.ApplyAddressLifecycle(fixture.address.ID, AddressLifecycleParams{
		Action: AddressLifecycleTransfer, TargetAgent: "beta", ExpectedVersion: intPointer(1), Confirm: fixture.address.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	newItem, err := fixture.h.IngestMessage(lifecycleIngress(fixture, "msg-after-transfer"))
	if err != nil {
		t.Fatal(err)
	}
	if newItem.InboxItem.AgentID != "agent-b" {
		t.Fatalf("post-transfer ingress target = %#v", newItem.InboxItem)
	}
	if _, err := fixture.h.NoReplyInboxItem(newItem.InboxItem.ID, InboxActionParams{Agent: "beta", Reason: "handled"}); err != nil {
		t.Fatal(err)
	}
	plan, err := fixture.h.PreflightAddressTransferRollback(transferred.Operation.ID)
	if err != nil || plan.Allowed || !hasLifecycleBlocker(plan, "post_transfer_inbox", newItem.InboxItem.ID) {
		t.Fatalf("post-activity rollback preflight = %#v, err=%v", plan, err)
	}
	if _, err := fixture.h.RollbackAddressTransfer(transferred.Operation.ID, AddressTransferRollbackParams{
		ExpectedVersion: intPointer(2), Confirm: transferred.Operation.ID,
	}); err == nil {
		t.Fatal("rollback succeeded after target activity")
	}
	addresses, err := fixture.h.ListAddresses("beta")
	if err != nil || len(addresses) != 1 || addresses[0].AgentID != "agent-b" {
		t.Fatalf("blocked rollback changed owner: %#v, err=%v", addresses, err)
	}
}

func TestAddressTransferRejectsTargetTrustDomainConflict(t *testing.T) {
	fixture := newAddressLifecycleFixture(t)
	second, err := fixture.h.CreateConnection(ConnectionParams{Provider: "lark"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := fixture.h.CreateAddress(AddressParams{
		Agent: "beta", ConnectionID: second.ID, ExternalIdentity: "lark://beta", TrustDomain: "other-domain",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := fixture.h.PreflightAddressLifecycle(fixture.address.ID, AddressLifecycleParams{Action: AddressLifecycleTransfer, TargetAgent: "beta"})
	if err != nil || plan.Allowed || !hasLifecycleBlocker(plan, "trust_domain", other.ID) {
		t.Fatalf("trust-domain preflight = %#v, err=%v", plan, err)
	}
}

func TestAddressTransferRejectsArchivedConnection(t *testing.T) {
	fixture := newAddressLifecycleFixture(t)
	fixture.h.mu.Lock()
	fixture.h.connections[fixture.connection.ID].Enabled = false
	fixture.h.connections[fixture.connection.ID].ArchivedAt = now()
	fixture.h.mu.Unlock()
	plan, err := fixture.h.PreflightAddressLifecycle(fixture.address.ID, AddressLifecycleParams{
		Action: AddressLifecycleTransfer, TargetAgent: "beta",
	})
	if err != nil || plan.Allowed || !hasLifecycleBlocker(plan, "connection", fixture.connection.ID) {
		t.Fatalf("archived-connection preflight = %#v, err=%v", plan, err)
	}
}

func TestAddressLifecycleRollsBackProjectionWhenPersistenceFails(t *testing.T) {
	fixture := newAddressLifecycleFixture(t)
	path := filepath.Join(fixture.st.Dir(), "integrations.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.ApplyAddressLifecycle(fixture.address.ID, AddressLifecycleParams{
		Action: AddressLifecycleArchive, ExpectedVersion: intPointer(1), Confirm: fixture.address.ID,
	}); err == nil {
		t.Fatal("archive succeeded after durable replace failed")
	}
	address := fixture.h.addresses[fixture.address.ID]
	if address == nil || address.ArchivedAt != "" || !address.Enabled || address.Version != 1 || len(fixture.h.addressOperations) != 0 {
		t.Fatalf("failed archive leaked projection: address=%#v operations=%#v", address, fixture.h.addressOperations)
	}
}

func lifecycleIngress(fixture addressLifecycleFixture, externalID string) IngressParams {
	return IngressParams{
		ConnectionID: fixture.connection.ID, AddressID: fixture.address.ID,
		ExternalMessageID: externalID, Sender: ActorRef{ExternalID: "usr_sender"},
		Conversation: ConversationRef{ConversationID: "dm-1", ConversationType: "dm"},
		Content:      MessageContent{Text: "question"}, Trigger: TriggerEvidence{Direct: true, ExplicitDispatch: true},
	}
}

func seedFailedLifecycleInbox(t *testing.T, fixture addressLifecycleFixture, externalID string) InboxItem {
	t.Helper()
	result, err := fixture.h.IngestMessage(lifecycleIngress(fixture, externalID))
	if err != nil {
		t.Fatal(err)
	}
	fixture.h.mu.Lock()
	next := *result.InboxItem
	next.State = "failed"
	next.LastError = "historical failure"
	next.UpdatedAt = now()
	err = fixture.h.commitInboxLocked(next)
	fixture.h.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func seedFailedLifecycleOutbox(t *testing.T, fixture addressLifecycleFixture) OutboxItem {
	t.Helper()
	created, err := fixture.h.CreateOutbox(OutboxParams{
		Agent: "alpha", AddressID: fixture.address.ID,
		Conversation: ConversationRef{ConversationID: "chat-1"}, Content: MessageContent{Text: "send"},
	})
	if err != nil {
		t.Fatal(err)
	}
	command, err := fixture.h.ClaimNextOutbox(fixture.connection.ID)
	if err != nil || command == nil || command.OutboxItem.ID != created.ID {
		t.Fatalf("claim outbox = %#v, err=%v", command, err)
	}
	failed, err := fixture.h.CompleteOutbox(fixture.connection.ID, created.ID, OutboxResultParams{
		AttemptToken: command.OutboxItem.AttemptToken, Error: "provider failure",
	})
	if err != nil || failed.State != "failed" {
		t.Fatalf("fail outbox = %#v, err=%v", failed, err)
	}
	return failed
}

func hasLifecycleBlocker(plan AddressLifecyclePreflight, kind, id string) bool {
	for _, blocker := range plan.Blockers {
		if blocker.Kind == kind && blocker.ID == id {
			return true
		}
	}
	return false
}

func intPointer(value int) *int          { return &value }
func boolPointer(value bool) *bool       { return &value }
func stringPointer(value string) *string { return &value }
