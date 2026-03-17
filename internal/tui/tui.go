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
)

type model struct {
	mods          []config.ModEntry
	cursor        int
	paths         config.Paths
	cfg           config.Config
	reg           *config.Registry
	err           error
	confirmRemove bool
}

func newModel(paths config.Paths, cfg config.Config, reg *config.Registry) model {
	m := model{
		paths: paths,
		cfg:   cfg,
		reg:   reg,
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
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Confirmation prompt for remove
		if m.confirmRemove {
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
		}
	}
	return m, nil
}

func (m model) View() string {
	var b strings.Builder

	fmt.Fprintf(&b, "\n  Mods in profile '\033[36m%s\033[0m':\n\n", m.cfg.ActiveProfile)

	if len(m.mods) == 0 {
		b.WriteString("  No mods installed.\n")
		b.WriteString("\n  \033[2mq quit\033[0m\n\n")
		return b.String()
	}

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

		modType := "\033[2mdependency\033[0m"
		if mod.IsLocal {
			modType = "\033[35mlocal\033[0m"
		} else if !mod.IsDependency {
			modType = "installed"
		}

		version := mod.Version
		if version == "" {
			version = "-"
		}

		fmt.Fprintf(&b, "  %s[%s] %s%s%-10s  %s\n", cursor, check, name, pad, version, modType)
	}

	b.WriteString("\n")
	if m.confirmRemove {
		mod := m.mods[m.cursor]
		fmt.Fprintf(&b, "  \033[33mRemove %s? (y/n)\033[0m\n", mod.FullName())
	} else if m.err != nil {
		fmt.Fprintf(&b, "  \033[31mError: %v\033[0m\n", m.err)
	}
	b.WriteString("  \033[2m↑/↓ navigate • space toggle • x remove • c config • q quit\033[0m\n\n")

	return b.String()
}

// findConfigFile looks for a .cfg file matching the mod name in the profile config dir.
// Falls back to the config directory itself if no match is found.
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
