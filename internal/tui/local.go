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
						m.anticheatSystem = resolveAnticheatSystem(m.cfg, m.local.mods)
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
				m.anticheatSystem = resolveAnticheatSystem(m.cfg, m.local.mods)
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
	// Common keys across all tabs
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "`", "2":
		return m, m.enterServerMode()
	case "3":
		return m, m.enterModpackMode()
	case "4":
		return m, m.enterSyncMode()
	case "tab":
		cmd := m.cycleLocalTab(1)
		return m, cmd
	case "shift+tab":
		cmd := m.cycleLocalTab(-1)
		return m, cmd
	case "l":
		if m.activeLocalTab != contentLogs {
			cmd := m.switchLocalTab(contentLogs)
			return m, cmd
		}
	}

	// Tab-specific keys
	switch m.activeLocalTab {
	case contentMods:
		return m.handleLocalModsKeys(msg)
	case contentLogs:
		return m.handleLocalLogsKeys(msg)
	case contentStatus:
		// No interactive keys on status tab
	case contentSettings:
		return m.handleLocalSettingsKeys(msg)
	}
	return m, nil
}

func (m model) handleLocalModsKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Update-all confirmation modal
	if m.local.confirmUpdateAll {
		switch msg.String() {
		case "y":
			m.local.confirmUpdateAll = false
			m.local.installBusy = true
			m.local.err = nil
			return m, updateAllMods(m.paths, m.cfg, m.reg, m.local.updates)
		case "ctrl+c":
			return m, tea.Quit
		default:
			m.local.confirmUpdateAll = false
		}
		return m, nil
	}

	switch msg.String() {
	case "up", "k":
		if m.local.cursor > 0 {
			m.local.cursor--
		} else if m.local.cursor == 0 && len(m.local.updates) > 0 {
			m.local.cursor = -1
		}
	case "down", "j":
		if m.local.cursor == -1 {
			m.local.cursor = 0
		} else if m.local.cursor < len(m.local.mods)-1 {
			m.local.cursor++
		}
	case "enter":
		if m.local.cursor == -1 && len(m.local.updates) > 0 {
			m.local.confirmUpdateAll = true
			return m, nil
		}
	case " ":
		if len(m.local.mods) > 0 && m.local.cursor >= 0 {
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
		if len(m.local.mods) > 0 && m.local.cursor >= 0 {
			m.local.confirmRemove = true
			m.local.err = nil
		}
	case "c":
		if len(m.local.mods) > 0 && m.local.cursor >= 0 {
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
		if len(m.local.mods) > 0 && m.local.cursor >= 0 {
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
	case "o":
		return m, openFile(m.paths.ProfileDir(m.cfg.ActiveProfile))
	}
	return m, nil
}

func (m model) handleLocalLogsKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.local.logs.active {
		switch msg.String() {
		case "up", "k":
			if m.local.logs.scroll > 0 {
				m.local.logs.scroll--
				m.local.logs.following = false
			}
		case "down", "j":
			maxScroll := max(0, len(m.local.logs.lines)-m.local.logs.visible)
			if m.local.logs.scroll < maxScroll {
				m.local.logs.scroll++
			}
			if m.local.logs.scroll >= maxScroll {
				m.local.logs.following = true
			}
		case "f", "G":
			m.local.logs.scroll = max(0, len(m.local.logs.lines)-m.local.logs.visible)
			m.local.logs.following = true
		}
	}
	return m, nil
}

// --- Views ---

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

	// Update-all confirmation modal
	if m.local.confirmUpdateAll {
		fmt.Fprintf(&b, "\n  \033[1mUpdate %d mod(s)?\033[0m\n\n", len(m.local.updates))
		for name, latest := range m.local.updates {
			cur := ""
			for _, mod := range m.local.mods {
				if mod.FullName() == name {
					cur = mod.Version
					break
				}
			}
			fmt.Fprintf(&b, "    \033[33m%s\033[0m  %s → %s\n", name, cur, latest)
		}
		b.WriteString("\n  \033[33my update all • any key cancel\033[0m\n\n")
		return b.String()
	}

	updateCount := len(m.local.updates)
	if m.local.checkingUpdates {
		b.WriteString("\n  \033[2mchecking for updates...\033[0m\n")
	} else if updateCount > 0 {
		if m.local.cursor == -1 {
			fmt.Fprintf(&b, "\n  \033[36m>\033[0m \033[33m%d update(s) available\033[0m    \033[2menter to update all\033[0m\n", updateCount)
		} else {
			fmt.Fprintf(&b, "\n    \033[33m%d update(s) available\033[0m\n", updateCount)
		}
	}
	b.WriteString("\n")

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
	renderModList(&b, items, m.local.cursor, listVisible(m.height, 11), false, m.anticheatSystem)

	// Status bar
	b.WriteString("\n")
	if m.local.err != nil {
		fmt.Fprintf(&b, "  \033[31mError: %v\033[0m\n", m.local.err)
	}
	hotkeys := []string{"↑/↓ navigate", "space toggle", "x remove", "u update", "i install", "c config", "o open folder", "s start", "l logs", "p profile"}
	if m.cfg.ActiveServer != "" {
		hotkeys = append(hotkeys, "` mode")
	}
	hotkeys = append(hotkeys, "tab next", "q quit")
	renderHotkeyBar(&b, hotkeys, m.width)

	return b.String()
}

func (m model) viewLocalLogs() string {
	var b strings.Builder

	if !m.local.logs.active {
		b.WriteString("\n  \033[2mNo logs available. Start the game to generate logs.\033[0m\n\n")
		hotkeys := []string{"tab next"}
		if m.cfg.ActiveServer != "" {
			hotkeys = append(hotkeys, "` mode")
		}
		hotkeys = append(hotkeys, "q quit")
		renderHotkeyBar(&b, hotkeys, m.width)
		return b.String()
	}

	renderLogViewer(&b, m.local.logs)
	return b.String()
}

func (m model) viewLocalStatus() string {
	var b strings.Builder

	b.WriteString("\n")

	// Profile
	fmt.Fprintf(&b, "  Profile:        \033[36m%s\033[0m\n", m.cfg.ActiveProfile)

	// Game status
	gameStatus := "\033[2mstopped\033[0m"
	if m.local.gameRunning {
		gameStatus = "\033[32mrunning\033[0m"
	}
	fmt.Fprintf(&b, "  Game:           %s\n", gameStatus)

	// Mod count
	fmt.Fprintf(&b, "  Mods:           %d\n", len(m.local.mods))

	// mmcli version
	fmt.Fprintf(&b, "  mmcli:          \033[36m%s\033[0m\n", Version)

	// BepInEx version
	bepVer := detectBepInExVersion(m.paths)
	if bepVer != "" {
		fmt.Fprintf(&b, "  BepInEx:        %s\n", bepVer)
	} else {
		fmt.Fprintf(&b, "  BepInEx:        \033[2m–\033[0m\n")
	}

	// Server
	if m.cfg.ActiveServer != "" {
		fmt.Fprintf(&b, "  Server:         \033[36m%s\033[0m\n", m.cfg.ActiveServer)
	}

	b.WriteString("\n")
	hotkeys := []string{"tab next"}
	if m.cfg.ActiveServer != "" {
		hotkeys = append(hotkeys, "` mode")
	}
	hotkeys = append(hotkeys, "q quit")
	renderHotkeyBar(&b, hotkeys, m.width)

	return b.String()
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

func (m model) viewLocalSettings() string {
	var b strings.Builder

	// Editing Valheim path
	if m.local.editingPath {
		b.WriteString("\n  Valheim Path:\n\n")
		fmt.Fprintf(&b, "  > %s\033[7m \033[0m\n", m.local.pathInput)
		b.WriteString("\n  \033[2menter save • esc cancel\033[0m\n\n")
		return b.String()
	}

	items := m.buildSettingsItems()
	b.WriteString("\n")

	for i, item := range items {
		cursor := "    "
		if i == m.local.settingsCursor {
			cursor = "  \033[36m>\033[0m "
		}
		label := fmt.Sprintf("%-16s", item.Label()+":")
		val := item.value
		if item.editable && i == m.local.settingsCursor {
			val = fmt.Sprintf("< %s >", val)
		}
		fmt.Fprintf(&b, "%s%s %s\n", cursor, label, val)
	}

	// Tooltip
	b.WriteString("\n")
	if m.local.settingsCursor < len(items) {
		fmt.Fprintf(&b, "  \033[2m%s\033[0m\n", items[m.local.settingsCursor].tooltip)
	}

	b.WriteString("\n")
	hotkeys := []string{"↑/↓ navigate"}
	if m.local.settingsCursor < len(items) && items[m.local.settingsCursor].editable {
		hotkeys = append(hotkeys, "enter/space change")
	}
	hotkeys = append(hotkeys, "tab next")
	if m.cfg.ActiveServer != "" {
		hotkeys = append(hotkeys, "` mode")
	}
	hotkeys = append(hotkeys, "q quit")
	renderHotkeyBar(&b, hotkeys, m.width)

	return b.String()
}

func (s settingsItem) Label() string {
	return s.label
}

func (m model) handleLocalSettingsKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Editing Valheim path
	if m.local.editingPath {
		switch msg.String() {
		case "esc":
			m.local.editingPath = false
		case "enter":
			if m.local.pathInput != "" {
				m.cfg.ValheimPath = m.local.pathInput
				m.paths.ValheimDir = m.local.pathInput
				config.Save(m.paths, m.cfg)
			}
			m.local.editingPath = false
		case "backspace":
			if len(m.local.pathInput) > 0 {
				m.local.pathInput = m.local.pathInput[:len(m.local.pathInput)-1]
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				m.local.pathInput += string(msg.Runes)
			}
		}
		return m, nil
	}

	items := m.buildSettingsItems()

	switch msg.String() {
	case "up", "k":
		if m.local.settingsCursor > 0 {
			m.local.settingsCursor--
		}
	case "down", "j":
		if m.local.settingsCursor < len(items)-1 {
			m.local.settingsCursor++
		}
	case "enter", " ":
		if m.local.settingsCursor >= len(items) {
			return m, nil
		}
		item := items[m.local.settingsCursor]
		if !item.editable {
			return m, nil
		}
		switch item.label {
		case "Anticheat":
			switch m.cfg.AnticheatSystem {
			case "", "auto":
				m.cfg.AnticheatSystem = "azu"
			case "azu":
				m.cfg.AnticheatSystem = "enforcer"
			case "enforcer":
				m.cfg.AnticheatSystem = "auto"
			}
			config.Save(m.paths, m.cfg)
			m.anticheatSystem = resolveAnticheatSystem(m.cfg, m.local.mods)
		case "Valheim Path":
			m.local.editingPath = true
			m.local.pathInput = m.cfg.ValheimPath
		}
	}
	return m, nil
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
