package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validRuntimeGatewayProcessFixture(t *testing.T) gatewayFoundationStateShape {
	t.Helper()
	digestA := strings.Repeat("a", 64)
	digestB := strings.Repeat("b", 64)
	descriptor := func(build, digest, executable string) gatewayFoundationLaunchDescriptorShape {
		return gatewayFoundationLaunchDescriptorShape{
			Manager: "fake", ConnectionID: "conn-r1", ServiceID: "service-r1", UnitPath: "/tmp/service-r1.unit",
			Executable: executable, WorkingDirectory: "/tmp", HubURL: "http://127.0.0.1:24821",
			DataDir: "/tmp/data-r1", LogPath: "/tmp/service-r1.log", Build: build, ExecutableDigest: digest,
		}
	}
	target := descriptor("target", digestA, "/tmp/gateway-target")
	anchorDescriptor := descriptor("anchor", digestB, "/tmp/gateway-anchor")
	anchorJSON, err := json.Marshal(struct {
		Descriptor gatewayFoundationLaunchDescriptorShape `json:"descriptor"`
		Generation string                                 `json:"generation,omitempty"`
	}{Descriptor: anchorDescriptor})
	if err != nil {
		t.Fatal(err)
	}
	anchorDigest := sha256.Sum256(anchorJSON)
	plan := &gatewayFoundationLaunchPlanShape{ConnectionID: "conn-r1", Target: target,
		Anchor: gatewayFoundationAnchorShape{Descriptor: anchorDescriptor, IntegritySHA256: hex.EncodeToString(anchorDigest[:])}}
	return gatewayFoundationStateShape{
		Version: 2,
		Controls: map[string]*gatewayFoundationControlShape{"conn-r1": {
			ConnectionID: "conn-r1", Epoch: 2, Lifecycle: "adopted", Recovery: "none",
			Binding: gatewayFoundationBindingShape{Connection: gatewayFoundationConnectionShape{
				ID: "conn-r1", Provider: "test", Enabled: true, CreatedAt: "2026-08-08T00:00:00Z",
			}, Addresses: []gatewayFoundationAddressShape{}}, UpdatedAt: "2026-08-08T00:00:00Z",
		}},
		Observations: map[string]*gatewayFoundationObservationShape{},
		LaunchPlans:  map[string]*gatewayFoundationLaunchPlanShape{"conn-r1": plan},
		Attempts:     map[string]*gatewayFoundationAttemptShape{},
	}
}

func validRuntimeGatewayLaunchProofFixture(t *testing.T) gatewayFoundationStateShape {
	t.Helper()
	value := validRuntimeGatewayProcessFixture(t)
	value.Version = 3
	control := value.Controls["conn-r1"]
	control.Binding.Connection.Provider = "lark"
	control.Binding.Connection.AccountRef = "cli_l2a"
	control.Binding.Connection.Domain = "lark"
	control.Binding.Connection.CredentialRef = "managed:" + strings.Repeat("c", 64)
	control.Binding.Addresses = []gatewayFoundationAddressShape{{
		ID: "addr-l2a", AgentID: "agent-l2a", ConnectionID: "conn-r1", ExternalIdentity: "lark://ou_l2a",
		TriggerPolicy: "mention", ReplyPolicy: "final_answer", TrustDomain: "lark:cli_l2a", Enabled: true,
		Version: 1, CreatedAt: "2026-08-09T00:00:00Z",
	}}
	plan := value.LaunchPlans["conn-r1"]
	for _, descriptor := range []*gatewayFoundationLaunchDescriptorShape{&plan.Target, &plan.Anchor.Descriptor} {
		descriptor.Provider = "lark"
		descriptor.AddressID = "addr-l2a"
		descriptor.AccountRef = "cli_l2a"
		descriptor.Domain = "lark"
	}
	plan.Target.ManagedCredentialRef = control.Binding.Connection.CredentialRef
	anchorJSON, err := json.Marshal(struct {
		Descriptor gatewayFoundationLaunchDescriptorShape `json:"descriptor"`
		AttemptID  string                                 `json:"attemptId,omitempty"`
		Generation string                                 `json:"generation,omitempty"`
	}{Descriptor: plan.Anchor.Descriptor})
	if err != nil {
		t.Fatal(err)
	}
	anchorDigest := sha256.Sum256(anchorJSON)
	plan.Anchor.IntegritySHA256 = hex.EncodeToString(anchorDigest[:])
	planJSON, err := json.Marshal(struct {
		ConnectionID string                                 `json:"connectionId"`
		Target       gatewayFoundationLaunchDescriptorShape `json:"target"`
		Anchor       gatewayFoundationAnchorShape           `json:"anchor"`
	}{ConnectionID: plan.ConnectionID, Target: plan.Target, Anchor: plan.Anchor})
	if err != nil {
		t.Fatal(err)
	}
	planDigest := sha256.Sum256(planJSON)
	plan.IntegritySHA256 = hex.EncodeToString(planDigest[:])
	return value
}

type runtimeGatewayFixture struct {
	Version      int            `json:"version"`
	Controls     map[string]any `json:"controls"`
	Observations map[string]any `json:"observations"`
}

func TestRuntimeGatewayStateRaisesFloorWithStateInOneFile(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := st.ClaimWritableOwnership()
	if err != nil {
		t.Fatal(err)
	}
	want := runtimeGatewayFixture{Version: 1, Controls: map[string]any{}, Observations: map[string]any{}}
	if err := st.SaveRuntimeGatewayState(want); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, foundationFileName))
	if err != nil {
		t.Fatal(err)
	}
	var envelope runtimeFoundationEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != runtimeFoundationSchemaVersion || envelope.MinimumWriter != runtimeWriterFloorGatewayState {
		t.Fatalf("foundation compatibility = schema %d floor %d", envelope.SchemaVersion, envelope.MinimumWriter)
	}
	var got runtimeGatewayFixture
	exists, err := st.LoadRuntimeGatewayState(&got)
	if err != nil || !exists || got.Version != 1 || got.Controls == nil || got.Observations == nil {
		t.Fatalf("round trip = %#v, exists=%v, err=%v", got, exists, err)
	}
	owner.Release()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("current writer rejected its own floor: %v", err)
	}
	_ = reopened.Close()
}

func TestRuntimeGatewayStateRequiresLiveHubOwnership(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	value := runtimeGatewayFixture{Version: 1, Controls: map[string]any{}, Observations: map[string]any{}}
	if err := st.SaveRuntimeGatewayState(value); err == nil {
		t.Fatal("foundation write did not require live Hub ownership")
	}
	if _, err := os.Stat(filepath.Join(dir, foundationFileName)); !os.IsNotExist(err) {
		t.Fatalf("rejected foundation write created a file: %v", err)
	}
}

func TestRuntimeGatewayStateRejectsOversizeDocumentBeforeCommit(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := st.ClaimWritableOwnership()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { owner.Release(); _ = st.Close() }()
	value := runtimeGatewayFixture{Version: 1, Controls: map[string]any{
		"conn": map[string]any{
			"connectionId": "conn", "epoch": 1, "lifecycle": "provisioning", "recovery": "none",
			"reason":    strings.Repeat("x", runtimeFoundationMaxBytes),
			"binding":   map[string]any{"connection": map[string]any{"id": "conn", "provider": "test", "enabled": true, "createdAt": "t"}, "addresses": []any{}},
			"updatedAt": "t",
		},
	}, Observations: map[string]any{}}
	if err := st.SaveRuntimeGatewayState(value); err == nil {
		t.Fatal("oversize foundation was committed")
	}
	if _, err := os.Stat(filepath.Join(dir, foundationFileName)); !os.IsNotExist(err) {
		t.Fatalf("oversize rejection created foundation: %v", err)
	}
}

func TestRuntimeGatewayFoundationRejectsUnknownOrIncompleteStateBeforeMutation(t *testing.T) {
	cases := map[string]string{
		"unknown":              `{"schemaVersion":1,"minimumWriter":2,"state":{"version":2,"gatewayState":{"version":1,"controls":{},"observations":{},"extra":true}}}`,
		"missing-observations": `{"schemaVersion":1,"minimumWriter":2,"state":{"version":2,"gatewayState":{"version":1,"controls":{}}}}`,
		"bad-control":          `{"schemaVersion":1,"minimumWriter":2,"state":{"version":2,"gatewayState":{"version":1,"controls":{"conn":{"connectionId":"conn","epoch":0,"lifecycle":"adopted","recovery":"none","binding":{"connection":{"id":"conn","provider":"x","enabled":true,"createdAt":"t"},"addresses":[]},"updatedAt":"t"}},"observations":{}}}}`,
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, foundationFileName)
			if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
				t.Fatal(err)
			}
			before := snapshotDirectory(t, dir)
			if st, err := Open(dir); err == nil {
				_ = st.Close()
				t.Fatal("invalid R0b foundation was accepted")
			}
			after := snapshotDirectory(t, dir)
			if len(before) != len(after) || before[foundationFileName] != after[foundationFileName] {
				t.Fatalf("failed open mutated foundation: before=%v after=%v", before, after)
			}
		})
	}
}

func TestRuntimeGatewayProcessStateRaisesFloorThreeAtomically(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := st.ClaimWritableOwnership()
	if err != nil {
		t.Fatal(err)
	}
	value := validRuntimeGatewayProcessFixture(t)
	if err := st.SaveRuntimeGatewayState(value); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, foundationFileName))
	if err != nil {
		t.Fatal(err)
	}
	var envelope runtimeFoundationEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.MinimumWriter != runtimeWriterFloorGatewayProcess || !strings.Contains(string(envelope.State), "launchPlans") {
		t.Fatalf("R1 foundation did not atomically raise floor with plan: %s", data)
	}
	owner.Release()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("R1 writer rejected its process floor: %v", err)
	}
	defer reopened.Close()
	var got gatewayFoundationStateShape
	exists, err := reopened.LoadRuntimeGatewayState(&got)
	if err != nil || !exists || got.Version != 2 || got.LaunchPlans["conn-r1"] == nil {
		t.Fatalf("R1 process round trip = %#v exists=%v err=%v", got, exists, err)
	}
}

func TestL2aGatewayLaunchProofRaisesFloorFiveAtomicallyAndCannotLower(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := st.ClaimWritableOwnership()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { owner.Release(); _ = st.Close() }()
	if err := st.SaveCredentialFloor(); err != nil {
		t.Fatal(err)
	}
	value := validRuntimeGatewayLaunchProofFixture(t)
	if err := st.SaveRuntimeGatewayState(value); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, foundationFileName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var envelope runtimeFoundationEnvelope
	if err := json.Unmarshal(before, &envelope); err != nil {
		t.Fatal(err)
	}
	var foundation foundationState
	if err := json.Unmarshal(envelope.State, &foundation); err != nil {
		t.Fatal(err)
	}
	if envelope.MinimumWriter != runtimeWriterFloorGatewayProof || foundation.Version != 4 || !foundation.CredentialManaged {
		t.Fatalf("L2a foundation was not committed atomically at floor 5: %s", before)
	}
	var got gatewayFoundationStateShape
	exists, err := st.LoadRuntimeGatewayState(&got)
	if err != nil || !exists || got.Version != 3 || got.LaunchPlans["conn-r1"].IntegritySHA256 == "" {
		t.Fatalf("L2a launch-proof round trip = %#v exists=%v err=%v", got, exists, err)
	}
	if err := st.SaveRuntimeGatewayState(validRuntimeGatewayProcessFixture(t)); err == nil {
		t.Fatal("floor-5 Gateway state was allowed to regress to an R1-only document")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("rejected floor regression changed durable bytes: equal=%v err=%v", bytes.Equal(before, after), err)
	}
}

func TestL2aGatewayLaunchProofRequiresCredentialFloorBeforeCommit(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := st.ClaimWritableOwnership()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { owner.Release(); _ = st.Close() }()
	if err := st.SaveRuntimeGatewayState(validRuntimeGatewayLaunchProofFixture(t)); err == nil {
		t.Fatal("typed managed launch plan committed before the credential writer floor")
	}
	if _, err := os.Stat(filepath.Join(dir, foundationFileName)); !os.IsNotExist(err) {
		t.Fatalf("rejected typed plan created a foundation file: %v", err)
	}
}

func TestRuntimeGatewayProcessStateRejectsCorruptAnchorAndUnknownFieldsBeforeCommit(t *testing.T) {
	for name, mutate := range map[string]func(*gatewayFoundationStateShape){
		"anchor integrity": func(value *gatewayFoundationStateShape) {
			value.LaunchPlans["conn-r1"].Anchor.IntegritySHA256 = strings.Repeat("0", 64)
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			st, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			owner, err := st.ClaimWritableOwnership()
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Release()
			value := validRuntimeGatewayProcessFixture(t)
			mutate(&value)
			if err := st.SaveRuntimeGatewayState(value); err == nil {
				t.Fatal("corrupt R1 process state was committed")
			}
			if _, err := os.Stat(filepath.Join(dir, foundationFileName)); !os.IsNotExist(err) {
				t.Fatalf("rejected R1 state created foundation: %v", err)
			}
		})
	}
}

func TestRuntimeGatewayProcessFoundationRejectsUnknownShapeAndWrongFloorBeforeOpenMutation(t *testing.T) {
	for name, mutate := range map[string]func([]byte) []byte{
		"unknown shape": func(value []byte) []byte {
			return bytes.Replace(value, []byte(`"launchPlans"`), []byte(`"unknownR1Field":true,"launchPlans"`), 1)
		},
		"wrong floor": func(value []byte) []byte {
			return bytes.Replace(value, []byte(`"minimumWriter": 3`), []byte(`"minimumWriter": 2`), 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			st, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			owner, err := st.ClaimWritableOwnership()
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SaveRuntimeGatewayState(validRuntimeGatewayProcessFixture(t)); err != nil {
				t.Fatal(err)
			}
			owner.Release()
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, foundationFileName)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			mutated := mutate(data)
			if bytes.Equal(mutated, data) {
				t.Fatal("fixture mutation did not apply")
			}
			if err := os.WriteFile(path, mutated, 0o600); err != nil {
				t.Fatal(err)
			}
			before := snapshotDirectory(t, dir)
			if reopened, err := Open(dir); err == nil {
				_ = reopened.Close()
				t.Fatal("invalid R1 foundation was accepted")
			}
			after := snapshotDirectory(t, dir)
			if len(before) != len(after) || before[foundationFileName] != after[foundationFileName] {
				t.Fatalf("failed R1 open mutated durable tree: before=%v after=%v", before, after)
			}
		})
	}
}
