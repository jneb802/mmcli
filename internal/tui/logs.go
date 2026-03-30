package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// --- Logs tab state ---

type logsFilter int

const (
	logsLocal logsFilter = iota
	logsServer
)

type logsState struct {
	filter logsFilter
}

// --- View ---

func (m model) viewLogs() string {
	var b strings.Builder

	if m.isFullMode() {
		// Show which source is active
		filterLabel := "Local"
		if m.logs.filter == logsServer {
			filterLabel = "Server"
		}
		lv := m.activeLogViewer()

		if lv == nil || !lv.active {
			if m.logs.filter == logsServer && m.server.client == nil {
				b.WriteString("\n  \033[2mNo server configured.\033[0m\n\n")
			} else if m.logs.filter == logsServer {
				b.WriteString("\n  \033[2mLoading server logs...\033[0m\n\n")
			} else {
				b.WriteString("\n  \033[2mNo logs available. Start the game to generate logs.\033[0m\n\n")
			}
			hotkeys := []string{fmt.Sprintf("f switch (%s)", filterLabel), "tab next", "q quit"}
			renderHotkeyBar(&b, hotkeys, m.width)
			return b.String()
		}

		renderLogViewer(&b, *lv)
		hotkeys := []string{"↑/↓ scroll", fmt.Sprintf("f switch (%s)", filterLabel), "o open log file", "G follow", "tab next", "q quit"}
		renderHotkeyBar(&b, hotkeys, m.width)
	} else {
		// Local-only mode
		if !m.local.logs.active {
			b.WriteString("\n  \033[2mNo logs available. Start the game to generate logs.\033[0m\n\n")
			hotkeys := []string{"o open log file", "tab next", "q quit"}
			renderHotkeyBar(&b, hotkeys, m.width)
			return b.String()
		}

		renderLogViewer(&b, m.local.logs)
		hotkeys := []string{"↑/↓ scroll", "o open log file", "G follow", "tab next", "q quit"}
		renderHotkeyBar(&b, hotkeys, m.width)
	}

	return b.String()
}

// --- Key handler ---

func (m model) handleLogsKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Filter toggle (full mode only)
	if m.isFullMode() && msg.String() == "f" {
		if m.logs.filter == logsLocal {
			m.logs.filter = logsServer
		} else {
			m.logs.filter = logsLocal
		}
		return m, m.startLogStream()
	}

	// Open log file
	if msg.String() == "o" {
		if m.logs.filter == logsServer || (!m.isFullMode() && false) {
			// No file to open for server logs
			return m, nil
		}
		return m, openFile(m.paths.ProfileLogFile(m.cfg.ActiveProfile))
	}

	// Scroll controls — operate on the active log viewer
	lv := m.activeLogViewerMut()
	if lv == nil || !lv.active {
		return m, nil
	}

	switch msg.String() {
	case "up", "k":
		if lv.scroll > 0 {
			lv.scroll--
			lv.following = false
		}
	case "down", "j":
		maxScroll := max(0, len(lv.lines)-lv.visible)
		if lv.scroll < maxScroll {
			lv.scroll++
		}
		if lv.scroll >= maxScroll {
			lv.following = true
		}
	case "G":
		lv.scroll = max(0, len(lv.lines)-lv.visible)
		lv.following = true
	}
	return m, nil
}

// --- Helpers ---

// activeLogViewer returns a copy of the active log viewer state for reading.
func (m model) activeLogViewer() *logViewerState {
	if m.isFullMode() && m.logs.filter == logsServer {
		return &m.server.logs
	}
	return &m.local.logs
}

// activeLogViewerMut returns a pointer to the active log viewer state for mutation.
func (m *model) activeLogViewerMut() *logViewerState {
	if m.isFullMode() && m.logs.filter == logsServer {
		return &m.server.logs
	}
	return &m.local.logs
}

// startLogStream stops any active streams and starts the appropriate one for the current filter.
func (m *model) startLogStream() tea.Cmd {
	m.stopLocalLogStream()
	m.stopServerLogStream()

	if m.isFullMode() && m.logs.filter == logsServer {
		if m.server.client != nil {
			return m.loadServerLogs()
		}
		return nil
	}
	return m.loadLocalLogs()
}
