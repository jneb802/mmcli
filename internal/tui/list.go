package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// modListItem is a unified representation for rendering mod lists in both tabs.
type modListItem struct {
	Name          string
	Version       string
	Disabled      bool
	Update        string // latest version, empty if no update
	Anticheat     string // "whitelist", "greylist", or ""
	Status        string // push diff: "added", "removed", "changed", "" (unchanged)
	ServerVersion string // push diff: previous version on server (for "changed" items)
}

// renderModList renders a list of mods with cursor, check/x, name, version, and update indicators.
// If showAnticheat is true, an anticheat column is shown after version.
func renderModList(b *strings.Builder, items []modListItem, cursor int, showAnticheat bool) {
	if len(items) == 0 {
		b.WriteString("  No mods.\n")
		return
	}

	maxName := 0
	maxVer := 0
	for _, item := range items {
		if l := len(item.Name); l > maxName {
			maxName = l
		}
		v := item.Version
		if v == "" {
			v = "-"
		}
		if l := len(v); l > maxVer {
			maxVer = l
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

		pad := strings.Repeat(" ", maxName-len(item.Name)+2)

		version := item.Version
		if version == "" {
			version = "-"
		}

		if showAnticheat {
			verPad := strings.Repeat(" ", maxVer-len(version)+2)
			ac := "-"
			switch item.Anticheat {
			case "whitelist":
				ac = "\033[32mW\033[0m"
			case "greylist":
				ac = "\033[33mG\033[0m"
			}
			fmt.Fprintf(b, "  %s[%s] %s%s%s%s%s\n", cur, check, item.Name, pad, version, verPad, ac)
		} else if item.Update != "" {
			fmt.Fprintf(b, "  %s[%s] %s%s\033[33m%s → %s\033[0m\n", cur, check, item.Name, pad, version, item.Update)
		} else {
			fmt.Fprintf(b, "  %s[%s] %s%s%s\n", cur, check, item.Name, pad, version)
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

// renderHotkeyBar renders a hotkey hint bar that wraps to multiple lines
// when the terminal is too narrow to fit everything on one line.
func renderHotkeyBar(b *strings.Builder, items []string, width int) {
	if width <= 0 {
		width = 120
	}

	b.WriteString("  \033[2m")
	col := 2 // 2-space indent
	for i, item := range items {
		itemWidth := utf8.RuneCountInString(item)
		if i > 0 {
			if col+3+itemWidth > width {
				b.WriteString("\n  ")
				col = 2
			} else {
				b.WriteString(" • ")
				col += 3
			}
		}
		b.WriteString(item)
		col += itemWidth
	}
	b.WriteString("\033[0m\n\n")
}
