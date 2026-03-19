package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"mmcli/internal/config"
	"mmcli/internal/installer"
	"mmcli/internal/profile"
	"mmcli/internal/thunderstore"
)

// Async message types for local tab.
type installDoneMsg struct{ err error }
type updateCheckDoneMsg struct{ updates map[string]string }

type localModel struct {
	mods    []config.ModEntry
	cursor  int
	err     error

	confirmRemove bool

	pickProfile     bool
	profiles        []string
	profileCursor   int
	creatingProfile bool
	newProfileInput string

	installing  bool
	installInput string
	installBusy bool

	logs logViewerState

	updates         map[string]string
	checkingUpdates bool
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
		mod := m.local.mods[m.local.cursor]
		if err := installer.Remove(m.paths, m.cfg, m.reg, mod.FullName()); err != nil {
			m.local.err = err
		} else {
			m.local.err = nil
			config.SaveRegistry(m.paths, *m.reg)
			m.refreshMods()
		}
	}
	m.local.confirmRemove = false
	return m, nil
}

func (m model) handleLocalNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "tab":
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
	case "a":
		if len(m.local.mods) > 0 {
			mod := m.local.mods[m.local.cursor]
			if mod.IsLocal {
				m.local.err = fmt.Errorf("local mods cannot be classified for anticheat")
			} else {
				regMod, ok := m.reg.GetMod(m.cfg.ActiveProfile, mod.FullName())
				if ok {
					switch regMod.Anticheat {
					case "":
						regMod.Anticheat = "whitelist"
					case "whitelist":
						regMod.Anticheat = "greylist"
					case "greylist":
						regMod.Anticheat = ""
					}
					m.reg.SetMod(m.cfg.ActiveProfile, regMod)
					config.SaveRegistry(m.paths, *m.reg)
					m.refreshMods()
					m.local.err = nil
				}
			}
		}
	case "l":
		logFile := m.paths.BepInExLogFile()
		data, err := os.ReadFile(logFile)
		if err != nil {
			m.local.err = fmt.Errorf("no log file found")
		} else {
			lines := strings.Split(string(data), "\n")
			// Take last 200 lines
			if len(lines) > 200 {
				lines = lines[len(lines)-200:]
			}
			m.local.logs = newLogViewerState("BepInEx Logs ("+m.cfg.ActiveProfile+")", lines)
		}
	}
	return m, nil
}

func (m model) viewLocal() string {
	var b strings.Builder

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
		b.WriteString("\n  Install mod (Owner-Name or Thunderstore URL):\n\n")
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

	// Header
	modCount := len(m.local.mods)
	updateCount := len(m.local.updates)
	if m.local.checkingUpdates {
		fmt.Fprintf(&b, "\n  Profile: \033[36m%s\033[0m    Mods: %d    \033[2mchecking for updates...\033[0m\n\n", m.cfg.ActiveProfile, modCount)
	} else if updateCount > 0 {
		fmt.Fprintf(&b, "\n  Profile: \033[36m%s\033[0m    Mods: %d    \033[33m%d update(s) available\033[0m\n\n", m.cfg.ActiveProfile, modCount, updateCount)
	} else {
		fmt.Fprintf(&b, "\n  Profile: \033[36m%s\033[0m    Mods: %d\n\n", m.cfg.ActiveProfile, modCount)
	}

	// Mod list
	items := make([]modListItem, len(m.local.mods))
	for i, mod := range m.local.mods {
		items[i] = modListItem{
			Name:      mod.FullName(),
			Version:   mod.Version,
			Disabled:  mod.Disabled,
			Update:    m.local.updates[mod.FullName()],
			Anticheat: mod.Anticheat,
		}
	}
	renderModList(&b, items, m.local.cursor)

	// Status bar
	b.WriteString("\n")
	if m.local.confirmRemove {
		mod := m.local.mods[m.local.cursor]
		fmt.Fprintf(&b, "  \033[33mRemove %s? (y/n)\033[0m\n", mod.FullName())
	} else if m.local.err != nil {
		fmt.Fprintf(&b, "  \033[31mError: %v\033[0m\n", m.local.err)
	}
	b.WriteString("  \033[2m↑/↓ navigate • space toggle • a anticheat • x remove • u update • i install • c config • l logs • p profile • tab server • q quit\033[0m\n\n")

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
		return installDoneMsg{err: installer.Install(paths, cfg, reg, query, "both")}
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
