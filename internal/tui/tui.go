package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"mmcli/internal/client"
	"mmcli/internal/config"
)

type tab int

const (
	tabLocal  tab = iota
	tabServer
)

type model struct {
	activeTab tab
	paths     config.Paths
	cfg       config.Config
	reg       *config.Registry
	local     localModel
	server    serverModel
}

func newModel(paths config.Paths, cfg config.Config, reg *config.Registry) model {
	m := model{
		activeTab: tabLocal,
		paths:     paths,
		cfg:       cfg,
		reg:       reg,
		local: localModel{
			updates: make(map[string]string),
		},
	}
	m.refreshMods()

	// Set up server client if configured
	if cfg.ActiveServer != "" {
		if srv, ok := cfg.Servers[cfg.ActiveServer]; ok {
			m.server.client = client.New(srv.Host, srv.Port, srv.Secret)
			m.server.serverName = cfg.ActiveServer
		}
	}

	return m
}

func (m model) Init() tea.Cmd {
	m.local.checkingUpdates = true
	return checkUpdates(m.local.mods)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	// --- Local async messages ---
	case installDoneMsg:
		m.local.installBusy = false
		m.local.installing = false
		if msg.err != nil {
			m.local.err = msg.err
		} else {
			m.local.err = nil
			config.SaveRegistry(m.paths, *m.reg)
			m.refreshMods()
			m.local.checkingUpdates = true
			return m, checkUpdates(m.local.mods)
		}
		return m, nil

	case updateCheckDoneMsg:
		m.local.checkingUpdates = false
		m.local.updates = msg.updates
		return m, nil

	// --- Server async messages ---
	case serverStatusMsg:
		m.server.fetching = false
		m.server.statusErr = msg.err
		if msg.status != nil {
			m.server.status = msg.status
		}
		if msg.mods != nil {
			m.server.mods = msg.mods
			if m.server.cursor >= len(m.server.mods) {
				m.server.cursor = max(0, len(m.server.mods)-1)
			}
		}
		return m, nil

	case serverActionMsg:
		m.server.actionBusy = false
		if msg.err != nil {
			m.server.statusErr = msg.err
		} else {
			m.server.statusErr = nil
		}
		// Re-fetch status after action
		if m.server.client != nil {
			m.server.fetching = true
			return m, fetchServerStatus(m.server.client)
		}
		return m, nil

	case serverPushMsg:
		m.server.actionBusy = false
		if msg.err != nil {
			m.server.statusErr = msg.err
		} else {
			m.server.statusErr = nil
		}
		// Re-fetch status after push
		if m.server.client != nil {
			m.server.fetching = true
			return m, fetchServerStatus(m.server.client)
		}
		return m, nil

	case serverLogsMsg:
		m.server.actionBusy = false
		if msg.err != nil {
			m.server.statusErr = msg.err
		} else {
			m.server.logs = newLogViewerState("Server Logs ("+m.server.serverName+")", msg.lines)
		}
		return m, nil

	case serverSettingsMsg:
		m.server.actionBusy = false
		if msg.err != nil {
			m.server.statusErr = msg.err
		} else {
			m.server.settings = msg.settings
			m.server.settingsVisible = true
			m.server.settingsScroll = 0
		}
		return m, nil

	case serverTickMsg:
		// Silent background refresh — no "fetching..." indicator
		if m.activeTab == tabServer && m.server.client != nil && !m.server.fetching && !m.server.actionBusy && !m.server.logs.active {
			return m, tea.Batch(fetchServerStatus(m.server.client), serverTick())
		}
		return m, nil

	// --- Key dispatch ---
	case tea.KeyMsg:
		// Local tab: check modals first (they intercept all keys including tab)
		if m.activeTab == tabLocal {
			if m.local.installBusy {
				if msg.String() == "ctrl+c" {
					return m, tea.Quit
				}
				return m, nil
			}
			if m.local.logs.active {
				if !handleLogViewerKeys(&m.local.logs, msg) {
					return m, tea.Quit
				}
				return m, nil
			}
			if m.local.installing {
				return m.handleInstallInput(msg)
			}
			if m.local.pickProfile {
				return m.handleProfilePicker(msg)
			}
			if m.local.confirmRemove {
				return m.handleConfirmRemove(msg)
			}
			return m.handleLocalNormal(msg)
		}

		// Server tab
		return m.handleServerNormal(msg)
	}

	return m, nil
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString(renderTabBar(m.activeTab))

	if m.activeTab == tabLocal {
		b.WriteString(m.viewLocal())
	} else {
		b.WriteString(m.viewServer())
	}

	return b.String()
}

func renderTabBar(active tab) string {
	if active == tabLocal {
		return fmt.Sprintf("  \033[1;36m[Local]\033[0m  \033[2mServer\033[0m\n")
	}
	return fmt.Sprintf("  \033[2mLocal\033[0m  \033[1;36m[Server]\033[0m\n")
}

// Run starts the interactive TUI.
func Run(paths config.Paths, cfg config.Config, reg *config.Registry) error {
	m := newModel(paths, cfg, reg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
