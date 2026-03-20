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

// --- Flat tab system (new) ---

type flatTab int

const (
	tabMods flatTab = iota
	tabLogs
	tabStatus
	tabSettings
	tabChanges // full mode only
)

func flatTabName(t flatTab) string {
	switch t {
	case tabMods:
		return "Mods"
	case tabLogs:
		return "Logs"
	case tabStatus:
		return "Status"
	case tabSettings:
		return "Settings"
	case tabChanges:
		return "Changes"
	default:
		return "?"
	}
}

func (m model) isFullMode() bool {
	return m.server.client != nil
}

func (m model) availableTabs() []flatTab {
	if m.isFullMode() {
		return []flatTab{tabMods, tabLogs, tabStatus, tabSettings, tabChanges}
	}
	return []flatTab{tabMods, tabLogs, tabStatus, tabSettings}
}

type model struct {
	activeTab flatTab

	paths           config.Paths
	cfg             config.Config
	reg             *config.Registry
	mods            modsState
	logs            logsState
	status          statusState
	settingsTab     settingsTabState
	changes         changesState
	local           localModel
	server          serverModel
	sync            syncModel
	modpack         modpackModel
	width           int
	height          int
	anticheatSystem string // resolved: "azu" or "enforcer"
}

func newModel(paths config.Paths, cfg config.Config, reg *config.Registry) model {
	m := model{
		paths:       paths,
		cfg:         cfg,
		reg:         reg,
		status:      statusState{cursor: 1},                        // skip "Local" section header
		settingsTab: settingsTabState{cursor: 1, editingField: -1}, // skip "Local" section header
		local: localModel{
			updates: make(map[string]string),
		},
	}
	m.refreshMods()
	m.anticheatSystem = resolveAnticheatSystem(cfg, m.local.mods)

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

	// Load last push result from disk
	if resp, t := loadLastPush(paths); resp != nil {
		m.server.lastPush = resp
		m.server.lastPushTime = t
	}

	// Load modpack data at startup (needed by audit rows)
	if cfg.ModpackPath != "" {
		m.modpack.loadFromDisk(cfg.ModpackPath)
	}

	// Build initial audit rows for full mode
	if m.isFullMode() {
		m.mods.auditRows = m.buildAuditRows()
	}

	return m
}

func resolveAnticheatSystem(cfg config.Config, mods []config.ModEntry) string {
	pref := cfg.AnticheatSystem
	if pref == "azu" || pref == "enforcer" {
		return pref
	}
	for _, mod := range mods {
		lower := strings.ToLower(mod.FullName())
		if strings.Contains(lower, "azuanticheat") {
			return "azu"
		}
		if strings.Contains(lower, "valheimenforcer") {
			return "enforcer"
		}
	}
	return "enforcer" // default — superset of azu
}

func (m model) Init() tea.Cmd {
	m.local.checkingUpdates = true
	cmds := []tea.Cmd{checkUpdates(m.local.mods), checkGameRunning(), localTick()}
	if m.isFullMode() {
		cmds = append(cmds, fetchServerStatus(m.server.client), serverTick())
	}
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	// --- Local async messages ---
	case installDoneMsg:
		m.mods.installBusy = false
		m.mods.installing = false
		m.local.installBusy = false
		m.local.installing = false
		if msg.err != nil {
			m.mods.err = msg.err
			m.local.err = msg.err
		} else {
			m.mods.err = nil
			m.local.err = nil
			m.mods.statusMsg = "Done"
			config.SaveRegistry(m.paths, *m.reg)
			m.refreshMods()
			if m.isFullMode() {
				m.modpack.loadFromDisk(m.cfg.ModpackPath)
				m.mods.auditRows = m.buildAuditRows()
			}
			m.local.checkingUpdates = true
			return m, checkUpdates(m.local.mods)
		}
		return m, nil

	case adminSaveDoneMsg:
		m.settingsTab.adminSaving = false
		if msg.err != nil {
			m.mods.err = msg.err
		} else if m.server.settings != nil {
			m.server.settings.Admins = m.settingsTab.adminIDs
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
			m.mods.err = msg.err
		} else {
			m.local.err = nil
			m.mods.err = nil
			m.local.gameRunning = true
		}
		return m, nil

	case localTickMsg:
		return m, tea.Batch(checkGameRunning(), localTick())

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
		m.height = msg.Height
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
		m.server.players = msg.players
		// Refresh views when server data arrives
		if msg.mods != nil {
			if m.activeTab == tabChanges {
				m.sync.modItems = buildPushItems(m.cfg, m.reg, m.paths, m.server.mods, m.modpack.versionMap)
			}
			if m.activeTab == tabMods && m.isFullMode() {
				m.mods.auditRows = m.buildAuditRows()
			}
		}
		// If we were waiting for server data to run preflight check
		if m.mods.preflightFetching {
			m.mods.preflightFetching = false
			if msg.err != nil {
				m.mods.err = fmt.Errorf("could not reach server for mod check: %v", msg.err)
				return m, startGame(m.paths, m.cfg)
			}
			warnings := preflightCheck(m.local.mods, m.server.mods)
			if len(warnings) > 0 {
				m.mods.preflightWarnings = warnings
				m.mods.confirmStart = true
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
		m.sync.confirmModPush = false
		if msg.err != nil {
			m.server.statusErr = msg.err
		} else {
			m.server.statusErr = nil
			if msg.resp != nil {
				m.server.lastPush = msg.resp
				m.server.lastPushTime = time.Now()
				saveLastPush(m.paths, msg.resp, m.server.lastPushTime)
			}
		}
		// Show push result screen in Changes tab
		if m.activeTab == tabChanges {
			m.sync.pushResult = true
			m.sync.pushResultScroll = 0
		}
		// Refresh audit rows after push
		if m.isFullMode() {
			m.mods.auditRows = m.buildAuditRows()
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

	case worldDeleteMsg:
		m.server.editor.worldDeleting = false
		if msg.err != nil {
			m.server.editor.worldErr = msg.err.Error()
		} else {
			m.server.editor.worldErr = ""
			// Re-fetch worlds list after delete
			m.server.editor.worldFetching = true
			return m, fetchWorlds(m.server.client)
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
		if m.isFullMode() && !m.server.fetching && !m.server.actionBusy {
			return m, tea.Batch(fetchServerStatus(m.server.client), serverTick())
		}
		return m, nil

	// --- Sync async messages ---
	case syncConfigListMsg:
		m.sync.configFetching = false
		if msg.err != nil {
			m.sync.configErr = msg.err
		} else {
			m.sync.configItems = msg.items
		}
		return m, nil

	case syncConfigPushMsg:
		m.sync.configPushBusy = false
		if msg.err != nil {
			m.sync.configErr = msg.err
		} else {
			m.sync.configErr = nil
			if msg.resp != nil {
				m.sync.lastConfigPush = msg.resp
			}
		}
		// Re-fetch config diffs after push
		if m.server.client != nil {
			m.sync.configFetching = true
			return m, fetchConfigDiffs(m.server.client, m.paths, m.cfg)
		}
		return m, nil

	// --- Modpack async messages ---
	case modpackPublishDoneMsg:
		m.modpack.publishing = false
		if msg.err != nil {
			m.modpack.publishErr = msg.err
		} else {
			m.modpack.publishDone = true
		}
		return m, nil

	case modpackUpdateCheckDoneMsg:
		m.modpack.checkingUpdates = false
		m.modpack.depUpdates = msg.updates
		return m, nil

	case modpackUpdateDoneMsg:
		m.modpack.updatingDep = false
		if msg.err != nil {
			m.modpack.statusMsg = msg.err.Error()
		} else {
			m.modpack.loadFromDisk(m.cfg.ModpackPath)
			m.modpack.statusMsg = "dependency updated"
		}
		return m, nil

	// --- Key dispatch ---
	case tea.KeyMsg:
		// Global blockers
		if m.mods.installBusy || m.local.installBusy {
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			return m, nil
		}
		if m.mods.preflightFetching || m.local.preflightFetching {
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			return m, nil
		}

		// Mods tab — delegate fully to mods handler (it handles its own modals)
		if m.activeTab == tabMods {
			if m.mods.installing || m.mods.pickProfile || m.mods.scopePicker ||
				m.mods.confirmRemove || m.mods.confirmStart || m.mods.confirmUpdateAll {
				return m.handleModsKeys(msg)
			}
		}

		// Changes tab modals (must be checked before global keys)
		if m.activeTab == tabChanges {
			if m.sync.pushResult || m.sync.confirmModPush ||
				m.sync.confirmConfigPush || m.sync.configPushBusy || m.server.actionBusy {
				return m.handleChangesKeys(msg)
			}
		}

		// Status tab modals (must be checked before global keys)
		if m.activeTab == tabStatus {
			if m.status.editingWebhook || m.status.editingEmbedURL ||
				m.status.confirmStart || m.status.confirmStop || m.status.confirmRestart {
				return m.handleStatusKeys(msg)
			}
		}

		// Settings tab modals (must be checked before global keys)
		if m.activeTab == tabSettings {
			if m.settingsTab.editingPath || m.settingsTab.editingField >= 0 || m.server.editor.active {
				return m.handleSettingsTabKeys(msg)
			}
		}

		// Global keys — consumed here, never reach old handlers
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q", "esc":
			return m, tea.Quit
		case "tab":
			return m, m.cycleTab(1)
		case "shift+tab":
			return m, m.cycleTab(-1)
		case "`", "1", "2", "3", "4":
			// Ignore legacy mode-switching keys
			return m, nil
		}

		// Tab-specific key dispatch
		switch m.activeTab {
		case tabMods:
			return m.handleModsKeys(msg)
		case tabLogs:
			return m.handleLogsKeys(msg)
		case tabStatus:
			return m.handleStatusKeys(msg)
		case tabSettings:
			return m.handleSettingsTabKeys(msg)
		case tabChanges:
			return m.handleChangesKeys(msg)
		}
	}

	return m, nil
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString(m.renderTabBar())
	b.WriteString(renderSeparator(m.width))

	switch m.activeTab {
	case tabMods:
		b.WriteString(m.viewMods())
	case tabLogs:
		b.WriteString(m.viewLogs())
	case tabStatus:
		b.WriteString(m.viewStatus())
	case tabSettings:
		b.WriteString(m.viewSettingsTab())
	case tabChanges:
		b.WriteString(m.viewChanges())
	}

	return b.String()
}

func (m model) renderTabBar() string {
	tabs := m.availableTabs()
	var b strings.Builder
	b.WriteString("  ")
	for i, t := range tabs {
		if i > 0 {
			b.WriteString("  ")
		}
		name := flatTabName(t)
		// Add context to tab names
		if t == tabMods {
			name = fmt.Sprintf("Mods — %s", m.cfg.ActiveProfile)
		}
		if t == m.activeTab {
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

// --- Flat tab lifecycle ---

// switchTab transitions to the given flat tab, setting up bridge state.
func (m *model) switchTab(to flatTab) tea.Cmd {
	if to == m.activeTab {
		return nil
	}

	// Tear down current tab
	m.stopLocalLogStream()
	m.stopServerLogStream()

	m.activeTab = to

	switch to {
	case tabMods:
		if m.isFullMode() {
			m.mods.auditRows = m.buildAuditRows()
		}
		return nil
	case tabLogs:
		return m.startLogStream()
	case tabStatus:
		return nil
	case tabSettings:
		if m.isFullMode() && m.server.settings == nil && m.server.client != nil {
			return fetchSettings(m.server.client)
		}
		return nil
	case tabChanges:
		m.sync.modItems = buildPushItems(m.cfg, m.reg, m.paths, m.server.mods, m.modpack.versionMap)
		return nil
	}
	return nil
}

func (m *model) cycleTab(dir int) tea.Cmd {
	tabs := m.availableTabs()
	for i, t := range tabs {
		if t == m.activeTab {
			next := tabs[(i+dir+len(tabs))%len(tabs)]
			return m.switchTab(next)
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
