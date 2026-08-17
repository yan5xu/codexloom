package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLCredentialFloorRaisesAtomicallyAndPreservesGateway(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimWritableOwnership(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if st.CredentialFloorPresent() {
		t.Fatal("credential floor present before any write")
	}
	if err := st.SaveRuntimeGatewayState(json.RawMessage(`{"version":1,"controls":{},"observations":{}}`)); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCredentialFloor(); err != nil {
		t.Fatal(err)
	}
	if !st.CredentialFloorPresent() {
		t.Fatal("credential floor absent after SaveCredentialFloor")
	}
	// A later Gateway write must preserve the credential floor.
	if err := st.SaveRuntimeGatewayState(json.RawMessage(`{"version":1,"controls":{},"observations":{}}`)); err != nil {
		t.Fatal(err)
	}
	if !st.CredentialFloorPresent() {
		t.Fatal("credential floor dropped by Gateway write")
	}
	data, err := st.ReadStableFile("runtime-foundation.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"minimumWriter": 4`) || !strings.Contains(string(data), `"credentialManaged": true`) {
		t.Fatalf("floor envelope incorrect: %s", data)
	}
	var gatewayState json.RawMessage
	exists, err := st.LoadRuntimeGatewayState(&gatewayState)
	if err != nil || !exists {
		t.Fatalf("gateway state lost after credential floor: exists=%v err=%v", exists, err)
	}
}

func TestLCredentialFloorRejectsWrongShape(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"wrong floor":  `{"schemaVersion":1,"minimumWriter":3,"state":{"version":3,"credentialManaged":true}}`,
		"missing flag": `{"schemaVersion":1,"minimumWriter":4,"state":{"version":3}}`,
		"v2 with flag": `{"schemaVersion":1,"minimumWriter":2,"state":{"version":2,"gatewayState":{"version":1,"controls":{},"observations":{}},"credentialManaged":true}}`,
	}
	for name, content := range cases {
		if err := os.WriteFile(filepath.Join(dir, "runtime-foundation.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dir); err == nil {
			t.Fatalf("invalid credential floor shape opened as writable: %s", name)
		}
	}
}

func TestLCredentialFloorRequiresLiveOwner(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SaveCredentialFloor(); err == nil {
		t.Fatal("credential floor raised without a live writable Hub owner")
	}
}
