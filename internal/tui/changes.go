package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// --- Changes tab state ---

type changesSubView int

const (
	changesMods changesSubView = iota
	changesConfigs
)

type changesState struct {
	subView changesSubView
}

// --- View ---

func (m model) viewChanges() string {
	var b strings.Builder

	if m.server.client == nil {
		b.WriteString("\n  No server configured.\n")
		b.WriteString("  Run \033[36mmmcli server add\033[0m to register one.\n\n")
		b.WriteString("  \033[2mtab next • q quit\033[0m\n\n")
		return b.String()
	}

	// Push result screen (handled by sync model)
	if m.sync.pushResult && m.server.lastPush != nil {
		renderSyncPushResult(&b, m.server.lastPush, m.server.lastPushTime, m.sync.pushResultScroll, m.server.role)
		return b.String()
	}

	// Action busy
	if m.server.actionBusy {
		renderSyncPushing(&b, m.sync.modItems)
		return b.String()
	}
	if m.sync.configPushBusy {
		b.WriteString("\n  \033[33mPushing configs...\033[0m\n\n")
		return b.String()
	}

	// Sub-view indicator
	modsLabel := "Mods"
	configsLabel := "Configs"
	if m.changes.subView == changesMods {
		modsLabel = fmt.Sprintf("\033[1;36m[%s]\033[0m", modsLabel)
		configsLabel = fmt.Sprintf("\033[2m%s\033[0m", configsLabel)
	} else {
		modsLabel = fmt.Sprintf("\033[2m%s\033[0m", modsLabel)
		configsLabel = fmt.Sprintf("\033[1;36m[%s]\033[0m", configsLabel)
	}
	fmt.Fprintf(&b, "  %s  %s\n", modsLabel, configsLabel)

	switch m.changes.subView {
	case changesMods:
		b.WriteString(m.viewChangesMods())
	case changesConfigs:
		b.WriteString(m.viewChangesConfigs())
	}

	return b.String()
}

func (m model) viewChangesMods() string {
	var b strings.Builder

	if m.server.fetching && len(m.sync.modItems) == 0 {
		b.WriteString("\n  \033[2mFetching server data...\033[0m\n\n")
		return b.String()
	}

	pending := buildPendingChanges(m.sync.modItems, m.reg, m.cfg.ActiveProfile, m.modpack.versionMap)
	b.WriteString("\n")
	showModpack := m.cfg.ModpackPath != ""
	renderPendingChanges(&b, pending, m.sync.modCursor, listVisible(m.height, 12), showModpack)

	b.WriteString("\n")
	if m.server.statusErr != nil {
		fmt.Fprintf(&b, "  \033[31mError: %v\033[0m\n", m.server.statusErr)
	}
	hotkeys := []string{"↑/↓ navigate", "p push", "h/l switch view", "tab next", "q quit"}
	renderHotkeyBar(&b, hotkeys, m.width)

	return b.String()
}

func (m model) viewChangesConfigs() string {
	var b strings.Builder

	if m.sync.configFetching {
		b.WriteString("\n  \033[2mFetching config diffs...\033[0m\n\n")
		return b.String()
	}
	if m.sync.configErr != nil {
		fmt.Fprintf(&b, "\n  \033[31mError: %v\033[0m\n\n", m.sync.configErr)
		return b.String()
	}

	b.WriteString("\n")

	if len(m.sync.configItems) == 0 {
		b.WriteString("  \033[32mNo config differences.\033[0m\n")
	} else {
		// Render config list
		for i, item := range m.sync.configItems {
			cur := "  "
			if i == m.sync.configCursor {
				cur = "\033[36m>\033[0m "
			}
			var statusColor string
			switch item.Status {
			case "modified":
				statusColor = fmt.Sprintf("\033[33m%s (%d changes)\033[0m", item.Status, item.DiffCount)
			case "local only":
				statusColor = fmt.Sprintf("\033[32m%s\033[0m", item.Status)
			case "server only":
				statusColor = fmt.Sprintf("\033[31m%s\033[0m", item.Status)
			default:
				statusColor = fmt.Sprintf("\033[2m%s\033[0m", item.Status)
			}
			fmt.Fprintf(&b, "  %s%-32s  %s\n", cur, item.Filename, statusColor)
		}
	}

	// Last push result
	if m.sync.lastConfigPush != nil {
		b.WriteString("\n")
		fmt.Fprintf(&b, "  \033[2mLast push: %d applied, %d written\033[0m\n",
			m.sync.lastConfigPush.Applied, m.sync.lastConfigPush.Written)
	}

	b.WriteString("\n")
	hotkeys := []string{"↑/↓ navigate", "p push", "r refresh", "h/l switch view", "tab next", "q quit"}
	renderHotkeyBar(&b, hotkeys, m.width)

	return b.String()
}

// --- Key handler ---

func (m model) handleChangesKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Push result screen
	if m.sync.pushResult {
		switch msg.String() {
		case "esc", "enter", "q":
			m.sync.pushResult = false
			m.sync.pushResultScroll = 0
		case "up", "k":
			if m.sync.pushResultScroll > 0 {
				m.sync.pushResultScroll--
			}
		case "down", "j":
			m.sync.pushResultScroll++
		case "ctrl+c":
			return m, tea.Quit
		}
		return m, nil
	}

	// Action busy
	if m.server.actionBusy || m.sync.configPushBusy {
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}

	// No server
	if m.server.client == nil {
		return m, nil
	}

	// Sub-view toggle
	switch msg.String() {
	case "h", "left":
		if m.changes.subView == changesConfigs {
			m.changes.subView = changesMods
		}
		return m, nil
	case "l", "right":
		if m.changes.subView == changesMods {
			m.changes.subView = changesConfigs
			// Fetch config diffs on first switch
			if m.sync.configItems == nil && m.server.client != nil {
				m.sync.configFetching = true
				return m, fetchConfigDiffs(m.server.client, m.paths, m.cfg)
			}
		}
		return m, nil
	}

	// Delegate to sub-view handler
	switch m.changes.subView {
	case changesMods:
		return m.handleSyncModsKeys(msg)
	case changesConfigs:
		return m.handleSyncConfigsKeys(msg)
	}
	return m, nil
}
