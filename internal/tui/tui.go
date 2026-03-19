package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"mmcli/internal/client"
	"mmcli/internal/config"
)

// Version is set from cmd package before Run.
var Version = "dev"

type mode int

const (
	modeLocal  mode = iota
	modeServer
)

type contentTab int

const (
	contentMods contentTab = iota
	contentLogs
	contentStatus
	contentWorld
)

var localTabs = []contentTab{contentMods, contentLogs, contentStatus}
var serverTabs = []contentTab{contentMods, contentLogs, contentWorld, contentStatus}

func contentTabName(t contentTab) string {
	switch t {
	case contentMods:
		return "Mods"
	case contentLogs:
		return "Logs"
	case contentStatus:
		return "Status"
	case contentWorld:
		return "World"
	default:
		return "?"
	}
}

type model struct {
	activeMode      mode
	activeLocalTab  contentTab
	activeServerTab contentTab
	paths           config.Paths
	cfg             config.Config
	reg             *config.Registry
	local           localModel
	server          serverModel
	width           int
}

func newModel(paths config.Paths, cfg config.Config, reg *config.Registry) model {
	m := model{
		activeMode: modeLocal,
		paths:      paths,
		cfg:        cfg,
		reg:        reg,
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
		if m.activeMode == modeLocal {
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
			m.server.settingsScroll = 0
			// Rebuild editor fields if active (e.g. after LC switch)
			if m.server.editor.active {
				m.server.editor.fields = buildEditorFields(msg.settings)
				m.server.editor.dirty = false
				m.server.editor.cursor = 0
				m.server.editor.lcManager = false
			}
		}
		return m, nil

	case settingsUpdateMsg:
		m.server.editor.saving = false
		if msg.err != nil {
			m.server.editor.err = msg.err.Error()
		} else {
			// Success — close editor, restart server, re-fetch settings
			m.server.editor.active = false
			m.server.actionBusy = true
			m.server.actionMsg = "Restarting server..."
			return m, tea.Batch(
				serverAction(m.server.client, "restart"),
				fetchSettings(m.server.client),
			)
		}
		return m, nil

	case worldListMsg:
		m.server.editor.worldFetching = false
		if msg.err != nil {
			m.server.editor.worldErr = msg.err.Error()
		} else {
			m.server.editor.worlds = msg.worlds
		}
		return m, nil

	case worldUploadMsg:
		m.server.editor.worldUploading = false
		if msg.err != nil {
			m.server.editor.worldErr = msg.err.Error()
		} else {
			m.setWorldField(msg.name)
			m.server.editor.worldPicker = false
			m.server.editor.worldInput = ""
		}
		return m, nil

	case editorLCInfoMsg:
		if msg.err == nil {
			m.server.editor.lcActive = msg.active
		}
		return m, nil

	case lcListMsg:
		m.server.editor.lcFetching = false
		if msg.err != nil {
			m.server.editor.lcErr = msg.err.Error()
		} else {
			m.server.editor.lcConfigs = msg.configs
			m.server.editor.lcActive = msg.active
			m.server.editor.lcCreating = false
			// If active config changed, reload settings
			if m.server.client != nil {
				return m, fetchSettings(m.server.client)
			}
		}
		return m, nil

	case lcActionMsg:
		m.server.editor.lcFetching = false
		if msg.err != nil {
			m.server.editor.lcErr = msg.err.Error()
		}
		return m, nil

	case serverTickMsg:
		// Silent background refresh — no "fetching..." indicator
		if m.activeMode == modeServer && m.server.client != nil && !m.server.fetching && !m.server.actionBusy {
			return m, tea.Batch(fetchServerStatus(m.server.client), serverTick())
		}
		return m, nil

	// --- Key dispatch ---
	case tea.KeyMsg:
		if m.activeMode == modeLocal {
			// Global blockers
			if m.local.installBusy {
				if msg.String() == "ctrl+c" {
					return m, tea.Quit
				}
				return m, nil
			}
			if m.local.preflightFetching {
				if msg.String() == "ctrl+c" {
					return m, tea.Quit
				}
				return m, nil
			}
			// Mods-tab modals
			if m.activeLocalTab == contentMods {
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
			}
			return m.handleLocalNormal(msg)
		}

		// Server mode
		return m.handleServerNormal(msg)
	}

	return m, nil
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString(m.renderModeBar())
	b.WriteString(renderSeparator(m.width))

	if m.activeMode == modeLocal {
		b.WriteString(renderContentTabBar(localTabs, m.activeLocalTab))
		switch m.activeLocalTab {
		case contentMods:
			b.WriteString(m.viewLocal())
		case contentLogs:
			b.WriteString(m.viewLocalLogs())
		case contentStatus:
			b.WriteString(m.viewLocalStatus())
		}
	} else {
		b.WriteString(renderContentTabBar(serverTabs, m.activeServerTab))
		switch m.activeServerTab {
		case contentMods:
			b.WriteString(m.viewServer())
		case contentLogs:
			b.WriteString(m.viewServerLogs())
		case contentWorld:
			b.WriteString(m.viewServerWorld())
		case contentStatus:
			b.WriteString(m.viewServerStatus())
		}
	}

	return b.String()
}

func (m model) renderModeBar() string {
	localLabel := fmt.Sprintf("Local — %s", m.cfg.ActiveProfile)
	serverLabel := "Server"
	if m.server.serverName != "" {
		serverLabel = fmt.Sprintf("Server — %s", m.server.serverName)
	}
	if m.activeMode == modeLocal {
		return fmt.Sprintf("  \033[1;37m[%s]\033[0m  \033[2m%s\033[0m\n", localLabel, serverLabel)
	}
	return fmt.Sprintf("  \033[2m%s\033[0m  \033[1;37m[%s]\033[0m\n", localLabel, serverLabel)
}

func renderContentTabBar(tabs []contentTab, active contentTab) string {
	var b strings.Builder
	b.WriteString("  ")
	for i, t := range tabs {
		if i > 0 {
			b.WriteString("  ")
		}
		name := contentTabName(t)
		if t == active {
			fmt.Fprintf(&b, "\033[1;36m[%s]\033[0m", name)
		} else {
			fmt.Fprintf(&b, "\033[2m%s\033[0m", name)
		}
	}
	b.WriteString("\n")
	return b.String()
}

func renderSeparator(width int) string {
	if width <= 0 {
		width = 80
	}
	return fmt.Sprintf("  \033[37m%s\033[0m\n", strings.Repeat("─", width-4))
}

// --- Tab lifecycle helpers ---

func (m *model) switchLocalTab(to contentTab) tea.Cmd {
	old := m.activeLocalTab
	m.activeLocalTab = to
	if old == to {
		return nil
	}
	if old == contentLogs {
		m.stopLocalLogStream()
	}
	if to == contentLogs {
		return m.loadLocalLogs()
	}
	return nil
}

func (m *model) switchServerTab(to contentTab) tea.Cmd {
	old := m.activeServerTab
	m.activeServerTab = to
	if old == to {
		return nil
	}
	if old == contentLogs {
		m.stopServerLogStream()
	}
	if to == contentLogs && m.server.client != nil {
		return m.loadServerLogs()
	}
	if to == contentWorld && m.server.client != nil && m.server.settings == nil {
		return fetchSettings(m.server.client)
	}
	return nil
}

func (m *model) cycleLocalTab(dir int) tea.Cmd {
	for i, t := range localTabs {
		if t == m.activeLocalTab {
			return m.switchLocalTab(localTabs[(i+dir+len(localTabs))%len(localTabs)])
		}
	}
	return nil
}

func (m *model) cycleServerTab(dir int) tea.Cmd {
	for i, t := range serverTabs {
		if t == m.activeServerTab {
			return m.switchServerTab(serverTabs[(i+dir+len(serverTabs))%len(serverTabs)])
		}
	}
	return nil
}

// Run starts the interactive TUI.
func Run(paths config.Paths, cfg config.Config, reg *config.Registry) error {
	m := newModel(paths, cfg, reg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
