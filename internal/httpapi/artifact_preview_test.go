package httpapi

import "testing"

func TestThreadArtifactDisposition(t *testing.T) {
	tests := []struct {
		name     string
		mimeType string
		preview  bool
		download bool
		want     string
	}{
		{name: "pdf preview", mimeType: "application/pdf", preview: true, want: "inline"},
		{name: "pdf default", mimeType: "application/pdf", want: "attachment"},
		{name: "raster preview", mimeType: "image/png", preview: true, want: "inline"},
		{name: "explicit image download", mimeType: "image/webp", preview: true, download: true, want: "attachment"},
		{name: "html never executes as preview", mimeType: "text/html", preview: true, want: "attachment"},
		{name: "svg preview is not embedded", mimeType: "image/svg+xml", preview: true, want: "attachment"},
		{name: "legacy image open stays inline", mimeType: "image/svg+xml", want: "inline"},
		{name: "parameters are ignored", mimeType: "IMAGE/JPEG; charset=binary", preview: true, want: "inline"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := threadArtifactDisposition(test.mimeType, test.preview, test.download); got != test.want {
				t.Fatalf("threadArtifactDisposition(%q, %v, %v) = %q, want %q", test.mimeType, test.preview, test.download, got, test.want)
			}
		})
	}
}

func TestThreadArtifactResourcePolicy(t *testing.T) {
	tests := []struct {
		mimeType string
		want     string
	}{
		{mimeType: "image/png", want: "cross-origin"},
		{mimeType: "IMAGE/JPEG; charset=binary", want: "cross-origin"},
		{mimeType: "image/gif", want: "cross-origin"},
		{mimeType: "image/webp", want: "cross-origin"},
		{mimeType: "image/svg+xml", want: "same-origin"},
		{mimeType: "application/pdf", want: "same-origin"},
		{mimeType: "text/html", want: "same-origin"},
	}
	for _, test := range tests {
		t.Run(test.mimeType, func(t *testing.T) {
			if got := threadArtifactResourcePolicy(test.mimeType); got != test.want {
				t.Fatalf("threadArtifactResourcePolicy(%q) = %q, want %q", test.mimeType, got, test.want)
			}
		})
	}
}
