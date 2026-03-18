package agent

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mmcli/internal/agentapi"
)

type Handlers struct {
	cfg AgentConfig
	pm  *ProcessManager
}

func NewHandlers(cfg AgentConfig, pm *ProcessManager) *Handlers {
	return &Handlers{cfg: cfg, pm: pm}
}

func (h *Handlers) HandleStatus(w http.ResponseWriter, r *http.Request) {
	mods := h.listModDirs()
	_, bepinexErr := os.Stat(h.cfg.BepInExDir())

	resp := agentapi.StatusResponse{
		Running:  h.pm.IsRunning(),
		ModCount: len(mods),
		Mods:     mods,
		BepInEx:  bepinexErr == nil,
		Version:  "0.1.0",
	}

	if resp.Running {
		uptime := h.pm.Uptime()
		resp.UptimeSecs = int64(uptime.Seconds())
		resp.Uptime = formatDuration(uptime)
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handlers) HandleStart(w http.ResponseWriter, r *http.Request) {
	if err := h.pm.Start(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agentapi.ActionResponse{OK: true, Message: "server started"})
}

func (h *Handlers) HandleStop(w http.ResponseWriter, r *http.Request) {
	if err := h.pm.Stop(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agentapi.ActionResponse{OK: true, Message: "server stopped"})
}

func (h *Handlers) HandleRestart(w http.ResponseWriter, r *http.Request) {
	if h.pm.IsRunning() {
		if err := h.pm.Stop(); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to stop: "+err.Error())
			return
		}
		// Brief pause between stop and start
		time.Sleep(2 * time.Second)
	}
	if err := h.pm.Start(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agentapi.ActionResponse{OK: true, Message: "server restarted"})
}

func (h *Handlers) HandleModsList(w http.ResponseWriter, r *http.Request) {
	pluginsDir := h.cfg.PluginsDir()
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, agentapi.ModListResponse{Mods: []agentapi.ModInfo{}})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var mods []agentapi.ModInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		disabled := false
		// Check if all DLLs are disabled (.dll.old)
		dlls := findDLLs(filepath.Join(pluginsDir, e.Name()))
		if len(dlls) > 0 {
			allDisabled := true
			for _, d := range dlls {
				if !strings.HasSuffix(d, ".dll.old") {
					allDisabled = false
					break
				}
			}
			disabled = allDisabled
		}
		mods = append(mods, agentapi.ModInfo{Name: e.Name(), Disabled: disabled})
	}

	writeJSON(w, http.StatusOK, agentapi.ModListResponse{Mods: mods})
}

func (h *Handlers) HandleModsPush(w http.ResponseWriter, r *http.Request) {
	// Parse multipart form (max 500MB)
	if err := r.ParseMultipartForm(500 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse upload: "+err.Error())
		return
	}

	file, _, err := r.FormFile("archive")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing 'archive' field: "+err.Error())
		return
	}
	defer file.Close()

	bepDir := h.cfg.BepInExDir()
	if _, err := os.Stat(bepDir); os.IsNotExist(err) {
		writeError(w, http.StatusBadRequest, "BepInEx not installed on server")
		return
	}

	// Extract tar.gz
	gz, err := gzip.NewReader(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid gzip: "+err.Error())
		return
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	count := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid tar: "+err.Error())
			return
		}

		// Safety: reject absolute paths and traversal
		name := filepath.Clean(hdr.Name)
		if filepath.IsAbs(name) || strings.Contains(name, "..") {
			continue
		}

		target := filepath.Join(bepDir, name)

		switch hdr.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(target, 0755)
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(target), 0755)
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				continue
			}
			io.Copy(f, tr)
			f.Close()
			count++
		}
	}

	writeJSON(w, http.StatusOK, agentapi.ActionResponse{
		OK:      true,
		Message: fmt.Sprintf("pushed %d files", count),
	})
}

func (h *Handlers) HandleLogs(w http.ResponseWriter, r *http.Request) {
	linesStr := r.URL.Query().Get("lines")
	lines := 100
	if linesStr != "" {
		if n, err := strconv.Atoi(linesStr); err == nil && n > 0 {
			lines = n
		}
	}
	follow := r.URL.Query().Get("follow") == "true"

	logFile := h.cfg.ResolvedLogFile()
	data, err := os.ReadFile(logFile)
	if err != nil {
		if os.IsNotExist(err) {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("(no log file yet)\n"))
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Get last N lines
	allLines := strings.Split(string(data), "\n")
	start := 0
	if len(allLines) > lines {
		start = len(allLines) - lines
	}
	tail := strings.Join(allLines[start:], "\n")

	w.Header().Set("Content-Type", "text/plain")

	if !follow {
		w.Write([]byte(tail))
		return
	}

	// Streaming mode
	flusher, ok := w.(http.Flusher)
	if !ok {
		w.Write([]byte(tail))
		return
	}

	w.Write([]byte(tail))
	flusher.Flush()

	// Tail the file
	lastSize := int64(len(data))
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(logFile)
			if err != nil {
				continue
			}
			if info.Size() <= lastSize {
				continue
			}
			f, err := os.Open(logFile)
			if err != nil {
				continue
			}
			f.Seek(lastSize, io.SeekStart)
			newData, err := io.ReadAll(f)
			f.Close()
			if err != nil {
				continue
			}
			lastSize = info.Size()
			w.Write(newData)
			flusher.Flush()
		}
	}
}

// Helper functions

func (h *Handlers) listModDirs() []string {
	entries, err := os.ReadDir(h.cfg.PluginsDir())
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

func findDLLs(dir string) []string {
	var dlls []string
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && (strings.HasSuffix(path, ".dll") || strings.HasSuffix(path, ".dll.old")) {
			dlls = append(dlls, path)
		}
		return nil
	})
	return dlls
}

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, agentapi.ErrorResponse{Error: msg})
}
