package modelcatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestManagedCatalogPreservesOpenAIModelsAndAppendsDeepSeek(t *testing.T) {
	snapshot, err := Describe("")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5",
		"gpt-5.4", "gpt-5.4-mini", "gpt-5.2", "codex-auto-review",
		"deepseek-v4-flash",
	}
	got := make([]string, 0, len(snapshot.Catalog.Models))
	for _, model := range snapshot.Catalog.Models {
		got = append(got, model.Slug)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("models = %v, want %v", got, want)
	}
	if snapshot.Catalog.Models[0].Priority >= snapshot.Catalog.Models[8].Priority {
		t.Fatalf("DeepSeek priority %d must not replace OpenAI default priority %d", snapshot.Catalog.Models[8].Priority, snapshot.Catalog.Models[0].Priority)
	}
	deepSeek := snapshot.PublicModels()[8]
	if deepSeek.ProviderID != "deepseek" || deepSeek.ContextWindow != 1_048_576 {
		t.Fatalf("DeepSeek projection = %#v", deepSeek)
	}
	if !reflect.DeepEqual(deepSeek.ReasoningEfforts, []string{"low", "high", "max"}) {
		t.Fatalf("DeepSeek reasoning efforts = %v", deepSeek.ReasoningEfforts)
	}
}

func TestMaterializeWritesProtectedStableSnapshot(t *testing.T) {
	snapshot, err := Materialize(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(snapshot.Path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(contents)
	if got := hex.EncodeToString(hash[:]); got != snapshot.SHA256 {
		t.Fatalf("sha256 = %s, want %s", got, snapshot.SHA256)
	}
	info, err := os.Stat(snapshot.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	if filepath.Base(snapshot.Path) != ManagedVersion+".json" {
		t.Fatalf("path = %s", snapshot.Path)
	}
}

func TestMaterializeRootWritesThroughOpenedDirectoryHandle(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	snapshot, err := MaterializeRoot(root, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	contents, err := root.ReadFile(filepath.Join("runtime", "model-catalog", ManagedVersion+".json"))
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(contents)
	if got := hex.EncodeToString(hash[:]); got != snapshot.SHA256 {
		t.Fatalf("rooted sha256 = %s, want %s", got, snapshot.SHA256)
	}
}

func TestOverrideCatalogIsReadOnlySource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	contents := []byte(`{"models":[{"slug":"custom-model","display_name":"Custom","supported_reasoning_levels":[],"visibility":"list","supported_in_api":true,"priority":1}]}`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Materialize(t.TempDir(), path)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Source != "override" || snapshot.Path != path || snapshot.Catalog.Models[0].Slug != "custom-model" {
		t.Fatalf("override snapshot = %#v", snapshot)
	}
}

func TestCompatibility(t *testing.T) {
	cases := map[string]string{
		"codex-cli 0.144.1": "verified",
		"0.143.9":           "unsupported",
		"0.146.0":           "unverified",
		"unknown":           "unverified",
	}
	for version, want := range cases {
		if got := Compatibility(version); got != want {
			t.Errorf("Compatibility(%q) = %q, want %q", version, got, want)
		}
	}
}
