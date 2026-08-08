package modelcatalog

import (
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	ManagedVersion = "codex-0.144.1+deepseek-v4-flash-0731"
	CodexBaseline  = "0.144.1"
	CodexMinimum   = "0.144.0"
)

//go:embed models.json
var managedJSON []byte

type ReasoningLevel struct {
	Effort      string `json:"effort"`
	Description string `json:"description,omitempty"`
}

type Model struct {
	Slug                     string           `json:"slug"`
	DisplayName              string           `json:"display_name"`
	ContextWindow            int64            `json:"context_window,omitempty"`
	MaxContextWindow         int64            `json:"max_context_window,omitempty"`
	SupportedReasoningLevels []ReasoningLevel `json:"supported_reasoning_levels"`
	DefaultReasoningLevel    string           `json:"default_reasoning_level,omitempty"`
	InputModalities          []string         `json:"input_modalities,omitempty"`
	Visibility               string           `json:"visibility"`
	SupportedInAPI           bool             `json:"supported_in_api"`
	Priority                 int              `json:"priority"`
}

type Catalog struct {
	Models []Model `json:"models"`
}

type Snapshot struct {
	Catalog Catalog
	Path    string
	Source  string
	Version string
	SHA256  string
}

type PublicModel struct {
	ID                     string   `json:"id"`
	DisplayName            string   `json:"displayName"`
	ProviderID             string   `json:"providerId"`
	ContextWindow          int64    `json:"contextWindow,omitempty"`
	MaxContextWindow       int64    `json:"maxContextWindow,omitempty"`
	ReasoningEfforts       []string `json:"reasoningEfforts"`
	DefaultReasoningEffort string   `json:"defaultReasoningEffort,omitempty"`
	InputModalities        []string `json:"inputModalities,omitempty"`
	Visible                bool     `json:"visible"`
}

type Status struct {
	Source          string        `json:"source"`
	Version         string        `json:"version"`
	SHA256          string        `json:"sha256"`
	CodexBaseline   string        `json:"codexBaseline"`
	CodexVersion    string        `json:"codexVersion,omitempty"`
	Compatibility   string        `json:"compatibility"`
	ModelCount      int           `json:"modelCount"`
	Models          []PublicModel `json:"models"`
	Applied         bool          `json:"applied"`
	RestartRequired bool          `json:"restartRequired"`
}

func Describe(overridePath string) (Snapshot, error) {
	source := "managed"
	version := ManagedVersion
	contents := managedJSON
	if path := strings.TrimSpace(overridePath); path != "" {
		var err error
		contents, err = os.ReadFile(path)
		if err != nil {
			return Snapshot{}, fmt.Errorf("read model catalog override: %w", err)
		}
		source = "override"
		version = "override"
	}
	var catalog Catalog
	if err := json.Unmarshal(contents, &catalog); err != nil {
		return Snapshot{}, fmt.Errorf("decode model catalog: %w", err)
	}
	if len(catalog.Models) == 0 {
		return Snapshot{}, fmt.Errorf("model catalog contains no models")
	}
	seen := make(map[string]bool, len(catalog.Models))
	for _, model := range catalog.Models {
		if strings.TrimSpace(model.Slug) == "" {
			return Snapshot{}, fmt.Errorf("model catalog contains an empty slug")
		}
		if seen[model.Slug] {
			return Snapshot{}, fmt.Errorf("model catalog contains duplicate slug %q", model.Slug)
		}
		seen[model.Slug] = true
	}
	hash := sha256.Sum256(contents)
	return Snapshot{
		Catalog: catalog,
		Source:  source,
		Version: version,
		SHA256:  hex.EncodeToString(hash[:]),
	}, nil
}

func Materialize(dataDir, overridePath string) (Snapshot, error) {
	snapshot, err := Describe(overridePath)
	if err != nil {
		return Snapshot{}, err
	}
	if snapshot.Source == "override" {
		path, err := filepath.Abs(strings.TrimSpace(overridePath))
		if err != nil {
			return Snapshot{}, fmt.Errorf("resolve model catalog override: %w", err)
		}
		snapshot.Path = path
		return snapshot, nil
	}
	dir := filepath.Join(dataDir, "runtime", "model-catalog")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Snapshot{}, fmt.Errorf("create model catalog directory: %w", err)
	}
	path := filepath.Join(dir, ManagedVersion+".json")
	if existing, readErr := os.ReadFile(path); readErr == nil {
		hash := sha256.Sum256(existing)
		if hex.EncodeToString(hash[:]) == snapshot.SHA256 {
			snapshot.Path = path
			return snapshot, nil
		}
	}
	temp, err := os.CreateTemp(dir, ".models-*.json")
	if err != nil {
		return Snapshot{}, fmt.Errorf("create model catalog snapshot: %w", err)
	}
	tempPath := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}
	if err := temp.Chmod(0o600); err != nil {
		cleanup()
		return Snapshot{}, fmt.Errorf("protect model catalog snapshot: %w", err)
	}
	if _, err := temp.Write(managedJSON); err != nil {
		cleanup()
		return Snapshot{}, fmt.Errorf("write model catalog snapshot: %w", err)
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return Snapshot{}, fmt.Errorf("sync model catalog snapshot: %w", err)
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return Snapshot{}, fmt.Errorf("close model catalog snapshot: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		cleanup()
		return Snapshot{}, fmt.Errorf("publish model catalog snapshot: %w", err)
	}
	snapshot.Path = path
	return snapshot, nil
}

// MaterializeRoot is the stable-directory-handle variant used by the Hub data
// directory. targetRoot is relative to root; dataDir is used only to publish
// the absolute path consumed by Codex after the rooted write succeeds.
func MaterializeRoot(root *os.Root, dataDir, overridePath string) (Snapshot, error) {
	if root == nil {
		return Snapshot{}, fmt.Errorf("stable model catalog root is unavailable")
	}
	snapshot, err := Describe(overridePath)
	if err != nil {
		return Snapshot{}, err
	}
	if snapshot.Source == "override" {
		path, err := filepath.Abs(strings.TrimSpace(overridePath))
		if err != nil {
			return Snapshot{}, fmt.Errorf("resolve model catalog override: %w", err)
		}
		snapshot.Path = path
		return snapshot, nil
	}
	relativeDir := filepath.Join("runtime", "model-catalog")
	if err := root.MkdirAll(relativeDir, 0o700); err != nil {
		return Snapshot{}, fmt.Errorf("create model catalog directory: %w", err)
	}
	relativePath := filepath.Join(relativeDir, ManagedVersion+".json")
	if existing, readErr := root.ReadFile(relativePath); readErr == nil {
		hash := sha256.Sum256(existing)
		if hex.EncodeToString(hash[:]) == snapshot.SHA256 {
			snapshot.Path = filepath.Join(dataDir, relativePath)
			return snapshot, nil
		}
	}
	temporary := filepath.Join(relativeDir, ".models-"+rootedRandomSuffix()+".json")
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Snapshot{}, fmt.Errorf("create model catalog snapshot: %w", err)
	}
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = root.Remove(temporary)
		}
	}()
	if _, err := file.Write(managedJSON); err != nil {
		return Snapshot{}, fmt.Errorf("write model catalog snapshot: %w", err)
	}
	if err := file.Sync(); err != nil {
		return Snapshot{}, fmt.Errorf("sync model catalog snapshot: %w", err)
	}
	if err := file.Close(); err != nil {
		return Snapshot{}, fmt.Errorf("close model catalog snapshot: %w", err)
	}
	if err := root.Rename(temporary, relativePath); err != nil {
		return Snapshot{}, fmt.Errorf("publish model catalog snapshot: %w", err)
	}
	committed = true
	directory, err := root.Open(relativeDir)
	if err != nil {
		return Snapshot{}, fmt.Errorf("open model catalog directory: %w", err)
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return Snapshot{}, fmt.Errorf("sync model catalog directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return Snapshot{}, fmt.Errorf("close model catalog directory: %w", err)
	}
	snapshot.Path = filepath.Join(dataDir, relativePath)
	return snapshot, nil
}

func rootedRandomSuffix() string {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		panic(fmt.Sprintf("model catalog random suffix: %v", err))
	}
	return hex.EncodeToString(value)
}

func SpawnArgs(path string) []string {
	quoted, _ := json.Marshal(path)
	return []string{"-c", "model_catalog_json=" + string(quoted)}
}

func (s Snapshot) PublicModels() []PublicModel {
	models := make([]PublicModel, 0, len(s.Catalog.Models))
	for _, model := range s.Catalog.Models {
		efforts := make([]string, 0, len(model.SupportedReasoningLevels))
		for _, level := range model.SupportedReasoningLevels {
			if effort := strings.TrimSpace(level.Effort); effort != "" {
				efforts = append(efforts, effort)
			}
		}
		models = append(models, PublicModel{
			ID:                     model.Slug,
			DisplayName:            model.DisplayName,
			ProviderID:             ProviderID(model.Slug),
			ContextWindow:          model.ContextWindow,
			MaxContextWindow:       model.MaxContextWindow,
			ReasoningEfforts:       efforts,
			DefaultReasoningEffort: model.DefaultReasoningLevel,
			InputModalities:        append([]string(nil), model.InputModalities...),
			Visible:                model.Visibility == "list" && model.SupportedInAPI,
		})
	}
	return models
}

func ProviderID(slug string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(slug)), "deepseek-") {
		return "deepseek"
	}
	return "openai"
}

func Compatibility(codexVersion string) string {
	version, ok := parseVersion(codexVersion)
	if !ok {
		return "unverified"
	}
	minimum, _ := parseVersion(CodexMinimum)
	baseline, _ := parseVersion(CodexBaseline)
	if compareVersion(version, minimum) < 0 {
		return "unsupported"
	}
	if compareVersion(version, baseline) == 0 {
		return "verified"
	}
	return "unverified"
}

func parseVersion(value string) ([3]int, bool) {
	var result [3]int
	value = strings.TrimSpace(strings.TrimPrefix(value, "codex-cli "))
	parts := strings.Split(value, ".")
	if len(parts) < 3 {
		return result, false
	}
	for i := range result {
		part := parts[i]
		if index := strings.IndexAny(part, "-+"); index >= 0 {
			part = part[:index]
		}
		number, err := strconv.Atoi(part)
		if err != nil {
			return [3]int{}, false
		}
		result[i] = number
	}
	return result, true
}

func compareVersion(left, right [3]int) int {
	for i := range left {
		if left[i] < right[i] {
			return -1
		}
		if left[i] > right[i] {
			return 1
		}
	}
	return 0
}
