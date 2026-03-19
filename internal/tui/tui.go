package tui

import (
	"fmt"
	"strings"
	"time"

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
	width     int
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
			m.server.role = srv.Role
			if m.server.role == "" {
				m.server.role = "admin"
			}
		}
	}

	return m
}

func (m model) Init() tea.Cmd {
	m.local.checkingUpdates = true
	return tea.Batch(checkUpdates(m.local.mods), checkGameRunning(), localTick())
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

	case gameStatusMsg:
		m.local.gameRunning = msg.running
		return m, nil

	case gameStartMsg:
		if msg.err != nil {
			m.local.err = msg.err
		} else {
			m.local.err = nil
			m.local.gameRunning = true
		}
		return m, nil

	case localTickMsg:
		if m.activeTab == tabLocal {
			return m, tea.Batch(checkGameRunning(), localTick())
		}
		return m, nil

	case localLogLineMsg:
		m.local.logs.lines = append(m.local.logs.lines, msg.lines...)
		if m.local.logs.following {
			m.local.logs.scroll = max(0, len(m.local.logs.lines)-m.local.logs.visible)
		}
		if m.local.logCh != nil {
			return m, nextLocalLogLine(m.local.logCh)
		}
		return m, nil

	case localLogDoneMsg:
		m.local.logCh = nil
		m.local.logStop = nil
		m.local.logs.live = false
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	// --- Server async messages ---
	case serverStatusMsg:
		m.server.fetching = false
		m.server.statusErr = msg.err
		if msg.status != nil {
			m.server.status = msg.status
			if msg.status.Role != "" {
				m.server.role = msg.status.Role
			}
		}
		if msg.mods != nil {
			m.server.mods = msg.mods
			if m.server.cursor >= len(m.server.mods) {
				m.server.cursor = max(0, len(m.server.mods)-1)
			}
		}
		if msg.modsResp != nil {
			m.server.modsResp = msg.modsResp
		}
		// If we were waiting for server data to run preflight check
		if m.local.preflightFetching {
			m.local.preflightFetching = false
			if msg.err != nil {
				m.local.err = fmt.Errorf("could not reach server for mod check: %v", msg.err)
				return m, startGame(m.paths, m.cfg)
			}
			warnings := preflightCheck(m.local.mods, m.server.mods)
			if len(warnings) > 0 {
				m.local.preflightWarnings = warnings
				m.local.confirmStart = true
				return m, nil
			}
			return m, startGame(m.paths, m.cfg)
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
			if msg.resp != nil {
				m.server.lastPush = msg.resp
				m.server.lastPushTime = time.Now()
			}
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
			m.server.logs = newLogViewerState("Server Logs ("+m.server.serverName+")", msg.lines, true)
			if m.server.logCh != nil {
				return m, nextServerLogLine(m.server.logCh)
			}
		}
		return m, nil

	case serverLogLineMsg:
		m.server.logs.lines = append(m.server.logs.lines, msg.lines...)
		if m.server.logs.following {
			m.server.logs.scroll = max(0, len(m.server.logs.lines)-m.server.logs.visible)
		}
		if m.server.logCh != nil {
			return m, nextServerLogLine(m.server.logCh)
		}
		return m, nil

	case serverLogDoneMsg:
		m.server.logCh = nil
		m.server.logStop = nil
		m.server.logs.live = false
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
				if !m.local.logs.active {
					m.stopLocalLogStream()
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
			if m.local.confirmStart {
				return m.handleConfirmStart(msg)
			}
			if m.local.preflightFetching {
				if msg.String() == "ctrl+c" {
					return m, tea.Quit
				}
				return m, nil
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
