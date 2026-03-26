package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mmcli/internal/agentapi"
	"mmcli/internal/cfgfile"
)

type Handlers struct {
	cfg              AgentConfig
	cfgPath          string
	pm               *ProcessManager
	st               *StateTracker
	version          string
	lastAPIPlugins   []ModAPIPlugin // cached last successful mod API response
}

func NewHandlers(cfg AgentConfig, cfgPath string, pm *ProcessManager, st *StateTracker, version string) *Handlers {
	return &Handlers{cfg: cfg, cfgPath: cfgPath, pm: pm, st: st, version: version}
}

// reloadConfig re-reads the config from disk to pick up external changes.
func (h *Handlers) reloadConfig() {
	if cfg, err := LoadConfig(h.cfgPath); err == nil {
		h.cfg = cfg
	}
}

func (h *Handlers) HandleStatus(w http.ResponseWriter, r *http.Request) {
	h.reloadConfig()
	mods := h.listModDirs()
	_, bepinexErr := os.Stat(h.cfg.BepInExDir())

	resp := agentapi.StatusResponse{
		Running:       h.pm.IsRunning(),
		ModCount:      len(mods),
		Mods:          mods,
		BepInEx:       bepinexErr == nil,
		Version:       h.version,
		Role:          RoleFromContext(r),
		ActiveProfile: h.cfg.ActiveProfileName(),
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
		// Mask status embed URL
		if h.cfg.DiscordWebhook.StatusEmbedURL != "" {
			embedURL := h.cfg.DiscordWebhook.StatusEmbedURL
			if len(embedURL) > 12 {
				resp.StatusEmbedURL = "…" + embedURL[len(embedURL)-8:]
			} else {
				resp.StatusEmbedURL = "***"
			}
		}
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
	h.reloadConfig()
	resp := agentapi.WebhookConfigResponse{}
	if h.cfg.DiscordWebhook != nil {
		resp.URL = h.cfg.DiscordWebhook.URL
		resp.ServerStarted = h.cfg.DiscordWebhook.ServerStarted
		resp.ServerStopped = h.cfg.DiscordWebhook.ServerStopped
		resp.WorldSaved = h.cfg.DiscordWebhook.WorldSaved
		resp.PlayerJoined = h.cfg.DiscordWebhook.PlayerJoined
		resp.PlayerLeft = h.cfg.DiscordWebhook.PlayerLeft
		resp.PlayerDied = h.cfg.DiscordWebhook.PlayerDied
		resp.PlayerFirstJoin = h.cfg.DiscordWebhook.PlayerFirstJoin
		resp.ServerRestarted = h.cfg.DiscordWebhook.ServerRestarted
		resp.ServerReady = h.cfg.DiscordWebhook.ServerReady
		resp.PlayerShout = h.cfg.DiscordWebhook.PlayerShout
		resp.EventStart = h.cfg.DiscordWebhook.EventStart
		resp.EventStop = h.cfg.DiscordWebhook.EventStop
		resp.NewDay = h.cfg.DiscordWebhook.NewDay
		resp.CronJob = h.cfg.DiscordWebhook.CronJob
		resp.StatusEmbedURL = h.cfg.DiscordWebhook.StatusEmbedURL
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handlers) HandleWebhookUpdate(w http.ResponseWriter, r *http.Request) {
	h.reloadConfig()

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
	if req.PlayerJoined != nil {
		h.cfg.DiscordWebhook.PlayerJoined = *req.PlayerJoined
	}
	if req.PlayerLeft != nil {
		h.cfg.DiscordWebhook.PlayerLeft = *req.PlayerLeft
	}
	if req.PlayerDied != nil {
		h.cfg.DiscordWebhook.PlayerDied = *req.PlayerDied
	}
	if req.PlayerFirstJoin != nil {
		h.cfg.DiscordWebhook.PlayerFirstJoin = *req.PlayerFirstJoin
	}
	if req.ServerRestarted != nil {
		h.cfg.DiscordWebhook.ServerRestarted = *req.ServerRestarted
	}
	if req.ServerReady != nil {
		h.cfg.DiscordWebhook.ServerReady = *req.ServerReady
	}
	if req.PlayerShout != nil {
		h.cfg.DiscordWebhook.PlayerShout = *req.PlayerShout
	}
	if req.EventStart != nil {
		h.cfg.DiscordWebhook.EventStart = *req.EventStart
	}
	if req.EventStop != nil {
		h.cfg.DiscordWebhook.EventStop = *req.EventStop
	}
	if req.NewDay != nil {
		h.cfg.DiscordWebhook.NewDay = *req.NewDay
	}
	if req.CronJob != nil {
		h.cfg.DiscordWebhook.CronJob = *req.CronJob
	}
	if req.StatusEmbedURL != nil {
		// If URL changed, clear the old message ID so a new embed is created
		if *req.StatusEmbedURL != h.cfg.DiscordWebhook.StatusEmbedURL {
			h.cfg.DiscordWebhook.StatusEmbedMessageID = ""
		}
		h.cfg.DiscordWebhook.StatusEmbedURL = *req.StatusEmbedURL
	}

	// Persist to disk
	if err := SaveConfig(h.cfgPath, h.cfg); err != nil {
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

func (h *Handlers) HandleNetwork(w http.ResponseWriter, r *http.Request) {
	result, _ := QueryModNetwork(h.cfg.ResolvedModAPIPort())
	if result == nil {
		writeJSON(w, http.StatusOK, agentapi.NetworkDiagnosticsResponse{
			Peers: []agentapi.PeerNetStats{},
		})
		return
	}
	writeJSON(w, http.StatusOK, result)
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
	h.lastAPIPlugins = nil // clear cached plugin data
	if h.st != nil {
		h.st.NotifyStopped()
	}
	writeJSON(w, http.StatusOK, agentapi.ActionResponse{OK: true, Message: "server stopped"})
}

func (h *Handlers) HandleRestart(w http.ResponseWriter, r *http.Request) {
	if h.pm.IsRunning() {
		if err := h.pm.Stop(); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to stop: "+err.Error())
			return
		}
		if h.st != nil {
			h.st.NotifyRestarted()
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
		if e.IsDir() {
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
		} else {
			name := e.Name()
			lower := strings.ToLower(name)
			if strings.HasSuffix(lower, ".dll") {
				modName := strings.TrimSuffix(name, filepath.Ext(name))
				modMap[modName] = &agentapi.ModInfo{Name: modName}
			} else if strings.HasSuffix(lower, ".dll.old") {
				modName := strings.TrimSuffix(strings.TrimSuffix(name, ".old"), filepath.Ext(strings.TrimSuffix(name, ".old")))
				modMap[modName] = &agentapi.ModInfo{Name: modName, Disabled: true}
			}
		}
	}

	// Layer 2: Manifest enrichment
	var manifestTime string
	var serverManifest *agentapi.PushManifest
	manifestNames := make(map[string]string) // dirName -> modName (for log matching)
	manifestPath := h.cfg.ActiveManifestPath()
	if data, err := os.ReadFile(manifestPath); err == nil {
		var manifest agentapi.PushManifest
		if json.Unmarshal(data, &manifest) == nil {
			serverManifest = &manifest
			manifestTime = manifest.PushedAt
			// Pass 1: exact DirName match
			for _, mm := range manifest.Mods {
				manifestNames[mm.DirName] = mm.Name
				if info, ok := modMap[mm.DirName]; ok {
					info.Name = mm.DirName // Canonical Thunderstore name
					info.Version = mm.Version
					info.Owner = mm.Owner
					info.Target = mm.Target
				}
			}
			// Pass 2: fallback — match unmatched filesystem entries by normalized name.
			// Collect renames first, then apply, to avoid mutating modMap during iteration.
			type fsRename struct {
				fsName string
				mm     agentapi.ManifestMod
			}
			var renames []fsRename
			for fsName, info := range modMap {
				if info.Owner != "" {
					continue // already matched in pass 1
				}
				normFS := normalize(fsName)
				for _, mm := range manifest.Mods {
					if _, alreadyInMap := modMap[mm.DirName]; alreadyInMap && mm.DirName != fsName {
						continue // this manifest mod already matched a different fs entry
					}
					normName := normalize(mm.Name)
					normSuffix := ""
					if idx := strings.Index(mm.DirName, "-"); idx >= 0 {
						normSuffix = normalize(mm.DirName[idx+1:])
					}
					if normFS == normName || (normSuffix != "" && normFS == normSuffix) {
						renames = append(renames, fsRename{fsName, mm})
						break
					}
				}
			}
			for _, r := range renames {
				info := modMap[r.fsName]
				info.Name = r.mm.DirName
				info.Version = r.mm.Version
				info.Owner = r.mm.Owner
				info.Target = r.mm.Target
				manifestNames[r.mm.DirName] = r.mm.Name
				modMap[r.mm.DirName] = info
				delete(modMap, r.fsName)
			}
		}
	}

	// Layer 3: Plugin load enrichment (mod API preferred, log parsing fallback)
	logParsed := false
	apiQueried := false

	apiPlugins, _ := QueryModAPI(h.cfg.ResolvedModAPIPort())
	if apiPlugins != nil {
		h.lastAPIPlugins = apiPlugins // cache for fallback
	} else if h.lastAPIPlugins != nil {
		apiPlugins = h.lastAPIPlugins // use cached result
	}
	var apiMatched map[string]ModMatch
	if apiPlugins != nil {
		apiQueried = true
		matched, unmatched := MatchAPIToMods(apiPlugins, modMap, manifestNames)
		apiMatched = matched

		// Update matched mods with loaded status, runtime version, and GUID
		for dirName, m := range matched {
			if info, ok := modMap[dirName]; ok {
				info.RuntimeVersion = m.Plugin.Version
				if info.Version == "" {
					info.Version = m.Plugin.Version // fallback for mods not in manifest
				}
				info.GUID = m.Plugin.GUID
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
				Name:       ap.Name,
				Version:    ap.Version,
				GUID:       ap.GUID,
				Loaded:     &t,
				PluginOnly: true,
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
					info.RuntimeVersion = lp.Version
					if info.Version == "" {
						info.Version = lp.Version // fallback for mods not in manifest
					}
					t := true
					info.Loaded = &t
				}
			}
		}
	}

	// GUID dedup: if two modMap entries share a GUID, keep the manifest-matched
	// one (has Owner) and drop the filesystem-only duplicate.
	guidOwner := make(map[string]string) // GUID → modMap key of the manifest-matched entry
	for key, info := range modMap {
		if info.GUID != "" && info.Owner != "" {
			guidOwner[info.GUID] = key
		}
	}
	for key, info := range modMap {
		if info.GUID != "" && info.Owner == "" {
			if canonical, ok := guidOwner[info.GUID]; ok && canonical != key {
				delete(modMap, key)
			}
		}
	}

	// Layer 4: Enforcer Mods.yaml is the exclusive source for moderation
	enforcerAC := readEnforcerClassifications(h.cfg.BepInExDir())
	if enforcerAC != nil {
		// Source 1: API-matched mods (have GUIDs from running plugins)
		if apiMatched != nil {
			for dirName, m := range apiMatched {
				if ac, ok := enforcerAC[m.Plugin.GUID]; ok {
					if info, ok := modMap[dirName]; ok {
						info.Anticheat = ac
					}
				}
			}
		}
		// Source 2: Manifest mods with persisted GUIDs (covers client-only mods)
		if serverManifest != nil {
			for _, mm := range serverManifest.Mods {
				if mm.GUID != "" {
					if ac, ok := enforcerAC[mm.GUID]; ok {
						if info, ok := modMap[mm.DirName]; ok && info.Anticheat == "" {
							info.Anticheat = ac
						}
					}
				}
			}
		}
		// Overlay enforcer classifications onto returned manifest so
		// the TUI's client-only fallback also reflects enforcer data.
		// Mods without GUIDs or not in the enforcer show no classification.
		if serverManifest != nil {
			for i := range serverManifest.Mods {
				mm := &serverManifest.Mods[i]
				if mm.GUID != "" {
					if ac, ok := enforcerAC[mm.GUID]; ok {
						mm.Anticheat = ac
					} else {
						mm.Anticheat = ""
					}
				} else {
					mm.Anticheat = "" // no GUID = can't be in enforcer
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
		Manifest:     serverManifest,
		ManifestTime: manifestTime,
		LogParsed:    logParsed,
		APIQueried:   apiQueried,
	})
}

func (h *Handlers) HandleModsModeration(w http.ResponseWriter, r *http.Request) {
	var req agentapi.ModerationUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Update the push manifest (add entry if not present, e.g. client-only mods)
	manifestPath := h.cfg.ActiveManifestPath()
	if data, err := os.ReadFile(manifestPath); err == nil {
		var manifest agentapi.PushManifest
		if json.Unmarshal(data, &manifest) == nil {
			found := false
			for i := range manifest.Mods {
				if manifest.Mods[i].DirName == req.ModName || manifest.Mods[i].Name == req.ModName {
					manifest.Mods[i].Anticheat = req.Anticheat
					found = true
					break
				}
			}
			if !found {
				// Add new entry for client-only mods not in manifest
				owner, name := "", req.ModName
				if idx := strings.Index(req.ModName, "-"); idx >= 0 {
					owner, name = req.ModName[:idx], req.ModName[idx+1:]
				}
				manifest.Mods = append(manifest.Mods, agentapi.ManifestMod{
					DirName:   req.ModName,
					Owner:     owner,
					Name:      name,
					Version:   req.Version,
					Target:    "client",
					Anticheat: req.Anticheat,
					GUID:      req.GUID,
				})
			}
			if updated, err := json.MarshalIndent(manifest, "", "  "); err == nil {
				os.WriteFile(manifestPath, updated, 0644)
			}
		}
	}

	// Update anticheat config (ValheimEnforcer Mods.yaml or AzuAntiCheat)
	hasAzu, hasEnforcer := detectAnticheatSystems(h.cfg.BepInExDir(), nil)
	if hasEnforcer {
		resolvedGUID, err := patchEnforcerModeration(h.cfg.BepInExDir(), req.ModName, req.Anticheat, req.GUID, req.Version, h.cfg.ResolvedModAPIPort())
		if err != nil {
			log.Printf("Moderation: enforcer patch failed: %v", err)
			writeJSON(w, http.StatusOK, agentapi.ActionResponse{
				OK:      true,
				Message: "manifest updated, but enforcer Mods.yaml failed: " + err.Error(),
			})
			return
		}
		// Persist the resolved GUID back to the manifest so future lookups work
		if resolvedGUID != "" {
			if data, err := os.ReadFile(manifestPath); err == nil {
				var manifest agentapi.PushManifest
				if json.Unmarshal(data, &manifest) == nil {
					for i := range manifest.Mods {
						if manifest.Mods[i].DirName == req.ModName || manifest.Mods[i].Name == req.ModName {
							if manifest.Mods[i].GUID != resolvedGUID {
								manifest.Mods[i].GUID = resolvedGUID
								if updated, err := json.MarshalIndent(manifest, "", "  "); err == nil {
									os.WriteFile(manifestPath, updated, 0644)
								}
							}
							break
						}
					}
				}
			}
		}
	}
	_ = hasAzu // TODO: AzuAntiCheat patching

	log.Printf("Moderation: %s → %s", req.ModName, req.Anticheat)
	writeJSON(w, http.StatusOK, agentapi.ActionResponse{OK: true, Message: "moderation updated"})
}

func (h *Handlers) HandleModsManage(w http.ResponseWriter, r *http.Request) {
	var req agentapi.ModManageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Auto-populate dir_name from owner + name if missing
	if req.Mod.DirName == "" && req.Mod.Owner != "" && req.Mod.Name != "" {
		req.Mod.DirName = req.Mod.Owner + "-" + req.Mod.Name
	}

	bepDir := h.cfg.BepInExDir()
	manifestPath := h.cfg.ActiveManifestPath()

	var manifest agentapi.PushManifest
	if data, err := os.ReadFile(manifestPath); err == nil {
		json.Unmarshal(data, &manifest)
	}

	switch req.Action {
	case "add", "update":
		cacheDir := agentCacheDir()
		zipPath, _, err := downloadModZip(cacheDir, req.Mod.Owner, req.Mod.Name, req.Mod.Version)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "download failed: "+err.Error())
			return
		}
		removeModDirs(bepDir, req.Mod.DirName)
		if err := extractModZip(zipPath, bepDir, req.Mod.Owner, req.Mod.Name); err != nil {
			writeError(w, http.StatusInternalServerError, "extract failed: "+err.Error())
			return
		}

		// Update manifest
		found := false
		for i := range manifest.Mods {
			if manifest.Mods[i].DirName == req.Mod.DirName {
				if req.Mod.Anticheat == "" {
					req.Mod.Anticheat = manifest.Mods[i].Anticheat
				}
				manifest.Mods[i] = req.Mod
				found = true
				break
			}
		}
		if !found {
			manifest.Mods = append(manifest.Mods, req.Mod)
		}

	case "remove":
		removeModDirs(bepDir, req.Mod.DirName)
		for i := range manifest.Mods {
			if manifest.Mods[i].DirName == req.Mod.DirName {
				manifest.Mods = append(manifest.Mods[:i], manifest.Mods[i+1:]...)
				break
			}
		}

	default:
		writeError(w, http.StatusBadRequest, "unknown action: "+req.Action)
		return
	}

	if data, err := json.MarshalIndent(manifest, "", "  "); err == nil {
		os.WriteFile(manifestPath, data, 0644)
	}

	log.Printf("ModManage: %s %s", req.Action, req.Mod.DirName)
	writeJSON(w, http.StatusOK, agentapi.ActionResponse{OK: true, Message: fmt.Sprintf("%s %s", req.Action, req.Mod.DirName)})
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

	// Write permission files if updated
	if req.Admins != nil {
		saveDir := h.resolveSaveDir()
		if err := writePermissionFile(filepath.Join(saveDir, "adminlist.txt"), settings.Admins); err != nil {
			log.Printf("Warning: failed to write adminlist.txt: %v", err)
		}
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
		} else if strings.HasSuffix(strings.ToLower(e.Name()), ".dll") {
			names = append(names, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
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

func (h *Handlers) HandleWorldDelete(w http.ResponseWriter, r *http.Request) {
	var req agentapi.WorldDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	// Validate name to prevent path traversal
	if strings.Contains(req.Name, "/") || strings.Contains(req.Name, "\\") || req.Name == "." || req.Name == ".." {
		writeError(w, http.StatusBadRequest, "invalid world name")
		return
	}

	// Prevent deleting the active world
	if lc, err := h.loadLaunchConfig(h.activeLaunchConfigName()); err == nil && lc.Settings.World == req.Name {
		writeError(w, http.StatusConflict, "cannot delete the active world — switch to a different world first")
		return
	}

	saveDir := h.resolveSaveDir()
	worldsDir := filepath.Join(saveDir, "worlds_local")

	dbPath := filepath.Join(worldsDir, req.Name+".db")
	fwlPath := filepath.Join(worldsDir, req.Name+".fwl")
	dbOldPath := filepath.Join(worldsDir, req.Name+".db.old")
	fwlOldPath := filepath.Join(worldsDir, req.Name+".fwl.old")

	removed := 0
	for _, p := range []string{dbPath, fwlPath, dbOldPath, fwlOldPath} {
		if err := os.Remove(p); err == nil {
			removed++
		}
	}

	if removed == 0 {
		writeError(w, http.StatusNotFound, "world not found: "+req.Name)
		return
	}

	writeJSON(w, http.StatusOK, agentapi.WorldDeleteResponse{
		OK:      true,
		Message: fmt.Sprintf("deleted %d file(s) for world %q", removed, req.Name),
	})
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

// --- Profile management ---

var validProfileName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// ensureProfiles migrates an existing server to the profile directory layout.
// Idempotent: returns immediately if mmcli-profiles/ already exists.
func (h *Handlers) ensureProfiles() error {
	profilesDir := h.cfg.ProfilesDir()
	if _, err := os.Stat(profilesDir); err == nil {
		return nil // already migrated
	}

	profileName := "default"

	// Create profile subdirectories
	for _, dir := range h.cfg.ProfileSubdirs(profileName) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create profile dir: %w", err)
		}
	}

	// Migrate existing BepInEx subdirectories into the default profile
	bepDir := h.cfg.BepInExDir()
	migrations := []struct {
		src string
		dst string
	}{
		{filepath.Join(bepDir, "plugins"), h.cfg.ProfilePluginsDir(profileName)},
		{filepath.Join(bepDir, "patchers"), h.cfg.ProfilePatchersDir(profileName)},
		{filepath.Join(bepDir, "monomod"), h.cfg.ProfileMonomodDir(profileName)},
	}

	for _, m := range migrations {
		info, err := os.Lstat(m.src)
		if err != nil {
			continue // directory doesn't exist, skip
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue // already a symlink, skip
		}
		if !info.IsDir() {
			continue
		}
		// Move contents into profile dir
		entries, err := os.ReadDir(m.src)
		if err != nil {
			continue
		}
		for _, e := range entries {
			srcPath := filepath.Join(m.src, e.Name())
			dstPath := filepath.Join(m.dst, e.Name())
			if _, err := os.Stat(dstPath); err == nil {
				continue // already exists in destination
			}
			os.Rename(srcPath, dstPath)
		}
		os.RemoveAll(m.src)
	}

	// Move manifest into profile
	oldManifest := filepath.Join(bepDir, agentapi.ManifestFileName)
	newManifest := h.cfg.ProfileManifestPath(profileName)
	if _, err := os.Stat(oldManifest); err == nil {
		os.Rename(oldManifest, newManifest)
	}

	// Create symlinks
	if err := h.activateProfileSymlinks(profileName); err != nil {
		return fmt.Errorf("failed to activate profile symlinks: %w", err)
	}

	// Save config
	h.cfg.ActiveProfile = profileName
	SaveConfig(DefaultConfigPath(), h.cfg)

	log.Printf("Migrated to profile system (active: %s)", profileName)
	return nil
}

// activateProfileSymlinks creates symlinks from BepInEx subdirs to the given profile's dirs.
func (h *Handlers) activateProfileSymlinks(name string) error {
	symlinks := []struct {
		link   string
		target string
	}{
		{h.cfg.PluginsDir(), h.cfg.ProfilePluginsDir(name)},
		{h.cfg.PatchersDir(), h.cfg.ProfilePatchersDir(name)},
		{h.cfg.MonomodDir(), h.cfg.ProfileMonomodDir(name)},
	}

	for _, s := range symlinks {
		// Ensure target exists
		os.MkdirAll(s.target, 0755)

		// Remove existing symlink or directory
		info, err := os.Lstat(s.link)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				os.Remove(s.link)
			} else if info.IsDir() {
				os.RemoveAll(s.link)
			}
		}

		// Ensure parent exists
		os.MkdirAll(filepath.Dir(s.link), 0755)

		if err := os.Symlink(s.target, s.link); err != nil {
			return fmt.Errorf("failed to symlink %s -> %s: %w", s.link, s.target, err)
		}
	}
	return nil
}

func (h *Handlers) HandleProfilesList(w http.ResponseWriter, r *http.Request) {
	if err := h.ensureProfiles(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	dir := h.cfg.ProfilesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	active := h.cfg.ActiveProfileName()

	var profiles []agentapi.ProfileSummary
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Count mod directories in plugins/
		pluginsDir := h.cfg.ProfilePluginsDir(e.Name())
		modCount := 0
		if pluginEntries, err := os.ReadDir(pluginsDir); err == nil {
			for _, pe := range pluginEntries {
				if pe.IsDir() || strings.HasSuffix(strings.ToLower(pe.Name()), ".dll") {
					modCount++
				}
			}
		}
		profiles = append(profiles, agentapi.ProfileSummary{
			Name:     e.Name(),
			ModCount: modCount,
		})
	}

	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	writeJSON(w, http.StatusOK, agentapi.ProfileListResponse{Profiles: profiles, Active: active})
}

func (h *Handlers) HandleProfileCreate(w http.ResponseWriter, r *http.Request) {
	if err := h.ensureProfiles(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var req agentapi.ProfileCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !validProfileName.MatchString(req.Name) {
		writeError(w, http.StatusBadRequest, "invalid profile name (use alphanumeric, hyphens, underscores)")
		return
	}

	profileDir := h.cfg.ProfileDir(req.Name)
	if _, err := os.Stat(profileDir); err == nil {
		writeError(w, http.StatusConflict, "profile already exists: "+req.Name)
		return
	}

	if req.CopyFrom != "" {
		srcDir := h.cfg.ProfileDir(req.CopyFrom)
		if _, err := os.Stat(srcDir); os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "source profile not found: "+req.CopyFrom)
			return
		}
		if err := copyDir(srcDir, profileDir); err != nil {
			os.RemoveAll(profileDir) // clean up on failure
			writeError(w, http.StatusInternalServerError, "failed to copy profile: "+err.Error())
			return
		}
	} else {
		for _, dir := range h.cfg.ProfileSubdirs(req.Name) {
			if err := os.MkdirAll(dir, 0755); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to create profile: "+err.Error())
				return
			}
		}
	}

	log.Printf("Profile created: %s", req.Name)
	writeJSON(w, http.StatusOK, agentapi.ActionResponse{OK: true, Message: "profile created"})
}

func (h *Handlers) HandleProfileDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.ensureProfiles(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	name := strings.TrimPrefix(r.URL.Path, agentapi.PathProfiles+"/")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		writeError(w, http.StatusBadRequest, "invalid profile name")
		return
	}

	if name == h.cfg.ActiveProfileName() {
		writeError(w, http.StatusConflict, "cannot delete the active profile")
		return
	}

	profileDir := h.cfg.ProfileDir(name)
	if _, err := os.Stat(profileDir); os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "profile not found: "+name)
		return
	}

	if err := os.RemoveAll(profileDir); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete profile: "+err.Error())
		return
	}

	log.Printf("Profile deleted: %s", name)
	writeJSON(w, http.StatusOK, agentapi.ActionResponse{OK: true, Message: "profile deleted"})
}

func (h *Handlers) HandleProfileActivate(w http.ResponseWriter, r *http.Request) {
	if err := h.ensureProfiles(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var req agentapi.ProfileActivateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	if _, err := os.Stat(h.cfg.ProfileDir(req.Name)); os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "profile not found: "+req.Name)
		return
	}

	if req.Name == h.cfg.ActiveProfileName() {
		writeJSON(w, http.StatusOK, agentapi.ActionResponse{OK: true, Message: "profile already active"})
		return
	}

	if h.pm.IsRunning() && !req.Force {
		writeError(w, http.StatusConflict, "server is running — stop it first or set force=true")
		return
	}

	if err := h.activateProfileSymlinks(req.Name); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to switch profile: "+err.Error())
		return
	}

	h.cfg.ActiveProfile = req.Name
	SaveConfig(DefaultConfigPath(), h.cfg)

	log.Printf("Profile activated: %s", req.Name)
	writeJSON(w, http.StatusOK, agentapi.ActionResponse{OK: true, Message: "profile activated, restart server to apply"})
}

// copyDir recursively copies a directory tree.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, info.Mode())
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, agentapi.ErrorResponse{Error: msg})
}
