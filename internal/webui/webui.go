// Package webui embeds the built React console (web/ -> npm run build ->
// internal/webui/dist). The Go binary serves it, so the deployed artifact is
// a single executable.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// FS returns the embedded web root, or nil if only the placeholder exists.
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
