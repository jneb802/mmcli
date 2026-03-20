package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"mmcli/internal/agentapi"
	"mmcli/internal/client"
	"mmcli/internal/config"
	"mmcli/internal/installer"
	"mmcli/internal/profile"
	"mmcli/internal/thunderstore"
)

// Async message types for local tab.
type installDoneMsg struct{ err error }
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
	logFile := m.paths.BepInExLogFile()
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
	mods    []config.ModEntry
	cursor  int
	err     error

	gameRunning bool

	confirmRemove bool

	pickProfile     bool
	profiles        []string
	profileCursor   int
	creatingProfile bool
	newProfileInput string

	installing  bool
	installInput string
	installBusy bool

	logs    logViewerState
	logCh   <-chan []string
	logStop chan struct{}

	updates          map[string]string
	checkingUpdates  bool
	confirmUpdateAll bool

	confirmStart      bool
	preflightWarnings []string
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
	label   string
	value   string
	tooltip string
	editable bool
}

func (m model) buildSettingsItems() []settingsItem {
	pref := m.cfg.AnticheatSystem
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

	if m.cfg.ActiveServer != "" {
		items = append(items, settingsItem{
			label:   "Linked Server",
			value:   m.cfg.ActiveServer,
			tooltip: "Remote server managed by mmcli. Set with 'mmcli server add'.",
		})
		if srv, ok := m.cfg.Servers[m.cfg.ActiveServer]; ok {
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

func installMod(paths config.Paths, cfg config.Config, reg *config.Registry, query string) tea.Cmd {
	return func() tea.Msg {
		old := os.Stdout
		if devnull, err := os.Open(os.DevNull); err == nil {
			os.Stdout = devnull
			defer devnull.Close()
			defer func() { os.Stdout = old }()
		}
		if installer.IsLocalPath(query) {
			return installDoneMsg{err: installer.InstallLocal(paths, cfg, reg, query, "both")}
		}
		return installDoneMsg{err: installer.Install(paths, cfg, reg, query, "both")}
	}
}

func installModToServer(paths config.Paths, cfg config.Config, reg *config.Registry, query string, c *client.AgentClient) tea.Cmd {
	return func() tea.Msg {
		old := os.Stdout
		if devnull, err := os.Open(os.DevNull); err == nil {
			os.Stdout = devnull
			defer devnull.Close()
			defer func() { os.Stdout = old }()
		}
		// Install locally (needed to build manifest and uploads for push)
		var err error
		if installer.IsLocalPath(query) {
			err = installer.InstallLocal(paths, cfg, reg, query, "server")
		} else {
			err = installer.Install(paths, cfg, reg, query, "server")
		}
		if err != nil {
			return installDoneMsg{err: err}
		}
		// Push to server
		if c != nil {
			config.SaveRegistry(paths, *reg)
			manifest := profile.BuildManifest(cfg.ActiveProfile, *reg)
			uploads, err := profile.BuildUploads(paths, cfg.ActiveProfile, manifest, *reg)
			if err != nil {
				return installDoneMsg{err: fmt.Errorf("push failed: %w", err)}
			}
			if _, err := c.SyncMods(manifest, uploads); err != nil {
				return installDoneMsg{err: fmt.Errorf("push failed: %w", err)}
			}
		}
		// Clean up local files — registry entry stays so future pushes
		// still include the mod in the manifest for the server.
		for _, mod := range reg.ListMods(cfg.ActiveProfile) {
			if mod.ResolvedTarget() != "server" {
				continue
			}
			name := mod.FullName()
			// Only clean up mods we just installed (match query loosely)
			if !strings.Contains(strings.ToLower(name), strings.ToLower(strings.ReplaceAll(query, " ", "_"))) {
				continue
			}
			for _, dir := range []string{
				filepath.Join(paths.ProfilePluginsDir(cfg.ActiveProfile), name),
				filepath.Join(paths.ProfilePatchersDir(cfg.ActiveProfile), name),
			} {
				os.RemoveAll(dir)
			}
		}
		return installDoneMsg{}
	}
}

func removeModFromServer(paths config.Paths, cfg config.Config, reg *config.Registry, c *client.AgentClient) tea.Cmd {
	return func() tea.Msg {
		manifest := profile.BuildManifest(cfg.ActiveProfile, *reg)
		uploads, err := profile.BuildUploads(paths, cfg.ActiveProfile, manifest, *reg)
		if err != nil {
			return installDoneMsg{err: fmt.Errorf("push failed: %w", err)}
		}
		if _, err := c.SyncMods(manifest, uploads); err != nil {
			return installDoneMsg{err: fmt.Errorf("push failed: %w", err)}
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
		os.Remove(paths.BepInExLogFile())

		if err := profile.ActivateSymlinks(paths, cfg.ActiveProfile); err != nil {
			return gameStartMsg{err: fmt.Errorf("failed to validate symlinks: %w", err)}
		}

		scriptPath := paths.RunBepInExScript()
		if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
			return gameStartMsg{err: fmt.Errorf("run_bepinex.sh not found — run `mmcli init` first")}
		}

		cmd := exec.Command("/bin/bash", scriptPath)
		cmd.Dir = paths.ValheimDir
		if err := cmd.Start(); err != nil {
			return gameStartMsg{err: fmt.Errorf("failed to start game: %w", err)}
		}

		return gameStartMsg{}
	}
}

func checkGameRunning() tea.Cmd {
	return func() tea.Msg {
		err := exec.Command("pgrep", "-x", "Valheim").Run()
		return gameStatusMsg{running: err == nil}
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
		return installDoneMsg{}
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
		exec.Command("open", path).Start()
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
	return ""
}
