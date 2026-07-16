package buildinfo

import (
	"testing"
	"testing/fstest"
	"time"
)

func TestCurrentIncludesRuntimeAndMainWebAsset(t *testing.T) {
	web := fstest.MapFS{"index.html": {Data: []byte(`<link href="/assets/index-style.css"><script src="/assets/index-app.js"></script>`)}}
	started := time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)
	info := Current(web, Runtime{StartedAt: started, DataDir: "/tmp/loom", Mode: "canary", ReadOnly: true})
	if info.Product != "CodexLoom" || info.StartedAt != "2026-07-15T01:02:03Z" {
		t.Fatalf("identity = %#v", info)
	}
	if info.Mode != "canary" || !info.ReadOnly || info.DataDir != "/tmp/loom" {
		t.Fatalf("runtime = %#v", info)
	}
	if info.WebAsset != "assets/index-app.js" {
		t.Fatalf("web asset = %q", info.WebAsset)
	}
}
