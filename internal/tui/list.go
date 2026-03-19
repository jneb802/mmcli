package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// modListItem is a unified representation for rendering mod lists in both tabs.
type modListItem struct {
	Name      string
	Version   string
	Disabled  bool
	Update    string // latest version, empty if no update
	Anticheat string // "whitelist", "greylist", or ""
}

// renderModList renders a list of mods with cursor, check/x, name, version, and update indicators.
func renderModList(b *strings.Builder, items []modListItem, cursor int) {
	if len(items) == 0 {
		b.WriteString("  No mods.\n")
		return
	}

	maxName := 0
	for _, item := range items {
		if l := len(item.Name); l > maxName {
			maxName = l
		}
	}

	for i, item := range items {
		cur := "  "
		if i == cursor {
			cur = "\033[36m>\033[0m "
		}

		check := "\033[32m✓\033[0m"
		if item.Disabled {
			check = "\033[31m✗\033[0m"
		}

		badge := ""
		switch item.Anticheat {
		case "whitelist":
			badge = " \033[32m[W]\033[0m"
		case "greylist":
			badge = " \033[33m[G]\033[0m"
		}

		pad := strings.Repeat(" ", maxName-len(item.Name)+2)

		version := item.Version
		if version == "" {
			version = "-"
		}

		if item.Update != "" {
			fmt.Fprintf(b, "  %s[%s] %s%s%s\033[33m%s → %s\033[0m\n", cur, check, item.Name, badge, pad, version, item.Update)
		} else {
			fmt.Fprintf(b, "  %s[%s] %s%s%s%s\n", cur, check, item.Name, badge, pad, version)
		}
	}
}

// logViewerState holds shared log viewer state used by both tabs.
type logViewerState struct {
	active   bool
	lines    []string
	scroll   int
	title    string
	visible  int
}

func newLogViewerState(title string, lines []string) logViewerState {
	visible := 30
	maxScroll := max(0, len(lines)-visible)
	return logViewerState{
		active:  true,
		lines:   lines,
		scroll:  maxScroll, // start at bottom
		title:   title,
		visible: visible,
	}
}

// handleLogViewerKeys handles shared scroll/exit keybindings. Returns true if handled.
func handleLogViewerKeys(lv *logViewerState, msg tea.KeyMsg) bool {
	switch msg.String() {
	case "q", "esc":
		lv.active = false
		lv.lines = nil
		lv.scroll = 0
		return true
	case "up", "k":
		if lv.scroll > 0 {
			lv.scroll--
		}
		return true
	case "down", "j":
		maxScroll := max(0, len(lv.lines)-lv.visible)
		if lv.scroll < maxScroll {
			lv.scroll++
		}
		return true
	case "ctrl+c":
		return false // let caller handle quit
	}
	return true
}

func renderLogViewer(b *strings.Builder, lv logViewerState) {
	fmt.Fprintf(b, "\n  \033[1m%s\033[0m\n\n", lv.title)
	if len(lv.lines) == 0 {
		b.WriteString("  (no logs)\n")
	} else {
		end := lv.scroll + lv.visible
		if end > len(lv.lines) {
			end = len(lv.lines)
		}
		for _, line := range lv.lines[lv.scroll:end] {
			fmt.Fprintf(b, "  %s\n", line)
		}
	}
	b.WriteString("\n  \033[2m↑/↓ scroll • esc back\033[0m\n\n")
}
