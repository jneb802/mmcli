package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"mmcli/internal/agentapi"
	"mmcli/internal/client"
	"mmcli/internal/config"
	"mmcli/internal/installer"
	"mmcli/internal/modpack"
	"mmcli/internal/platform"
	"mmcli/internal/profile"
	"mmcli/internal/thunderstore"
)

// Async message types for local tab.
type installDoneMsg struct {
	err        error
	updatedAll bool
}
type updateCheckDoneMsg struct{ updates map[string]string }
type gameStatusMsg struct{ running bool }
type gameStartMsg struct{ err error }
type localTickMsg struct{}
type localLogLineMsg struct{ lines []string }
type localLogDoneMsg struct{}

func nextLocalLogLine(ch <-chan []string) tea.Cmd {
	return waitForLogLines(ch,
		func(lines []string) tea.Msg { return localLogLineMsg{lines: lines} },
		func() tea.Msg { return localLogDoneMsg{} },
	)
}

func (m *model) stopLocalLogStream() {
	if m.local.logStop != nil {
		close(m.local.logStop)
		m.local.logStop = nil
		m.local.logCh = nil
		m.local.logs.active = false
	}
}

func (m *model) loadLocalLogs() tea.Cmd {
	m.stopLocalLogStream()
	logFile := m.paths.ProfileLogFile(m.cfg.ActiveProfile)
	lines, size, err := readLogFile(logFile)
	if err != nil {
		m.local.err = fmt.Errorf("no log file found")
		return nil
	}
	ch, stop := streamLocalLogs(logFile, size)
	m.local.logCh = ch
	m.local.logStop = stop
	m.local.logs = newLogViewerState("BepInEx Logs ("+m.cfg.ActiveProfile+")", lines, true)
	return nextLocalLogLine(ch)
}

type localModel struct {
	mods   []config.ModEntry
	cursor int
	err    error

	gameRunning bool

	pickProfile     bool
	profiles        []string
	profileCursor   int
	creatingProfile bool
	newProfileInput string

	installing   bool
	installInput string
	installBusy  bool

	logs    logViewerState
	logCh   <-chan []string
	logStop chan struct{}

	updates           map[string]string
	checkingUpdates   bool
	preflightFetching bool

	settingsCursor int
	editingPath    bool
	pathInput      string

	configFiles  []string
	configCursor int
}

func (m *model) refreshMods() {
	mods := m.reg.ListAllMods(m.cfg.ActiveProfile, m.paths.ProfilePluginsDir(m.cfg.ActiveProfile))

	sort.Slice(mods, func(i, j int) bool {
		rank := func(m config.ModEntry) int {
			if m.IsLocal {
				return 0
			}
			if !m.IsDependency {
				return 1
			}
			return 2
		}
		ri, rj := rank(mods[i]), rank(mods[j])
		if ri != rj {
			return ri < rj
		}
		return mods[i].FullName() < mods[j].FullName()
	})
	m.local.mods = mods
	if m.local.cursor >= len(m.local.mods) {
		m.local.cursor = max(0, len(m.local.mods)-1)
	}
}

type settingsItem struct {
	label    string
	value    string
	tooltip  string
	editable bool
}

func (m model) buildSettingsItems() []settingsItem {
	pref := m.profileSettings.AnticheatSystem
	if pref == "" {
		pref = "auto"
	}
	acValue := pref
	if pref == "auto" {
		acValue = fmt.Sprintf("%s \033[2m(resolved: %s)\033[0m", pref, m.anticheatSystem)
	}

	items := []settingsItem{
		{
			label:    "Anticheat",
			value:    acValue,
			tooltip:  "Which anticheat mod to configure on push. Auto detects from installed mods.",
			editable: true,
		},
		{
			label:    "Valheim Path",
			value:    m.cfg.ValheimPath,
			tooltip:  "Local Valheim installation directory. Used for BepInEx and mod file paths.",
			editable: true,
		},
		{
			label:   "Profile",
			value:   m.cfg.ActiveProfile,
			tooltip: "Active mod profile. Switch with 'p' on the Mods tab.",
		},
	}

	if m.profileSettings.Server != "" {
		items = append(items, settingsItem{
			label:   "Linked Server",
			value:   m.profileSettings.Server,
			tooltip: "Remote server managed by mmcli. Set with 'mmcli server add'.",
		})
		if srv, ok := m.cfg.Servers[m.profileSettings.Server]; ok {
			items = append(items, settingsItem{
				label:   "Server Host",
				value:   fmt.Sprintf("%s:%d", srv.Host, srv.Port),
				tooltip: "Address of the linked server's mmcli agent.",
			})
			role := srv.Role
			if role == "" {
				role = "admin"
			}
			items = append(items, settingsItem{
				label:   "Role",
				value:   role,
				tooltip: "Your permission level on the linked server. Admins can push, start, and stop.",
			})
		}
	} else {
		items = append(items, settingsItem{
			label:   "Linked Server",
			value:   "\033[2m–\033[0m",
			tooltip: "No server linked. Run 'mmcli server add' to connect one.",
		})
	}

	items = append(items, settingsItem{
		label:   "mmcli",
		value:   Version,
		tooltip: "Current mmcli version.",
	})

	return items
}

func (s settingsItem) Label() string {
	return s.label
}

// --- Async commands ---

func checkUpdates(mods []config.ModEntry) tea.Cmd {
	return func() tea.Msg {
		updates := make(map[string]string)
		for _, mod := range mods {
			if mod.IsLocal || mod.Owner == "" {
				continue
			}
			pkg, err := thunderstore.GetPackage(mod.Owner, mod.Name)
			if err != nil || len(pkg.Versions) == 0 {
				continue
			}
			latest := pkg.Versions[0].VersionNumber
			if latest != mod.Version {
				updates[mod.FullName()] = latest
			}
		}
		return updateCheckDoneMsg{updates: updates}
	}
}

func installMod(paths config.Paths, cfg config.Config, reg *config.Registry, query string, target string) tea.Cmd {
	return func() tea.Msg {
		old := os.Stdout
		if devnull, err := os.Open(os.DevNull); err == nil {
			os.Stdout = devnull
			defer devnull.Close()
			defer func() { os.Stdout = old }()
		}
		if installer.IsLocalPath(query) {
			return installDoneMsg{err: installer.InstallLocal(paths, cfg, reg, query, target)}
		}
		return installDoneMsg{err: installer.Install(paths, cfg, reg, query, target)}
	}
}

func installModToModpack(modpackPath, query string) tea.Cmd {
	return func() tea.Msg {
		pkg, err := thunderstore.FindPackageByQuery(query)
		if err != nil {
			return installDoneMsg{err: err}
		}
		if len(pkg.Versions) == 0 {
			return installDoneMsg{err: fmt.Errorf("package %s has no versions", pkg.FullName)}
		}
		depString := fmt.Sprintf("%s-%s-%s", pkg.Owner, pkg.Name, pkg.Versions[0].VersionNumber)
		return installDoneMsg{err: modpack.AddDep(modpackPath, depString)}
	}
}

func installModToServer(query string, c *client.AgentClient) tea.Cmd {
	return func() tea.Msg {
		if c == nil {
			return installDoneMsg{err: fmt.Errorf("no server connection")}
		}
		pkg, err := thunderstore.FindPackageByQuery(query)
		if err != nil {
			return installDoneMsg{err: err}
		}
		if len(pkg.Versions) == 0 {
			return installDoneMsg{err: fmt.Errorf("no versions found for %s-%s", pkg.Owner, pkg.Name)}
		}
		latest := pkg.Versions[0]
		req := agentapi.ModManageRequest{
			Action: "add",
			Mod: agentapi.ManifestMod{
				DirName: fmt.Sprintf("%s-%s", pkg.Owner, pkg.Name),
				Owner:   pkg.Owner,
				Name:    pkg.Name,
				Version: latest.VersionNumber,
				Source:  "thunderstore",
			},
		}
		if _, err := c.ManageMod(req); err != nil {
			return installDoneMsg{err: fmt.Errorf("server install failed: %w", err)}
		}
		return installDoneMsg{}
	}
}

func updateServerModerationFull(c *client.AgentClient, modName, anticheat, guid, version string) tea.Cmd {
	return func() tea.Msg {
		resp, err := c.UpdateModeration(agentapi.ModerationUpdateRequest{
			ModName:   modName,
			Anticheat: anticheat,
			GUID:      guid,
			Version:   version,
		})
		// Re-fetch server status so the moderation column updates immediately
		status, _ := c.Status()
		modsResp, _ := c.ListMods()
		var mods []agentapi.ModInfo
		if modsResp != nil {
			mods = modsResp.Mods
		}
		msg := serverStatusMsg{status: status, mods: mods, modsResp: modsResp}
		if err != nil {
			msg.err = fmt.Errorf("moderation update failed: %w", err)
		} else if resp != nil && strings.Contains(resp.Message, "failed") {
			msg.err = fmt.Errorf("%s", resp.Message)
		}
		return msg
	}
}

func removeModFromServer(c *client.AgentClient, modName string) tea.Cmd {
	return func() tea.Msg {
		req := agentapi.ModManageRequest{
			Action: "remove",
			Mod:    agentapi.ManifestMod{DirName: modName, Name: modName},
		}
		if _, err := c.ManageMod(req); err != nil {
			return installDoneMsg{err: fmt.Errorf("server remove failed: %w", err)}
		}
		return installDoneMsg{}
	}
}

func readLogFile(path string) ([]string, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	lines := strings.Split(string(data), "\n")
	return lines, int64(len(data)), nil
}

func streamLocalLogs(path string, lastSize int64) (<-chan []string, chan struct{}) {
	ch := make(chan []string, 16)
	stop := make(chan struct{})

	go func() {
		defer close(ch)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				info, err := os.Stat(path)
				if err != nil || info.Size() <= lastSize {
					continue
				}
				f, err := os.Open(path)
				if err != nil {
					continue
				}
				f.Seek(lastSize, 0)
				newData := make([]byte, info.Size()-lastSize)
				n, _ := f.Read(newData)
				f.Close()
				lastSize = info.Size()
				if n > 0 {
					lines := strings.Split(string(newData[:n]), "\n")
					// Remove empty trailing line from split
					if len(lines) > 0 && lines[len(lines)-1] == "" {
						lines = lines[:len(lines)-1]
					}
					if len(lines) > 0 {
						select {
						case ch <- lines:
						case <-stop:
							return
						}
					}
				}
			}
		}
	}()

	return ch, stop
}

func startGame(paths config.Paths, cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		logPath := paths.ProfileLogFile(cfg.ActiveProfile)
		os.Remove(logPath)

		if err := profile.Activate(paths, cfg.ActiveProfile); err != nil {
			return gameStartMsg{err: fmt.Errorf("failed to activate profile: %w", err)}
		}

		target := platform.GameLaunchTarget(paths.ValheimDir)
		if _, err := os.Stat(target); os.IsNotExist(err) {
			return gameStartMsg{err: fmt.Errorf("game launch target not found — run `mmcli init` first")}
		}

		cmd, _, lf, err := platform.StartGameProcess(paths.ValheimDir, target, logPath)
		if err != nil {
			return gameStartMsg{err: fmt.Errorf("failed to start game: %w", err)}
		}
		go func() {
			cmd.Wait()
			if lf != nil {
				lf.Close()
			}
		}()

		return gameStartMsg{}
	}
}

func checkGameRunning() tea.Cmd {
	return func() tea.Msg {
		return gameStatusMsg{running: platform.IsGameRunning()}
	}
}

func localTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg {
		return localTickMsg{}
	})
}

func updateAllMods(paths config.Paths, cfg config.Config, reg *config.Registry, updates map[string]string) tea.Cmd {
	return func() tea.Msg {
		old := os.Stdout
		if devnull, err := os.Open(os.DevNull); err == nil {
			os.Stdout = devnull
			defer devnull.Close()
			defer func() { os.Stdout = old }()
		}
		for name := range updates {
			if err := installer.Update(paths, cfg, reg, name); err != nil {
				return installDoneMsg{err: fmt.Errorf("failed to update %s: %w", name, err)}
			}
		}
		return installDoneMsg{updatedAll: true}
	}
}

func updateMod(paths config.Paths, cfg config.Config, reg *config.Registry, fullName string) tea.Cmd {
	return func() tea.Msg {
		old := os.Stdout
		if devnull, err := os.Open(os.DevNull); err == nil {
			os.Stdout = devnull
			defer devnull.Close()
			defer func() { os.Stdout = old }()
		}
		return installDoneMsg{err: installer.Update(paths, cfg, reg, fullName)}
	}
}

func updateModToServer(paths config.Paths, cfg config.Config, reg *config.Registry, fullName string, c *client.AgentClient) tea.Cmd {
	return func() tea.Msg {
		old := os.Stdout
		if devnull, err := os.Open(os.DevNull); err == nil {
			os.Stdout = devnull
			defer devnull.Close()
			defer func() { os.Stdout = old }()
		}
		if err := installer.Update(paths, cfg, reg, fullName); err != nil {
			return installDoneMsg{err: err}
		}
		if c != nil {
			config.SaveRegistry(paths, *reg)
			mod, ok := reg.GetMod(cfg.ActiveProfile, fullName)
			if ok {
				source := "thunderstore"
				if mod.Owner == "local" {
					source = "upload"
				}
				req := agentapi.ModManageRequest{
					Action: "update",
					Mod: agentapi.ManifestMod{
						DirName: mod.FullName(),
						Owner:   mod.Owner,
						Name:    mod.Name,
						Version: mod.Version,
						Target:  mod.ResolvedTarget(),
						Source:  source,
						GUID:    mod.GUID,
					},
				}
				if _, err := c.ManageMod(req); err != nil {
					return installDoneMsg{err: fmt.Errorf("server sync failed: %w", err)}
				}
			}
		}
		return installDoneMsg{}
	}
}

func updateModpackDep(modpackPath, fullName, newVersion string) tea.Cmd {
	return func() tea.Msg {
		if err := modpack.UpdateDep(modpackPath, fullName, newVersion); err != nil {
			return installDoneMsg{err: err}
		}
		return installDoneMsg{}
	}
}

// --- Helpers ---

func findConfigFile(paths config.Paths, profileName string, mod config.ModEntry) string {
	configDir := paths.ProfileConfigDir(profileName)
	entries, err := os.ReadDir(configDir)
	if err != nil {
		return configDir
	}

	nameLower := strings.ToLower(mod.Name)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".cfg") && strings.Contains(strings.ToLower(e.Name()), nameLower) {
			return filepath.Join(configDir, e.Name())
		}
	}

	return configDir
}

func openFile(path string) tea.Cmd {
	return func() tea.Msg {
		_ = platform.OpenPath(path)
		return nil
	}
}

// preflightCheck compares local mods against server mods and returns warnings.
func preflightCheck(localMods []config.ModEntry, serverMods []agentapi.ModInfo) []string {
	// Build server mod lookup by name
	type serverMod struct {
		version   string
		anticheat string
	}
	serverSet := make(map[string]serverMod)
	for _, m := range serverMods {
		serverSet[m.Name] = serverMod{version: m.Version, anticheat: m.Anticheat}
	}

	// Build local mod lookup by full name
	localSet := make(map[string]bool)
	for _, m := range localMods {
		if !m.Disabled {
			localSet[m.FullName()] = true
		}
	}

	var warnings []string

	// Check for missing whitelisted mods (server requires, player doesn't have)
	for _, sm := range serverMods {
		if sm.Anticheat == "whitelist" && !localSet[sm.Name] {
			warnings = append(warnings, fmt.Sprintf("\033[31mmissing\033[0m  %s (whitelisted — required)", sm.Name))
		}
	}

	// Check local mods against server
	for _, lm := range localMods {
		if lm.Disabled || lm.ResolvedTarget() == "client" {
			continue
		}
		sm, onServer := serverSet[lm.FullName()]
		if !onServer {
			warnings = append(warnings, fmt.Sprintf("\033[33mextra\033[0m    %s (not on server whitelist/greylist)", lm.FullName()))
		} else if sm.anticheat == "whitelist" && sm.version != "" && lm.Version != "" && sm.version != lm.Version {
			warnings = append(warnings, fmt.Sprintf("\033[33mversion\033[0m  %s (local %s, server %s)", lm.FullName(), lm.Version, sm.version))
		}
	}

	return warnings
}

// detectBepInExVersion checks the cache dir for a BepInExPack zip and extracts the version.
func detectBepInExVersion(paths config.Paths) string {
	entries, err := os.ReadDir(paths.CacheDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "BepInExPack_Valheim-") && strings.HasSuffix(name, ".zip") {
			ver := strings.TrimPrefix(name, "BepInExPack_Valheim-")
			ver = strings.TrimSuffix(ver, ".zip")
			return ver
		}
	}
	if _, err := os.Stat(paths.BepInExDir()); err == nil {
		return "installed"
	}
	if runtime.GOOS == "windows" {
		if _, err := os.Stat(filepath.Join(paths.ValheimDir, "winhttp.dll")); err == nil {
			return "installed"
		}
	}
	return ""
}
