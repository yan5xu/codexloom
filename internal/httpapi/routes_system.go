package httpapi

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/hub"
)

func (s *Server) registerSystemRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"build": s.build})
	})

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		agents := s.hub.ListAgents()
		writeJSON(w, 200, map[string]any{
			"ok": true, "product": "CodexLoom", "dataDir": s.st.Dir(),
			"agents": len(agents), "sessions": len(agents), "build": s.build,
		})
	})

	mux.HandleFunc("POST /api/admin/restart", s.adminRestart)
	mux.HandleFunc("GET /api/admin/backups", s.adminListBackups)
	mux.HandleFunc("POST /api/admin/backup", s.adminBackup)
	mux.HandleFunc("POST /api/admin/backups/prune", s.adminPruneBackups)
	mux.HandleFunc("POST /api/files/open", func(w http.ResponseWriter, r *http.Request) {
		if !allowAdminRequest(r) {
			writeErr(w, &hub.HubError{Status: 403, Message: "opening local files is only allowed from localhost"})
			return
		}
		var body struct {
			Path string `json:"path"`
		}
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		path, err := resolveLocalOpenPath(body.Path)
		if err != nil {
			writeErr(w, err)
			return
		}
		if err := s.openLocalPath(path); err != nil {
			writeErr(w, fmt.Errorf("open local file: %w", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"opened": true, "path": path})
	})
	mux.HandleFunc("POST /api/skills/reload", func(w http.ResponseWriter, r *http.Request) {
		inventory, err := s.hub.ReloadSkills()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"inventory": inventory})
	})
	mux.HandleFunc("GET /api/usage", func(w http.ResponseWriter, r *http.Request) {
		start, endExclusive, explicit, err := calendarWindowFromRequest(r, time.Now())
		if err != nil {
			writeErr(w, &hub.HubError{Status: 400, Message: err.Error()})
			return
		}
		if explicit {
			writeJSON(w, 200, map[string]any{"usage": s.hub.TokenUsageOverviewRange(start, endExclusive)})
			return
		}
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		writeJSON(w, 200, map[string]any{"usage": s.hub.TokenUsageOverview(days)})
	})
	mux.HandleFunc("GET /api/workload", func(w http.ResponseWriter, r *http.Request) {
		start, endExclusive, explicit, err := calendarWindowFromRequest(r, time.Now())
		if err != nil {
			writeErr(w, &hub.HubError{Status: 400, Message: err.Error()})
			return
		}
		if explicit {
			writeJSON(w, 200, map[string]any{"workload": s.hub.WorkloadOverviewRange(start, endExclusive)})
			return
		}
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		writeJSON(w, 200, map[string]any{"workload": s.hub.WorkloadOverview(days)})
	})
	mux.HandleFunc("GET /api/activity/daily", func(w http.ResponseWriter, r *http.Request) {
		start, endExclusive, bucketMinutes, err := dailyActivityWindowFromRequest(r, time.Now())
		if err != nil {
			writeErr(w, &hub.HubError{Status: 400, Message: err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"activity": s.hub.DailyActivity(start, endExclusive, bucketMinutes)})
	})

	mux.HandleFunc("GET /api/remote", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"remote": s.hub.RemoteSnapshot()})
	})
	mux.HandleFunc("POST /api/remote/enable", func(w http.ResponseWriter, r *http.Request) {
		remote, err := s.hub.EnableRemote()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"remote": remote})
	})
	mux.HandleFunc("POST /api/remote/disable", func(w http.ResponseWriter, r *http.Request) {
		remote, err := s.hub.DisableRemote()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"remote": remote})
	})
	mux.HandleFunc("POST /api/remote/pairing", func(w http.ResponseWriter, r *http.Request) {
		pairing, err := s.hub.StartRemotePairing()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 201, map[string]any{"pairing": pairing})
	})
	mux.HandleFunc("GET /api/remote/pairing", func(w http.ResponseWriter, r *http.Request) {
		pairing, err := s.hub.ReadRemotePairing()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"pairing": pairing})
	})
	mux.HandleFunc("GET /api/remote/devices", func(w http.ResponseWriter, r *http.Request) {
		devices, err := s.hub.ListRemoteDevices()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"devices": devices})
	})
	mux.HandleFunc("DELETE /api/remote/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.hub.RevokeRemoteDevice(r.PathValue("id")); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"revoked": true})
	})

}

func resolveLocalOpenPath(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(path), "file://") {
		parsed, err := url.Parse(path)
		if err != nil || parsed.Scheme != "file" || parsed.Host != "" && parsed.Host != "localhost" {
			return "", &hub.HubError{Status: 400, Message: "invalid local file URL"}
		}
		path, err = url.PathUnescape(parsed.Path)
		if err != nil {
			return "", &hub.HubError{Status: 400, Message: "invalid local file URL"}
		}
	}
	if path == "" {
		return "", &hub.HubError{Status: 400, Message: "path is required"}
	}
	if !filepath.IsAbs(path) {
		return "", &hub.HubError{Status: 400, Message: "path must be absolute"}
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		// Codex-style file links may append :line or :line:column. Only strip
		// that suffix after the exact path failed, so filenames containing a
		// colon continue to work when they exist.
		if candidate := stripFilePosition(path); candidate != path {
			if candidateInfo, candidateErr := os.Stat(candidate); candidateErr == nil {
				path, info, err = candidate, candidateInfo, nil
			}
		}
	}
	if err != nil {
		if os.IsNotExist(err) {
			return "", &hub.HubError{Status: 404, Message: "local file not found"}
		}
		return "", fmt.Errorf("inspect local file: %w", err)
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return "", &hub.HubError{Status: 400, Message: "path must be a file or directory"}
	}
	return path, nil
}

func stripFilePosition(path string) string {
	parts := strings.Split(path, ":")
	if len(parts) < 2 || !allDigits(parts[len(parts)-1]) {
		return path
	}
	parts = parts[:len(parts)-1]
	if len(parts) > 1 && allDigits(parts[len(parts)-1]) {
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, ":")
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func systemOpenPath(path string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", path)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		command = exec.Command("xdg-open", path)
	}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func dailyActivityWindowFromRequest(r *http.Request, now time.Time) (time.Time, time.Time, int, error) {
	location := time.Local
	if timezone := strings.TrimSpace(r.URL.Query().Get("tz")); timezone != "" {
		var err error
		location, err = time.LoadLocation(timezone)
		if err != nil {
			return time.Time{}, time.Time{}, 0, fmt.Errorf("invalid timezone %q", timezone)
		}
	}
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" {
		date = now.In(location).Format("2006-01-02")
	}
	start, err := time.ParseInLocation("2006-01-02", date, location)
	if err != nil {
		return time.Time{}, time.Time{}, 0, fmt.Errorf("invalid date %q", date)
	}
	today := now.In(location)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, location)
	if start.After(today) {
		return time.Time{}, time.Time{}, 0, fmt.Errorf("date must not be in the future")
	}
	bucketMinutes := 30
	if value := strings.TrimSpace(r.URL.Query().Get("bucket")); value != "" {
		bucketMinutes, err = strconv.Atoi(value)
		if err != nil {
			return time.Time{}, time.Time{}, 0, fmt.Errorf("invalid bucket %q", value)
		}
	}
	if bucketMinutes != 15 && bucketMinutes != 30 && bucketMinutes != 60 {
		return time.Time{}, time.Time{}, 0, fmt.Errorf("bucket must be 15, 30, or 60 minutes")
	}
	return start, start.AddDate(0, 0, 1), bucketMinutes, nil
}
