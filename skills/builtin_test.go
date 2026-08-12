package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeAndInspectBundledSkills(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	results, err := Materialize(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 10 {
		t.Fatalf("materialize results = %#v", results)
	}
	for _, result := range results {
		if !result.Changed {
			t.Fatalf("materialize result = %#v", result)
		}
	}
	statuses, err := Inspect(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range statuses {
		if status.State != StateInstalled || status.Hash == "" {
			t.Fatalf("status = %#v", status)
		}
		if _, err := os.Stat(filepath.Join(status.Path, "SKILL.md")); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(status.Path, "agents", "openai.yaml")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExternalMessagingSkillDocumentsDelegationTerminal(t *testing.T) {
	data, err := bundledFS.ReadFile("loom-external-messaging/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"`delegate_command`",
		"loom integration delegate --reply-to INBOX_ITEM_ID",
		"A successful delegation is the source Agent's terminal action",
		"reply, delegation, no-reply, defer, or failed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("external messaging Skill omitted %q", want)
		}
	}
}

func TestInstallPreservesModifiedSkillUnlessForced(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	if _, err := Install(root, []string{"loom-communication"}, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "loom-communication", "SKILL.md")
	if err := os.WriteFile(path, []byte("local changes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(root, []string{"loom-communication"}, false); err == nil {
		t.Fatal("modified skill was replaced without force")
	}
	results, err := Install(root, []string{"loom-communication"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].Changed || results[0].State != StateInstalled {
		t.Fatalf("forced install results = %#v", results)
	}
}

func TestMaterializeSelectedReplacesManagedRootExactly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	if _, err := Materialize(root); err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeSelected(root, []string{"loom-communication"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "loom-communication", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "domain-agent-coaching")); !os.IsNotExist(err) {
		t.Fatalf("unselected skill remains in managed root: %v", err)
	}
}

func TestInstallPreflightsAllConflicts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	modified := filepath.Join(root, "domain-agent-coaching")
	if err := os.MkdirAll(modified, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modified, "SKILL.md"), []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(root, nil, false); err == nil {
		t.Fatal("expected install conflict")
	}
	if _, err := os.Stat(filepath.Join(root, "loom-communication")); !os.IsNotExist(err) {
		t.Fatalf("install wrote another skill before conflict preflight: %v", err)
	}
}

func TestUserRootUsesHomeAgentsDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root, err := UserRoot()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".agents", "skills")
	if root != want {
		t.Fatalf("UserRoot() = %q, want %q", root, want)
	}
}
