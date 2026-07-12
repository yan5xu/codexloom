package hub

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestRemoteLifecyclePairingAndDevices(t *testing.T) {
	installFakeRemoteCodex(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.loadRemoteLocked()

	snapshot, err := h.EnableRemote()
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Config.Enabled || snapshot.Status.ServerName != "test-host.local" || snapshot.Status.EnvironmentID != "env-test" {
		t.Fatalf("enabled snapshot = %#v", snapshot)
	}

	pairing, err := h.StartRemotePairing()
	if err != nil {
		t.Fatal(err)
	}
	if pairing.PairingCode != "https://chatgpt.com/remote/pair-test" || pairing.ManualPairingCode != "ABCD-EFGH" {
		t.Fatalf("pairing = %#v", pairing)
	}
	pairing, err = h.ReadRemotePairing()
	if err != nil || !pairing.Claimed {
		t.Fatalf("pairing status = %#v, %v", pairing, err)
	}

	devices, err := h.ListRemoteDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].ClientID != "phone-1" || devices[0].DisplayName != "Test Phone" {
		t.Fatalf("devices = %#v", devices)
	}
	if err := h.RevokeRemoteDevice("phone-1"); err != nil {
		t.Fatal(err)
	}

	snapshot, err = h.DisableRemote()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Config.Enabled || snapshot.Status.State != "disabled" {
		t.Fatalf("disabled snapshot = %#v", snapshot)
	}
	var persisted RemoteConfig
	if err := st.LoadRemote(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Enabled {
		t.Fatalf("persisted config = %#v", persisted)
	}
}

func installFakeRemoteCodex(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "codex")
	script := `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s\n' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  [ -z "$id" ] && continue
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"id":%s,"result":{"userAgent":"fake"}}\n' "$id" ;;
    *'"method":"remoteControl/status/read"'*)
      printf '{"id":%s,"result":{"status":"disabled","serverName":"test-host.local","installationId":"install-test","environmentId":null}}\n' "$id" ;;
    *'"method":"remoteControl/enable"'*)
      printf '{"id":%s,"result":{"status":"connecting","serverName":"test-host.local","installationId":"install-test","environmentId":"env-test"}}\n' "$id" ;;
    *'"method":"remoteControl/disable"'*)
      printf '{"id":%s,"result":{"status":"disabled","serverName":"test-host.local","installationId":"install-test","environmentId":"env-test"}}\n' "$id" ;;
    *'"method":"remoteControl/pairing/start"'*)
      printf '{"id":%s,"result":{"pairingCode":"https://chatgpt.com/remote/pair-test","manualPairingCode":"ABCD-EFGH","environmentId":"env-test","expiresAt":1999999999}}\n' "$id" ;;
    *'"method":"remoteControl/pairing/status"'*)
      printf '{"id":%s,"result":{"claimed":true}}\n' "$id" ;;
    *'"method":"remoteControl/client/list"'*)
      printf '{"id":%s,"result":{"data":[{"clientId":"phone-1","displayName":"Test Phone","deviceType":"mobile","platform":"ios","lastSeenAt":1999999900}],"nextCursor":null}}\n' "$id" ;;
    *'"method":"remoteControl/client/revoke"'*)
      printf '{"id":%s,"result":{}}\n' "$id" ;;
    *)
      printf '{"id":%s,"result":{}}\n' "$id" ;;
  esac
done
`
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_REMOTE_BIN", binPath)
}
