package tui

import (
	"fmt"
	"strings"

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
	default:
		return "?"
	}
}

// connectServer resets the server model and connects to the profile's server if configured.
func (m *model) connectServer() {
	m.stopServerLogStream()
	m.server = serverModel{}
	if m.profileSettings.Server != "" {
		if srv, ok := m.cfg.Servers[m.profileSettings.Server]; ok {
			m.server.client = client.New(srv.Host, srv.Port, srv.Secret)
			m.server.serverName = m.profileSettings.Server
			m.server.role = srv.Role
			if m.server.role == "" {
				m.server.role = "admin"
			}
		}
	}
}

func (m model) isFullMode() bool {
	return m.server.client != nil && m.profileSettings.ServerManagementEnabled()
}

func (m model) isModpackMode() bool {
	return m.profileSettings.ModpackPath != "" && m.profileSettings.ModpackManagementEnabled()
}

func (m model) availableTabs() []flatTab {
	if m.isFullMode() {
		return []flatTab{tabMods, tabLogs, tabStatus, tabSettings}
	}
	return []flatTab{tabMods, tabLogs, tabStatus, tabSettings}
}

type model struct {
	activeTab flatTab

	paths           config.Paths
	cfg             config.Config
	reg             *config.Registry
	profileSettings config.ProfileSettings
	mods            modsState
	logs            logsState
	status          statusState
	settingsTab     settingsTabState
	local           localModel
	server          serverModel
	modpack         modpackModel
	confirm         confirmModal
	width           int
	height          int
	anticheatSystem string // resolved: "azu" or "enforcer"
}

func newModel(paths config.Paths, cfg config.Config, reg *config.Registry) model {
	ps := reg.GetSettings(cfg.ActiveProfile)
	m := model{
		paths:           paths,
		cfg:             cfg,
		reg:             reg,
		profileSettings: ps,
		status:          statusState{cursor: 1},                        // skip "Local" section header
		settingsTab:     settingsTabState{cursor: 1, editingField: -1}, // skip "Local" section header
		local: localModel{
			updates: make(map[string]string),
		},
	}
	m.refreshMods()
	m.anticheatSystem = resolveAnticheatSystem(ps.AnticheatSystem, m.local.mods)

	m.connectServer()

	// Load modpack data at startup (needed by audit rows)
	if m.isModpackMode() {
		m.modpack.loadFromDisk(ps.ModpackPath)
	}

	// Build initial audit rows for full mode
	if m.isFullMode() {
		m.mods.auditRows = m.buildAuditRows()
	}

	return m
}

func resolveAnticheatSystem(anticheatPref string, mods []config.ModEntry) string {
	pref := anticheatPref
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
	cmds := []tea.Cmd{checkUpdates(m.local.mods), checkGameRunning(m.cfg.ActiveGame), localTick()}
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
		// If a single mod was updated successfully, remove it from the update list
		// so the banner reflects the change immediately before the recheck runs.
		updatedName := m.mods.updateName
		m.mods.updateName = ""
		m.mods.updateFromVer = ""
		m.mods.updateToVer = ""
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
				if m.isModpackMode() {
					m.modpack.loadFromDisk(m.profileSettings.ModpackPath)
				}
				m.mods.auditRows = m.buildAuditRows()
			}
			if msg.updatedAll {
				m.local.updates = make(map[string]string)
			} else if updatedName != "" {
				delete(m.local.updates, updatedName)
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
		return m, tea.Batch(checkGameRunning(m.cfg.ActiveGame), localTick())

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
			if msg.modsResp.Manifest != nil {
				m.server.manifest = msg.modsResp.Manifest
			}
		}
		m.server.players = msg.players

		// Reconcile server state back to local registry
		if msg.mods != nil {
			dirty := false
			for _, sm := range msg.mods {
				mod, ok := m.reg.GetMod(m.cfg.ActiveProfile, sm.Name)
				if !ok {
					continue
				}
				// Sync GUID
				if sm.GUID != "" && mod.GUID != sm.GUID {
					mod.GUID = sm.GUID
					dirty = true
				}
				// Sync version (fill empty local, never overwrite)
				if mod.Version == "" && sm.Version != "" {
					mod.Version = sm.Version
					dirty = true
				}
				if dirty {
					m.reg.SetMod(m.cfg.ActiveProfile, mod)
				}
			}
			if dirty {
				config.SaveRegistry(m.paths, *m.reg)
			}
		}

		// Refresh views when server data arrives
		if msg.mods != nil {
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
				m.confirm = buildPreflightConfirm(warnings)
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

	case webhookCfgMsg:
		if msg.err == nil && msg.cfg != nil {
			m.server.webhookCfg = msg.cfg
		}
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
			cmds := []tea.Cmd{fetchServerStatus(m.server.client), serverTick()}
			// Refresh settings and webhook config if on Settings tab
			if m.activeTab == tabSettings && m.server.client != nil && !m.server.editor.active {
				cmds = append(cmds, fetchSettings(m.server.client), fetchWebhookConfig(m.server.client))
			}
			return m, tea.Batch(cmds...)
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
			m.modpack.loadFromDisk(m.profileSettings.ModpackPath)
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

		// Global confirmation modal takes priority over all tab dispatch
		if m.confirm.Active {
			return m.handleConfirm(msg)
		}

		// Mods tab — delegate fully to mods handler (it handles its own modals)
		if m.activeTab == tabMods {
			if m.mods.installing || m.mods.pickProfile || m.mods.scopePicker {
				return m.handleModsKeys(msg)
			}
		}

		// (Status tab confirms now use global confirmModal)

		// Settings tab modals (must be checked before global keys)
		if m.activeTab == tabSettings {
			if m.settingsTab.editingPath || m.settingsTab.editingField >= 0 || m.server.editor.active ||
				m.settingsTab.editingWebhook || m.settingsTab.editingEmbedURL || m.settingsTab.adminList {
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
		}
	}

	return m, nil
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString(m.renderTabBar())
	b.WriteString(renderSeparator(m.width))

	if m.confirm.Active {
		b.WriteString(m.confirm.View())
		return b.String()
	}

	switch m.activeTab {
	case tabMods:
		b.WriteString(m.viewMods())
	case tabLogs:
		b.WriteString(m.viewLogs())
	case tabStatus:
		b.WriteString(m.viewStatus())
	case tabSettings:
		b.WriteString(m.viewSettingsTab())
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
		if m.isFullMode() && m.server.client != nil {
			return tea.Batch(fetchSettings(m.server.client), fetchWebhookConfig(m.server.client))
		}
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
