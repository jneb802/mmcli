package tui

import (
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"mmcli/internal/agentapi"
	"mmcli/internal/client"
	"mmcli/internal/config"
	"mmcli/internal/profile"
)

// Async message types for server tab.
type serverStatusMsg struct {
	status *agentapi.StatusResponse
	mods   []agentapi.ModInfo
	err    error
}

type serverActionMsg struct {
	action string
	resp   *agentapi.ActionResponse
	err    error
}

type serverPushMsg struct {
	resp *agentapi.ActionResponse
	err  error
}

type serverLogsMsg struct {
	lines []string
	err   error
}

type serverTickMsg struct{}

type serverModel struct {
	client     *client.AgentClient
	serverName string

	status    *agentapi.StatusResponse
	statusErr error
	fetching  bool

	mods   []agentapi.ModInfo
	cursor int

	actionBusy bool
	actionMsg  string

	logs logViewerState
}

func (m model) handleServerNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Log viewer mode
	if m.server.logs.active {
		if !handleLogViewerKeys(&m.server.logs, msg) {
			return m, tea.Quit
		}
		return m, nil
	}

	// Action busy — only allow quit
	if m.server.actionBusy {
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}

	// No server configured
	if m.server.client == nil {
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.activeTab = tabLocal
			return m, nil
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "tab":
		m.activeTab = tabLocal
		return m, nil
	case "up", "k":
		if m.server.cursor > 0 {
			m.server.cursor--
		}
	case "down", "j":
		if m.server.cursor < len(m.server.mods)-1 {
			m.server.cursor++
		}
	case "s":
		m.server.actionBusy = true
		m.server.actionMsg = "Starting server..."
		return m, serverAction(m.server.client, "start")
	case "d":
		m.server.actionBusy = true
		m.server.actionMsg = "Stopping server..."
		return m, serverAction(m.server.client, "stop")
	case "r":
		m.server.actionBusy = true
		m.server.actionMsg = "Restarting server..."
		return m, serverAction(m.server.client, "restart")
	case "p":
		m.server.actionBusy = true
		m.server.actionMsg = "Pushing mods..."
		return m, pushMods(m.server.client, m.paths, m.cfg, *m.reg)
	case "l":
		m.server.actionBusy = true
		m.server.actionMsg = "Fetching logs..."
		return m, fetchLogs(m.server.client)
	}
	return m, nil
}

func (m model) viewServer() string {
	var b strings.Builder

	// No server configured
	if m.server.client == nil {
		b.WriteString("\n  No server configured.\n")
		b.WriteString("  Run \033[36mmmcli server add\033[0m to register one.\n\n")
		b.WriteString("  \033[2mtab local • q quit\033[0m\n\n")
		return b.String()
	}

	// Log viewer
	if m.server.logs.active {
		renderLogViewer(&b, m.server.logs)
		return b.String()
	}

	// Action busy
	if m.server.actionBusy {
		fmt.Fprintf(&b, "\n  \033[33m%s\033[0m\n\n", m.server.actionMsg)
		return b.String()
	}

	// Server status header
	statusText := "\033[31mstopped\033[0m"
	if m.server.status != nil && m.server.status.Running {
		statusText = fmt.Sprintf("\033[32mrunning\033[0m (%s)", m.server.status.Uptime)
	}
	if m.server.statusErr != nil {
		statusText = fmt.Sprintf("\033[31merror: %v\033[0m", m.server.statusErr)
	}
	if m.server.fetching {
		statusText = "\033[2mfetching...\033[0m"
	}

	modCount := len(m.server.mods)
	fmt.Fprintf(&b, "\n  Server: \033[1m%s\033[0m    Status: %s    Mods: %d\n\n", m.server.serverName, statusText, modCount)

	// Mod list
	items := make([]modListItem, len(m.server.mods))
	for i, mod := range m.server.mods {
		items[i] = modListItem{
			Name:     mod.Name,
			Disabled: mod.Disabled,
		}
	}
	renderModList(&b, items, m.server.cursor)

	// Status bar
	b.WriteString("\n")
	if m.server.statusErr != nil {
		fmt.Fprintf(&b, "  \033[31mError: %v\033[0m\n", m.server.statusErr)
	}
	b.WriteString("  \033[2ms start • d stop • r restart • p push • l logs • tab local • q quit\033[0m\n\n")

	return b.String()
}

// --- Async commands ---

func fetchServerStatus(c *client.AgentClient) tea.Cmd {
	return func() tea.Msg {
		status, err := c.Status()
		if err != nil {
			return serverStatusMsg{err: err}
		}
		modsResp, err := c.ListMods()
		if err != nil {
			return serverStatusMsg{status: status, err: err}
		}
		return serverStatusMsg{status: status, mods: modsResp.Mods}
	}
}

func serverAction(c *client.AgentClient, action string) tea.Cmd {
	return func() tea.Msg {
		var resp *agentapi.ActionResponse
		var err error
		switch action {
		case "start":
			resp, err = c.Start()
		case "stop":
			resp, err = c.Stop()
		case "restart":
			resp, err = c.Restart()
		}
		return serverActionMsg{action: action, resp: resp, err: err}
	}
}

func pushMods(c *client.AgentClient, paths config.Paths, cfg config.Config, reg config.Registry) tea.Cmd {
	return func() tea.Msg {
		pr, pw := io.Pipe()
		errCh := make(chan error, 1)
		go func() {
			errCh <- profile.BuildProfileArchive(pw, paths, cfg.ActiveProfile, reg)
			pw.Close()
		}()

		resp, err := c.PushMods(pr, false)
		if archiveErr := <-errCh; archiveErr != nil {
			return serverPushMsg{err: archiveErr}
		}
		return serverPushMsg{resp: resp, err: err}
	}
}

func fetchLogs(c *client.AgentClient) tea.Cmd {
	return func() tea.Msg {
		body, err := c.Logs(200, false)
		if err != nil {
			return serverLogsMsg{err: err}
		}
		defer body.Close()
		data, err := io.ReadAll(body)
		if err != nil {
			return serverLogsMsg{err: err}
		}
		lines := strings.Split(string(data), "\n")
		return serverLogsMsg{lines: lines}
	}
}

func serverTick() tea.Cmd {
	return tea.Tick(30*time.Second, func(time.Time) tea.Msg {
		return serverTickMsg{}
	})
}
