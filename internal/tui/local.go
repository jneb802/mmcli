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

	updates         map[string]string
	checkingUpdates bool

	confirmStart      bool
	preflightWarnings []string
	preflightFetching bool
}

func (m *model) refreshMods() {
	mods := m.reg.ListMods(m.cfg.ActiveProfile)

	pluginsDir := m.paths.ProfilePluginsDir(m.cfg.ActiveProfile)
	registered := m.reg.Profiles[m.cfg.ActiveProfile]
	if registered == nil {
		registered = make(map[string]config.ModEntry)
	}
	locals := config.DetectLocalMods(pluginsDir, registered)
	mods = append(mods, locals...)

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

func (m model) handleInstallInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.local.installing = false
	case "enter":
		if m.local.installInput != "" {
			m.local.installBusy = true
			return m, installMod(m.paths, m.cfg, m.reg, m.local.installInput)
		}
	case "backspace":
		if len(m.local.installInput) > 0 {
			m.local.installInput = m.local.installInput[:len(m.local.installInput)-1]
		}
	case "ctrl+c":
		return m, tea.Quit
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			m.local.installInput += string(msg.Runes)
		}
	}
	return m, nil
}

func (m model) handleProfilePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.local.creatingProfile {
		switch msg.String() {
		case "esc":
			m.local.creatingProfile = false
		case "enter":
			if m.local.newProfileInput != "" {
				if err := profile.Create(m.paths, m.local.newProfileInput); err != nil {
					m.local.err = err
				} else {
					m.reg.EnsureProfile(m.local.newProfileInput)
					config.SaveRegistry(m.paths, *m.reg)
					if err := profile.Switch(m.paths, &m.cfg, m.local.newProfileInput); err != nil {
						m.local.err = err
					} else {
						config.Save(m.paths, m.cfg)
						m.local.cursor = 0
						m.refreshMods()
						m.local.updates = make(map[string]string)
						m.local.err = nil
						m.local.pickProfile = false
						m.local.creatingProfile = false
						return m, nil
					}
				}
				m.local.creatingProfile = false
			}
		case "backspace":
			if len(m.local.newProfileInput) > 0 {
				m.local.newProfileInput = m.local.newProfileInput[:len(m.local.newProfileInput)-1]
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				m.local.newProfileInput += string(msg.Runes)
			}
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "esc":
		m.local.pickProfile = false
	case "up", "k":
		if m.local.profileCursor > 0 {
			m.local.profileCursor--
		}
	case "down", "j":
		if m.local.profileCursor < len(m.local.profiles)-1 {
			m.local.profileCursor++
		}
	case "enter":
		name := m.local.profiles[m.local.profileCursor]
		if name != m.cfg.ActiveProfile {
			if err := profile.Switch(m.paths, &m.cfg, name); err != nil {
				m.local.err = err
			} else {
				config.Save(m.paths, m.cfg)
				m.local.cursor = 0
				m.refreshMods()
				m.local.updates = make(map[string]string)
				m.local.checkingUpdates = true
				m.local.err = nil
				m.local.pickProfile = false
				return m, checkUpdates(m.local.mods)
			}
		}
		m.local.pickProfile = false
	case "n":
		m.local.creatingProfile = true
		m.local.newProfileInput = ""
		m.local.err = nil
	}
	return m, nil
}

func (m model) handleConfirmRemove(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		m.local.confirmRemove = false
		mod := m.local.mods[m.local.cursor]
		var err error
		if mod.IsLocal {
			pluginsDir := m.paths.ProfilePluginsDir(m.cfg.ActiveProfile)
			err = installer.RemoveLocalMod(pluginsDir, mod)
		} else {
			err = installer.Remove(m.paths, m.cfg, m.reg, mod.FullName())
		}
		if err != nil {
			m.local.err = err
		} else {
			m.local.err = nil
			config.SaveRegistry(m.paths, *m.reg)
			m.refreshMods()
		}
	case "ctrl+c":
		return m, tea.Quit
	default:
		m.local.confirmRemove = false
	}
	return m, nil
}

func (m model) handleLocalNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "tab":
		m.stopLocalLogStream()
		m.activeTab = tabServer
		if m.server.client != nil && m.server.status == nil {
			m.server.fetching = true
			return m, tea.Batch(fetchServerStatus(m.server.client), serverTick())
		}
		if m.server.client != nil {
			return m, serverTick()
		}
		return m, nil
	case "up", "k":
		if m.local.cursor > 0 {
			m.local.cursor--
		}
	case "down", "j":
		if m.local.cursor < len(m.local.mods)-1 {
			m.local.cursor++
		}
	case " ":
		if len(m.local.mods) > 0 {
			mod := m.local.mods[m.local.cursor]
			if mod.IsLocal {
				pluginsDir := m.paths.ProfilePluginsDir(m.cfg.ActiveProfile)
				if err := installer.ToggleLocalMod(pluginsDir, mod); err != nil {
					m.local.err = err
				} else {
					m.local.mods[m.local.cursor].Disabled = !m.local.mods[m.local.cursor].Disabled
					m.local.err = nil
				}
			} else if err := installer.Toggle(m.paths, m.cfg, m.reg, mod.FullName()); err != nil {
				m.local.err = err
			} else {
				m.local.mods[m.local.cursor].Disabled = !m.local.mods[m.local.cursor].Disabled
				m.local.err = nil
				config.SaveRegistry(m.paths, *m.reg)
			}
		}
	case "x":
		if len(m.local.mods) > 0 {
			m.local.confirmRemove = true
			m.local.err = nil
		}
	case "c":
		if len(m.local.mods) > 0 {
			mod := m.local.mods[m.local.cursor]
			path := findConfigFile(m.paths, m.cfg.ActiveProfile, mod)
			return m, openFile(path)
		}
	case "p":
		profiles, err := profile.List(m.paths)
		if err != nil {
			m.local.err = err
		} else {
			m.local.profiles = profiles
			m.local.profileCursor = 0
			for i, name := range profiles {
				if name == m.cfg.ActiveProfile {
					m.local.profileCursor = i
					break
				}
			}
			m.local.pickProfile = true
			m.local.err = nil
		}
	case "i":
		m.local.installing = true
		m.local.installInput = ""
		m.local.err = nil
	case "u":
		if len(m.local.mods) > 0 {
			mod := m.local.mods[m.local.cursor]
			if _, ok := m.local.updates[mod.FullName()]; ok {
				m.local.installBusy = true
				m.local.err = nil
				return m, updateMod(m.paths, m.cfg, m.reg, mod.FullName())
			}
		}
	case "s":
		if !m.local.gameRunning {
			// Preflight check if server is linked
			if m.server.client != nil {
				if len(m.server.mods) == 0 {
					// Need to fetch server mods first
					m.local.preflightFetching = true
					return m, fetchServerStatus(m.server.client)
				}
				warnings := preflightCheck(m.local.mods, m.server.mods)
				if len(warnings) > 0 {
					m.local.preflightWarnings = warnings
					m.local.confirmStart = true
					return m, nil
				}
			}
			return m, startGame(m.paths, m.cfg)
		}
	case "l":
		m.stopLocalLogStream()
		logFile := m.paths.BepInExLogFile()
		lines, size, err := readLogTail(logFile, 200)
		if err != nil {
			m.local.err = fmt.Errorf("no log file found")
		} else {
			ch, stop := streamLocalLogs(logFile, size)
			m.local.logCh = ch
			m.local.logStop = stop
			m.local.logs = newLogViewerState("BepInEx Logs ("+m.cfg.ActiveProfile+")", lines, true)
			return m, nextLocalLogLine(ch)
		}
	}
	return m, nil
}

func (m model) viewLocal() string {
	var b strings.Builder

	// Preflight fetching
	if m.local.preflightFetching {
		b.WriteString("\n  \033[33mChecking server mods...\033[0m\n\n")
		return b.String()
	}

	// Preflight confirmation
	if m.local.confirmStart {
		b.WriteString("\n  \033[33mMod mismatch with server:\033[0m\n\n")
		for _, w := range m.local.preflightWarnings {
			fmt.Fprintf(&b, "    %s\n", w)
		}
		b.WriteString("\n  \033[33mStart anyway? (y/n)\033[0m\n\n")
		return b.String()
	}

	// Log viewer
	if m.local.logs.active {
		renderLogViewer(&b, m.local.logs)
		return b.String()
	}

	// Profile picker
	if m.local.pickProfile {
		if m.local.creatingProfile {
			b.WriteString("\n  New profile name:\n\n")
			fmt.Fprintf(&b, "  > %s\033[7m \033[0m\n", m.local.newProfileInput)
			b.WriteString("\n  \033[2menter create • esc cancel\033[0m\n\n")
			return b.String()
		}
		b.WriteString("\n  Switch profile:\n\n")
		for i, name := range m.local.profiles {
			cursor := "  "
			if i == m.local.profileCursor {
				cursor = "\033[36m>\033[0m "
			}
			active := ""
			if name == m.cfg.ActiveProfile {
				active = " \033[32m(active)\033[0m"
			}
			fmt.Fprintf(&b, "  %s%s%s\n", cursor, name, active)
		}
		b.WriteString("\n  \033[2m↑/↓ navigate • enter select • n new profile • esc back\033[0m\n\n")
		return b.String()
	}

	// Install input mode
	if m.local.installing && !m.local.installBusy {
		b.WriteString("\n  Install mod (Owner-Name, URL, or local path):\n\n")
		fmt.Fprintf(&b, "  > %s\033[7m \033[0m\n", m.local.installInput)
		b.WriteString("\n  \033[2menter install • esc cancel\033[0m\n\n")
		return b.String()
	}

	// Installing busy
	if m.local.installBusy {
		query := m.local.installInput
		if query == "" {
			query = "mod"
		}
		fmt.Fprintf(&b, "\n  \033[33mInstalling %s...\033[0m\n\n", query)
		return b.String()
	}

	// Remove confirmation
	if m.local.confirmRemove {
		mod := m.local.mods[m.local.cursor]
		fmt.Fprintf(&b, "\n  \033[33mRemove %s? (y/n)\033[0m\n\n", mod.FullName())
		return b.String()
	}

	// Header
	gameStatus := "\033[2mstopped\033[0m"
	if m.local.gameRunning {
		gameStatus = "\033[32mrunning\033[0m"
	}
	modCount := len(m.local.mods)
	updateCount := len(m.local.updates)
	if m.local.checkingUpdates {
		fmt.Fprintf(&b, "\n  Profile: \033[36m%s\033[0m    Game: %s    Mods: %d    \033[2mchecking for updates...\033[0m\n\n", m.cfg.ActiveProfile, gameStatus, modCount)
	} else if updateCount > 0 {
		fmt.Fprintf(&b, "\n  Profile: \033[36m%s\033[0m    Game: %s    Mods: %d    \033[33m%d update(s) available\033[0m\n\n", m.cfg.ActiveProfile, gameStatus, modCount, updateCount)
	} else {
		fmt.Fprintf(&b, "\n  Profile: \033[36m%s\033[0m    Game: %s    Mods: %d\n\n", m.cfg.ActiveProfile, gameStatus, modCount)
	}

	// Mod list
	items := make([]modListItem, len(m.local.mods))
	for i, mod := range m.local.mods {
		items[i] = modListItem{
			Name:     mod.FullName(),
			Version:  mod.Version,
			Disabled: mod.Disabled,
			Update:   m.local.updates[mod.FullName()],
		}
	}
	renderModList(&b, items, m.local.cursor, false)

	// Status bar
	b.WriteString("\n")
	if m.local.err != nil {
		fmt.Fprintf(&b, "  \033[31mError: %v\033[0m\n", m.local.err)
	}
	hotkeys := []string{"↑/↓ navigate", "space toggle", "x remove", "u update", "i install", "c config", "s start", "l logs", "p profile"}
	if m.cfg.ActiveServer != "" {
		hotkeys = append(hotkeys, "tab server")
	}
	hotkeys = append(hotkeys, "q quit")
	renderHotkeyBar(&b, hotkeys, m.width)

	return b.String()
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

func readLogTail(path string, n int) ([]string, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
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

func (m model) handleConfirmStart(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		m.local.confirmStart = false
		return m, startGame(m.paths, m.cfg)
	case "ctrl+c":
		return m, tea.Quit
	default:
		m.local.confirmStart = false
	}
	return m, nil
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
