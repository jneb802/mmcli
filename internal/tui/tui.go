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

// Async message types.
type installDoneMsg struct{ err error }
type updateCheckDoneMsg struct{ updates map[string]string }

type model struct {
	mods   []config.ModEntry
	cursor int
	paths  config.Paths
	cfg    config.Config
	reg    *config.Registry
	err    error

	confirmRemove bool

	pickProfile      bool
	profiles         []string
	profileCursor    int
	creatingProfile  bool
	newProfileInput  string

	installing   bool
	installInput string
	installBusy  bool

	updates         map[string]string // fullName -> latest version
	checkingUpdates bool
}

func newModel(paths config.Paths, cfg config.Config, reg *config.Registry) model {
	m := model{
		paths:   paths,
		cfg:     cfg,
		reg:     reg,
		updates: make(map[string]string),
	}
	m.refreshMods()
	return m
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
	m.mods = mods
	if m.cursor >= len(m.mods) {
		m.cursor = max(0, len(m.mods)-1)
	}
}

func (m model) Init() tea.Cmd {
	m.checkingUpdates = true
	return checkUpdates(m.mods)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case installDoneMsg:
		m.installBusy = false
		m.installing = false
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.err = nil
			config.SaveRegistry(m.paths, *m.reg)
			m.refreshMods()
			// Re-check updates with new mod list
			m.checkingUpdates = true
			return m, checkUpdates(m.mods)
		}
		return m, nil

	case updateCheckDoneMsg:
		m.checkingUpdates = false
		m.updates = msg.updates
		return m, nil

	case tea.KeyMsg:
		// Busy installing — only allow quit
		if m.installBusy {
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			return m, nil
		}

		// Install text input mode
		if m.installing {
			return m.handleInstallInput(msg)
		}

		// Profile picker mode
		if m.pickProfile {
			return m.handleProfilePicker(msg)
		}

		// Remove confirmation
		if m.confirmRemove {
			return m.handleConfirmRemove(msg)
		}

		// Normal mode
		return m.handleNormal(msg)
	}
	return m, nil
}

func (m model) handleInstallInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.installing = false
	case "enter":
		if m.installInput != "" {
			m.installBusy = true
			return m, installMod(m.paths, m.cfg, m.reg, m.installInput)
		}
	case "backspace":
		if len(m.installInput) > 0 {
			m.installInput = m.installInput[:len(m.installInput)-1]
		}
	case "ctrl+c":
		return m, tea.Quit
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			m.installInput += string(msg.Runes)
		}
	}
	return m, nil
}

func (m model) handleProfilePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// New profile text input
	if m.creatingProfile {
		switch msg.String() {
		case "esc":
			m.creatingProfile = false
		case "enter":
			if m.newProfileInput != "" {
				if err := profile.Create(m.paths, m.newProfileInput); err != nil {
					m.err = err
				} else {
					m.reg.EnsureProfile(m.newProfileInput)
					config.SaveRegistry(m.paths, *m.reg)
					// Switch to the new profile
					if err := profile.Switch(m.paths, &m.cfg, m.newProfileInput); err != nil {
						m.err = err
					} else {
						config.Save(m.paths, m.cfg)
						m.cursor = 0
						m.refreshMods()
						m.updates = make(map[string]string)
						m.err = nil
						m.pickProfile = false
						m.creatingProfile = false
						return m, nil
					}
				}
				m.creatingProfile = false
			}
		case "backspace":
			if len(m.newProfileInput) > 0 {
				m.newProfileInput = m.newProfileInput[:len(m.newProfileInput)-1]
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				m.newProfileInput += string(msg.Runes)
			}
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "esc":
		m.pickProfile = false
	case "up", "k":
		if m.profileCursor > 0 {
			m.profileCursor--
		}
	case "down", "j":
		if m.profileCursor < len(m.profiles)-1 {
			m.profileCursor++
		}
	case "enter":
		name := m.profiles[m.profileCursor]
		if name != m.cfg.ActiveProfile {
			if err := profile.Switch(m.paths, &m.cfg, name); err != nil {
				m.err = err
			} else {
				config.Save(m.paths, m.cfg)
				m.cursor = 0
				m.refreshMods()
				m.updates = make(map[string]string)
				m.checkingUpdates = true
				m.err = nil
				m.pickProfile = false
				return m, checkUpdates(m.mods)
			}
		}
		m.pickProfile = false
	case "n":
		m.creatingProfile = true
		m.newProfileInput = ""
		m.err = nil
	}
	return m, nil
}

func (m model) handleConfirmRemove(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		mod := m.mods[m.cursor]
		if err := installer.Remove(m.paths, m.cfg, m.reg, mod.FullName()); err != nil {
			m.err = err
		} else {
			m.err = nil
			config.SaveRegistry(m.paths, *m.reg)
			m.refreshMods()
		}
	}
	m.confirmRemove = false
	return m, nil
}

func (m model) handleNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.mods)-1 {
			m.cursor++
		}
	case " ":
		if len(m.mods) > 0 {
			mod := m.mods[m.cursor]
			if mod.IsLocal {
				pluginsDir := m.paths.ProfilePluginsDir(m.cfg.ActiveProfile)
				if err := installer.ToggleLocalMod(pluginsDir, mod); err != nil {
					m.err = err
				} else {
					m.mods[m.cursor].Disabled = !m.mods[m.cursor].Disabled
					m.err = nil
				}
			} else if err := installer.Toggle(m.paths, m.cfg, m.reg, mod.FullName()); err != nil {
				m.err = err
			} else {
				m.mods[m.cursor].Disabled = !m.mods[m.cursor].Disabled
				m.err = nil
				config.SaveRegistry(m.paths, *m.reg)
			}
		}
	case "x":
		if len(m.mods) > 0 {
			m.confirmRemove = true
			m.err = nil
		}
	case "c":
		if len(m.mods) > 0 {
			mod := m.mods[m.cursor]
			path := findConfigFile(m.paths, m.cfg.ActiveProfile, mod)
			return m, openFile(path)
		}
	case "p":
		profiles, err := profile.List(m.paths)
		if err != nil {
			m.err = err
		} else {
			m.profiles = profiles
			m.profileCursor = 0
			for i, name := range profiles {
				if name == m.cfg.ActiveProfile {
					m.profileCursor = i
					break
				}
			}
			m.pickProfile = true
			m.err = nil
		}
	case "i":
		m.installing = true
		m.installInput = ""
		m.err = nil
	case "u":
		if len(m.mods) > 0 {
			mod := m.mods[m.cursor]
			if _, ok := m.updates[mod.FullName()]; ok {
				m.installBusy = true
				m.err = nil
				return m, updateMod(m.paths, m.cfg, m.reg, mod.FullName())
			}
		}
	}
	return m, nil
}

// --- View ---

func (m model) View() string {
	var b strings.Builder

	// Profile picker
	if m.pickProfile {
		if m.creatingProfile {
			b.WriteString("\n  New profile name:\n\n")
			fmt.Fprintf(&b, "  > %s\033[7m \033[0m\n", m.newProfileInput)
			b.WriteString("\n  \033[2menter create • esc cancel\033[0m\n\n")
			return b.String()
		}
		b.WriteString("\n  Switch profile:\n\n")
		for i, name := range m.profiles {
			cursor := "  "
			if i == m.profileCursor {
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
	if m.installing && !m.installBusy {
		b.WriteString("\n  Install mod (Owner-Name or Thunderstore URL):\n\n")
		fmt.Fprintf(&b, "  > %s\033[7m \033[0m\n", m.installInput)
		b.WriteString("\n  \033[2menter install • esc cancel\033[0m\n\n")
		return b.String()
	}

	// Installing busy
	if m.installBusy {
		query := m.installInput
		if query == "" {
			query = "mod"
		}
		fmt.Fprintf(&b, "\n  \033[33mInstalling %s...\033[0m\n\n", query)
		return b.String()
	}

	// Header
	updateCount := len(m.updates)
	if m.checkingUpdates {
		fmt.Fprintf(&b, "\n  Mods in profile '\033[36m%s\033[0m':  \033[2mchecking for updates...\033[0m\n\n", m.cfg.ActiveProfile)
	} else if updateCount > 0 {
		fmt.Fprintf(&b, "\n  Mods in profile '\033[36m%s\033[0m':  \033[33m%d update(s) available\033[0m\n\n", m.cfg.ActiveProfile, updateCount)
	} else {
		fmt.Fprintf(&b, "\n  Mods in profile '\033[36m%s\033[0m':\n\n", m.cfg.ActiveProfile)
	}

	if len(m.mods) == 0 {
		b.WriteString("  No mods installed.\n")
	}

	// Mod list
	maxName := 0
	for _, mod := range m.mods {
		if l := len(mod.FullName()); l > maxName {
			maxName = l
		}
	}

	for i, mod := range m.mods {
		cursor := "  "
		if i == m.cursor {
			cursor = "\033[36m>\033[0m "
		}

		check := "\033[32m✓\033[0m"
		if mod.Disabled {
			check = "\033[31m✗\033[0m"
		}

		name := mod.FullName()
		pad := strings.Repeat(" ", maxName-len(name)+2)

		version := mod.Version
		if version == "" {
			version = "-"
		}

		// Show update indicator
		if latest, ok := m.updates[mod.FullName()]; ok {
			fmt.Fprintf(&b, "  %s[%s] %s%s\033[33m%s → %s\033[0m\n", cursor, check, name, pad, mod.Version, latest)
		} else {
			fmt.Fprintf(&b, "  %s[%s] %s%s%s\n", cursor, check, name, pad, version)
		}
	}

	// Status bar
	b.WriteString("\n")
	if m.confirmRemove {
		mod := m.mods[m.cursor]
		fmt.Fprintf(&b, "  \033[33mRemove %s? (y/n)\033[0m\n", mod.FullName())
	} else if m.err != nil {
		fmt.Fprintf(&b, "  \033[31mError: %v\033[0m\n", m.err)
	}
	b.WriteString("  \033[2m↑/↓ navigate • space toggle • x remove • u update • i install • c config • p profile • q quit\033[0m\n\n")

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

func findConfigFile(paths config.Paths, profile string, mod config.ModEntry) string {
	configDir := paths.ProfileConfigDir(profile)
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

// Run starts the interactive TUI.
func Run(paths config.Paths, cfg config.Config, reg *config.Registry) error {
	m := newModel(paths, cfg, reg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
