package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"mmcli/internal/agentapi"
)

// --- Status tab state ---

type statusState struct {
	cursor         int
	confirmStart   bool
	confirmStop    bool
	confirmRestart bool

	// Webhook/embed editing (reused from serverModel)
	editingWebhook  bool
	webhookInput    string
	editingEmbedURL bool
	embedURLInput   string
}

// --- View ---

func (m model) viewStatus() string {
	var b strings.Builder

	// Webhook editing modals
	if m.status.editingWebhook {
		b.WriteString("\n  Discord webhook URL:\n\n")
		fmt.Fprintf(&b, "  > %s\033[7m \033[0m\n", m.status.webhookInput)
		b.WriteString("\n  \033[2menter save • esc cancel • empty to clear\033[0m\n\n")
		return b.String()
	}
	if m.status.editingEmbedURL {
		b.WriteString("\n  Status embed webhook URL:\n\n")
		fmt.Fprintf(&b, "  > %s\033[7m \033[0m\n", m.status.embedURLInput)
		b.WriteString("\n  \033[2menter save • esc cancel • empty to clear\033[0m\n\n")
		return b.String()
	}

	// Confirm modals
	if m.status.confirmStart {
		b.WriteString("\n  \033[33mStart server? (y/n)\033[0m\n\n")
		return b.String()
	}
	if m.status.confirmStop {
		b.WriteString("\n  \033[33mStop server? (y/n)\033[0m\n\n")
		return b.String()
	}
	if m.status.confirmRestart {
		b.WriteString("\n  \033[33mRestart server? (y/n)\033[0m\n\n")
		return b.String()
	}

	items := m.buildStatusItems()
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
		if i == m.status.cursor {
			cursor = "  \033[36m>\033[0m "
		}
		label := fmt.Sprintf("%-18s", item.label+":")
		val := item.value
		if item.editable && i == m.status.cursor {
			val = fmt.Sprintf("< %s >", val)
		}
		fmt.Fprintf(&b, "%s%s %s\n", cursor, label, val)
	}

	// Tooltip
	b.WriteString("\n")
	if m.status.cursor < len(items) && items[m.status.cursor].tooltip != "" {
		fmt.Fprintf(&b, "  \033[2m%s\033[0m\n", items[m.status.cursor].tooltip)
	}

	// Players section (full mode, inline)
	if m.isFullMode() {
		b.WriteString("\n")
		m.renderPlayersSection(&b)
	}

	b.WriteString("\n")
	hotkeys := []string{"↑/↓ navigate"}
	if m.isFullMode() {
		hotkeys = append(hotkeys, "s start", "d stop", "r restart")
	}
	if m.status.cursor < len(items) && items[m.status.cursor].editable {
		hotkeys = append(hotkeys, "enter/space edit")
	}
	hotkeys = append(hotkeys, "tab next", "q quit")
	renderHotkeyBar(&b, hotkeys, m.width)

	return b.String()
}

// --- Status items builder ---

type statusItem struct {
	label           string
	value           string
	tooltip         string
	editable        bool
	isSectionHeader bool
}

func (m model) buildStatusItems() []statusItem {
	var items []statusItem

	// --- Local section ---
	items = append(items, statusItem{label: "Local", isSectionHeader: true})

	items = append(items, statusItem{
		label:   "Profile",
		value:   fmt.Sprintf("\033[36m%s\033[0m", m.cfg.ActiveProfile),
		tooltip: "Active mod profile.",
	})

	gameStatus := "\033[2mstopped\033[0m"
	if m.local.gameRunning {
		gameStatus = "\033[32mrunning\033[0m"
	}
	items = append(items, statusItem{
		label:   "Game",
		value:   gameStatus,
		tooltip: "Whether Valheim is currently running.",
	})

	items = append(items, statusItem{
		label:   "Mods",
		value:   fmt.Sprintf("%d", len(m.local.mods)),
		tooltip: "Number of mods in the active profile.",
	})

	items = append(items, statusItem{
		label:   "mmcli",
		value:   fmt.Sprintf("\033[36m%s\033[0m", Version),
		tooltip: "Current mmcli version.",
	})

	bepVer := detectBepInExVersion(m.paths)
	if bepVer == "" {
		bepVer = "\033[2m–\033[0m"
	}
	items = append(items, statusItem{
		label:   "BepInEx",
		value:   bepVer,
		tooltip: "Installed BepInEx version.",
	})

	if !m.isFullMode() {
		return items
	}

	// --- Server section ---
	items = append(items, statusItem{label: "Server", isSectionHeader: true})

	// Reuse buildServerStatusItems content
	serverItems := m.buildServerStatusItems()
	for _, si := range serverItems {
		items = append(items, statusItem{
			label:    si.label,
			value:    si.value,
			tooltip:  si.tooltip,
			editable: si.editable,
		})
	}

	return items
}

// --- Key handler ---

func (m model) handleStatusKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Webhook editing modals
	if m.status.editingWebhook {
		return m.handleStatusWebhookInput(msg)
	}
	if m.status.editingEmbedURL {
		return m.handleStatusEmbedInput(msg)
	}

	// Confirm modals
	if m.status.confirmStart {
		switch msg.String() {
		case "y":
			m.status.confirmStart = false
			m.server.actionBusy = true
			m.server.actionMsg = "Starting server..."
			return m, serverAction(m.server.client, "start")
		case "ctrl+c":
			return m, tea.Quit
		default:
			m.status.confirmStart = false
		}
		return m, nil
	}
	if m.status.confirmStop {
		switch msg.String() {
		case "y":
			m.status.confirmStop = false
			m.server.actionBusy = true
			m.server.actionMsg = "Stopping server..."
			return m, serverAction(m.server.client, "stop")
		case "ctrl+c":
			return m, tea.Quit
		default:
			m.status.confirmStop = false
		}
		return m, nil
	}
	if m.status.confirmRestart {
		switch msg.String() {
		case "y":
			m.status.confirmRestart = false
			m.server.actionBusy = true
			m.server.actionMsg = "Restarting server..."
			return m, serverAction(m.server.client, "restart")
		case "ctrl+c":
			return m, tea.Quit
		default:
			m.status.confirmRestart = false
		}
		return m, nil
	}

	items := m.buildStatusItems()

	switch msg.String() {
	case "up", "k":
		m.status.cursor--
		// Skip section headers
		for m.status.cursor >= 0 && items[m.status.cursor].isSectionHeader {
			m.status.cursor--
		}
		if m.status.cursor < 0 {
			// Find first non-header
			for i, item := range items {
				if !item.isSectionHeader {
					m.status.cursor = i
					break
				}
			}
		}
	case "down", "j":
		m.status.cursor++
		// Skip section headers
		for m.status.cursor < len(items) && items[m.status.cursor].isSectionHeader {
			m.status.cursor++
		}
		if m.status.cursor >= len(items) {
			m.status.cursor = len(items) - 1
		}

	case "s":
		if m.isFullMode() && m.server.role == agentapi.RoleAdmin && m.server.client != nil {
			m.status.confirmStart = true
		}
	case "d":
		if m.isFullMode() && m.server.role == agentapi.RoleAdmin && m.server.client != nil {
			m.status.confirmStop = true
		}
	case "r":
		if m.isFullMode() && m.server.role == agentapi.RoleAdmin && m.server.client != nil {
			m.status.confirmRestart = true
		}

	case "enter", " ":
		if m.status.cursor < len(items) && items[m.status.cursor].editable {
			switch items[m.status.cursor].label {
			case "Discord webhook":
				m.status.editingWebhook = true
				if m.server.status != nil && m.server.status.WebhookURL != "" {
					m.status.webhookInput = m.server.status.WebhookURL
				} else {
					m.status.webhookInput = ""
				}
			case "Status embed":
				m.status.editingEmbedURL = true
				if m.server.status != nil && m.server.status.StatusEmbedURL != "" {
					m.status.embedURLInput = m.server.status.StatusEmbedURL
				} else {
					m.status.embedURLInput = ""
				}
			}
		}
	}
	return m, nil
}

func (m model) handleStatusWebhookInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.status.editingWebhook = false
	case "enter":
		url := m.status.webhookInput
		m.status.editingWebhook = false
		if m.server.client != nil {
			return m, setWebhookURL(m.server.client, url)
		}
	case "backspace":
		if len(m.status.webhookInput) > 0 {
			m.status.webhookInput = m.status.webhookInput[:len(m.status.webhookInput)-1]
		}
	case "ctrl+c":
		return m, tea.Quit
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			m.status.webhookInput += string(msg.Runes)
		}
	}
	return m, nil
}

func (m model) handleStatusEmbedInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.status.editingEmbedURL = false
	case "enter":
		url := m.status.embedURLInput
		m.status.editingEmbedURL = false
		if m.server.client != nil {
			return m, setStatusEmbedURL(m.server.client, url)
		}
	case "backspace":
		if len(m.status.embedURLInput) > 0 {
			m.status.embedURLInput = m.status.embedURLInput[:len(m.status.embedURLInput)-1]
		}
	case "ctrl+c":
		return m, tea.Quit
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			m.status.embedURLInput += string(msg.Runes)
		}
	}
	return m, nil
}

// --- Helpers ---

func (m model) renderPlayersSection(b *strings.Builder) {
	fmt.Fprintf(b, "  \033[1mPlayers\033[0m\n")
	if m.server.status == nil || !m.server.status.Running {
		b.WriteString("    \033[2mServer is not running.\033[0m\n")
	} else if len(m.server.players) == 0 {
		b.WriteString("    \033[2mNo players online.\033[0m\n")
	} else {
		for _, p := range m.server.players {
			name := p.Name
			if name == "" {
				name = "\033[2munknown\033[0m"
			}
			fmt.Fprintf(b, "    %s\n", name)
		}
		fmt.Fprintf(b, "    \033[2m%d online\033[0m\n", len(m.server.players))
	}
}
