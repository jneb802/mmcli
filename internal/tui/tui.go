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
	modeSync
	modeModpack
)

type contentTab int

const (
	contentMods contentTab = iota
	contentLogs
	contentStatus
	contentWorld
	contentSettings
	contentPlayers
	contentConfig
	contentSyncMods
	contentSyncConfigs
	contentSyncModeration
	contentSyncAudit
	contentModpackMods
	contentModpackConfig
	contentModpackReadme
	contentModpackManifest
	contentModpackImage
	contentModpackSettings
)

var localTabs = []contentTab{contentMods, contentConfig, contentLogs, contentStatus, contentSettings}
var serverTabs = []contentTab{contentMods, contentConfig, contentLogs, contentPlayers, contentWorld, contentSyncModeration, contentStatus}
var syncTabs = []contentTab{contentSyncMods, contentSyncConfigs, contentSyncModeration, contentSyncAudit}
var modpackTabs = []contentTab{contentModpackMods, contentModpackConfig, contentModpackReadme, contentModpackManifest, contentModpackImage, contentModpackSettings}

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
	case contentSettings:
		return "Settings"
	case contentPlayers:
		return "Players"
	case contentConfig:
		return "Config"
	case contentSyncMods:
		return "Mods"
	case contentSyncConfigs:
		return "Configs"
	case contentSyncModeration:
		return "Moderation"
	case contentSyncAudit:
		return "Audit"
	case contentModpackMods:
		return "Mods"
	case contentModpackConfig:
		return "Config"
	case contentModpackReadme:
		return "README"
	case contentModpackManifest:
		return "Manifest"
	case contentModpackImage:
		return "Image"
	case contentModpackSettings:
		return "Settings"
	default:
		return "?"
	}
}

type model struct {
	activeMode      mode
	activeLocalTab  contentTab
	activeServerTab contentTab
	activeSyncTab    contentTab
	activeModpackTab contentTab
	paths            config.Paths
	cfg             config.Config
	reg             *config.Registry
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
		activeMode:       modeLocal,
		activeSyncTab:    contentSyncMods,
		activeModpackTab: contentModpackMods,
		paths:         paths,
		cfg:           cfg,
		reg:           reg,
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
		// Refresh sync mod items when server data arrives,
		// but not while the user is viewing the Moderation tab (avoids re-sort jitter).
		if m.activeMode == modeSync && msg.mods != nil && m.activeSyncTab != contentSyncModeration {
			m.sync.modItems = buildPushItems(m.cfg, m.reg, m.paths, m.server.mods, m.modpack.versionMap)
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
		// Show push result screen in sync mode
		if m.activeMode == modeSync {
			m.sync.pushResult = true
			m.sync.pushResultScroll = 0
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
		if (m.activeMode == modeServer || m.activeMode == modeSync) && m.server.client != nil && !m.server.fetching && !m.server.actionBusy {
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

		if m.activeMode == modeServer {
			return m.handleServerNormal(msg)
		}

		if m.activeMode == modeSync {
			return m.handleSyncNormal(msg)
		}

		// Modpack mode
		return m.handleModpackNormal(msg)
	}

	return m, nil
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString(m.renderModeBar())
	b.WriteString(renderSeparator(m.width))

	switch m.activeMode {
	case modeLocal:
		b.WriteString(renderContentTabBar(localTabs, m.activeLocalTab))
		switch m.activeLocalTab {
		case contentMods:
			b.WriteString(m.viewLocal())
		case contentConfig:
			b.WriteString(m.viewLocalConfig())
		case contentLogs:
			b.WriteString(m.viewLocalLogs())
		case contentStatus:
			b.WriteString(m.viewLocalStatus())
		case contentSettings:
			b.WriteString(m.viewLocalSettings())
		}
	case modeServer:
		b.WriteString(renderContentTabBar(serverTabs, m.activeServerTab))
		switch m.activeServerTab {
		case contentMods:
			b.WriteString(m.viewServer())
		case contentConfig:
			b.WriteString(m.viewServerConfig())
		case contentLogs:
			b.WriteString(m.viewServerLogs())
		case contentWorld:
			b.WriteString(m.viewServerWorld())
		case contentSyncModeration:
			b.WriteString(m.viewServerModeration())
		case contentStatus:
			b.WriteString(m.viewServerStatus())
		case contentPlayers:
			b.WriteString(m.viewServerPlayers())
		}
	case modeSync:
		b.WriteString(renderContentTabBar(syncTabs, m.activeSyncTab))
		switch m.activeSyncTab {
		case contentSyncMods:
			b.WriteString(m.viewSyncMods())
		case contentSyncConfigs:
			b.WriteString(m.viewSyncConfigs())
		case contentSyncModeration:
			b.WriteString(m.viewSyncModeration())
		case contentSyncAudit:
			b.WriteString(m.viewSyncAudit())
		}
	case modeModpack:
		if m.modpack.editingPath {
			b.WriteString(m.viewModpackPathInput())
			break
		}
		b.WriteString(renderContentTabBar(modpackTabs, m.activeModpackTab))
		switch m.activeModpackTab {
		case contentModpackMods:
			b.WriteString(m.viewModpackMods())
		case contentModpackConfig:
			b.WriteString(m.viewModpackConfig())
		case contentModpackReadme:
			b.WriteString(m.viewModpackReadme())
		case contentModpackManifest:
			b.WriteString(m.viewModpackManifest())
		case contentModpackImage:
			b.WriteString(m.viewModpackImage())
		case contentModpackSettings:
			b.WriteString(m.viewModpackSettings())
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
	modpackLabel := "Modpack"
	changesLabel := "Changes"

	labels := []struct {
		text   string
		active bool
	}{
		{localLabel, m.activeMode == modeLocal},
		{serverLabel, m.activeMode == modeServer},
		{modpackLabel, m.activeMode == modeModpack},
		{changesLabel, m.activeMode == modeSync},
	}

	var b strings.Builder
	b.WriteString("  ")
	for i, l := range labels {
		if i > 0 {
			b.WriteString("  ")
		}
		if l.active {
			fmt.Fprintf(&b, "\033[1;37m[%s]\033[0m", l.text)
		} else {
			fmt.Fprintf(&b, "\033[2m%s\033[0m", l.text)
		}
	}
	b.WriteString("\n")
	return b.String()
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
	if to == contentConfig {
		m.local.configFiles = listProfileConfigs(m.paths, m.cfg.ActiveProfile)
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
	if to == contentConfig {
		m.server.configFiles = listProfileConfigs(m.paths, m.cfg.ActiveProfile)
	}
	if to == contentSyncModeration {
		m.sync.modItems = buildPushItems(m.cfg, m.reg, m.paths, m.server.mods, m.modpack.versionMap)
	}
	if to == contentLogs && m.server.client != nil {
		return m.loadServerLogs()
	}
	if to == contentWorld && m.server.client != nil && m.server.settings == nil {
		return fetchSettings(m.server.client)
	}
	return nil
}

func (m *model) switchSyncTab(to contentTab) tea.Cmd {
	old := m.activeSyncTab
	m.activeSyncTab = to
	if old == to {
		return nil
	}
	if to == contentSyncConfigs && m.server.client != nil && m.sync.configItems == nil {
		m.sync.configFetching = true
		return fetchConfigDiffs(m.server.client, m.paths, m.cfg)
	}
	if to == contentSyncAudit {
		m.sync.auditRows = m.buildAuditRows()
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

func (m *model) cycleSyncTab(dir int) tea.Cmd {
	for i, t := range syncTabs {
		if t == m.activeSyncTab {
			return m.switchSyncTab(syncTabs[(i+dir+len(syncTabs))%len(syncTabs)])
		}
	}
	return nil
}

func (m *model) switchModpackTab(to contentTab) tea.Cmd {
	m.activeModpackTab = to
	return nil
}

func (m *model) cycleModpackTab(dir int) tea.Cmd {
	for i, t := range modpackTabs {
		if t == m.activeModpackTab {
			return m.switchModpackTab(modpackTabs[(i+dir+len(modpackTabs))%len(modpackTabs)])
		}
	}
	return nil
}

// enterLocalMode switches to local mode with game status check.
func (m *model) enterLocalMode() tea.Cmd {
	m.stopLocalLogStream()
	m.stopServerLogStream()
	m.activeMode = modeLocal
	return tea.Batch(checkGameRunning(), localTick())
}

// enterServerMode switches to server mode, fetching status if needed.
func (m *model) enterServerMode() tea.Cmd {
	m.stopLocalLogStream()
	m.stopServerLogStream()
	m.activeMode = modeServer
	cmds := []tea.Cmd{}
	if m.server.client != nil {
		if m.server.status == nil {
			m.server.fetching = true
			cmds = append(cmds, fetchServerStatus(m.server.client))
		}
		cmds = append(cmds, serverTick())
		if m.activeServerTab == contentLogs {
			cmds = append(cmds, m.loadServerLogs())
		}
	}
	return tea.Batch(cmds...)
}

// enterModpackMode loads modpack data and switches to modpack mode.
func (m *model) enterModpackMode() tea.Cmd {
	m.activeMode = modeModpack
	m.modpack.loadFromDisk(m.cfg.ModpackPath)
	return nil
}

// enterSyncMode sets up sync mode state and returns any needed commands.
func (m *model) enterSyncMode() tea.Cmd {
	m.activeMode = modeSync
	cmds := []tea.Cmd{}
	if m.server.client != nil {
		// Fetch server data if not already loaded
		if m.server.status == nil {
			m.server.fetching = true
			cmds = append(cmds, fetchServerStatus(m.server.client))
		}
		cmds = append(cmds, serverTick())
	}
	// Populate mod items from current data
	m.sync.modItems = buildPushItems(m.cfg, m.reg, m.paths, m.server.mods, m.modpack.versionMap)
	return tea.Batch(cmds...)
}

// Run starts the interactive TUI.
func Run(paths config.Paths, cfg config.Config, reg *config.Registry) error {
	m := newModel(paths, cfg, reg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
