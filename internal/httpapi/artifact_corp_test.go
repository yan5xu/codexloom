package httpapi

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestRasterArtifactCanBeEmbeddedCrossOrigin(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	stamp := nowForTest()
	if err := st.SaveAgents(map[string]*hub.Agent{
		"viewer": {
			ID: "viewer", Name: "viewer", ThreadID: "thread-viewer",
			Status: "idle", CreatedAt: stamp, UpdatedAt: stamp,
		},
	}); err != nil {
		t.Fatal(err)
	}
	writer, err := hub.Open(st)
	if err != nil {
		t.Fatal(err)
	}

	var imageBytes bytes.Buffer
	pixel := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	pixel.Set(0, 0, color.NRGBA{R: 72, G: 151, B: 92, A: 255})
	if err := png.Encode(&imageBytes, pixel); err != nil {
		t.Fatal(err)
	}
	artifact, err := writer.StageThreadArtifact("viewer", "evidence.png", "image/png", bytes.NewReader(imageBytes.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	writer.Shutdown()

	reader, err := st.OpenReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	h, err := hub.OpenWithOptions(reader, hub.OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()

	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok"), Mode: fs.FileMode(0o644)}}
	server := httptest.NewServer(New(h, reader, web).Handler())
	defer server.Close()

	request, err := http.NewRequest(
		http.MethodGet,
		server.URL+"/api/agents/viewer/artifacts/"+artifact.ID+"?preview=1",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "http://127.0.0.1:8766")
	request.Header.Set("Sec-Fetch-Dest", "image")
	request.Header.Set("Sec-Fetch-Mode", "no-cors")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "image/png") {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := response.Header.Get("Cross-Origin-Resource-Policy"); got != "cross-origin" {
		t.Fatalf("Cross-Origin-Resource-Policy = %q", got)
	}
	if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := response.Header.Get("Content-Disposition"); !strings.HasPrefix(got, "inline;") {
		t.Fatalf("Content-Disposition = %q", got)
	}
}
