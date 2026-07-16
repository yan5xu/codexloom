package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	MaxThreadArtifactBytes = int64(25 << 20)
	MaxThreadArtifacts     = 8
)

// ThreadArtifact is a file snapshot owned by one Agent Thread. The original
// source can change or disappear without changing what Codex receives.
type ThreadArtifact struct {
	ID          string `json:"id"`
	AgentID     string `json:"agentId"`
	Name        string `json:"name"`
	MimeType    string `json:"mimeType"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	Path        string `json:"path"`
	URL         string `json:"url"`
	CreatedAt   string `json:"createdAt"`
	PublishedAt string `json:"publishedAt,omitempty"`
}

type threadArtifactRecord struct {
	ID          string `json:"id"`
	AgentID     string `json:"agentId"`
	Name        string `json:"name"`
	MimeType    string `json:"mimeType"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	FileName    string `json:"fileName"`
	CreatedAt   string `json:"createdAt"`
	PublishedAt string `json:"publishedAt,omitempty"`
}

func (h *Hub) StageThreadArtifact(key, name, declaredMime string, source io.Reader) (ThreadArtifact, error) {
	h.mu.Lock()
	agent := h.resolveLocked(key)
	if agent == nil {
		h.mu.Unlock()
		return ThreadArtifact{}, errf(404, "agent not found: %s", key)
	}
	agentID := agent.ID
	h.mu.Unlock()

	name = safeArtifactName(name)
	root := h.threadArtifactRoot(agentID)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return ThreadArtifact{}, errf(500, "create Thread artifact store: %s", err)
	}
	temporary, err := os.CreateTemp(root, ".upload-*")
	if err != nil {
		return ThreadArtifact{}, errf(500, "stage Thread artifact: %s", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return ThreadArtifact{}, errf(500, "secure Thread artifact: %s", err)
	}

	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(source, MaxThreadArtifactBytes+1))
	closeErr := temporary.Close()
	if copyErr != nil || closeErr != nil {
		return ThreadArtifact{}, errf(400, "read Thread artifact")
	}
	if written <= 0 || written > MaxThreadArtifactBytes {
		return ThreadArtifact{}, errf(400, "Thread artifact must be between 1 byte and 25 MB")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	id := "art_" + digest[:24]

	if artifact, loadErr := h.loadThreadArtifact(agentID, id); loadErr == nil {
		return artifact, nil
	}

	directory := filepath.Join(root, id)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return ThreadArtifact{}, errf(500, "create Thread artifact directory: %s", err)
	}
	extension := safeArtifactExtension(name)
	fileName := "content" + extension
	contentPath := filepath.Join(directory, fileName)
	if err := os.Rename(temporaryPath, contentPath); err != nil {
		if _, statErr := os.Stat(contentPath); statErr != nil {
			return ThreadArtifact{}, errf(500, "publish Thread artifact: %s", err)
		}
	}

	mimeType := detectArtifactMime(contentPath, name, declaredMime)
	record := threadArtifactRecord{
		ID: id, AgentID: agentID, Name: name, MimeType: mimeType, Size: written,
		SHA256: digest, FileName: fileName, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeArtifactRecord(filepath.Join(directory, "metadata.json"), record); err != nil {
		return ThreadArtifact{}, errf(500, "persist Thread artifact: %s", err)
	}
	if err := secureThreadArtifact(directory, contentPath); err != nil {
		return ThreadArtifact{}, errf(500, "secure Thread artifact snapshot: %s", err)
	}
	return h.artifactFromRecord(record), nil
}

func (h *Hub) ThreadArtifact(key, artifactID string) (ThreadArtifact, error) {
	h.mu.Lock()
	agent := h.resolveLocked(key)
	if agent == nil {
		h.mu.Unlock()
		return ThreadArtifact{}, errf(404, "agent not found: %s", key)
	}
	agentID := agent.ID
	h.mu.Unlock()
	artifact, err := h.loadThreadArtifact(agentID, artifactID)
	if err != nil {
		return ThreadArtifact{}, errf(404, "Thread artifact not found: %s", artifactID)
	}
	return artifact, nil
}

func (h *Hub) OpenThreadArtifact(key, artifactID string) (ThreadArtifact, *os.File, error) {
	artifact, err := h.ThreadArtifact(key, artifactID)
	if err != nil {
		return ThreadArtifact{}, nil, err
	}
	file, err := os.Open(artifact.Path)
	if err != nil {
		return ThreadArtifact{}, nil, errf(404, "Thread artifact content is unavailable")
	}
	return artifact, file, nil
}

func (h *Hub) PublishThreadArtifact(key, artifactID string) (ThreadArtifact, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	agent := h.resolveLocked(key)
	if agent == nil {
		return ThreadArtifact{}, errf(404, "agent not found: %s", key)
	}
	artifact, newlyPublished, err := h.markThreadArtifactPublished(agent.ID, artifactID)
	if err != nil {
		return ThreadArtifact{}, errf(404, "Thread artifact not found: %s", artifactID)
	}
	if newlyPublished {
		h.emitLocked(agent.ID, "loom/artifact-published", map[string]any{"artifact": artifact})
	}
	return artifact, nil
}

func (h *Hub) PublishedThreadArtifacts(key string) ([]ThreadArtifact, error) {
	h.mu.Lock()
	agent := h.resolveLocked(key)
	if agent == nil {
		h.mu.Unlock()
		return nil, errf(404, "agent not found: %s", key)
	}
	agentID := agent.ID
	h.mu.Unlock()

	entries, err := os.ReadDir(h.threadArtifactRoot(agentID))
	if err != nil {
		if os.IsNotExist(err) {
			return []ThreadArtifact{}, nil
		}
		return nil, errf(500, "list Thread artifacts: %s", err)
	}
	artifacts := make([]ThreadArtifact, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "art_") {
			continue
		}
		artifact, loadErr := h.loadThreadArtifact(agentID, entry.Name())
		if loadErr == nil && artifact.PublishedAt != "" {
			artifacts = append(artifacts, artifact)
		}
	}
	sort.SliceStable(artifacts, func(i, j int) bool {
		return artifacts[i].PublishedAt < artifacts[j].PublishedAt
	})
	return artifacts, nil
}

func (h *Hub) resolveThreadArtifacts(agentID string, artifactIDs []string) ([]ThreadArtifact, error) {
	if len(artifactIDs) > MaxThreadArtifacts {
		return nil, errf(400, "a Turn supports at most %d artifacts", MaxThreadArtifacts)
	}
	seen := map[string]bool{}
	artifacts := make([]ThreadArtifact, 0, len(artifactIDs))
	for _, id := range artifactIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		artifact, err := h.loadThreadArtifact(agentID, id)
		if err != nil {
			return nil, errf(400, "invalid Thread artifact %q", id)
		}
		seen[id] = true
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func (h *Hub) loadThreadArtifact(agentID, artifactID string) (ThreadArtifact, error) {
	if !safeStoreComponent(agentID) || !safeStoreComponent(artifactID) || !strings.HasPrefix(artifactID, "art_") {
		return ThreadArtifact{}, fmt.Errorf("invalid artifact identity")
	}
	directory := filepath.Join(h.threadArtifactRoot(agentID), artifactID)
	data, err := os.ReadFile(filepath.Join(directory, "metadata.json"))
	if err != nil {
		return ThreadArtifact{}, err
	}
	var record threadArtifactRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return ThreadArtifact{}, err
	}
	if record.ID != artifactID || record.AgentID != agentID || !safeStoreComponent(record.FileName) {
		return ThreadArtifact{}, fmt.Errorf("artifact ownership mismatch")
	}
	artifact := h.artifactFromRecord(record)
	info, err := os.Lstat(artifact.Path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != record.Size {
		return ThreadArtifact{}, fmt.Errorf("artifact content is unavailable")
	}
	file, err := os.Open(artifact.Path)
	if err != nil {
		return ThreadArtifact{}, fmt.Errorf("artifact content is unavailable")
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || hex.EncodeToString(hash.Sum(nil)) != record.SHA256 {
		return ThreadArtifact{}, fmt.Errorf("artifact content checksum mismatch")
	}
	if err := secureThreadArtifact(directory, artifact.Path); err != nil {
		return ThreadArtifact{}, err
	}
	return artifact, nil
}

func (h *Hub) artifactFromRecord(record threadArtifactRecord) ThreadArtifact {
	return ThreadArtifact{
		ID: record.ID, AgentID: record.AgentID, Name: record.Name, MimeType: record.MimeType,
		Size: record.Size, SHA256: record.SHA256,
		Path:        filepath.Join(h.threadArtifactRoot(record.AgentID), record.ID, record.FileName),
		URL:         "/api/agents/" + record.AgentID + "/artifacts/" + record.ID,
		CreatedAt:   record.CreatedAt,
		PublishedAt: record.PublishedAt,
	}
}

func (h *Hub) markThreadArtifactPublished(agentID, artifactID string) (ThreadArtifact, bool, error) {
	artifact, err := h.loadThreadArtifact(agentID, artifactID)
	if err != nil || artifact.PublishedAt != "" {
		return artifact, false, err
	}
	directory := filepath.Join(h.threadArtifactRoot(agentID), artifactID)
	metadataPath := filepath.Join(directory, "metadata.json")
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return ThreadArtifact{}, false, err
	}
	var record threadArtifactRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return ThreadArtifact{}, false, err
	}
	record.PublishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := os.Chmod(metadataPath, 0o600); err != nil {
		return ThreadArtifact{}, false, err
	}
	if err := writeArtifactRecord(metadataPath, record); err != nil {
		_ = os.Chmod(metadataPath, 0o400)
		return ThreadArtifact{}, false, err
	}
	if err := secureThreadArtifact(directory, artifact.Path); err != nil {
		return ThreadArtifact{}, false, err
	}
	return h.artifactFromRecord(record), true, nil
}

func (h *Hub) threadArtifactRoot(agentID string) string {
	return filepath.Join(h.st.Dir(), "attachments", "threads", agentID)
}

func safeArtifactName(value string) string {
	value = strings.TrimSpace(filepath.Base(strings.ReplaceAll(value, "\\", "/")))
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, value)
	if value == "" || value == "." {
		return "attachment"
	}
	return value
}

func safeArtifactExtension(name string) string {
	extension := strings.ToLower(filepath.Ext(name))
	if len(extension) > 16 || strings.ContainsAny(extension, `/\\`) {
		return ""
	}
	return extension
}

func safeStoreComponent(value string) bool {
	return value != "" && value == filepath.Base(value) && value != "." && value != ".." && !strings.ContainsAny(value, `/\\`)
}

func detectArtifactMime(path, name, declared string) string {
	file, err := os.Open(path)
	if err == nil {
		defer file.Close()
		head := make([]byte, 512)
		if count, readErr := file.Read(head); readErr == nil || readErr == io.EOF {
			if detected := http.DetectContentType(head[:count]); detected != "application/octet-stream" {
				return detected
			}
		}
	}
	if byExtension := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); byExtension != "" {
		return byExtension
	}
	declared = strings.TrimSpace(strings.Split(declared, ";")[0])
	if declared != "" {
		return declared
	}
	return "application/octet-stream"
}

func writeArtifactRecord(path string, record threadArtifactRecord) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	defer os.Remove(temporary)
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func secureThreadArtifact(directory, contentPath string) error {
	if err := os.Chmod(contentPath, 0o400); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Join(directory, "metadata.json"), 0o400); err != nil {
		return err
	}
	return os.Chmod(directory, 0o700)
}
