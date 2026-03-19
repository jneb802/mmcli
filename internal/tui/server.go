package tui

import (
	"bufio"
	"fmt"
	"sort"
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
	status   *agentapi.StatusResponse
	mods     []agentapi.ModInfo
	modsResp *agentapi.ModListResponse
	err      error
}

type serverActionMsg struct {
	action string
	resp   *agentapi.ActionResponse
	err    error
}

type serverPushMsg struct {
	resp *agentapi.SyncResponse
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

	mods   []agentapi.ModInfo
	cursor int

	actionBusy bool
	actionMsg  string

	confirmPush    bool
	pushItems      []modListItem
	pushScroll     int
	lastPush        *agentapi.SyncResponse
	lastPushTime    time.Time
	pushDetail      bool // push detail view open
	pushDetailScroll int
	confirmStart    bool
	confirmStop    bool
	confirmRestart bool

	logs     logViewerState
	logCh    <-chan []string
	logStop  chan struct{}
	modsResp *agentapi.ModListResponse

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
		if !m.server.logs.active {
			m.stopServerLogStream()
		}
		return m, nil
	}

	// Push detail view
	if m.server.pushDetail {
		switch msg.String() {
		case "q", "esc":
			m.server.pushDetail = false
			m.server.pushDetailScroll = 0
		case "up", "k":
			if m.server.pushDetailScroll > 0 {
				m.server.pushDetailScroll--
			}
		case "down", "j":
			m.server.pushDetailScroll++
		case "r":
			if m.server.role == agentapi.RoleAdmin && pushNeedsRestart(m.server.lastPush) {
				m.server.confirmRestart = true
			}
		case "ctrl+c":
			return m, tea.Quit
		}
		return m, nil
	}

	// Push confirmation
	if m.server.confirmPush {
		switch msg.String() {
		case "y":
			m.server.confirmPush = false
			m.server.actionBusy = true
			m.server.actionMsg = "Pushing mods..."
			return m, pushMods(m.server.client, m.paths, m.cfg, *m.reg)
		case "up", "k":
			if m.server.pushScroll > 0 {
				m.server.pushScroll--
			}
		case "down", "j":
			m.server.pushScroll++
		case "ctrl+c":
			return m, tea.Quit
		default:
			m.server.confirmPush = false
		}
		return m, nil
	}

	// Start confirmation (shown when server is already running)
	if m.server.confirmStart {
		switch msg.String() {
		case "y":
			m.server.confirmStart = false
			m.server.actionBusy = true
			m.server.actionMsg = "Starting server..."
			return m, serverAction(m.server.client, "start")
		case "ctrl+c":
			return m, tea.Quit
		default:
			m.server.confirmStart = false
		}
		return m, nil
	}

	// Stop confirmation
	if m.server.confirmStop {
		switch msg.String() {
		case "y":
			m.server.confirmStop = false
			m.server.actionBusy = true
			m.server.actionMsg = "Stopping server..."
			return m, serverAction(m.server.client, "stop")
		case "ctrl+c":
			return m, tea.Quit
		default:
			m.server.confirmStop = false
		}
		return m, nil
	}

	// Restart confirmation
	if m.server.confirmRestart {
		switch msg.String() {
		case "y":
			m.server.confirmRestart = false
			m.server.actionBusy = true
			m.server.actionMsg = "Restarting server..."
			return m, serverAction(m.server.client, "restart")
		case "ctrl+c":
			return m, tea.Quit
		default:
			m.server.confirmRestart = false
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
		m.stopServerLogStream()
		m.activeTab = tabLocal
		return m, tea.Batch(checkGameRunning(), localTick())
	case "up", "k":
		if m.server.cursor > 0 {
			m.server.cursor--
		} else if m.server.cursor == 0 && m.server.lastPush != nil {
			m.server.cursor = -1
		}
	case "down", "j":
		if m.server.cursor == -1 {
			m.server.cursor = 0
		} else if m.server.cursor < len(m.server.mods)-1 {
			m.server.cursor++
		}
	case "enter", " ":
		if m.server.cursor == -1 && m.server.lastPush != nil {
			m.server.pushDetail = true
			m.server.pushDetailScroll = 0
			return m, nil
		}
	case "s":
		if m.server.role != agentapi.RoleAdmin {
			return m, nil
		}
		if m.server.status != nil && m.server.status.Running {
			m.server.confirmStart = true
			return m, nil
		}
		m.server.actionBusy = true
		m.server.actionMsg = "Starting server..."
		return m, serverAction(m.server.client, "start")
	case "d":
		if m.server.role != agentapi.RoleAdmin {
			return m, nil
		}
		m.server.confirmStop = true
		return m, nil
	case "r":
		if m.server.role != agentapi.RoleAdmin {
			return m, nil
		}
		m.server.confirmRestart = true
		return m, nil
	case "p":
		if m.server.role != agentapi.RoleAdmin {
			return m, nil
		}
		items := buildPushItems(m.cfg, m.reg, m.server.mods)
		if len(items) == 0 {
			m.server.statusErr = fmt.Errorf("no mods to push")
			return m, nil
		}
		m.server.confirmPush = true
		m.server.pushItems = items
		m.server.pushScroll = 0
		return m, nil
	case "l":
		m.stopServerLogStream()
		m.server.actionBusy = true
		m.server.actionMsg = "Fetching logs..."
		cmd, ch, stop := streamServerLogs(m.server.client)
		m.server.logCh = ch
		m.server.logStop = stop
		return m, cmd
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

	// Push detail view
	if m.server.pushDetail && m.server.lastPush != nil {
		renderPushDetail(&b, m.server.lastPush, m.server.lastPushTime, m.server.pushDetailScroll, m.server.role)
		return b.String()
	}

	// Push confirmation
	if m.server.confirmPush {
		renderPushConfirm(&b, m.server.serverName, m.cfg.ActiveProfile, m.server.pushItems, m.server.pushScroll, m.server.status)
		return b.String()
	}

	// Start confirmation (server already running)
	if m.server.confirmStart {
		fmt.Fprintf(&b, "\n  \033[33mServer is already running. Restart %s? (y/n)\033[0m\n\n", m.server.serverName)
		return b.String()
	}

	// Stop confirmation
	if m.server.confirmStop {
		fmt.Fprintf(&b, "\n  \033[33mStop server %s? (y/n)\033[0m\n\n", m.server.serverName)
		return b.String()
	}

	// Restart confirmation
	if m.server.confirmRestart {
		fmt.Fprintf(&b, "\n  \033[33mRestart server %s? (y/n)\033[0m\n\n", m.server.serverName)
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
	roleTag := ""
	if m.server.role == agentapi.RolePlayer {
		roleTag = " \033[33m(player)\033[0m"
	}
	fmt.Fprintf(&b, "\n  Server: \033[1m%s\033[0m%s    Status: %s    Mods: %d\n", m.server.serverName, roleTag, statusText, modCount)
	// Push status line
	if m.server.lastPush != nil {
		lp := m.server.lastPush
		ago := time.Since(m.server.lastPushTime).Truncate(time.Second)
		var parts []string
		if lp.Downloaded > 0 {
			parts = append(parts, fmt.Sprintf("%d downloaded", lp.Downloaded))
		}
		if lp.Cached > 0 {
			parts = append(parts, fmt.Sprintf("%d cached", lp.Cached))
		}
		if lp.Removed > 0 {
			parts = append(parts, fmt.Sprintf("%d removed", lp.Removed))
		}
		if lp.Skipped > 0 {
			parts = append(parts, fmt.Sprintf("%d unchanged", lp.Skipped))
		}
		if len(lp.Failures) > 0 {
			parts = append(parts, fmt.Sprintf("\033[31m%d failed\033[0m", len(lp.Failures)))
		}
		summary := strings.Join(parts, ", ")

		if m.server.cursor == -1 {
			fmt.Fprintf(&b, "  \033[36m>\033[0m Last push: %s ago — %s    \033[33menter details\033[0m\n", ago, summary)
		} else {
			fmt.Fprintf(&b, "    Last push: \033[2m%s ago — %s\033[0m\n", ago, summary)
		}

	} else if m.server.modsResp != nil && m.server.modsResp.ManifestTime != "" {
		fmt.Fprintf(&b, "    Last push: \033[2m%s\033[0m\n", m.server.modsResp.ManifestTime)
	}
	b.WriteString("\n")

	// Mod list — use server-side data (manifest + log enrichment)
	items := make([]modListItem, len(m.server.mods))
	for i, mod := range m.server.mods {
		item := modListItem{
			Name:      mod.Name,
			Version:   mod.Version,
			Disabled:  mod.Disabled,
			Anticheat: mod.Anticheat,
		}
		// Fallback to local registry for anticheat if not in manifest
		if item.Anticheat == "" {
			if regMod, ok := m.reg.GetMod(m.cfg.ActiveProfile, mod.Name); ok {
				item.Anticheat = regMod.Anticheat
			}
		}
		items[i] = item
	}
	renderModList(&b, items, m.server.cursor, true)

	// Status bar
	b.WriteString("\n")
	if m.server.statusErr != nil {
		fmt.Fprintf(&b, "  \033[31mError: %v\033[0m\n", m.server.statusErr)
	}
	var hotkeys []string
	if m.server.role == agentapi.RoleAdmin {
		hotkeys = []string{"s start", "d stop", "r restart", "p push", "l logs", "w settings", "tab local", "q quit"}
	} else {
		hotkeys = []string{"l logs", "w settings", "tab local", "q quit"}
	}
	renderHotkeyBar(&b, hotkeys, m.width)

	return b.String()
}

// pushNeedsRestart returns true if the push result has actual changes and no failures.
func pushNeedsRestart(lp *agentapi.SyncResponse) bool {
	hasChanges := lp.Downloaded > 0 || lp.Cached > 0 || lp.Removed > 0
	return hasChanges && len(lp.Failures) == 0
}

func renderPushDetail(b *strings.Builder, lp *agentapi.SyncResponse, pushTime time.Time, scroll int, role string) {
	ago := time.Since(pushTime).Truncate(time.Second)
	fmt.Fprintf(b, "\n  \033[1mLast Push\033[0m  \033[2m%s ago\033[0m\n", ago)

	total := lp.Downloaded + lp.Cached + lp.Skipped
	fmt.Fprintf(b, "  %d mods synced", total)
	if lp.Removed > 0 {
		fmt.Fprintf(b, ", %d removed", lp.Removed)
	}
	if len(lp.Failures) > 0 {
		fmt.Fprintf(b, ", \033[31m%d failed\033[0m", len(lp.Failures))
	}
	b.WriteString("\n\n")

	// Per-mod results
	if len(lp.Results) > 0 {
		visible := 25
		maxScroll := len(lp.Results) - visible
		if maxScroll < 0 {
			maxScroll = 0
		}
		if scroll > maxScroll {
			scroll = maxScroll
		}
		end := scroll + visible
		if end > len(lp.Results) {
			end = len(lp.Results)
		}

		maxName := 0
		for _, r := range lp.Results {
			if l := len(r.Mod); l > maxName {
				maxName = l
			}
		}

		for _, r := range lp.Results[scroll:end] {
			pad := strings.Repeat(" ", maxName-len(r.Mod)+2)
			switch r.Status {
			case "downloaded":
				fmt.Fprintf(b, "    \033[32m↓\033[0m %s%s%s  \033[32mdownloaded\033[0m\n", r.Mod, pad, r.Version)
			case "cached":
				fmt.Fprintf(b, "    \033[36m↓\033[0m %s%s%s  \033[36mcached\033[0m\n", r.Mod, pad, r.Version)
			case "skipped":
				fmt.Fprintf(b, "    \033[2m· %s%s%s  unchanged\033[0m\n", r.Mod, pad, r.Version)
			case "removed":
				fmt.Fprintf(b, "    \033[31m✗ %s%s     removed\033[0m\n", r.Mod, pad)
			case "failed":
				fmt.Fprintf(b, "    \033[31m✗ %s%s     %s\033[0m\n", r.Mod, pad, r.Reason)
			}
		}

		if len(lp.Results) > visible {
			fmt.Fprintf(b, "\n  \033[2m(%d more — ↑/↓ scroll)\033[0m\n", len(lp.Results)-visible)
		}
	}

	// Hotkeys
	hints := []string{"esc back"}
	if role == agentapi.RoleAdmin && pushNeedsRestart(lp) {
		hints = append(hints, "r restart server")
	}
	fmt.Fprintf(b, "\n  \033[2m%s\033[0m\n\n", strings.Join(hints, " • "))
}

func buildPushItems(cfg config.Config, reg *config.Registry, serverMods []agentapi.ModInfo) []modListItem {
	// Build local mod list (non-client)
	var items []modListItem
	localSet := make(map[string]bool)
	for _, mod := range reg.ListMods(cfg.ActiveProfile) {
		if mod.ResolvedTarget() == "client" {
			continue
		}
		localSet[mod.FullName()] = true
		items = append(items, modListItem{
			Name:      mod.FullName(),
			Version:   mod.Version,
			Disabled:  mod.Disabled,
			Anticheat: mod.Anticheat,
		})
	}

	// If we have server mods, compute diff
	if len(serverMods) > 0 {
		serverMap := make(map[string]agentapi.ModInfo)
		for _, sm := range serverMods {
			serverMap[sm.Name] = sm
		}

		// Tag local items
		for i := range items {
			sm, onServer := serverMap[items[i].Name]
			if !onServer {
				items[i].Status = "added"
			} else if sm.Version != "" && items[i].Version != "" && sm.Version != items[i].Version {
				items[i].Status = "changed"
				items[i].ServerVersion = sm.Version
			}
		}

		// Add removed items (on server but not local)
		for _, sm := range serverMods {
			if !localSet[sm.Name] {
				items = append(items, modListItem{
					Name:      sm.Name,
					Version:   sm.Version,
					Anticheat: sm.Anticheat,
					Status:    "removed",
				})
			}
		}

		// Sort: changes first (added, removed, changed), then unchanged
		statusOrder := map[string]int{"added": 0, "removed": 1, "changed": 2, "": 3}
		sort.SliceStable(items, func(i, j int) bool {
			return statusOrder[items[i].Status] < statusOrder[items[j].Status]
		})
	}

	return items
}

func renderPushConfirm(b *strings.Builder, serverName, profileName string, items []modListItem, scroll int, status *agentapi.StatusResponse) {
	fmt.Fprintf(b, "\n  \033[1mPush mods to %s?\033[0m\n", serverName)

	// Count changes
	changes := 0
	unchanged := 0
	for _, item := range items {
		if item.Status != "" {
			changes++
		} else {
			unchanged++
		}
	}

	if changes > 0 {
		fmt.Fprintf(b, "  Profile: \033[36m%s\033[0m    %d changes, %d unchanged\n", profileName, changes, unchanged)
	} else {
		fmt.Fprintf(b, "  Profile: \033[36m%s\033[0m    Mods: %d\n", profileName, len(items))
	}
	if status != nil && status.Running {
		b.WriteString("  Server: \033[32mrunning\033[0m — will restart after push\n")
	}
	b.WriteString("\n")

	// Display list: items are sorted changes-first, so just slice
	displayItems := items
	if changes > 0 {
		displayItems = items[:changes]
	}

	visible := 20
	maxScroll := len(displayItems) - visible
	if maxScroll < 0 {
		maxScroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}
	end := scroll + visible
	if end > len(displayItems) {
		end = len(displayItems)
	}

	maxName := 0
	for _, item := range displayItems {
		if l := len(item.Name); l > maxName {
			maxName = l
		}
	}

	for _, item := range displayItems[scroll:end] {
		pad := strings.Repeat(" ", maxName-len(item.Name)+2)
		version := item.Version
		if version == "" {
			version = "-"
		}

		switch item.Status {
		case "added":
			fmt.Fprintf(b, "    \033[32m+ %s%s%s\033[0m\n", item.Name, pad, version)
		case "removed":
			fmt.Fprintf(b, "    \033[31m- %s%s%s\033[0m\n", item.Name, pad, version)
		case "changed":
			fmt.Fprintf(b, "    \033[33m~ %s%s%s → %s\033[0m\n", item.Name, pad, item.ServerVersion, version)
		default:
			target := ""
			if item.Disabled {
				target = "  \033[2m(server-only)\033[0m"
			}
			fmt.Fprintf(b, "    \033[2m  %s%s%s%s\033[0m\n", item.Name, pad, version, target)
		}
	}

	if len(displayItems) > visible {
		fmt.Fprintf(b, "\n  \033[2m(%d more — ↑/↓ scroll)\033[0m\n", len(displayItems)-visible)
	}

	if changes > 0 && unchanged > 0 {
		fmt.Fprintf(b, "\n  \033[2m%d unchanged mods not shown\033[0m\n", unchanged)
	}

	b.WriteString("\n  \033[33my push • any key cancel\033[0m\n\n")
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
		return serverStatusMsg{status: status, mods: modsResp.Mods, modsResp: modsResp}
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
		manifest := profile.BuildManifest(cfg.ActiveProfile, reg)
		uploads, err := profile.BuildUploads(paths, cfg.ActiveProfile, manifest, reg)
		if err != nil {
			return serverPushMsg{err: err}
		}
		resp, err := c.SyncMods(manifest, uploads)
		return serverPushMsg{resp: resp, err: err}
	}
}

func streamServerLogs(c *client.AgentClient) (tea.Cmd, <-chan []string, chan struct{}) {
	ch := make(chan []string, 16)
	stop := make(chan struct{})

	// Start background goroutine
	initCmd := func() tea.Msg {
		body, err := c.Logs(200, true)
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
			var batch []string
			for scanner.Scan() {
				select {
				case <-stop:
					return
				default:
				}
				batch = append(batch, scanner.Text())
				// Send batch when channel is ready (non-blocking) or batch is large
				if len(batch) >= 50 {
					select {
					case ch <- batch:
						batch = nil
					case <-stop:
						return
					}
				}
			}
			// Flush remaining
			if len(batch) > 0 {
				select {
				case ch <- batch:
				case <-stop:
				}
			}
		}()

		// Wait for first batch
		lines, ok := <-ch
		if !ok {
			return serverLogsMsg{lines: nil}
		}
		return serverLogsMsg{lines: lines}
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
