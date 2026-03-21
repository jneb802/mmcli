package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"mmcli/internal/agentapi"
	"mmcli/internal/client"
	"mmcli/internal/config"
)

// --- Settings tab state ---

type settingsTabState struct {
	cursor  int
	section int // 0=local, 1=server, 2=modpack

	// Local editing
	editingPath bool
	pathInput   string

	// Modpack editing
	editingField int // -1=none, 0=token, 1=author, 2=path
	fieldInput   string

	// Webhook editing
	editingWebhook  bool
	webhookInput    string
	editingEmbedURL bool
	embedURLInput   string

	// Admin list modal
	adminList    bool
	adminCursor  int
	adminAdding  bool
	adminEditing bool
	adminInput   string
	adminIDs     []string // working copy
	adminSaving  bool
}

// --- Settings item used by this tab ---

type settingsTabItem struct {
	label           string
	value           string
	tooltip         string
	editable        bool
	action          string // "anticheat", "path", "server-editor", "modpack-field-0/1/2", "open-readme", "open-manifest"
	isSectionHeader bool
}

// --- View ---

func (m model) viewSettingsTab() string {
	var b strings.Builder

	// Local path editing modal
	if m.settingsTab.editingPath {
		b.WriteString("\n  Valheim Path:\n\n")
		fmt.Fprintf(&b, "  > %s\033[7m \033[0m\n", m.settingsTab.pathInput)
		b.WriteString("\n  \033[2menter save • esc cancel\033[0m\n\n")
		return b.String()
	}

	// Modpack field editing modal
	if m.settingsTab.editingField >= 0 {
		var label string
		switch m.settingsTab.editingField {
		case 0:
			label = "Thunderstore Token"
		case 1:
			label = "Thunderstore Author"
		case 2:
			label = "Modpack Path"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "\n  %s:\n\n", label)
		fmt.Fprintf(&b, "  > %s\033[7m \033[0m\n", m.settingsTab.fieldInput)
		b.WriteString("\n  \033[2menter save • esc cancel\033[0m\n\n")
		return b.String()
	}

	// Webhook URL editing
	if m.settingsTab.editingWebhook {
		b.WriteString("\n  Discord webhook URL:\n\n")
		fmt.Fprintf(&b, "  > %s\033[7m \033[0m\n", m.settingsTab.webhookInput)
		b.WriteString("\n  \033[2menter save • esc cancel • empty to clear\033[0m\n\n")
		return b.String()
	}
	if m.settingsTab.editingEmbedURL {
		b.WriteString("\n  Status embed webhook URL:\n\n")
		fmt.Fprintf(&b, "  > %s\033[7m \033[0m\n", m.settingsTab.embedURLInput)
		b.WriteString("\n  \033[2menter save • esc cancel • empty to clear\033[0m\n\n")
		return b.String()
	}

	// Admin list modal
	if m.settingsTab.adminList {
		return m.viewAdminList()
	}

	// Server settings editor (takes over the whole view)
	if m.server.editor.active {
		var sb strings.Builder
		renderSettingsEdit(&sb, &m.server.editor, m.width)
		return sb.String()
	}

	items := m.buildSettingsTabItems()
	b.WriteString("\n")

	for i, item := range items {
		if item.isSectionHeader {
			if i > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "  \033[1m%s\033[0m\n", item.label)
			continue
		}
		cursor := "    "
		if i == m.settingsTab.cursor {
			cursor = "  \033[36m>\033[0m "
		}
		label := fmt.Sprintf("%-22s", item.label+":")
		val := item.value
		if item.editable && i == m.settingsTab.cursor {
			val = fmt.Sprintf("< %s >", val)
		}
		fmt.Fprintf(&b, "%s%s %s\n", cursor, label, val)
	}

	// Tooltip
	b.WriteString("\n")
	if m.settingsTab.cursor < len(items) && items[m.settingsTab.cursor].tooltip != "" {
		fmt.Fprintf(&b, "  \033[2m%s\033[0m\n", items[m.settingsTab.cursor].tooltip)
	}

	b.WriteString("\n")
	hotkeys := []string{"↑/↓ navigate", "enter/space change"}
	hotkeys = append(hotkeys, "tab next", "q quit")
	renderHotkeyBar(&b, hotkeys, m.width)

	return b.String()
}

// --- Items builder ---

func (m model) buildSettingsTabItems() []settingsTabItem {
	var items []settingsTabItem

	// --- Local section ---
	items = append(items, settingsTabItem{label: "Local", isSectionHeader: true})

	// Anticheat
	pref := m.cfg.AnticheatSystem
	if pref == "" {
		pref = "auto"
	}
	acValue := pref
	if pref == "auto" {
		acValue = fmt.Sprintf("%s \033[2m(resolved: %s)\033[0m", pref, m.anticheatSystem)
	}
	items = append(items, settingsTabItem{
		label:    "Anticheat",
		value:    acValue,
		tooltip:  "Which anticheat mod to configure on push. Auto detects from installed mods.",
		editable: true,
		action:   "anticheat",
	})

	// Valheim path
	items = append(items, settingsTabItem{
		label:    "Valheim Path",
		value:    m.cfg.ValheimPath,
		tooltip:  "Local Valheim installation directory.",
		editable: true,
		action:   "path",
	})

	// Profile (read-only)
	items = append(items, settingsTabItem{
		label:   "Profile",
		value:   m.cfg.ActiveProfile,
		tooltip: "Active mod profile. Switch with 'p' on the Mods tab.",
	})

	if !m.isFullMode() {
		return items
	}

	// --- Server section ---
	items = append(items, settingsTabItem{label: "Server", isSectionHeader: true})

	// Server settings editor entry
	items = append(items, settingsTabItem{
		label:    "World Settings",
		value:    "press to edit",
		tooltip:  "Edit world settings, manage launch configs. Opens the server settings editor.",
		editable: true,
		action:   "server-editor",
	})

	// Admin list
	adminCount := 0
	if m.server.settings != nil {
		adminCount = len(m.server.settings.Admins)
	}
	adminVal := fmt.Sprintf("%d entries", adminCount)
	if adminCount == 0 {
		adminVal = "\033[2mnone\033[0m"
	}
	items = append(items, settingsTabItem{
		label:    "Admin List",
		value:    adminVal,
		tooltip:  "Steam IDs with admin permissions on the server.",
		editable: true,
		action:   "admin-list",
	})

	// --- Discord section ---
	items = append(items, settingsTabItem{label: "Discord", isSectionHeader: true})

	// Webhook URL
	var webhookVal string
	if m.server.status != nil && m.server.status.WebhookEnabled {
		webhookVal = fmt.Sprintf("\033[32menabled\033[0m (%s)", m.server.status.WebhookURL)
	} else {
		webhookVal = "\033[2mnot configured\033[0m"
	}
	items = append(items, settingsTabItem{
		label:    "Webhook URL",
		value:    webhookVal,
		tooltip:  "Discord webhook URL for server event notifications.",
		editable: true,
		action:   "webhook-url",
	})

	// Status embed URL
	var embedVal string
	if m.server.status != nil && m.server.status.StatusEmbedURL != "" {
		embedVal = fmt.Sprintf("\033[32menabled\033[0m (%s)", m.server.status.StatusEmbedURL)
	} else {
		embedVal = "\033[2mnot configured\033[0m"
	}
	items = append(items, settingsTabItem{
		label:    "Status Embed",
		value:    embedVal,
		tooltip:  "Discord webhook for a continuously-updated status embed.",
		editable: true,
		action:   "embed-url",
	})

	// Webhook event toggles
	if m.server.webhookCfg != nil {
		wcfg := m.server.webhookCfg
		for _, toggle := range []struct {
			label, tooltip, action string
			enabled                bool
		}{
			{"Server Started", "Send notification when server starts.", "wh-server-started", wcfg.ServerStarted},
			{"Server Stopped", "Send notification when server stops.", "wh-server-stopped", wcfg.ServerStopped},
			{"Server Restarted", "Send notification when server is restarted.", "wh-server-restarted", wcfg.ServerRestarted},
			{"Server Ready", "Send notification when server is ready to join.", "wh-server-ready", wcfg.ServerReady},
			{"World Saved", "Send notification when world is saved.", "wh-world-saved", wcfg.WorldSaved},
			{"Player Joined", "Send notification when a player joins.", "wh-player-joined", wcfg.PlayerJoined},
			{"Player Left", "Send notification when a player leaves.", "wh-player-left", wcfg.PlayerLeft},
			{"Player Died", "Send notification when a player dies.", "wh-player-died", wcfg.PlayerDied},
			{"Player Shout", "Send notification when a player shouts.", "wh-player-shout", wcfg.PlayerShout},
			{"First Join", "Send notification on a player's first connection.", "wh-player-first-join", wcfg.PlayerFirstJoin},
			{"Raid Start", "Send notification when a raid event begins.", "wh-event-start", wcfg.EventStart},
			{"Raid End", "Send notification when a raid event ends.", "wh-event-stop", wcfg.EventStop},
			{"New Day", "Send notification at the start of each in-game day.", "wh-new-day", wcfg.NewDay},
			{"Cron Job", "Send notification when a cron job executes.", "wh-cronjob", wcfg.CronJob},
		} {
			val := "\033[2moff\033[0m"
			if toggle.enabled {
				val = "\033[32mon\033[0m"
			}
			items = append(items, settingsTabItem{
				label:    toggle.label,
				value:    val,
				tooltip:  toggle.tooltip,
				editable: true,
				action:   toggle.action,
			})
		}
	}

	// --- Modpack section ---
	if m.cfg.ModpackPath != "" {
		items = append(items, settingsTabItem{label: "Modpack", isSectionHeader: true})

		// Token
		tokenVal := m.cfg.ThunderstoreToken
		if tokenVal == "" {
			tokenVal = "\033[2m(not set)\033[0m"
		} else if len(tokenVal) > 4 {
			tokenVal = strings.Repeat("*", len(tokenVal)-4) + tokenVal[len(tokenVal)-4:]
		}
		items = append(items, settingsTabItem{
			label:    "Thunderstore Token",
			value:    tokenVal,
			tooltip:  "API token for publishing to Thunderstore.",
			editable: true,
			action:   "modpack-field-0",
		})

		// Author
		authorVal := m.cfg.ThunderstoreAuthor
		if authorVal == "" {
			authorVal = "\033[2m(not set)\033[0m"
		}
		items = append(items, settingsTabItem{
			label:    "Thunderstore Author",
			value:    authorVal,
			tooltip:  "Author name for Thunderstore publishing.",
			editable: true,
			action:   "modpack-field-1",
		})

		// Modpack path
		items = append(items, settingsTabItem{
			label:    "Modpack Path",
			value:    m.cfg.ModpackPath,
			tooltip:  "Local directory containing modpack files (manifest.json, README.md, icon.png).",
			editable: true,
			action:   "modpack-field-2",
		})

		// README (open action)
		readmeStatus := "\033[2mnot found\033[0m"
		if len(m.modpack.readmeLines) > 0 {
			readmeStatus = "\033[32mfound\033[0m"
		}
		items = append(items, settingsTabItem{
			label:    "README",
			value:    readmeStatus,
			tooltip:  "Open README.md in your editor.",
			editable: true,
			action:   "open-readme",
		})

		// Manifest (open action)
		manifestStatus := "\033[2mnot found\033[0m"
		if m.modpack.manifest != nil {
			manifestStatus = fmt.Sprintf("\033[32m%s v%s\033[0m", m.modpack.manifest.Name, m.modpack.manifest.VersionNumber)
		}
		items = append(items, settingsTabItem{
			label:    "Manifest",
			value:    manifestStatus,
			tooltip:  "Open manifest.json in your editor.",
			editable: true,
			action:   "open-manifest",
		})

		// Image
		iconStatus := "\033[31mnot found\033[0m"
		if m.modpack.iconFile != "" {
			iconStatus = fmt.Sprintf("\033[32m%s\033[0m", m.modpack.iconFile)
		}
		items = append(items, settingsTabItem{
			label:   "Image",
			value:   iconStatus,
			tooltip: "Expected: icon.png (256x256) in modpack directory.",
		})
	}

	return items
}

// --- Key handler ---

func (m model) handleSettingsTabKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Local path editing
	if m.settingsTab.editingPath {
		switch msg.String() {
		case "esc":
			m.settingsTab.editingPath = false
		case "enter":
			if m.settingsTab.pathInput != "" {
				m.cfg.ValheimPath = m.settingsTab.pathInput
				m.paths.ValheimDir = m.settingsTab.pathInput
				config.Save(m.paths, m.cfg)
			}
			m.settingsTab.editingPath = false
		case "backspace":
			if len(m.settingsTab.pathInput) > 0 {
				m.settingsTab.pathInput = m.settingsTab.pathInput[:len(m.settingsTab.pathInput)-1]
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				m.settingsTab.pathInput += string(msg.Runes)
			}
		}
		return m, nil
	}

	// Modpack field editing
	if m.settingsTab.editingField >= 0 {
		switch msg.String() {
		case "esc":
			m.settingsTab.editingField = -1
		case "enter":
			switch m.settingsTab.editingField {
			case 0:
				m.cfg.ThunderstoreToken = m.settingsTab.fieldInput
			case 1:
				m.cfg.ThunderstoreAuthor = m.settingsTab.fieldInput
			case 2:
				m.cfg.ModpackPath = m.settingsTab.fieldInput
				m.modpack.loadFromDisk(m.settingsTab.fieldInput)
			}
			config.Save(m.paths, m.cfg)
			m.settingsTab.editingField = -1
		case "backspace":
			if len(m.settingsTab.fieldInput) > 0 {
				m.settingsTab.fieldInput = m.settingsTab.fieldInput[:len(m.settingsTab.fieldInput)-1]
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				m.settingsTab.fieldInput += string(msg.Runes)
			}
		}
		return m, nil
	}

	// Webhook URL editing
	if m.settingsTab.editingWebhook {
		switch msg.String() {
		case "esc":
			m.settingsTab.editingWebhook = false
		case "enter":
			url := m.settingsTab.webhookInput
			m.settingsTab.editingWebhook = false
			if m.server.client != nil {
				return m, setWebhookURL(m.server.client, url)
			}
		case "backspace":
			if len(m.settingsTab.webhookInput) > 0 {
				m.settingsTab.webhookInput = m.settingsTab.webhookInput[:len(m.settingsTab.webhookInput)-1]
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				m.settingsTab.webhookInput += string(msg.Runes)
			}
		}
		return m, nil
	}
	if m.settingsTab.editingEmbedURL {
		switch msg.String() {
		case "esc":
			m.settingsTab.editingEmbedURL = false
		case "enter":
			url := m.settingsTab.embedURLInput
			m.settingsTab.editingEmbedURL = false
			if m.server.client != nil {
				return m, setStatusEmbedURL(m.server.client, url)
			}
		case "backspace":
			if len(m.settingsTab.embedURLInput) > 0 {
				m.settingsTab.embedURLInput = m.settingsTab.embedURLInput[:len(m.settingsTab.embedURLInput)-1]
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				m.settingsTab.embedURLInput += string(msg.Runes)
			}
		}
		return m, nil
	}

	// Admin list modal
	if m.settingsTab.adminList {
		return m.handleAdminListKeys(msg)
	}

	// Server settings editor
	if m.server.editor.active {
		return m.handleSettingsEditor(msg)
	}

	items := m.buildSettingsTabItems()

	switch msg.String() {
	case "up", "k":
		m.settingsTab.cursor--
		for m.settingsTab.cursor >= 0 && items[m.settingsTab.cursor].isSectionHeader {
			m.settingsTab.cursor--
		}
		if m.settingsTab.cursor < 0 {
			for i, item := range items {
				if !item.isSectionHeader {
					m.settingsTab.cursor = i
					break
				}
			}
		}
	case "down", "j":
		m.settingsTab.cursor++
		for m.settingsTab.cursor < len(items) && items[m.settingsTab.cursor].isSectionHeader {
			m.settingsTab.cursor++
		}
		if m.settingsTab.cursor >= len(items) {
			m.settingsTab.cursor = len(items) - 1
		}
	case "enter", " ":
		if m.settingsTab.cursor >= len(items) || !items[m.settingsTab.cursor].editable {
			return m, nil
		}
		item := items[m.settingsTab.cursor]
		switch item.action {
		case "anticheat":
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
		case "path":
			m.settingsTab.editingPath = true
			m.settingsTab.pathInput = m.cfg.ValheimPath
		case "server-editor":
			if m.server.role == agentapi.RoleAdmin && m.server.client != nil {
				if m.server.settings == nil {
					return m, fetchSettings(m.server.client)
				}
				m.server.editor = settingsEditor{
					active: true,
					fields: buildEditorFields(m.server.settings),
					cursor: 0,
				}
				return m, fetchLaunchConfigsForEditor(m.server.client)
			}
		case "webhook-url":
			m.settingsTab.editingWebhook = true
			if m.server.status != nil && m.server.status.WebhookURL != "" {
				m.settingsTab.webhookInput = m.server.status.WebhookURL
			} else {
				m.settingsTab.webhookInput = ""
			}
		case "embed-url":
			m.settingsTab.editingEmbedURL = true
			if m.server.status != nil && m.server.status.StatusEmbedURL != "" {
				m.settingsTab.embedURLInput = m.server.status.StatusEmbedURL
			} else {
				m.settingsTab.embedURLInput = ""
			}
		case "wh-server-started", "wh-server-stopped", "wh-server-restarted", "wh-server-ready",
			"wh-world-saved", "wh-player-joined", "wh-player-left", "wh-player-died", "wh-player-shout",
			"wh-player-first-join", "wh-event-start", "wh-event-stop", "wh-new-day", "wh-cronjob":
			return m.toggleWebhookEvent(item.action)
		case "admin-list":
			if m.server.role == agentapi.RoleAdmin && m.server.settings != nil {
				m.settingsTab.adminList = true
				m.settingsTab.adminCursor = 0
				m.settingsTab.adminAdding = false
				m.settingsTab.adminEditing = false
				m.settingsTab.adminInput = ""
				m.settingsTab.adminIDs = append([]string{}, m.server.settings.Admins...)
			}
		case "modpack-field-0":
			m.settingsTab.editingField = 0
			m.settingsTab.fieldInput = m.cfg.ThunderstoreToken
		case "modpack-field-1":
			m.settingsTab.editingField = 1
			m.settingsTab.fieldInput = m.cfg.ThunderstoreAuthor
		case "modpack-field-2":
			m.settingsTab.editingField = 2
			m.settingsTab.fieldInput = m.cfg.ModpackPath
		case "open-readme":
			if m.cfg.ModpackPath != "" {
				return m, openFile(filepath.Join(m.cfg.ModpackPath, "README.md"))
			}
		case "open-manifest":
			if m.cfg.ModpackPath != "" {
				return m, openFile(filepath.Join(m.cfg.ModpackPath, "manifest.json"))
			}
		}
	}
	return m, nil
}

// --- Webhook toggle ---

func (m model) toggleWebhookEvent(action string) (tea.Model, tea.Cmd) {
	if m.server.webhookCfg == nil || m.server.client == nil {
		return m, nil
	}
	wcfg := m.server.webhookCfg
	req := agentapi.WebhookConfigUpdate{}

	switch action {
	case "wh-server-started":
		v := !wcfg.ServerStarted
		req.ServerStarted = &v
		wcfg.ServerStarted = v
	case "wh-server-stopped":
		v := !wcfg.ServerStopped
		req.ServerStopped = &v
		wcfg.ServerStopped = v
	case "wh-world-saved":
		v := !wcfg.WorldSaved
		req.WorldSaved = &v
		wcfg.WorldSaved = v
	case "wh-player-joined":
		v := !wcfg.PlayerJoined
		req.PlayerJoined = &v
		wcfg.PlayerJoined = v
	case "wh-player-left":
		v := !wcfg.PlayerLeft
		req.PlayerLeft = &v
		wcfg.PlayerLeft = v
	case "wh-player-died":
		v := !wcfg.PlayerDied
		req.PlayerDied = &v
		wcfg.PlayerDied = v
	case "wh-player-first-join":
		v := !wcfg.PlayerFirstJoin
		req.PlayerFirstJoin = &v
		wcfg.PlayerFirstJoin = v
	case "wh-server-restarted":
		v := !wcfg.ServerRestarted
		req.ServerRestarted = &v
		wcfg.ServerRestarted = v
	case "wh-server-ready":
		v := !wcfg.ServerReady
		req.ServerReady = &v
		wcfg.ServerReady = v
	case "wh-player-shout":
		v := !wcfg.PlayerShout
		req.PlayerShout = &v
		wcfg.PlayerShout = v
	case "wh-event-start":
		v := !wcfg.EventStart
		req.EventStart = &v
		wcfg.EventStart = v
	case "wh-event-stop":
		v := !wcfg.EventStop
		req.EventStop = &v
		wcfg.EventStop = v
	case "wh-new-day":
		v := !wcfg.NewDay
		req.NewDay = &v
		wcfg.NewDay = v
	case "wh-cronjob":
		v := !wcfg.CronJob
		req.CronJob = &v
		wcfg.CronJob = v
	}

	return m, func() tea.Msg {
		m.server.client.UpdateWebhookConfig(req)
		return nil
	}
}

// --- Admin list modal ---

type adminSaveDoneMsg struct{ err error }

func (m model) viewAdminList() string {
	var b strings.Builder

	if m.settingsTab.adminAdding || m.settingsTab.adminEditing {
		label := "Add Steam ID"
		if m.settingsTab.adminEditing {
			label = "Edit Steam ID"
		}
		fmt.Fprintf(&b, "\n  %s:\n\n", label)
		fmt.Fprintf(&b, "  > %s\033[7m \033[0m\n", m.settingsTab.adminInput)
		b.WriteString("\n  \033[2menter save • esc cancel\033[0m\n\n")
		return b.String()
	}

	if m.settingsTab.adminSaving {
		b.WriteString("\n  \033[33mSaving admin list...\033[0m\n\n")
		return b.String()
	}

	b.WriteString("\n  \033[1mAdmin List\033[0m\n\n")

	if len(m.settingsTab.adminIDs) == 0 {
		b.WriteString("  \033[2mNo admins configured.\033[0m\n")
	} else {
		for i, id := range m.settingsTab.adminIDs {
			cursor := "    "
			if i == m.settingsTab.adminCursor {
				cursor = "  \033[36m>\033[0m "
			}
			fmt.Fprintf(&b, "%s%s\n", cursor, id)
		}
	}

	b.WriteString("\n")
	hotkeys := []string{"↑/↓ navigate", "a add", "e edit", "x remove", "esc back"}
	renderHotkeyBar(&b, hotkeys, m.width)
	return b.String()
}

func (m model) handleAdminListKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Text input mode (add/edit)
	if m.settingsTab.adminAdding || m.settingsTab.adminEditing {
		switch msg.String() {
		case "esc":
			m.settingsTab.adminAdding = false
			m.settingsTab.adminEditing = false
		case "enter":
			id := strings.TrimSpace(m.settingsTab.adminInput)
			if id != "" {
				if m.settingsTab.adminAdding {
					m.settingsTab.adminIDs = append(m.settingsTab.adminIDs, id)
					m.settingsTab.adminCursor = len(m.settingsTab.adminIDs) - 1
				} else if m.settingsTab.adminEditing && m.settingsTab.adminCursor < len(m.settingsTab.adminIDs) {
					m.settingsTab.adminIDs[m.settingsTab.adminCursor] = id
				}
				m.settingsTab.adminAdding = false
				m.settingsTab.adminEditing = false
				// Save immediately
				m.settingsTab.adminSaving = true
				return m, saveAdminList(m.server.client, m.settingsTab.adminIDs)
			}
		case "backspace":
			if len(m.settingsTab.adminInput) > 0 {
				m.settingsTab.adminInput = m.settingsTab.adminInput[:len(m.settingsTab.adminInput)-1]
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				m.settingsTab.adminInput += string(msg.Runes)
			}
		}
		return m, nil
	}

	switch msg.String() {
	case "esc", "q":
		m.settingsTab.adminList = false
	case "up", "k":
		if m.settingsTab.adminCursor > 0 {
			m.settingsTab.adminCursor--
		}
	case "down", "j":
		if m.settingsTab.adminCursor < len(m.settingsTab.adminIDs)-1 {
			m.settingsTab.adminCursor++
		}
	case "a":
		m.settingsTab.adminAdding = true
		m.settingsTab.adminInput = ""
	case "e":
		if m.settingsTab.adminCursor < len(m.settingsTab.adminIDs) {
			m.settingsTab.adminEditing = true
			m.settingsTab.adminInput = m.settingsTab.adminIDs[m.settingsTab.adminCursor]
		}
	case "x", "d":
		if m.settingsTab.adminCursor < len(m.settingsTab.adminIDs) {
			m.settingsTab.adminIDs = append(m.settingsTab.adminIDs[:m.settingsTab.adminCursor], m.settingsTab.adminIDs[m.settingsTab.adminCursor+1:]...)
			if m.settingsTab.adminCursor >= len(m.settingsTab.adminIDs) && m.settingsTab.adminCursor > 0 {
				m.settingsTab.adminCursor--
			}
			// Save immediately
			m.settingsTab.adminSaving = true
			return m, saveAdminList(m.server.client, m.settingsTab.adminIDs)
		}
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func saveAdminList(c *client.AgentClient, admins []string) tea.Cmd {
	return func() tea.Msg {
		if c == nil {
			return adminSaveDoneMsg{err: fmt.Errorf("no server connection")}
		}
		_, err := c.UpdateSettings(&agentapi.SettingsUpdateRequest{
			Admins: admins,
		})
		return adminSaveDoneMsg{err: err}
	}
}
