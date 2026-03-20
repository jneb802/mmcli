package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// confirmModal holds state for a generic y/n confirmation overlay.
// Zero value means inactive (Active == false).
type confirmModal struct {
	Active bool
	Prompt string
	Body   string // pre-rendered rich content (empty for simple confirms)
	Scroll int
	OnYes  func(m model) (tea.Model, tea.Cmd)
}

func (c confirmModal) View() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n  \033[33m%s\033[0m\n", c.Prompt)

	if c.Body != "" {
		lines := strings.Split(c.Body, "\n")
		visible := 20
		scroll := c.Scroll
		maxScroll := len(lines) - visible
		if maxScroll < 0 {
			maxScroll = 0
		}
		if scroll > maxScroll {
			scroll = maxScroll
		}
		end := scroll + visible
		if end > len(lines) {
			end = len(lines)
		}
		b.WriteString("\n")
		for _, line := range lines[scroll:end] {
			fmt.Fprintf(&b, "%s\n", line)
		}
		if len(lines) > visible {
			fmt.Fprintf(&b, "\n  \033[2m(%d more — ↑/↓ scroll)\033[0m\n", len(lines)-visible)
		}
	}

	b.WriteString("\n  \033[33my confirm • any key cancel\033[0m\n\n")
	return b.String()
}

func (m model) handleConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		cb := m.confirm.OnYes
		m.confirm = confirmModal{}
		if cb != nil {
			return cb(m)
		}
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.confirm.Scroll > 0 {
			m.confirm.Scroll--
		}
		return m, nil
	case "down", "j":
		if m.confirm.Body != "" {
			m.confirm.Scroll++
		}
		return m, nil
	default:
		m.confirm = confirmModal{}
		return m, nil
	}
}
