package tui

import (
	"bufio"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"mmcli/internal/agentapi"
	"mmcli/internal/client"
)

// Async message types for server tab.
type serverStatusMsg struct {
	status   *agentapi.StatusResponse
	mods     []agentapi.ModInfo
	modsResp *agentapi.ModListResponse
	players  []agentapi.PlayerInfo
	err      error
}

type serverActionMsg struct {
	action string
	resp   *agentapi.ActionResponse
	err    error
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

type serverLogLineMsg struct{ lines []string }
type serverLogDoneMsg struct{}

func nextServerLogLine(ch <-chan []string) tea.Cmd {
	return waitForLogLines(ch,
		func(lines []string) tea.Msg { return serverLogLineMsg{lines: lines} },
		func() tea.Msg { return serverLogDoneMsg{} },
	)
}

func (m *model) stopServerLogStream() {
	if m.server.logStop != nil {
		close(m.server.logStop)
		m.server.logStop = nil
		m.server.logCh = nil
		m.server.logs.active = false
	}
}

type serverModel struct {
	client     *client.AgentClient
	serverName string
	role       string

	status    *agentapi.StatusResponse
	statusErr error
	fetching  bool

	mods    []agentapi.ModInfo
	players []agentapi.PlayerInfo
	cursor  int

	actionBusy bool
	actionMsg  string

	logs     logViewerState
	logCh    <-chan []string
	logStop  chan struct{}
	modsResp *agentapi.ModListResponse

	statusCursor    int
	editingWebhook  bool
	webhookInput    string
	editingEmbedURL bool
	embedURLInput   string
	configFiles     []string
	configCursor    int

	settings       *agentapi.SettingsResponse
	settingsScroll int

	webhookCfg *agentapi.WebhookConfigResponse
	manifest   *agentapi.PushManifest // latest server manifest for reconciliation

	editor settingsEditor
}

func (m *model) loadServerLogs() tea.Cmd {
	m.stopServerLogStream()
	cmd, ch, stop := streamServerLogs(m.server.client)
	m.server.logCh = ch
	m.server.logStop = stop
	return cmd
}

func (m model) buildServerStatusItems() []settingsItem {
	var items []settingsItem

	// Server name
	items = append(items, settingsItem{
		label:   "Server",
		value:   m.server.serverName,
		tooltip: "Name of the linked remote server.",
	})

	// Role
	roleVal := "admin"
	if m.server.role == agentapi.RolePlayer {
		roleVal = "\033[33mplayer\033[0m"
	}
	items = append(items, settingsItem{
		label:   "Role",
		value:   roleVal,
		tooltip: "Your permission level. Admins can push mods, start/stop the server, and edit world settings.",
	})

	// Status
	var statusVal string
	if m.server.status != nil && m.server.status.Running {
		statusVal = fmt.Sprintf("\033[32mrunning\033[0m (%s)", m.server.status.Uptime)
	} else if m.server.statusErr != nil {
		statusVal = fmt.Sprintf("\033[31merror: %v\033[0m", m.server.statusErr)
	} else if m.server.fetching {
		statusVal = "\033[2mfetching...\033[0m"
	} else {
		statusVal = "\033[31mstopped\033[0m"
	}
	items = append(items, settingsItem{
		label:   "Status",
		value:   statusVal,
		tooltip: "Whether the Valheim dedicated server process is running.",
	})

	// Mod count
	items = append(items, settingsItem{
		label:   "Mods",
		value:   fmt.Sprintf("%d", len(m.server.mods)),
		tooltip: "Number of mods installed on the server from the last push.",
	})

	// Agent version
	agentVal := "\033[2m–\033[0m"
	if m.server.status != nil && m.server.status.Version != "" {
		agentVal = fmt.Sprintf("\033[36m%s\033[0m", m.server.status.Version)
	}
	items = append(items, settingsItem{
		label:   "Agent",
		value:   agentVal,
		tooltip: "Version of the mmcli agent running on the server.",
	})

	// MMCLI Server Mod
	serverModVer := ""
	for _, mod := range m.server.mods {
		lower := strings.ToLower(mod.Name)
		if strings.Contains(lower, "mmcliservermod") || strings.Contains(lower, "mmcli_servermod") || strings.Contains(lower, "mmcli-servermod") {
			serverModVer = mod.Version
			break
		}
	}
	var serverModVal string
	if m.server.modsResp != nil && m.server.modsResp.APIQueried {
		if serverModVer != "" {
			serverModVal = fmt.Sprintf("\033[32m%s\033[0m", serverModVer)
		} else {
			serverModVal = "\033[32minstalled\033[0m"
		}
	} else {
		serverModVal = "\033[2mnot installed\033[0m"
	}
	items = append(items, settingsItem{
		label:   "MMCLI Server Mod",
		value:   serverModVal,
		tooltip: "BepInEx plugin that exposes game state (day, time, players) to the agent API.",
	})

	// Player count
	var playerVal string
	if m.server.status != nil && m.server.status.Running && m.server.status.PlayerCount > 0 {
		playerVal = fmt.Sprintf("\033[32m%d online\033[0m", m.server.status.PlayerCount)
	} else if m.server.status != nil && m.server.status.Running {
		playerVal = "0 online"
	} else {
		playerVal = "\033[2m–\033[0m"
	}
	items = append(items, settingsItem{
		label:   "Players",
		value:   playerVal,
		tooltip: "Number of players currently connected to the server.",
	})

	// Game day & time
	var dayVal string
	if m.server.status != nil && m.server.status.Running && m.server.status.Day > 0 {
		dayNight := "Night"
		dayNightColor := "\033[34m"
		if m.server.status.IsDay != nil && *m.server.status.IsDay {
			dayNight = "Day"
			dayNightColor = "\033[33m"
		}
		gameTime := m.server.status.GameTime
		if gameTime == "" {
			gameTime = "–"
		}
		dayVal = fmt.Sprintf("Day %d  %s%s\033[0m  (%s)", m.server.status.Day, dayNightColor, dayNight, gameTime)
	} else if m.server.status != nil && m.server.status.Running {
		dayVal = "\033[2mloading...\033[0m"
	} else {
		dayVal = "\033[2m–\033[0m"
	}
	items = append(items, settingsItem{
		label:   "Game day",
		value:   dayVal,
		tooltip: "Current in-game day and time of day. Requires MMCLI Server Mod.",
	})

	// World
	worldVal := "\033[2m–\033[0m"
	if m.server.status != nil && m.server.status.World != "" {
		worldVal = m.server.status.World
	} else if m.server.settings != nil && m.server.settings.World != "" {
		worldVal = m.server.settings.World
	}
	items = append(items, settingsItem{
		label:   "World",
		value:   worldVal,
		tooltip: "Name of the active Valheim world save.",
	})

	// Last restart
	var restartVal string
	if m.server.status != nil && m.server.status.Running && m.server.status.UptimeSecs > 0 {
		restartTime := time.Now().Add(-time.Duration(m.server.status.UptimeSecs) * time.Second)
		restartVal = restartTime.Format("2006-01-02 15:04:05")
	} else {
		restartVal = "\033[2m–\033[0m"
	}
	items = append(items, settingsItem{
		label:   "Last restart",
		value:   restartVal,
		tooltip: "When the server process was last started, computed from uptime.",
	})

	return items
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
		var players []agentapi.PlayerInfo
		if playersResp, err := c.ListPlayers(); err == nil && playersResp != nil {
			players = playersResp.Players
		}
		return serverStatusMsg{status: status, mods: modsResp.Mods, modsResp: modsResp, players: players}
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

func setWebhookURL(c *client.AgentClient, url string) tea.Cmd {
	return func() tea.Msg {
		_, err := c.UpdateWebhookConfig(agentapi.WebhookConfigUpdate{
			URL: &url,
		})
		if err != nil {
			return serverActionMsg{action: "webhook", err: err}
		}
		// Re-fetch status to update the display
		return serverActionMsg{action: "webhook"}
	}
}

func setStatusEmbedURL(c *client.AgentClient, url string) tea.Cmd {
	return func() tea.Msg {
		_, err := c.UpdateWebhookConfig(agentapi.WebhookConfigUpdate{
			StatusEmbedURL: &url,
		})
		if err != nil {
			return serverActionMsg{action: "webhook", err: err}
		}
		return serverActionMsg{action: "webhook"}
	}
}

func streamServerLogs(c *client.AgentClient) (tea.Cmd, <-chan []string, chan struct{}) {
	ch := make(chan []string, 16)
	stop := make(chan struct{})

	// Start background goroutine
	initCmd := func() tea.Msg {
		body, err := c.Logs(0, true)
		if err != nil {
			close(ch)
			return serverLogsMsg{err: err}
		}

		// Read initial + stream in background
		go func() {
			defer body.Close()
			defer close(ch)
			scanner := bufio.NewScanner(body)
			scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)
			for scanner.Scan() {
				select {
				case <-stop:
					return
				default:
				}
				select {
				case ch <- []string{scanner.Text()}:
				case <-stop:
					return
				}
			}
		}()

		// Drain all immediately-available batches for initial display
		lines, ok := <-ch
		if !ok {
			return serverLogsMsg{lines: nil}
		}
		for {
			select {
			case more, ok := <-ch:
				if !ok {
					return serverLogsMsg{lines: lines}
				}
				lines = append(lines, more...)
			default:
				return serverLogsMsg{lines: lines}
			}
		}
	}

	return initCmd, ch, stop
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

type webhookCfgMsg struct {
	cfg *agentapi.WebhookConfigResponse
	err error
}

func fetchWebhookConfig(c *client.AgentClient) tea.Cmd {
	return func() tea.Msg {
		cfg, err := c.GetWebhookConfig()
		return webhookCfgMsg{cfg: cfg, err: err}
	}
}

// fetchLaunchConfigsForEditor loads the active launch config name when opening the editor.
type editorLCInfoMsg struct {
	active string
	err    error
}

func fetchLaunchConfigsForEditor(c *client.AgentClient) tea.Cmd {
	return func() tea.Msg {
		resp, err := c.ListLaunchConfigs()
		if err != nil {
			return editorLCInfoMsg{err: err}
		}
		return editorLCInfoMsg{active: resp.Active}
	}
}

func serverTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg {
		return serverTickMsg{}
	})
}

func renderSettingsView(b *strings.Builder, s *agentapi.SettingsResponse, scroll int, role string) {
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
		lines = append(lines, fmt.Sprintf("    %-18s \033[32myes\033[0m", "Crossplay:"))
	} else {
		lines = append(lines, fmt.Sprintf("    %-18s \033[2mno\033[0m", "Crossplay:"))
	}
	if s.Preset != "" {
		lines = append(lines, fmt.Sprintf("    %-18s %s", "Preset:", s.Preset))
	} else {
		lines = append(lines, fmt.Sprintf("    %-18s \033[2mnone\033[0m", "Preset:"))
	}
	for _, mod := range []struct{ key, label string }{
		{"combat", "Combat"},
		{"deathpenalty", "Death Penalty"},
		{"resources", "Resources"},
		{"raids", "Raids"},
		{"portals", "Portals"},
	} {
		if v, ok := s.Modifiers[mod.key]; ok {
			lines = append(lines, fmt.Sprintf("    %-18s %s", mod.label+":", v))
		} else {
			lines = append(lines, fmt.Sprintf("    %-18s \033[2mnot set\033[0m", mod.label+":"))
		}
	}
	setKeys := make(map[string]bool)
	for _, k := range s.SetKeys {
		setKeys[k] = true
	}
	for _, k := range []string{"nobuildcost", "playerevents", "passivemobs", "nomap"} {
		if setKeys[k] {
			lines = append(lines, fmt.Sprintf("    %-18s \033[32mon\033[0m", k+":"))
		} else {
			lines = append(lines, fmt.Sprintf("    %-18s \033[2moff\033[0m", k+":"))
		}
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
	hints := "↑/↓ scroll • esc back"
	if role == agentapi.RoleAdmin {
		hints = "e edit • ↑/↓ scroll • esc back"
	}
	fmt.Fprintf(b, "\n  \033[2m%s\033[0m\n\n", hints)
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
