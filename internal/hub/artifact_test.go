package hub

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestThreadArtifactsStageSendAndPublish(t *testing.T) {
	logPath := installFakeSharedCodexHost(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	defer h.Shutdown()
	h.agents["agent-artifacts"] = &Agent{
		ID: "agent-artifacts", Name: "artifacts", Cwd: "/tmp/artifacts", ThreadID: "thr-stale",
		Sandbox: "danger-full-access", ApprovalPolicy: "never", Status: "idle",
		CreatedAt: now(), UpdatedAt: now(),
	}

	imageBytes := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)
	image, err := h.StageThreadArtifact("artifacts", "screen.png", "image/png", bytes.NewReader(imageBytes))
	if err != nil {
		t.Fatal(err)
	}
	document, err := h.StageThreadArtifact("artifacts", "brief.pdf", "application/pdf", strings.NewReader("%PDF-1.4\nloom"))
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := h.StageThreadArtifact("artifacts", "renamed.png", "image/png", bytes.NewReader(imageBytes))
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.ID != image.ID || image.URL == "" || document.Path == "" {
		t.Fatalf("staged artifacts = image %#v, document %#v, duplicate %#v", image, document, duplicate)
	}
	for _, artifact := range []ThreadArtifact{image, document} {
		info, statErr := os.Stat(artifact.Path)
		if statErr != nil || info.Mode().Perm() != 0o400 {
			t.Fatalf("artifact %s mode = %v err=%v", artifact.ID, info.Mode().Perm(), statErr)
		}
	}

	result, err := h.SendTaskWithArtifacts("agent-artifacts", "Review these files", []string{image.ID, document.ID}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if result.TurnID != "turn-stale" {
		t.Fatalf("turn ID = %q", result.TurnID)
	}
	turn := lastRequestParams(t, logPath, "turn/start")
	input, ok := turn["input"].([]any)
	if !ok || len(input) != 2 {
		t.Fatalf("artifact input = %#v", turn["input"])
	}
	text, _ := input[0].(map[string]any)
	if text["type"] != "text" || !strings.Contains(text["text"].(string), `<loom_attachments version="1"`) || !strings.Contains(text["text"].(string), document.Path) {
		t.Fatalf("artifact manifest = %#v", text)
	}
	localImage, _ := input[1].(map[string]any)
	if localImage["type"] != "localImage" || localImage["path"] != image.Path {
		t.Fatalf("local image input = %#v", localImage)
	}

	opened, file, err := h.OpenThreadArtifact("agent-artifacts", document.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil || opened.Name != "brief.pdf" || string(data) != "%PDF-1.4\nloom" {
		t.Fatalf("opened artifact = %#v data=%q err=%v", opened, data, err)
	}
	published, err := h.PublishThreadArtifact("agent-artifacts", document.ID)
	if err != nil {
		t.Fatal(err)
	}
	if published.PublishedAt == "" {
		t.Fatal("published artifact has no publishedAt")
	}
	if _, err := h.PublishThreadArtifact("agent-artifacts", document.ID); err != nil {
		t.Fatal(err)
	}
	publishedArtifacts, err := h.PublishedThreadArtifacts("agent-artifacts")
	if err != nil || len(publishedArtifacts) != 1 || publishedArtifacts[0].ID != document.ID {
		t.Fatalf("published artifacts = %#v err=%v", publishedArtifacts, err)
	}
	events, err := st.ReadEvents("agent-artifacts", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var sawUser, sawPublished bool
	publishedEvents := 0
	for _, event := range events {
		switch event.Type {
		case "loom/user-message":
			sawUser = strings.Contains(string(event.Data), image.ID) && strings.Contains(string(event.Data), document.ID)
		case "loom/artifact-published":
			sawPublished = strings.Contains(string(event.Data), document.ID)
			publishedEvents++
		}
	}
	if !sawUser || !sawPublished || publishedEvents != 1 {
		t.Fatalf("artifact events user=%v published=%v count=%d", sawUser, sawPublished, publishedEvents)
	}
}

func TestDisplayUserTaskStripsAttachmentManifest(t *testing.T) {
	manifest := `<loom_attachments version="1"><attachment id="art_1" /></loom_attachments>`
	if got := displayUserTask("Review this\n\n" + manifest); got != "Review this" {
		t.Fatalf("display task = %q", got)
	}
	if got := displayUserTask(manifest); got != "Attached files" {
		t.Fatalf("attachment-only display task = %q", got)
	}
}

func TestThreadArtifactRejectsOversizeInput(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["agent-artifacts"] = &Agent{ID: "agent-artifacts", Name: "artifacts"}
	_, err = h.StageThreadArtifact("artifacts", "too-large.bin", "application/octet-stream", io.LimitReader(zeroReader{}, MaxThreadArtifactBytes+1))
	if err == nil || !strings.Contains(err.Error(), "25 MB") {
		t.Fatalf("oversize error = %v", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = 0
	}
	return len(buffer), nil
}
