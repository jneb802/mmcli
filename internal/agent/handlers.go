package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
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

		// Enrich with game state from MMCLIServerMod
		if modStatus, _ := QueryModStatus(h.cfg.ResolvedModAPIPort()); modStatus != nil {
			resp.PlayerCount = modStatus.PlayerCount
			resp.Day = modStatus.Day
			resp.GameTime = modStatus.GameTime
			resp.IsDay = &modStatus.IsDay
			resp.World = modStatus.World
		}
	}

	// Webhook summary
	if h.cfg.DiscordWebhook != nil && h.cfg.DiscordWebhook.URL != "" {
		resp.WebhookEnabled = true
		// Mask the URL for security — show last 8 chars
		url := h.cfg.DiscordWebhook.URL
		if len(url) > 12 {
			resp.WebhookURL = "…" + url[len(url)-8:]
		} else {
			resp.WebhookURL = "***"
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handlers) HandleWebhookGet(w http.ResponseWriter, r *http.Request) {
	resp := agentapi.WebhookConfigResponse{}
	if h.cfg.DiscordWebhook != nil {
		resp.URL = h.cfg.DiscordWebhook.URL
		resp.ServerStarted = h.cfg.DiscordWebhook.ServerStarted
		resp.ServerStopped = h.cfg.DiscordWebhook.ServerStopped
		resp.WorldSaved = h.cfg.DiscordWebhook.WorldSaved
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handlers) HandleWebhookUpdate(w http.ResponseWriter, r *http.Request) {
	var req agentapi.WebhookConfigUpdate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if h.cfg.DiscordWebhook == nil {
		h.cfg.DiscordWebhook = &DiscordWebhookConfig{}
	}
	if req.URL != nil {
		h.cfg.DiscordWebhook.URL = *req.URL
	}
	if req.ServerStarted != nil {
		h.cfg.DiscordWebhook.ServerStarted = *req.ServerStarted
	}
	if req.ServerStopped != nil {
		h.cfg.DiscordWebhook.ServerStopped = *req.ServerStopped
	}
	if req.WorldSaved != nil {
		h.cfg.DiscordWebhook.WorldSaved = *req.WorldSaved
	}

	// Persist to disk
	cfgPath := DefaultConfigPath()
	if err := SaveConfig(cfgPath, h.cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save config: "+err.Error())
		return
	}

	log.Printf("Webhook config updated")
	writeJSON(w, http.StatusOK, agentapi.ActionResponse{OK: true, Message: "webhook config updated"})
}

func (h *Handlers) HandlePlayers(w http.ResponseWriter, r *http.Request) {
	players, _ := QueryModPlayers(h.cfg.ResolvedModAPIPort())
	if players == nil {
		writeJSON(w, http.StatusOK, agentapi.PlayersResponse{Players: []agentapi.PlayerInfo{}})
		return
	}

	result := make([]agentapi.PlayerInfo, len(players))
	for i, p := range players {
		result[i] = agentapi.PlayerInfo{
			Name:    p.Name,
			SteamID: p.Host,
		}
	}
	writeJSON(w, http.StatusOK, agentapi.PlayersResponse{Players: result})
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

	// Layer 3: Plugin load enrichment (mod API preferred, log parsing fallback)
	logParsed := false
	apiQueried := false

	apiPlugins, _ := QueryModAPI(h.cfg.ResolvedModAPIPort())
	if apiPlugins != nil {
		apiQueried = true
		matched, unmatched := MatchAPIToMods(apiPlugins, modMap, manifestNames)

		// Update matched mods with loaded status (and version if exact match)
		for dirName, m := range matched {
			if info, ok := modMap[dirName]; ok {
				if m.Exact || info.Version == "" {
					info.Version = m.Plugin.Version
				}
				t := true
				info.Loaded = &t
			}
		}

		// Add unmatched plugins (mods not in manifest or filesystem)
		for _, ap := range unmatched {
			// Skip BepInEx core plugins — infrastructure, not user mods
			if strings.HasPrefix(ap.GUID, "BepInEx.") || ap.GUID == "BepInEx" {
				continue
			}
			t := true
			modMap[ap.GUID] = &agentapi.ModInfo{
				Name:    ap.Name,
				Version: ap.Version,
				Loaded:  &t,
			}
		}
	} else {
		// Fallback: BepInEx log enrichment
		logPlugins, _ := ParseBepInExLog(h.cfg.ResolvedLogFile())
		if logPlugins != nil {
			logParsed = true
			matched := MatchLogToManifest(logPlugins, manifestNames)
			for dirName, lp := range matched {
				if info, ok := modMap[dirName]; ok {
					info.Version = lp.Version
					t := true
					info.Loaded = &t
				}
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
		APIQueried:   apiQueried,
	})
}

func (h *Handlers) HandleModsSync(w http.ResponseWriter, r *http.Request) {
	log.Println("Sync request received, parsing multipart...")

	if err := r.ParseMultipartForm(1 << 30); err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse multipart: "+err.Error())
		return
	}

	manifestJSON := r.FormValue("manifest")
	if manifestJSON == "" {
		writeError(w, http.StatusBadRequest, "missing 'manifest' field")
		return
	}

	var manifest agentapi.PushManifest
	if err := json.Unmarshal([]byte(manifestJSON), &manifest); err != nil {
		writeError(w, http.StatusBadRequest, "invalid manifest JSON: "+err.Error())
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
	for _, mod := range manifest.Mods {
		newModSet[mod.DirName] = true
	}

	// Remove mods no longer in manifest
	for dirName := range oldMods {
		if !newModSet[dirName] {
			log.Printf("Sync: removing %s (no longer in manifest)", dirName)
			removeModDirs(bepDir, dirName)
			resp.Removed++
			resp.Results = append(resp.Results, agentapi.SyncModResult{
				Mod: dirName, Status: "removed",
			})
		}
	}

	// Process each mod by source
	for _, mod := range manifest.Mods {
		source := mod.Source
		if source == "" {
			source = "thunderstore" // backward compat with old manifests
		}

		if source == "upload" {
			// Upload mods are always re-extracted (dev builds)
			removeModDirs(bepDir, mod.DirName)

			file, fh, err := r.FormFile(mod.DirName)
			if err != nil {
				log.Printf("Sync: missing upload for %s: %v", mod.DirName, err)
				resp.Failures = append(resp.Failures, agentapi.SyncFailure{
					Mod:    mod.DirName,
					Reason: "missing upload attachment",
				})
				resp.Results = append(resp.Results, agentapi.SyncModResult{
					Mod: mod.DirName, Version: mod.Version, Status: "failed", Reason: "missing upload attachment",
				})
				continue
			}

			if err := extractUploadZip(file, fh.Size, bepDir, mod.DirName); err != nil {
				file.Close()
				log.Printf("Sync: failed to extract upload %s: %v", mod.DirName, err)
				reason := "extract failed: " + err.Error()
				resp.Failures = append(resp.Failures, agentapi.SyncFailure{
					Mod:    mod.DirName,
					Reason: reason,
				})
				resp.Results = append(resp.Results, agentapi.SyncModResult{
					Mod: mod.DirName, Version: mod.Version, Status: "failed", Reason: reason,
				})
				continue
			}
			file.Close()

			log.Printf("Sync: extracted upload %s", mod.DirName)
			resp.Uploaded++
			resp.Results = append(resp.Results, agentapi.SyncModResult{
				Mod: mod.DirName, Version: mod.Version, Status: "uploaded",
			})
		} else {
			// Thunderstore mod — skip if unchanged
			if oldVersion, exists := oldMods[mod.DirName]; exists && oldVersion == mod.Version {
				resp.Skipped++
				resp.Results = append(resp.Results, agentapi.SyncModResult{
					Mod: mod.DirName, Version: mod.Version, Status: "skipped",
				})
				continue
			}

			removeModDirs(bepDir, mod.DirName)

			zipPath, wasCached, err := downloadModZip(cacheDir, mod.Owner, mod.Name, mod.Version)
			if err != nil {
				log.Printf("Sync: failed to download %s: %v", mod.DirName, err)
				resp.Failures = append(resp.Failures, agentapi.SyncFailure{
					Mod:    mod.DirName,
					Reason: err.Error(),
				})
				resp.Results = append(resp.Results, agentapi.SyncModResult{
					Mod: mod.DirName, Version: mod.Version, Status: "failed", Reason: err.Error(),
				})
				continue
			}

			if err := extractModZip(zipPath, bepDir, mod.Owner, mod.Name); err != nil {
				log.Printf("Sync: failed to extract %s: %v", mod.DirName, err)
				os.Remove(zipPath)
				reason := "extract failed: " + err.Error()
				resp.Failures = append(resp.Failures, agentapi.SyncFailure{
					Mod:    mod.DirName,
					Reason: reason,
				})
				resp.Results = append(resp.Results, agentapi.SyncModResult{
					Mod: mod.DirName, Version: mod.Version, Status: "failed", Reason: reason,
				})
				continue
			}

			if wasCached {
				log.Printf("Sync: extracted %s (cached)", mod.DirName)
				resp.Cached++
				resp.Results = append(resp.Results, agentapi.SyncModResult{
					Mod: mod.DirName, Version: mod.Version, Status: "cached",
				})
			} else {
				log.Printf("Sync: downloaded and extracted %s", mod.DirName)
				resp.Downloaded++
				resp.Results = append(resp.Results, agentapi.SyncModResult{
					Mod: mod.DirName, Version: mod.Version, Status: "downloaded",
				})
			}
		}
	}

	// Rebuild anticheat folders
	if err := setupAnticheatSystems(bepDir, manifest.Mods, h.cfg.ResolvedModAPIPort()); err != nil {
		log.Printf("Sync: anticheat setup error: %v", err)
	}

	// Write new manifest
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err == nil {
		os.WriteFile(manifestPath, data, 0644)
	}

	total := resp.Downloaded + resp.Uploaded + resp.Cached + resp.Skipped
	resp.Message = fmt.Sprintf("synced %d mods (%d downloaded, %d uploaded, %d cached, %d unchanged, %d removed, %d failed)",
		total, resp.Downloaded, resp.Uploaded, resp.Cached, resp.Skipped, resp.Removed, len(resp.Failures))
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

	// Get last N lines (0 = all)
	allLines := strings.Split(string(data), "\n")
	start := 0
	if lines > 0 && len(allLines) > lines {
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

	settings, err := ParseStartScript(h.cfg.ResolvedStartScript())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to parse start script: "+err.Error())
		return
	}

	ApplySettingsUpdate(settings, &req)

	if err := h.rebuildStartScript(settings); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	log.Printf("Settings updated: %s", h.cfg.ResolvedStartScript())
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

	// Flush response then restart
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		// Prefer systemd restart if running as a service
		if err := exec.Command("systemctl", "restart", "mmcli-agent").Run(); err == nil {
			return
		}
		// Fallback: re-exec with new binary
		exe, _ := os.Executable()
		syscall.Exec(exe, os.Args, os.Environ())
	}()
}

// --- World handlers ---

func (h *Handlers) HandleWorldsList(w http.ResponseWriter, r *http.Request) {
	saveDir := h.resolveSaveDir()
	worldsDir := filepath.Join(saveDir, "worlds_local")

	entries, err := os.ReadDir(worldsDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, agentapi.WorldListResponse{
				Worlds:  []agentapi.WorldInfo{},
				SaveDir: saveDir,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	seen := make(map[string]bool)
	var worlds []agentapi.WorldInfo
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".fwl") {
			continue
		}
		worldName := strings.TrimSuffix(name, ".fwl")
		if seen[worldName] {
			continue
		}
		seen[worldName] = true

		info := agentapi.WorldInfo{Name: worldName, SizeDB: -1, SizeFWL: -1}
		if fi, err := os.Stat(filepath.Join(worldsDir, worldName+".fwl")); err == nil {
			info.SizeFWL = fi.Size()
			info.Modified = fi.ModTime().UTC().Format(time.RFC3339)
		}
		if fi, err := os.Stat(filepath.Join(worldsDir, worldName+".db")); err == nil {
			info.SizeDB = fi.Size()
			info.Modified = fi.ModTime().UTC().Format(time.RFC3339)
		}
		worlds = append(worlds, info)
	}

	sort.Slice(worlds, func(i, j int) bool { return worlds[i].Name < worlds[j].Name })
	writeJSON(w, http.StatusOK, agentapi.WorldListResponse{Worlds: worlds, SaveDir: saveDir})
}

func (h *Handlers) HandleWorldUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(1 << 30); err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse multipart: "+err.Error())
		return
	}

	name := r.FormValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing 'name' field")
		return
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, "/\\") {
		writeError(w, http.StatusBadRequest, "invalid world name")
		return
	}

	dbFile, _, err := r.FormFile("db")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing 'db' file")
		return
	}
	defer dbFile.Close()

	fwlFile, _, err := r.FormFile("fwl")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing 'fwl' file")
		return
	}
	defer fwlFile.Close()

	saveDir := h.resolveSaveDir()
	worldsDir := filepath.Join(saveDir, "worlds_local")
	if err := os.MkdirAll(worldsDir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create worlds dir: "+err.Error())
		return
	}

	for _, item := range []struct {
		src  io.Reader
		dest string
	}{
		{dbFile, filepath.Join(worldsDir, name+".db")},
		{fwlFile, filepath.Join(worldsDir, name+".fwl")},
	} {
		tmp, err := os.CreateTemp(worldsDir, ".mmcli-world-*")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create temp file: "+err.Error())
			return
		}
		if _, err := io.Copy(tmp, item.src); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			writeError(w, http.StatusInternalServerError, "failed to write world file: "+err.Error())
			return
		}
		tmp.Close()
		if err := os.Rename(tmp.Name(), item.dest); err != nil {
			os.Remove(tmp.Name())
			writeError(w, http.StatusInternalServerError, "failed to install world file: "+err.Error())
			return
		}
	}

	log.Printf("World uploaded: %s", name)
	writeJSON(w, http.StatusOK, agentapi.WorldUploadResponse{OK: true, Name: name, Message: "world uploaded"})
}

func (h *Handlers) resolveSaveDir() string {
	// Try parsing current start script for savedir
	settings, err := ParseStartScript(h.cfg.ResolvedStartScript())
	if err == nil && settings.SaveDir != "" {
		return settings.SaveDir
	}
	// Default Valheim save location on Linux
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "unity3d", "IronGate", "Valheim")
}

// --- Launch config handlers ---

func (h *Handlers) activeLaunchConfigName() string {
	if h.cfg.ActiveLaunchConfig != "" {
		return h.cfg.ActiveLaunchConfig
	}
	return "default"
}

// loadLaunchConfig reads and parses a launch config JSON file by name.
func (h *Handlers) loadLaunchConfig(name string) (*agentapi.LaunchConfig, error) {
	path := filepath.Join(h.cfg.LaunchConfigsDir(), name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lc agentapi.LaunchConfig
	if err := json.Unmarshal(data, &lc); err != nil {
		return nil, fmt.Errorf("corrupt config file: %w", err)
	}
	return &lc, nil
}

// saveLaunchConfig writes a launch config JSON file.
func (h *Handlers) saveLaunchConfig(lc *agentapi.LaunchConfig) error {
	data, err := json.MarshalIndent(lc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(h.cfg.LaunchConfigsDir(), lc.Name+".json"), data, 0644)
}

// extractLaunchConfigName extracts the config name from a URL path like /api/v1/launch-configs/{name}.
func extractLaunchConfigName(r *http.Request) (string, bool) {
	name := strings.TrimPrefix(r.URL.Path, agentapi.PathLaunchConfigs+"/")
	if name == "" || name == "active" {
		return "", false
	}
	return name, true
}

func (h *Handlers) ensureLaunchConfigs() error {
	dir := h.cfg.LaunchConfigsDir()
	if _, err := os.Stat(dir); err == nil {
		return nil // already migrated
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create launch configs dir: %w", err)
	}

	// Import current start script as "default"
	settings, err := ParseStartScript(h.cfg.ResolvedStartScript())
	if err != nil {
		// No valid script — create empty default
		settings = &agentapi.SettingsResponse{
			Name:  "My Server",
			Port:  2456,
			World: "Dedicated",
		}
	}

	lc := &agentapi.LaunchConfig{
		Name:      "default",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Settings:  *settings,
	}
	if err := h.saveLaunchConfig(lc); err != nil {
		return err
	}

	h.cfg.ActiveLaunchConfig = "default"
	SaveConfig(DefaultConfigPath(), h.cfg)
	log.Printf("Migrated start script to launch config 'default'")
	return nil
}

func (h *Handlers) HandleLaunchConfigsList(w http.ResponseWriter, r *http.Request) {
	if err := h.ensureLaunchConfigs(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	dir := h.cfg.LaunchConfigsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	active := h.activeLaunchConfigName()

	var configs []agentapi.LaunchConfigSummary
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var lc agentapi.LaunchConfig
		if json.Unmarshal(data, &lc) != nil {
			continue
		}
		configs = append(configs, agentapi.LaunchConfigSummary{
			Name:        lc.Name,
			Description: lc.Description,
			World:       lc.Settings.World,
			Preset:      lc.Settings.Preset,
		})
	}

	sort.Slice(configs, func(i, j int) bool { return configs[i].Name < configs[j].Name })
	writeJSON(w, http.StatusOK, agentapi.LaunchConfigListResponse{Configs: configs, Active: active})
}

func (h *Handlers) HandleLaunchConfigCreate(w http.ResponseWriter, r *http.Request) {
	if err := h.ensureLaunchConfigs(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var req agentapi.LaunchConfigCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if strings.Contains(req.Name, "..") || strings.ContainsAny(req.Name, "/\\") {
		writeError(w, http.StatusBadRequest, "invalid config name")
		return
	}

	// Check if already exists
	if _, err := h.loadLaunchConfig(req.Name); err == nil {
		writeError(w, http.StatusConflict, "launch config already exists: "+req.Name)
		return
	}

	lc := &agentapi.LaunchConfig{
		Name:        req.Name,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		Description: req.Description,
	}

	if req.CopyFrom != "" {
		src, err := h.loadLaunchConfig(req.CopyFrom)
		if err != nil {
			writeError(w, http.StatusNotFound, "source config not found: "+req.CopyFrom)
			return
		}
		lc.Settings = src.Settings
	} else if req.Settings != nil {
		lc.Settings = *req.Settings
	} else {
		lc.Settings = agentapi.SettingsResponse{
			Name:  "My Server",
			Port:  2456,
			World: "Dedicated",
		}
	}

	if err := h.saveLaunchConfig(lc); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	log.Printf("Launch config created: %s", req.Name)
	writeJSON(w, http.StatusOK, agentapi.ActionResponse{OK: true, Message: "launch config created"})
}

func (h *Handlers) HandleLaunchConfigGet(w http.ResponseWriter, r *http.Request) {
	if err := h.ensureLaunchConfigs(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	name, ok := extractLaunchConfigName(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "config name required")
		return
	}

	lc, err := h.loadLaunchConfig(name)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "launch config not found: "+name)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lc)
}

func (h *Handlers) HandleLaunchConfigUpdate(w http.ResponseWriter, r *http.Request) {
	if err := h.ensureLaunchConfigs(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	name, ok := extractLaunchConfigName(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "config name required")
		return
	}

	lc, err := h.loadLaunchConfig(name)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "launch config not found: "+name)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var settings agentapi.SettingsResponse
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	lc.Settings = settings

	if err := h.saveLaunchConfig(lc); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if name == h.activeLaunchConfigName() {
		if err := h.rebuildStartScript(&lc.Settings); err != nil {
			writeError(w, http.StatusInternalServerError, "config saved but failed to rebuild start script: "+err.Error())
			return
		}
	}

	log.Printf("Launch config updated: %s", name)
	writeJSON(w, http.StatusOK, agentapi.ActionResponse{OK: true, Message: "launch config updated"})
}

func (h *Handlers) HandleLaunchConfigDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.ensureLaunchConfigs(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	name, ok := extractLaunchConfigName(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "config name required")
		return
	}

	if name == h.activeLaunchConfigName() {
		writeError(w, http.StatusConflict, "cannot delete the active launch config")
		return
	}

	path := filepath.Join(h.cfg.LaunchConfigsDir(), name+".json")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "launch config not found: "+name)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	log.Printf("Launch config deleted: %s", name)
	writeJSON(w, http.StatusOK, agentapi.ActionResponse{OK: true, Message: "launch config deleted"})
}

func (h *Handlers) HandleLaunchConfigActivate(w http.ResponseWriter, r *http.Request) {
	if err := h.ensureLaunchConfigs(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var req agentapi.LaunchConfigActivateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	lc, err := h.loadLaunchConfig(req.Name)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "launch config not found: "+req.Name)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := h.rebuildStartScript(&lc.Settings); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to rebuild start script: "+err.Error())
		return
	}

	h.cfg.ActiveLaunchConfig = req.Name
	SaveConfig(DefaultConfigPath(), h.cfg)

	log.Printf("Launch config activated: %s", req.Name)
	writeJSON(w, http.StatusOK, agentapi.ActionResponse{OK: true, Message: "launch config activated, restart server to apply"})
}

func (h *Handlers) rebuildStartScript(settings *agentapi.SettingsResponse) error {
	scriptPath := h.cfg.ResolvedStartScript()
	ps, _, err := ParseStartScriptFull(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to parse start script: %w", err)
	}

	content := RebuildStartScript(ps, settings)

	dir := filepath.Dir(scriptPath)
	tmp, err := os.CreateTemp(dir, ".mmcli-script-*.sh")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	tmp.Close()

	if err := os.Chmod(tmpName, 0755); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, scriptPath)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, agentapi.ErrorResponse{Error: msg})
}
