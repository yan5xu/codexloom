package httpapi

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestTopicArtifactRoutesExposePreviewableLinkedArtifacts(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stamp := nowForTest()
	if err := st.SaveAgents(map[string]*hub.Agent{
		"lead":  {ID: "lead", Name: "lead", ThreadID: "thread-lead", Status: "idle", CreatedAt: stamp, UpdatedAt: stamp},
		"maker": {ID: "maker", Name: "maker", ThreadID: "thread-maker", Status: "idle", CreatedAt: stamp, UpdatedAt: stamp},
	}); err != nil {
		t.Fatal(err)
	}
	h, err := hub.Open(st)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Shutdown(); _ = st.Close() })
	topic, err := h.CreateTopic(hub.CreateTopicParams{
		Title: "Preview artifacts", Purpose: "Verify Topic evidence.", CompletionBoundary: "Preview opens.",
		ResponsibleAgent: "lead", CreatedBy: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := h.StageThreadArtifact("maker", "proof.txt", "text/plain", strings.NewReader("preview body"))
	if err != nil {
		t.Fatal(err)
	}
	artifact, err = h.PublishThreadArtifact("maker", artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.LinkTopic(topic.ID, "lead", hub.TopicLink{Type: "artifact", ID: artifact.ID, Label: "Product proof"}); err != nil {
		t.Fatal(err)
	}

	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok"), Mode: fs.FileMode(0o644)}}
	server := httptest.NewServer(New(h, st, web).Handler())
	defer server.Close()

	response, err := http.Get(server.URL + "/api/topics/" + topic.ID + "/artifacts")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("metadata status = %d", response.StatusCode)
	}
	var body struct {
		Artifacts []hub.ThreadArtifact `json:"artifacts"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Artifacts) != 1 || body.Artifacts[0].ID != artifact.ID {
		t.Fatalf("metadata = %#v", body.Artifacts)
	}
	wantURL := "/api/topics/" + topic.ID + "/artifacts/" + artifact.ID
	if body.Artifacts[0].URL != wantURL || body.Artifacts[0].Path != "" {
		t.Fatalf("public metadata = %#v", body.Artifacts[0])
	}

	preview, err := http.Get(server.URL + wantURL + "?preview=1")
	if err != nil {
		t.Fatal(err)
	}
	defer preview.Body.Close()
	data, err := io.ReadAll(preview.Body)
	if err != nil {
		t.Fatal(err)
	}
	if preview.StatusCode != http.StatusOK || string(data) != "preview body" {
		t.Fatalf("preview status=%d body=%q", preview.StatusCode, data)
	}
	if got := preview.Header.Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") {
		t.Fatalf("text preview disposition = %q", got)
	}
	if got := preview.Header.Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
		t.Fatalf("text preview resource policy = %q", got)
	}
}
