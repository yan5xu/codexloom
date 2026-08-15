package httpapi

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultFilePreviewBytes = 256 * 1024
	maxFilePreviewBytes     = 1 * 1024 * 1024
)

type fileHTTPError struct {
	status  int
	code    string
	message string
}

func (e *fileHTTPError) Error() string { return e.message }

type fileEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Size       int64  `json:"size,omitempty"`
	ModifiedAt string `json:"modifiedAt,omitempty"`
	Mode       string `json:"mode,omitempty"`
	MimeType   string `json:"mimeType,omitempty"`
	Readable   bool   `json:"readable"`
	ErrorCode  string `json:"errorCode,omitempty"`
}

type fileListing struct {
	Path       string      `json:"path"`
	Name       string      `json:"name"`
	Kind       string      `json:"kind"`
	Size       int64       `json:"size,omitempty"`
	ModifiedAt string      `json:"modifiedAt,omitempty"`
	Mode       string      `json:"mode,omitempty"`
	MimeType   string      `json:"mimeType,omitempty"`
	Readable   bool        `json:"readable"`
	Entries    []fileEntry `json:"entries,omitempty"`
}

type fileHomeResponse struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Readable bool   `json:"readable"`
}

func (s *Server) registerFileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/files", s.fileMetadata)
	mux.HandleFunc("GET /api/files/home", s.fileHome)
	mux.HandleFunc("GET /api/files/preview", s.filePreview)
	mux.HandleFunc("GET /api/files/content", s.fileContent)
}

func (s *Server) fileHome(w http.ResponseWriter, r *http.Request) {
	home, err := os.UserHomeDir()
	if err != nil {
		writeFileError(w, &fileHTTPError{status: http.StatusInternalServerError, code: "home_unavailable", message: "host home is unavailable"})
		return
	}
	home = filepath.Clean(home)
	info, err := os.Stat(home)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeFileError(w, &fileHTTPError{status: http.StatusNotFound, code: "home_not_found", message: "host home was not found"})
			return
		}
		if errors.Is(err, os.ErrPermission) {
			writeFileError(w, &fileHTTPError{status: http.StatusForbidden, code: "not_readable", message: "host home is not readable"})
			return
		}
		writeFileError(w, &fileHTTPError{status: http.StatusInternalServerError, code: "home_unavailable", message: "host home is unavailable"})
		return
	}
	if !info.IsDir() {
		writeFileError(w, &fileHTTPError{status: http.StatusConflict, code: "home_not_directory", message: "host home is not a directory"})
		return
	}
	f, err := os.Open(home)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			writeFileError(w, &fileHTTPError{status: http.StatusForbidden, code: "not_readable", message: "host home is not readable"})
			return
		}
		writeFileError(w, &fileHTTPError{status: http.StatusInternalServerError, code: "home_unavailable", message: "host home is unavailable"})
		return
	}
	_ = f.Close()
	writeJSON(w, http.StatusOK, fileHomeResponse{
		Path: home, Name: filepath.Base(home), Kind: "directory", Readable: true,
	})
}

func (s *Server) fileMetadata(w http.ResponseWriter, r *http.Request) {
	path, err := absoluteFilePath(r.URL.Query().Get("path"))
	if err != nil {
		writeFileError(w, err)
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		writeFileError(w, classifyFileError(err, "path"))
		return
	}
	entry := describeFile(path, filepath.Base(path), info)
	listing := fileListing{
		Path: entry.Path, Name: entry.Name, Kind: entry.Kind, Size: entry.Size,
		ModifiedAt: entry.ModifiedAt, Mode: entry.Mode, MimeType: entry.MimeType,
		Readable: entry.Readable,
	}
	if info.IsDir() {
		entries, readErr := os.ReadDir(path)
		if readErr != nil {
			writeFileError(w, classifyFileError(readErr, "directory"))
			return
		}
		listing.Entries = make([]fileEntry, 0, len(entries))
		for _, child := range entries {
			childPath := filepath.Join(path, child.Name())
			childInfo, statErr := os.Stat(childPath)
			if statErr != nil {
				listing.Entries = append(listing.Entries, fileEntry{
					Name: child.Name(), Path: childPath, Kind: fileKind(child.Type()),
					Readable: false, ErrorCode: classifyFileCode(statErr),
				})
				continue
			}
			listing.Entries = append(listing.Entries, describeFile(childPath, child.Name(), childInfo))
		}
	}
	writeJSON(w, http.StatusOK, listing)
}

func (s *Server) filePreview(w http.ResponseWriter, r *http.Request) {
	path, err := absoluteFilePath(r.URL.Query().Get("path"))
	if err != nil {
		writeFileError(w, err)
		return
	}
	f, info, err := openRegularFile(path)
	if err != nil {
		writeFileError(w, err)
		return
	}
	defer f.Close()

	limit, err := previewLimit(r.URL.Query().Get("maxBytes"))
	if err != nil {
		writeFileError(w, err)
		return
	}
	contentType, err := detectFileContentType(f, path)
	if err != nil {
		writeFileError(w, &fileHTTPError{status: http.StatusInternalServerError, code: "read_failed", message: "failed to inspect file"})
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		writeFileError(w, &fileHTTPError{status: http.StatusInternalServerError, code: "read_failed", message: "failed to seek file"})
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		writeFileError(w, &fileHTTPError{status: http.StatusInternalServerError, code: "read_failed", message: "failed to read file"})
		return
	}
	truncated := len(data) > limit
	if truncated {
		data = data[:limit]
	}

	setFileHeaders(w, contentType, filepath.Base(path), info.ModTime(), true, false)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("X-Codex-Loom-Preview-Limit", strconv.Itoa(limit))
	w.Header().Set("X-Codex-Loom-Preview-Truncated", strconv.FormatBool(truncated))
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func (s *Server) fileContent(w http.ResponseWriter, r *http.Request) {
	path, err := absoluteFilePath(r.URL.Query().Get("path"))
	if err != nil {
		writeFileError(w, err)
		return
	}
	f, info, err := openRegularFile(path)
	if err != nil {
		writeFileError(w, err)
		return
	}
	defer f.Close()
	contentType, err := detectFileContentType(f, path)
	if err != nil {
		writeFileError(w, &fileHTTPError{status: http.StatusInternalServerError, code: "read_failed", message: "failed to inspect file"})
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		writeFileError(w, &fileHTTPError{status: http.StatusInternalServerError, code: "read_failed", message: "failed to seek file"})
		return
	}
	setFileHeaders(w, contentType, filepath.Base(path), info.ModTime(), r.URL.Query().Get("preview") == "1", r.URL.Query().Get("download") == "1")
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
}

func absoluteFilePath(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", &fileHTTPError{status: http.StatusBadRequest, code: "path_required", message: "path is required"}
	}
	if !filepath.IsAbs(raw) {
		return "", &fileHTTPError{status: http.StatusBadRequest, code: "path_must_be_absolute", message: "path must be absolute"}
	}
	return filepath.Clean(raw), nil
}

func openRegularFile(path string) (*os.File, os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, classifyFileError(err, "file")
	}
	if info.IsDir() {
		return nil, nil, &fileHTTPError{status: http.StatusConflict, code: "not_file", message: "path is a directory"}
	}
	if !info.Mode().IsRegular() {
		return nil, nil, &fileHTTPError{status: http.StatusConflict, code: "not_regular_file", message: "path is not a regular file"}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, classifyFileError(err, "file")
	}
	openedInfo, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, &fileHTTPError{status: http.StatusInternalServerError, code: "stat_failed", message: "failed to stat file"}
	}
	if !openedInfo.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, &fileHTTPError{status: http.StatusConflict, code: "not_regular_file", message: "path is not a regular file"}
	}
	return f, openedInfo, nil
}

func describeFile(path, name string, info os.FileInfo) fileEntry {
	entry := fileEntry{
		Name: name, Path: path, Kind: fileKind(info.Mode()), Size: info.Size(),
		ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano), Mode: info.Mode().String(),
		MimeType: fileMimeType(path),
	}
	if info.Mode().IsRegular() || info.IsDir() {
		f, err := os.Open(path)
		if err == nil {
			entry.Readable = true
			_ = f.Close()
		} else {
			entry.ErrorCode = classifyFileCode(err)
		}
	} else {
		entry.ErrorCode = "not_regular_file"
	}
	return entry
}

func fileKind(mode os.FileMode) string {
	switch {
	case mode.IsDir():
		return "directory"
	case mode.IsRegular():
		return "file"
	case mode&os.ModeSymlink != 0:
		return "symlink"
	default:
		return "other"
	}
}

func fileMimeType(path string) string {
	if value := mime.TypeByExtension(filepath.Ext(path)); value != "" {
		return value
	}
	return "application/octet-stream"
}

func detectFileContentType(f *os.File, path string) (string, error) {
	head := make([]byte, 512)
	n, err := f.Read(head)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if byExtension := mime.TypeByExtension(filepath.Ext(path)); byExtension != "" {
		return byExtension, nil
	}
	if n > 0 {
		return http.DetectContentType(head[:n]), nil
	}
	return fileMimeType(path), nil
}

func previewLimit(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return defaultFilePreviewBytes, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > maxFilePreviewBytes {
		return 0, &fileHTTPError{status: http.StatusBadRequest, code: "invalid_preview_limit", message: fmt.Sprintf("maxBytes must be between 1 and %d", maxFilePreviewBytes)}
	}
	return value, nil
}

func classifyFileError(err error, subject string) error {
	code := classifyFileCode(err)
	status := http.StatusInternalServerError
	switch code {
	case "not_found":
		status = http.StatusNotFound
	case "not_readable":
		status = http.StatusForbidden
	}
	message := subject + " is unavailable"
	if code == "not_found" {
		message = subject + " was not found"
	}
	if code == "not_readable" {
		message = subject + " is not readable"
	}
	return &fileHTTPError{status: status, code: code, message: message}
}

func classifyFileCode(err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "not_found"
	case errors.Is(err, os.ErrPermission):
		return "not_readable"
	default:
		return "io_error"
	}
}

func setFileHeaders(w http.ResponseWriter, contentType, name string, modTime time.Time, preview, download bool) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(fileDisposition(contentType, preview, download), map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !modTime.IsZero() {
		w.Header().Set("Last-Modified", modTime.UTC().Format(http.TimeFormat))
	}
}

func fileDisposition(contentType string, preview, download bool) string {
	if download {
		return "attachment"
	}
	if preview {
		normalized := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
		switch normalized {
		case "application/pdf", "image/png", "image/jpeg", "image/gif", "image/webp", "text/plain", "text/markdown", "application/json":
			return "inline"
		default:
			if strings.HasPrefix(normalized, "audio/") || strings.HasPrefix(normalized, "video/") {
				return "inline"
			}
			return "attachment"
		}
	}
	return "attachment"
}

func writeFileError(w http.ResponseWriter, err error) {
	var fileErr *fileHTTPError
	if !errors.As(err, &fileErr) {
		fileErr = &fileHTTPError{status: http.StatusInternalServerError, code: "internal_error", message: "file request failed"}
	}
	writeJSON(w, fileErr.status, map[string]any{"error": map[string]string{
		"code": fileErr.code, "message": fileErr.message,
	}})
}
