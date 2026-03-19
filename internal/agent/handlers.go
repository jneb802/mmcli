package agent

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mmcli/internal/agentapi"
	"mmcli/internal/cfgfile"
)

type Handlers struct {
	cfg     AgentConfig
	pm      *ProcessManager
	version string
}

func NewHandlers(cfg AgentConfig, pm *ProcessManager, version string) *Handlers {
	return &Handlers{cfg: cfg, pm: pm, version: version}
}

func (h *Handlers) HandleStatus(w http.ResponseWriter, r *http.Request) {
	mods := h.listModDirs()
	_, bepinexErr := os.Stat(h.cfg.BepInExDir())

	resp := agentapi.StatusResponse{
		Running:  h.pm.IsRunning(),
		ModCount: len(mods),
		Mods:     mods,
		BepInEx:  bepinexErr == nil,
		Version:  h.version,
		Role:     RoleFromContext(r),
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

	// Layer 1: Filesystem scan
	modMap := make(map[string]*agentapi.ModInfo)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		disabled := false
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
		modMap[e.Name()] = &agentapi.ModInfo{Name: e.Name(), Disabled: disabled}
	}

	// Layer 2: Manifest enrichment
	var manifestTime string
	manifestNames := make(map[string]string) // dirName -> modName (for log matching)
	manifestPath := filepath.Join(h.cfg.BepInExDir(), agentapi.ManifestFileName)
	if data, err := os.ReadFile(manifestPath); err == nil {
		var manifest agentapi.PushManifest
		if json.Unmarshal(data, &manifest) == nil {
			manifestTime = manifest.PushedAt
			for _, mm := range manifest.Mods {
				manifestNames[mm.DirName] = mm.Name
				if info, ok := modMap[mm.DirName]; ok {
					info.Version = mm.Version
					info.Owner = mm.Owner
					info.Anticheat = mm.Anticheat
					info.Target = mm.Target
				}
			}
		}
	}

	// Layer 3: BepInEx log enrichment
	logParsed := false
	logPlugins, _ := ParseBepInExLog(h.cfg.ResolvedLogFile())
	if logPlugins != nil {
		logParsed = true
		matched := MatchLogToManifest(logPlugins, manifestNames)
		for dirName, lp := range matched {
			if info, ok := modMap[dirName]; ok {
				info.Version = lp.Version // log version is most authoritative
				t := true
				info.Loaded = &t
			}
		}
	}

	// Collect and sort
	mods := make([]agentapi.ModInfo, 0, len(modMap))
	for _, info := range modMap {
		mods = append(mods, *info)
	}
	sort.Slice(mods, func(i, j int) bool {
		return mods[i].Name < mods[j].Name
	})

	writeJSON(w, http.StatusOK, agentapi.ModListResponse{
		Mods:         mods,
		ManifestTime: manifestTime,
		LogParsed:    logParsed,
	})
}

func (h *Handlers) HandleModsPush(w http.ResponseWriter, r *http.Request) {
	log.Println("Push request received, parsing multipart...")

	// Parse multipart form (max 1GB in memory, excess spills to disk)
	if err := r.ParseMultipartForm(1 << 30); err != nil {
		log.Printf("Push: failed to parse multipart: %v", err)
		writeError(w, http.StatusBadRequest, "failed to parse upload: "+err.Error())
		return
	}

	file, fh, err := r.FormFile("archive")
	if err != nil {
		log.Printf("Push: missing archive field: %v", err)
		writeError(w, http.StatusBadRequest, "missing 'archive' field: "+err.Error())
		return
	}
	defer file.Close()
	log.Printf("Push: received archive (%d bytes)", fh.Size)

	bepDir := h.cfg.BepInExDir()
	if _, err := os.Stat(bepDir); os.IsNotExist(err) {
		writeError(w, http.StatusBadRequest, "BepInEx not installed on server")
		return
	}

	// Extract tar.gz
	gz, err := gzip.NewReader(file)
	if err != nil {
		log.Printf("Push: invalid gzip: %v", err)
		writeError(w, http.StatusBadRequest, "invalid gzip: "+err.Error())
		return
	}
	defer gz.Close()

	// Clean anticheat folders before extraction so pushed state is authoritative.
	// These folders are managed exclusively by mmcli — the fresh push recreates them.
	os.RemoveAll(filepath.Join(bepDir, "config", "AzuAntiCheat_Whitelist"))
	os.RemoveAll(filepath.Join(bepDir, "config", "AzuAntiCheat_Greylist"))

	tr := tar.NewReader(gz)
	count := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("Push: tar read error: %v", err)
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
				log.Printf("Push: failed to create file %s: %v", target, err)
				continue
			}
			io.Copy(f, tr)
			f.Close()
			count++
		}
	}

	log.Printf("Push: extracted %d files", count)
	writeJSON(w, http.StatusOK, agentapi.ActionResponse{
		OK:      true,
		Message: fmt.Sprintf("pushed %d files", count),
	})
}

func (h *Handlers) HandleModsSync(w http.ResponseWriter, r *http.Request) {
	log.Println("Sync request received, parsing JSON...")

	var req agentapi.SyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	bepDir := h.cfg.BepInExDir()
	if _, err := os.Stat(bepDir); os.IsNotExist(err) {
		writeError(w, http.StatusBadRequest, "BepInEx not installed on server")
		return
	}

	// Load existing manifest to diff
	oldMods := make(map[string]string) // dirName -> version
	manifestPath := filepath.Join(bepDir, agentapi.ManifestFileName)
	if data, err := os.ReadFile(manifestPath); err == nil {
		var oldManifest agentapi.PushManifest
		if json.Unmarshal(data, &oldManifest) == nil {
			for _, m := range oldManifest.Mods {
				oldMods[m.DirName] = m.Version
			}
		}
	}

	cacheDir := agentCacheDir()
	var resp agentapi.SyncResponse
	resp.OK = true

	// Build set of new mod dirNames for removal detection
	newModSet := make(map[string]bool)
	for _, mod := range req.Manifest.Mods {
		newModSet[mod.DirName] = true
	}

	// Remove mods no longer in manifest
	for dirName := range oldMods {
		if !newModSet[dirName] {
			log.Printf("Sync: removing %s (no longer in manifest)", dirName)
			removeModDirs(bepDir, dirName)
			resp.Removed++
		}
	}

	// Download and extract new/updated mods
	for _, mod := range req.Manifest.Mods {
		// Check if unchanged
		if oldVersion, exists := oldMods[mod.DirName]; exists && oldVersion == mod.Version {
			resp.Skipped++
			continue
		}

		// Remove old version dirs before extracting new
		removeModDirs(bepDir, mod.DirName)

		// Download (cache-aware)
		zipPath, wasCached, err := downloadModZip(cacheDir, mod.Owner, mod.Name, mod.Version)
		if err != nil {
			log.Printf("Sync: failed to download %s: %v", mod.DirName, err)
			resp.Failures = append(resp.Failures, agentapi.SyncFailure{
				Mod:    mod.DirName,
				Reason: err.Error(),
			})
			continue
		}

		// Extract
		if err := extractModZip(zipPath, bepDir, mod.Owner, mod.Name); err != nil {
			log.Printf("Sync: failed to extract %s: %v", mod.DirName, err)
			// Delete potentially corrupt cache entry and report failure
			os.Remove(zipPath)
			resp.Failures = append(resp.Failures, agentapi.SyncFailure{
				Mod:    mod.DirName,
				Reason: "extract failed: " + err.Error(),
			})
			continue
		}

		if wasCached {
			log.Printf("Sync: extracted %s (cached)", mod.DirName)
			resp.Cached++
		} else {
			log.Printf("Sync: downloaded and extracted %s", mod.DirName)
			resp.Downloaded++
		}
	}

	// Rebuild anticheat folders
	if err := setupAnticheat(bepDir, req.Manifest.Mods); err != nil {
		log.Printf("Sync: anticheat setup error: %v", err)
	}

	// Write new manifest
	data, err := json.MarshalIndent(req.Manifest, "", "  ")
	if err == nil {
		os.WriteFile(manifestPath, data, 0644)
	}

	total := resp.Downloaded + resp.Cached + resp.Skipped
	resp.Message = fmt.Sprintf("synced %d mods (%d downloaded, %d cached, %d unchanged, %d removed, %d failed)",
		total, resp.Downloaded, resp.Cached, resp.Skipped, resp.Removed, len(resp.Failures))
	log.Printf("Sync: %s", resp.Message)

	writeJSON(w, http.StatusOK, resp)
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

func (h *Handlers) HandleConfigList(w http.ResponseWriter, r *http.Request) {
	configDir := h.cfg.ConfigDir()
	files, err := cfgfile.ListConfigFiles(configDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, agentapi.ConfigListResponse{Files: []string{}})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if files == nil {
		files = []string{}
	}
	writeJSON(w, http.StatusOK, agentapi.ConfigListResponse{Files: files})
}

func (h *Handlers) HandleConfigGet(w http.ResponseWriter, r *http.Request) {
	filename := strings.TrimPrefix(r.URL.Path, agentapi.PathConfigs+"/")
	if filename == "" {
		writeError(w, http.StatusBadRequest, "filename required")
		return
	}

	// Security: reject path traversal
	if strings.Contains(filename, "..") || filepath.IsAbs(filename) {
		writeError(w, http.StatusBadRequest, "invalid filename")
		return
	}

	path := filepath.Join(h.cfg.ConfigDir(), filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "config file not found: "+filename)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, agentapi.ConfigFileResponse{
		Filename: filename,
		Content:  string(data),
	})
}

func (h *Handlers) HandleConfigPush(w http.ResponseWriter, r *http.Request) {
	var req agentapi.ConfigPushRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}

	configDir := h.cfg.ConfigDir()
	totalApplied := 0
	totalWritten := 0

	// Apply .cfg entry-level patches grouped by filename
	patchesByFile := make(map[string][]cfgfile.Patch)
	for _, p := range req.Patches {
		if strings.Contains(p.Filename, "..") || filepath.IsAbs(p.Filename) {
			continue
		}
		patchesByFile[p.Filename] = append(patchesByFile[p.Filename], cfgfile.Patch{
			Section: p.Section,
			Key:     p.Key,
			Value:   p.Value,
		})
	}

	for filename, patches := range patchesByFile {
		path := filepath.Join(configDir, filename)
		applied, err := cfgfile.PatchFile(path, patches)
		if err != nil {
			log.Printf("Config push: failed to patch %s: %v", filename, err)
			continue
		}
		totalApplied += applied
	}

	// Write whole files (YAML/JSON)
	for _, f := range req.Files {
		if strings.Contains(f.Filename, "..") || filepath.IsAbs(f.Filename) {
			continue
		}
		path := filepath.Join(configDir, f.Filename)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			log.Printf("Config push: failed to create dir for %s: %v", f.Filename, err)
			continue
		}
		if err := os.WriteFile(path, []byte(f.Content), 0644); err != nil {
			log.Printf("Config push: failed to write %s: %v", f.Filename, err)
			continue
		}
		totalWritten++
	}

	msg := fmt.Sprintf("applied %d cfg patches, wrote %d files", totalApplied, totalWritten)
	log.Printf("Config push: %s", msg)
	writeJSON(w, http.StatusOK, agentapi.ConfigPushResponse{
		OK:      true,
		Applied: totalApplied,
		Written: totalWritten,
		Message: msg,
	})
}

func (h *Handlers) HandleSettingsGet(w http.ResponseWriter, r *http.Request) {
	settings, err := ParseStartScript(h.cfg.ResolvedStartScript())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to parse start script: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *Handlers) HandleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	var req agentapi.SettingsUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}

	scriptPath := h.cfg.ResolvedStartScript()
	ps, settings, err := ParseStartScriptFull(scriptPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to parse start script: "+err.Error())
		return
	}

	ApplySettingsUpdate(settings, &req)
	content := RebuildStartScript(ps, settings)

	// Atomic write: temp file + chmod + rename
	dir := filepath.Dir(scriptPath)
	tmp, err := os.CreateTemp(dir, ".mmcli-script-*.sh")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create temp file: "+err.Error())
		return
	}
	tmpName := tmp.Name()

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		writeError(w, http.StatusInternalServerError, "failed to write script: "+err.Error())
		return
	}
	tmp.Close()

	if err := os.Chmod(tmpName, 0755); err != nil {
		os.Remove(tmpName)
		writeError(w, http.StatusInternalServerError, "failed to set permissions: "+err.Error())
		return
	}

	if err := os.Rename(tmpName, scriptPath); err != nil {
		os.Remove(tmpName)
		writeError(w, http.StatusInternalServerError, "failed to replace script: "+err.Error())
		return
	}

	log.Printf("Settings updated: %s", scriptPath)
	writeJSON(w, http.StatusOK, agentapi.ActionResponse{OK: true, Message: "settings updated"})
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

func (h *Handlers) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	log.Println("Update request received, checking GitHub for latest release...")

	// Query GitHub API for latest release
	resp, err := http.Get(fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", agentapi.GitHubRepo))
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to check GitHub: "+err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("GitHub API returned %d", resp.StatusCode))
		return
	}

	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		writeError(w, http.StatusBadGateway, "failed to parse GitHub response: "+err.Error())
		return
	}

	newVersion := strings.TrimPrefix(release.TagName, "v")
	oldVersion := strings.TrimPrefix(h.version, "v")

	if newVersion == oldVersion {
		writeJSON(w, http.StatusOK, agentapi.UpdateResponse{
			OK:         true,
			OldVersion: h.version,
			NewVersion: release.TagName,
			Message:    "already up to date",
		})
		return
	}

	// Find agent binary asset
	var downloadURL string
	for _, asset := range release.Assets {
		if asset.Name == agentapi.AgentBinaryName {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		writeError(w, http.StatusNotFound, fmt.Sprintf("release %s has no %s asset", release.TagName, agentapi.AgentBinaryName))
		return
	}

	// Download to temp file in same directory as current binary (for atomic rename)
	exePath, err := os.Executable()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot determine executable path: "+err.Error())
		return
	}
	exePath, _ = filepath.EvalSymlinks(exePath)

	log.Printf("Update: downloading %s → %s", release.TagName, exePath)

	dlResp, err := http.Get(downloadURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to download binary: "+err.Error())
		return
	}
	defer dlResp.Body.Close()

	tmpFile, err := os.CreateTemp(filepath.Dir(exePath), "mmcli-agent-update-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create temp file: "+err.Error())
		return
	}
	tmpPath := tmpFile.Name()

	if _, err := io.Copy(tmpFile, dlResp.Body); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		writeError(w, http.StatusInternalServerError, "failed to download binary: "+err.Error())
		return
	}
	tmpFile.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		writeError(w, http.StatusInternalServerError, "failed to set permissions: "+err.Error())
		return
	}

	// Atomic rename
	if err := os.Rename(tmpPath, exePath); err != nil {
		os.Remove(tmpPath)
		writeError(w, http.StatusInternalServerError, "cannot overwrite binary — ensure it is owned by the agent user: "+err.Error())
		return
	}

	log.Printf("Update: replaced binary, restarting as %s", release.TagName)

	writeJSON(w, http.StatusOK, agentapi.UpdateResponse{
		OK:         true,
		OldVersion: h.version,
		NewVersion: release.TagName,
		Message:    "updated, restarting",
	})

	// Flush response then re-exec with new binary
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		exe, _ := os.Executable()
		syscall.Exec(exe, os.Args, os.Environ())
	}()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, agentapi.ErrorResponse{Error: msg})
}
