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

type serverSettingsMsg struct {
	settings *agentapi.SettingsResponse
	err      error
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

	settings        *agentapi.SettingsResponse
	settingsVisible bool
	settingsScroll  int
}

func (m model) handleServerNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Settings viewer mode
	if m.server.settingsVisible {
		switch msg.String() {
		case "q", "esc":
			m.server.settingsVisible = false
		case "up", "k":
			if m.server.settingsScroll > 0 {
				m.server.settingsScroll--
			}
		case "down", "j":
			m.server.settingsScroll++
		case "ctrl+c":
			return m, tea.Quit
		}
		return m, nil
	}

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
	case "w":
		m.server.actionBusy = true
		m.server.actionMsg = "Fetching settings..."
		return m, fetchSettings(m.server.client)
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

	// Settings viewer
	if m.server.settingsVisible && m.server.settings != nil {
		renderSettingsView(&b, m.server.settings, m.server.settingsScroll)
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
		item := modListItem{
			Name:     mod.Name,
			Disabled: mod.Disabled,
		}
		if regMod, ok := m.reg.GetMod(m.cfg.ActiveProfile, mod.Name); ok {
			item.Anticheat = regMod.Anticheat
		}
		items[i] = item
	}
	renderModList(&b, items, m.server.cursor, true)

	// Status bar
	b.WriteString("\n")
	if m.server.statusErr != nil {
		fmt.Fprintf(&b, "  \033[31mError: %v\033[0m\n", m.server.statusErr)
	}
	b.WriteString("  \033[2ms start • d stop • r restart • p push • l logs • w settings • tab local • q quit\033[0m\n\n")

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

func fetchSettings(c *client.AgentClient) tea.Cmd {
	return func() tea.Msg {
		settings, err := c.GetSettings()
		if err != nil {
			return serverSettingsMsg{err: err}
		}
		return serverSettingsMsg{settings: settings}
	}
}

func serverTick() tea.Cmd {
	return tea.Tick(30*time.Second, func(time.Time) tea.Msg {
		return serverTickMsg{}
	})
}

func renderSettingsView(b *strings.Builder, s *agentapi.SettingsResponse, scroll int) {
	var lines []string
	lines = append(lines, "")
	lines = append(lines, "  \033[1mServer World Settings\033[0m")
	lines = append(lines, "")

	// Core
	lines = append(lines, "  \033[36mCore\033[0m")
	lines = append(lines, fmt.Sprintf("    Name:       %s", s.Name))
	lines = append(lines, fmt.Sprintf("    Port:       %d", s.Port))
	lines = append(lines, fmt.Sprintf("    World:      %s", s.World))
	lines = append(lines, "    Password:   ***")
	if s.Public == 1 {
		lines = append(lines, "    Public:     \033[32myes\033[0m")
	} else {
		lines = append(lines, "    Public:     \033[2mno\033[0m")
	}
	lines = append(lines, fmt.Sprintf("    Save Dir:   %s", s.SaveDir))
	if s.LogFile != "" {
		lines = append(lines, fmt.Sprintf("    Log File:   %s", s.LogFile))
	}
	if s.InstanceID != "" {
		lines = append(lines, fmt.Sprintf("    Instance:   %s", s.InstanceID))
	}
	lines = append(lines, "")

	// Backup
	lines = append(lines, "  \033[36mBackup\033[0m")
	lines = append(lines, fmt.Sprintf("    Save Interval:   %s", settingSeconds(s.SaveInterval, 1800)))
	lines = append(lines, fmt.Sprintf("    Backups:         %s", settingDefault(s.Backups, 4)))
	lines = append(lines, fmt.Sprintf("    Short Interval:  %s", settingSeconds(s.BackupShort, 7200)))
	lines = append(lines, fmt.Sprintf("    Long Interval:   %s", settingSeconds(s.BackupLong, 43200)))
	lines = append(lines, "")

	// World
	lines = append(lines, "  \033[36mWorld\033[0m")
	if s.Crossplay {
		lines = append(lines, "    Crossplay:  \033[32myes\033[0m")
	} else {
		lines = append(lines, "    Crossplay:  \033[2mno\033[0m")
	}
	if s.Preset != "" {
		lines = append(lines, fmt.Sprintf("    Preset:     %s", s.Preset))
	} else {
		lines = append(lines, "    Preset:     \033[2mnone\033[0m")
	}
	if len(s.Modifiers) > 0 {
		lines = append(lines, "    Modifiers:")
		for k, v := range s.Modifiers {
			lines = append(lines, fmt.Sprintf("      %-14s %s", k, v))
		}
	}
	if len(s.SetKeys) > 0 {
		keys := ""
		for i, k := range s.SetKeys {
			if i > 0 {
				keys += ", "
			}
			keys += k
		}
		lines = append(lines, fmt.Sprintf("    Keys:       %s", keys))
	}
	lines = append(lines, "")

	// Permissions
	if len(s.Admins) > 0 || len(s.Banned) > 0 || len(s.Permitted) > 0 {
		lines = append(lines, "  \033[36mPermissions\033[0m")
		if len(s.Admins) > 0 {
			lines = append(lines, fmt.Sprintf("    Admins:     %d entries", len(s.Admins)))
		}
		if len(s.Banned) > 0 {
			lines = append(lines, fmt.Sprintf("    Banned:     %d entries", len(s.Banned)))
		}
		if len(s.Permitted) > 0 {
			lines = append(lines, fmt.Sprintf("    Permitted:  %d entries", len(s.Permitted)))
		}
		lines = append(lines, "")
	}

	// Scrollable output
	visible := 30
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
	for _, line := range lines[scroll:end] {
		fmt.Fprintf(b, "%s\n", line)
	}
	b.WriteString("\n  \033[2m↑/↓ scroll • esc back\033[0m\n\n")
}

func settingSeconds(val, defaultVal int) string {
	if val == 0 {
		return fmt.Sprintf("\033[2mnot set (default: %s)\033[0m", tuiHumanDuration(defaultVal))
	}
	s := tuiHumanDuration(val)
	if val == defaultVal {
		return fmt.Sprintf("%s \033[2m(default)\033[0m", s)
	}
	return s
}

func settingDefault(val, defaultVal int) string {
	if val == 0 {
		return fmt.Sprintf("\033[2mnot set (default: %d)\033[0m", defaultVal)
	}
	if val == defaultVal {
		return fmt.Sprintf("%d \033[2m(default)\033[0m", val)
	}
	return fmt.Sprintf("%d", val)
}

func tuiHumanDuration(seconds int) string {
	if seconds >= 3600 && seconds%3600 == 0 {
		return fmt.Sprintf("%dh", seconds/3600)
	}
	if seconds >= 60 && seconds%60 == 0 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	return fmt.Sprintf("%ds", seconds)
}
