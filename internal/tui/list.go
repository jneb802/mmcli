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

// anticheatLabel returns the short display character for an anticheat value.
func anticheatLabel(value, system string) string {
	switch value {
	case "whitelist":
		if system == "enforcer" {
			return "\033[32mR\033[0m"
		}
		return "\033[32mW\033[0m"
	case "greylist":
		if system == "enforcer" {
			return "\033[33mO\033[0m"
		}
		return "\033[33mG\033[0m"
	case "adminonly":
		return "\033[35mA\033[0m"
	default:
		return "-"
	}
}

// nextAnticheatValue cycles to the next anticheat classification for the given system.
func nextAnticheatValue(current, system string) string {
	var values []string
	if system == "azu" {
		values = []string{"whitelist", "greylist", ""}
	} else {
		values = []string{"whitelist", "greylist", "adminonly", ""}
	}
	for i, v := range values {
		if v == current {
			return values[(i+1)%len(values)]
		}
	}
	return values[0]
}

// renderModList renders a list of mods with cursor, check/x, name, version, and update indicators.
// If showAnticheat is true, an anticheat column is shown after version.
func renderModList(b *strings.Builder, items []modListItem, cursor int, showAnticheat bool, anticheatSystem string) {
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
			ac := anticheatLabel(item.Anticheat, anticheatSystem)
			fmt.Fprintf(b, "  %s[%s] %s%s%s%s%s\n", cur, check, item.Name, pad, version, verPad, ac)
		} else if item.Update != "" {
			fmt.Fprintf(b, "  %s[%s] %s%s\033[33m%s → %s\033[0m\n", cur, check, item.Name, pad, version, item.Update)
		} else {
			fmt.Fprintf(b, "  %s[%s] %s%s%s\n", cur, check, item.Name, pad, version)
		}
	}
}

// renderSyncModList renders a mod list with dual local/server version columns and diff status.
func renderSyncModList(b *strings.Builder, items []modListItem, cursor int) {
	if len(items) == 0 {
		b.WriteString("  No mods.\n")
		return
	}

	maxName := 0
	maxLocal := len("Local")
	maxServer := len("Server")
	for _, item := range items {
		if l := len(item.Name); l > maxName {
			maxName = l
		}
		v := item.Version
		if v == "" {
			v = "—"
		}
		if l := len(v); l > maxLocal {
			maxLocal = l
		}
		sv := item.ServerVersion
		if sv == "" && item.Status != "added" {
			sv = item.Version // unchanged items have same version on both sides
		}
		if sv == "" {
			sv = "—"
		}
		if l := len(sv); l > maxServer {
			maxServer = l
		}
	}

	// Header
	namePad := strings.Repeat(" ", maxName-len("Name")+2)
	localPad := strings.Repeat(" ", maxLocal-len("Local")+2)
	serverPad := strings.Repeat(" ", maxServer-len("Server")+2)
	fmt.Fprintf(b, "  \033[2m    Name%sLocal%sServer%sStatus\033[0m\n", namePad, localPad, serverPad)

	for i, item := range items {
		cur := "  "
		if i == cursor {
			cur = "\033[36m>\033[0m "
		}

		pad := strings.Repeat(" ", maxName-len(item.Name)+2)

		localVer := item.Version
		if localVer == "" {
			localVer = "—"
		}
		lPad := strings.Repeat(" ", maxLocal-len(localVer)+2)

		// Determine server version
		serverVer := item.ServerVersion
		if item.Status == "" {
			// Unchanged — server has same version
			serverVer = item.Version
		}
		if item.Status == "added" {
			serverVer = "—"
		}
		if serverVer == "" {
			serverVer = "—"
		}
		sPad := strings.Repeat(" ", maxServer-len(serverVer)+2)

		switch item.Status {
		case "added":
			fmt.Fprintf(b, "  %s\033[32m%s%s%s%s%s%s+ added\033[0m\n", cur, item.Name, pad, localVer, lPad, serverVer, sPad)
		case "removed":
			fmt.Fprintf(b, "  %s\033[31m%s%s%s%s%s%s- removed\033[0m\n", cur, item.Name, pad, localVer, lPad, serverVer, sPad)
		case "changed":
			fmt.Fprintf(b, "  %s\033[33m%s%s%s%s%s%s~ changed\033[0m\n", cur, item.Name, pad, localVer, lPad, serverVer, sPad)
		default:
			fmt.Fprintf(b, "  %s\033[2m%s%s%s%s%s%s✓\033[0m\n", cur, item.Name, pad, localVer, lPad, serverVer, sPad)
		}
	}
}

// logViewerState holds shared log viewer state used by both tabs.
type logViewerState struct {
	active    bool
	lines     []string
	scroll    int
	title     string
	visible   int
	following bool // auto-scroll to bottom on new lines
	live      bool // show LIVE indicator
}

func newLogViewerState(title string, lines []string, live bool) logViewerState {
	visible := 30
	maxScroll := max(0, len(lines)-visible)
	return logViewerState{
		active:    true,
		lines:     lines,
		scroll:    maxScroll, // start at bottom
		title:     title,
		visible:   visible,
		following: live,
		live:      live,
	}
}

func renderLogViewer(b *strings.Builder, lv logViewerState) {
	liveTag := ""
	if lv.live {
		liveTag = "  \033[32mLIVE\033[0m"
	}
	fmt.Fprintf(b, "\n  \033[1m%s\033[0m%s\n\n", lv.title, liveTag)
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
	hints := "↑/↓ scroll • esc back"
	if lv.live && !lv.following {
		hints = "↑/↓ scroll • f follow • esc back"
	}
	fmt.Fprintf(b, "\n  \033[2m%s\033[0m\n\n", hints)
}

// waitForLogLines returns a tea.Cmd that blocks on a channel for new log lines.
func waitForLogLines(ch <-chan []string, wrap func([]string) tea.Msg, done func() tea.Msg) tea.Cmd {
	return func() tea.Msg {
		lines, ok := <-ch
		if !ok {
			return done()
		}
		return wrap(lines)
	}
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
