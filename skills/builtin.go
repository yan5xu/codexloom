// Package skills owns the CodexLoom skills shipped with the service and CLI.
package skills

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed loom-communication domain-agent-coaching loom-integrations loom-external-messaging loom-parall loom-feishu loom-needs-you loom-artifacts
var bundledFS embed.FS

type Definition struct {
	Name        string
	Description string
}

var definitions = []Definition{
	{
		Name:        "loom-communication",
		Description: "Coordinate long-lived Agents through CodexLoom.",
	},
	{
		Name:        "domain-agent-coaching",
		Description: "Coach durable Agent scopes and organization design.",
	},
	{
		Name:        "loom-integrations",
		Description: "Configure and verify external platform integrations through CodexLoom.",
	},
	{
		Name:        "loom-external-messaging",
		Description: "Reply and publish through governed CodexLoom connectors.",
	},
	{
		Name:        "loom-parall",
		Description: "Read native Parall chats and messages through a managed CodexLoom connector.",
	},
	{
		Name:        "loom-feishu",
		Description: "Read native Feishu messages and thread context through a managed CodexLoom connector.",
	},
	{
		Name:        "loom-needs-you",
		Description: "Ask the human for durable input without holding a Turn open.",
	},
	{
		Name:        "loom-artifacts",
		Description: "Receive and publish managed files in an Agent Thread.",
	},
}

type State string

const (
	StateMissing   State = "missing"
	StateInstalled State = "installed"
	StateModified  State = "modified"
)

type Status struct {
	Name  string
	Path  string
	State State
	Hash  string
}

type InstallResult struct {
	Status
	Changed bool
}

func Definitions() []Definition {
	return append([]Definition(nil), definitions...)
}

func UserRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".agents", "skills"), nil
}

// Materialize writes the bundled skills into a root owned by CodexLoom. The
// resulting root can be passed directly to app-server skills/extraRoots/set.
func Materialize(root string) ([]InstallResult, error) {
	return MaterializeSelected(root, nil)
}

// MaterializeSelected replaces a CodexLoom-owned root with exactly the named
// bundled skills. A nil or empty selection means all bundled skills.
func MaterializeSelected(root string, names []string) ([]InstallResult, error) {
	selected, err := selectDefinitions(names)
	if err != nil {
		return nil, err
	}
	parent := filepath.Dir(root)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("create managed skill parent: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".codexloom-skills-")
	if err != nil {
		return nil, fmt.Errorf("stage managed skills: %w", err)
	}
	defer os.RemoveAll(stage)
	results := make([]InstallResult, 0, len(selected))
	for _, definition := range selected {
		if err := writeBundledSkill(stage, definition.Name); err != nil {
			return nil, err
		}
		files, err := bundledFiles(definition.Name)
		if err != nil {
			return nil, err
		}
		results = append(results, InstallResult{Status: Status{
			Name:  definition.Name,
			Path:  filepath.Join(root, definition.Name),
			State: StateInstalled,
			Hash:  bundledHash(files),
		}, Changed: true})
	}
	if err := swapManagedRoot(root, stage); err != nil {
		return nil, err
	}
	return results, nil
}

// Inspect compares installed skills with the exact version bundled in this
// binary. An existing directory with user changes is reported as modified.
func Inspect(root string, names []string) ([]Status, error) {
	selected, err := selectDefinitions(names)
	if err != nil {
		return nil, err
	}
	statuses := make([]Status, 0, len(selected))
	for _, definition := range selected {
		files, err := bundledFiles(definition.Name)
		if err != nil {
			return nil, err
		}
		state, err := inspectTarget(filepath.Join(root, definition.Name), files)
		if err != nil {
			return nil, fmt.Errorf("inspect %s: %w", definition.Name, err)
		}
		statuses = append(statuses, Status{
			Name:  definition.Name,
			Path:  filepath.Join(root, definition.Name),
			State: state,
			Hash:  bundledHash(files),
		})
	}
	return statuses, nil
}

// Install copies bundled skills into root. Existing modified skills are
// preserved unless force is explicit.
func Install(root string, names []string, force bool) ([]InstallResult, error) {
	statuses, err := Inspect(root, names)
	if err != nil {
		return nil, err
	}
	for _, status := range statuses {
		if status.State == StateModified && !force {
			return nil, fmt.Errorf("skill %q has local changes at %s; use --force to replace it", status.Name, status.Path)
		}
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create skill root: %w", err)
	}
	results := make([]InstallResult, 0, len(statuses))
	for _, status := range statuses {
		result := InstallResult{Status: status}
		if status.State != StateInstalled {
			if err := replaceTarget(root, status.Name); err != nil {
				return nil, err
			}
			result.State = StateInstalled
			result.Changed = true
		}
		results = append(results, result)
	}
	return results, nil
}

func selectDefinitions(names []string) ([]Definition, error) {
	if len(names) == 0 {
		return Definitions(), nil
	}
	byName := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		byName[definition.Name] = definition
	}
	seen := map[string]bool{}
	selected := make([]Definition, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		definition, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("unknown bundled skill %q", name)
		}
		if !seen[name] {
			selected = append(selected, definition)
			seen[name] = true
		}
	}
	return selected, nil
}

func bundledFiles(name string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(bundledFS, name, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(name, filepath.FromSlash(path))
		if err != nil {
			return err
		}
		data, err := bundledFS.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.Clean(rel)] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read bundled skill %s: %w", name, err)
	}
	return files, nil
}

func inspectTarget(target string, expected map[string][]byte) (State, error) {
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return StateMissing, nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return StateModified, nil
	}
	actual := map[string][]byte{}
	err = filepath.WalkDir(target, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == target || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			actual["__non_regular__:"+path] = nil
			return nil
		}
		rel, err := filepath.Rel(target, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		actual[filepath.Clean(rel)] = data
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(actual) != len(expected) {
		return StateModified, nil
	}
	for path, want := range expected {
		got, ok := actual[path]
		if !ok || string(got) != string(want) {
			return StateModified, nil
		}
	}
	return StateInstalled, nil
}

func replaceTarget(root, name string) error {
	stage, err := os.MkdirTemp(root, ".codexloom-"+name+"-")
	if err != nil {
		return fmt.Errorf("stage skill %s: %w", name, err)
	}
	defer os.RemoveAll(stage)
	if err := writeBundledSkillContents(stage, name); err != nil {
		return err
	}

	target := filepath.Join(root, name)
	if _, err := os.Lstat(target); os.IsNotExist(err) {
		if err := os.Rename(stage, target); err != nil {
			return fmt.Errorf("install skill %s: %w", name, err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect existing skill %s: %w", name, err)
	}

	backup, err := os.MkdirTemp(root, ".codexloom-previous-"+name+"-")
	if err != nil {
		return fmt.Errorf("prepare skill backup %s: %w", name, err)
	}
	if err := os.Remove(backup); err != nil {
		return fmt.Errorf("prepare skill backup %s: %w", name, err)
	}
	if err := os.Rename(target, backup); err != nil {
		return fmt.Errorf("replace skill %s: %w", name, err)
	}
	if err := os.Rename(stage, target); err != nil {
		_ = os.Rename(backup, target)
		return fmt.Errorf("install skill %s: %w", name, err)
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove previous skill %s: %w", name, err)
	}
	return nil
}

func writeBundledSkill(root, name string) error {
	target := filepath.Join(root, name)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("stage skill %s: %w", name, err)
	}
	return writeBundledSkillContents(target, name)
}

func writeBundledSkillContents(target, name string) error {
	files, err := bundledFiles(name)
	if err != nil {
		return err
	}
	for rel, data := range files {
		path := filepath.Join(target, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("stage skill %s: %w", name, err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fmt.Errorf("stage skill %s: %w", name, err)
		}
	}
	return nil
}

func swapManagedRoot(root, stage string) error {
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		if err := os.Rename(stage, root); err != nil {
			return fmt.Errorf("install managed skill root: %w", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect managed skill root: %w", err)
	}
	parent := filepath.Dir(root)
	backup, err := os.MkdirTemp(parent, ".codexloom-skills-previous-")
	if err != nil {
		return fmt.Errorf("prepare managed skill backup: %w", err)
	}
	if err := os.Remove(backup); err != nil {
		return fmt.Errorf("prepare managed skill backup: %w", err)
	}
	if err := os.Rename(root, backup); err != nil {
		return fmt.Errorf("replace managed skill root: %w", err)
	}
	if err := os.Rename(stage, root); err != nil {
		_ = os.Rename(backup, root)
		return fmt.Errorf("install managed skill root: %w", err)
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove previous managed skill root: %w", err)
	}
	return nil
}

func bundledHash(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		_, _ = hash.Write([]byte(path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(files[path])
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))[:12]
}
