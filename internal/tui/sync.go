package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"mmcli/internal/agentapi"
	"mmcli/internal/cfgfile"
	"mmcli/internal/client"
	"mmcli/internal/config"
	"mmcli/internal/installer"
)

type syncModel struct {
	// Mods sub-tab
	modItems []modListItem
	modCursor int

	// Configs sub-tab
	configItems    []configListItem
	configCursor   int
	configFetching bool
	configErr      error

	// Moderation sub-tab
	moderationCursor int

	// Audit sub-tab
	auditCursor int
	auditRows   []auditRow

	// Push state (mods — shared by Mods and Moderation tabs)
	confirmModPush   bool
	pushScroll       int
	pushResult       bool // showing push result screen
	pushResultScroll int

	// Push state (configs)
	confirmConfigPush bool
	configPushBusy    bool
	lastConfigPush    *agentapi.ConfigPushResponse
}

type configListItem struct {
	Filename  string
	Status    string // "modified", "unchanged", "local only", "server only"
	DiffCount int    // number of changed entries (for .cfg files)
}

// Async message types for sync tab.
type syncConfigListMsg struct {
	items []configListItem
	err   error
}

type syncConfigPushMsg struct {
	resp *agentapi.ConfigPushResponse
	err  error
}

// pendingMod represents a mod with at least one pending action across server/modpack.
type pendingMod struct {
	Name       string
	LocalVer   string
	ServerAct  string // "add", "remove", "update", "" (in sync)
	ServerVer  string // current version on server
	ModpackAct string // "add", "remove", "update", "" (in sync)
	ModpackVer string // current version in modpack
}

// buildPendingChanges computes the list of mods with pending actions needed
// to bring server and modpack in sync with local.
func buildPendingChanges(items []modListItem, reg *config.Registry, profileName string, modpackDeps map[string]string) []pendingMod {
	byName := make(map[string]*pendingMod)

	// Server actions: derived from buildPushItems diff
	for _, item := range items {
		if item.Status == "" {
			continue
		}
		pm := &pendingMod{Name: item.Name, LocalVer: item.Version}
		switch item.Status {
		case "added":
			pm.ServerAct = "add"
		case "removed":
			pm.ServerAct = "remove"
			pm.ServerVer = item.ServerVersion
		case "changed":
			pm.ServerAct = "update"
			pm.ServerVer = item.ServerVersion
		}
		byName[item.Name] = pm
	}

	// Modpack actions: compare all local Thunderstore mods to modpack deps
	if modpackDeps != nil {
		localTS := make(map[string]string)
		for _, mod := range reg.ListMods(profileName) {
			if mod.IsLocal || mod.Owner == "" {
				continue
			}
			name := mod.FullName()
			ver := mod.Version
			localTS[name] = ver

			mpVer := modpackDeps[name]
			var act, actVer string
			if mpVer == "" {
				act = "add"
			} else if mpVer != ver {
				act = "update"
				actVer = mpVer
			}

			if act != "" {
				if pm, ok := byName[name]; ok {
					pm.ModpackAct = act
					pm.ModpackVer = actVer
				} else {
					byName[name] = &pendingMod{
						Name:       name,
						LocalVer:   ver,
						ModpackAct: act,
						ModpackVer: actVer,
					}
				}
			}
		}

		// Mods in modpack but not local → remove from modpack
		for name, ver := range modpackDeps {
			if _, isLocal := localTS[name]; !isLocal {
				if pm, ok := byName[name]; ok {
					pm.ModpackAct = "remove"
					pm.ModpackVer = ver
				} else {
					byName[name] = &pendingMod{
						Name:       name,
						ModpackAct: "remove",
						ModpackVer: ver,
					}
				}
			}
		}
	}

	var result []pendingMod
	for _, pm := range byName {
		result = append(result, *pm)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// actionText returns the plain display text for a pending action cell.
func actionText(act, targetVer, localVer string) string {
	switch act {
	case "add":
		return "+ add"
	case "remove":
		return "- remove"
	case "update":
		return "~ " + targetVer + " → " + localVer
	default:
		return "✓"
	}
}

// colorAction wraps action text with ANSI color based on action type.
func colorAction(act, text string, width int) string {
	if width > 0 {
		text = padRight(text, width)
	}
	switch act {
	case "add":
		return "\033[32m" + text + "\033[0m"
	case "remove":
		return "\033[31m" + text + "\033[0m"
	case "update":
		return "\033[33m" + text + "\033[0m"
	default:
		return "\033[2m" + text + "\033[0m"
	}
}

func renderPendingChanges(b *strings.Builder, items []pendingMod, cursor, visible int, showModpack bool) {
	if len(items) == 0 {
		b.WriteString("  \033[32mAll in sync.\033[0m\n")
		return
	}

	// Build plain-text cells for width calculation.
	type row struct {
		name, server, modpack string
	}
	rows := make([]row, len(items))
	for i, pm := range items {
		rows[i] = row{
			name:    pm.Name,
			server:  actionText(pm.ServerAct, pm.ServerVer, pm.LocalVer),
			modpack: actionText(pm.ModpackAct, pm.ModpackVer, pm.LocalVer),
		}
	}

	colName := len("Mod")
	colServer := len("Server")
	colModpack := len("Modpack")
	for _, r := range rows {
		if w := displayWidth(r.name); w > colName {
			colName = w
		}
		if w := displayWidth(r.server); w > colServer {
			colServer = w
		}
		if showModpack {
			if w := displayWidth(r.modpack); w > colModpack {
				colModpack = w
			}
		}
	}
	colName += 2
	colServer += 2

	// Header
	b.WriteString("  \033[2m  ")
	b.WriteString(padRight("Mod", colName))
	b.WriteString(padRight("Server", colServer))
	if showModpack {
		b.WriteString("Modpack")
	}
	b.WriteString("\033[0m\n")

	start, end := listWindow(len(items), cursor, visible)
	if start > 0 {
		fmt.Fprintf(b, "  \033[2m  ↑ %d more\033[0m\n", start)
	}

	for i := start; i < end; i++ {
		pm := items[i]
		r := rows[i]
		cur := "  "
		if i == cursor {
			cur = "\033[36m>\033[0m "
		}

		b.WriteString("  ")
		b.WriteString(cur)
		b.WriteString(padRight(r.name, colName))
		b.WriteString(colorAction(pm.ServerAct, r.server, colServer))
		if showModpack {
			b.WriteString(colorAction(pm.ModpackAct, r.modpack, 0))
		}
		b.WriteString("\n")
	}

	if end < len(items) {
		fmt.Fprintf(b, "  \033[2m  ↓ %d more\033[0m\n", len(items)-end)
	}
}

func (m model) handleSyncNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Push result screen — stay until dismissed
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

	// Mod push confirmation modal
	if m.sync.confirmModPush {
		switch msg.String() {
		case "y":
			m.sync.confirmModPush = false
			m.server.actionBusy = true
			m.server.actionMsg = "Pushing mods..."
			return m, pushMods(m.server.client, m.paths, m.cfg, *m.reg)
		case "up", "k":
			if m.sync.pushScroll > 0 {
				m.sync.pushScroll--
			}
		case "down", "j":
			m.sync.pushScroll++
		case "ctrl+c":
			return m, tea.Quit
		default:
			m.sync.confirmModPush = false
		}
		return m, nil
	}

	// Config push confirmation modal
	if m.sync.confirmConfigPush {
		switch msg.String() {
		case "y":
			m.sync.confirmConfigPush = false
			m.sync.configPushBusy = true
			return m, pushAllConfigs(m.server.client, m.paths, m.cfg)
		case "ctrl+c":
			return m, tea.Quit
		default:
			m.sync.confirmConfigPush = false
		}
		return m, nil
	}

	// Action busy — only allow quit
	if m.server.actionBusy || m.sync.configPushBusy {
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
		case "`":
			return m, m.enterLocalMode()
		case "1":
			return m, m.enterLocalMode()
		case "2":
			return m, m.enterServerMode()
		case "3":
			return m, m.enterModpackMode()
		}
		return m, nil
	}

	// Common keys across all sync tabs
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "`":
		return m, m.enterLocalMode()
	case "1":
		return m, m.enterLocalMode()
	case "2":
		return m, m.enterServerMode()
	case "3":
		return m, m.enterModpackMode()
	case "tab":
		cmd := m.cycleSyncTab(1)
		return m, cmd
	case "shift+tab":
		cmd := m.cycleSyncTab(-1)
		return m, cmd
	}

	// Tab-specific keys
	switch m.activeSyncTab {
	case contentSyncMods:
		return m.handleSyncModsKeys(msg)
	case contentSyncConfigs:
		return m.handleSyncConfigsKeys(msg)
	case contentSyncModeration:
		return m.handleSyncModerationKeys(msg)
	case contentSyncAudit:
		return m.handleSyncAuditKeys(msg)
	}
	return m, nil
}

func (m model) handleSyncModsKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	pending := buildPendingChanges(m.sync.modItems, m.reg, m.cfg.ActiveProfile, m.modpack.versionMap)
	n := len(pending)
	if m.sync.modCursor >= n && n > 0 {
		m.sync.modCursor = n - 1
	}
	switch msg.String() {
	case "up", "k":
		if m.sync.modCursor > 0 {
			m.sync.modCursor--
		}
	case "down", "j":
		if m.sync.modCursor < n-1 {
			m.sync.modCursor++
		}
	case "p":
		if m.server.role != agentapi.RoleAdmin {
			return m, nil
		}
		items := buildPushItems(m.cfg, m.reg, m.paths, m.server.mods, m.modpack.versionMap)
		if len(items) == 0 {
			return m, nil
		}
		m.sync.modItems = items
		m.sync.confirmModPush = true
		m.sync.pushScroll = 0
		return m, nil
	}
	return m, nil
}

func (m model) handleSyncConfigsKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.sync.configCursor > 0 {
			m.sync.configCursor--
		}
	case "down", "j":
		if m.sync.configCursor < len(m.sync.configItems)-1 {
			m.sync.configCursor++
		}
	case "p":
		if m.server.role != agentapi.RoleAdmin {
			return m, nil
		}
		if len(m.sync.configItems) == 0 {
			return m, nil
		}
		m.sync.confirmConfigPush = true
		return m, nil
	case "r":
		// Refresh config diffs
		if m.server.client != nil && !m.sync.configFetching {
			m.sync.configFetching = true
			return m, fetchConfigDiffs(m.server.client, m.paths, m.cfg)
		}
	}
	return m, nil
}

func (m model) handleSyncModerationKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.sync.modItems
	switch msg.String() {
	case "up", "k":
		if m.sync.moderationCursor > 0 {
			m.sync.moderationCursor--
		}
	case "down", "j":
		if m.sync.moderationCursor < len(items)-1 {
			m.sync.moderationCursor++
		}
	case "a":
		if m.server.role != agentapi.RoleAdmin || len(items) == 0 {
			return m, nil
		}
		modName := items[m.sync.moderationCursor].Name
		regMod, ok := m.reg.GetMod(m.cfg.ActiveProfile, modName)
		if !ok {
			return m, nil
		}
		newValue := nextAnticheatValue(regMod.Anticheat, m.anticheatSystem)
		regMod.Anticheat = newValue
		m.reg.SetMod(m.cfg.ActiveProfile, regMod)
		// Propagate to dependencies
		for _, depName := range regMod.Dependencies {
			dep, depOk := m.reg.GetMod(m.cfg.ActiveProfile, depName)
			if !depOk {
				continue
			}
			dep.Anticheat = newValue
			m.reg.SetMod(m.cfg.ActiveProfile, dep)
		}
		config.SaveRegistry(m.paths, *m.reg)
		// Refresh mod items to reflect the change
		m.sync.modItems = buildPushItems(m.cfg, m.reg, m.paths, m.server.mods, m.modpack.versionMap)
		return m, nil
	case "p":
		if m.server.role != agentapi.RoleAdmin {
			return m, nil
		}
		pushItems := buildPushItems(m.cfg, m.reg, m.paths, m.server.mods, m.modpack.versionMap)
		if len(pushItems) == 0 {
			return m, nil
		}
		m.sync.modItems = pushItems
		m.sync.confirmModPush = true
		m.sync.pushScroll = 0
		return m, nil
	}
	return m, nil
}

func (m model) viewSyncModeration() string {
	var b strings.Builder

	if m.server.client == nil {
		b.WriteString("\n  No server configured.\n")
		b.WriteString("  Run \033[36mmmcli server add\033[0m to register one.\n\n")
		b.WriteString("  \033[2m` mode • q quit\033[0m\n\n")
		return b.String()
	}

	// Push result screen — stays until dismissed
	if m.sync.pushResult && m.server.lastPush != nil {
		renderSyncPushResult(&b, m.server.lastPush, m.server.lastPushTime, m.sync.pushResultScroll, m.server.role)
		return b.String()
	}

	// Push confirmation modal (shared with Mods tab)
	if m.sync.confirmModPush {
		renderPushConfirm(&b, m.server.serverName, m.cfg.ActiveProfile, m.sync.modItems, m.sync.pushScroll, m.server.status)
		return b.String()
	}

	// Action busy — show syncing progress
	if m.server.actionBusy {
		renderSyncPushing(&b, m.sync.modItems)
		return b.String()
	}

	if m.server.fetching && len(m.sync.modItems) == 0 {
		b.WriteString("\n  \033[2mFetching server data...\033[0m\n\n")
		return b.String()
	}

	b.WriteString("\n")

	// Header with system info
	systemLabel := m.anticheatSystem
	if systemLabel == "azu" {
		systemLabel = "AzuAntiCheat"
	} else {
		systemLabel = "ValheimEnforcer"
	}
	fmt.Fprintf(&b, "  \033[1mModeration\033[0m  \033[2m%s\033[0m\n\n", systemLabel)

	renderModerationList(&b, m.sync.modItems, m.sync.moderationCursor, listVisible(m.height, 12), m.anticheatSystem)

	b.WriteString("\n")
	if m.server.statusErr != nil {
		fmt.Fprintf(&b, "  \033[31mError: %v\033[0m\n", m.server.statusErr)
	}

	var hotkeys []string
	if m.server.role == agentapi.RoleAdmin {
		hotkeys = []string{"a classify", "p push", "` mode", "tab next", "q quit"}
	} else {
		hotkeys = []string{"` mode", "tab next", "q quit"}
	}
	renderHotkeyBar(&b, hotkeys, m.width)

	return b.String()
}

func renderModerationList(b *strings.Builder, items []modListItem, cursor, visible int, anticheatSystem string) {
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

	// Header
	namePad := strings.Repeat(" ", maxName-len("Name")+2)
	fmt.Fprintf(b, "  \033[2m    Name%sClassification\033[0m\n", namePad)

	start, end := listWindow(len(items), cursor, visible)

	if start > 0 {
		fmt.Fprintf(b, "  \033[2m  ↑ %d more\033[0m\n", start)
	}

	for i := start; i < end; i++ {
		item := items[i]
		cur := "  "
		if i == cursor {
			cur = "\033[36m>\033[0m "
		}

		pad := strings.Repeat(" ", maxName-len(item.Name)+2)

		ac := item.Anticheat
		var label string
		switch ac {
		case "whitelist":
			if anticheatSystem == "enforcer" {
				label = "\033[32mR  required\033[0m"
			} else {
				label = "\033[32mW  whitelist\033[0m"
			}
		case "greylist":
			if anticheatSystem == "enforcer" {
				label = "\033[33mO  optional\033[0m"
			} else {
				label = "\033[33mG  greylist\033[0m"
			}
		case "adminonly":
			label = "\033[35mA  admin only\033[0m"
		default:
			label = "\033[2m-  none\033[0m"
		}

		fmt.Fprintf(b, "  %s%s%s%s\n", cur, item.Name, pad, label)
	}

	if end < len(items) {
		fmt.Fprintf(b, "  \033[2m  ↓ %d more\033[0m\n", len(items)-end)
	}
}

func (m model) viewSyncMods() string {
	var b strings.Builder

	if m.server.client == nil {
		b.WriteString("\n  No server configured.\n")
		b.WriteString("  Run \033[36mmmcli server add\033[0m to register one.\n\n")
		b.WriteString("  \033[2m` mode • q quit\033[0m\n\n")
		return b.String()
	}

	// Push result screen — stays until dismissed
	if m.sync.pushResult && m.server.lastPush != nil {
		renderSyncPushResult(&b, m.server.lastPush, m.server.lastPushTime, m.sync.pushResultScroll, m.server.role)
		return b.String()
	}

	// Push confirmation modal
	if m.sync.confirmModPush {
		renderPushConfirm(&b, m.server.serverName, m.cfg.ActiveProfile, m.sync.modItems, m.sync.pushScroll, m.server.status)
		return b.String()
	}

	// Action busy — show syncing progress with mod list
	if m.server.actionBusy {
		renderSyncPushing(&b, m.sync.modItems)
		return b.String()
	}

	if m.server.fetching && len(m.sync.modItems) == 0 {
		b.WriteString("\n  \033[2mFetching server data...\033[0m\n\n")
		return b.String()
	}

	b.WriteString("\n")

	// Header
	fmt.Fprintf(&b, "  \033[1mPending Changes\033[0m\n\n")

	pending := buildPendingChanges(m.sync.modItems, m.reg, m.cfg.ActiveProfile, m.modpack.versionMap)
	cursor := m.sync.modCursor
	if cursor >= len(pending) {
		cursor = max(0, len(pending)-1)
	}

	renderPendingChanges(&b, pending, cursor, listVisible(m.height, 12), m.modpack.manifest != nil)

	// Status
	b.WriteString("\n")
	if m.server.statusErr != nil {
		fmt.Fprintf(&b, "  \033[31mError: %v\033[0m\n", m.server.statusErr)
	}

	var hotkeys []string
	if m.server.role == agentapi.RoleAdmin {
		hotkeys = []string{"p push", "` mode", "tab next", "q quit"}
	} else {
		hotkeys = []string{"` mode", "tab next", "q quit"}
	}
	renderHotkeyBar(&b, hotkeys, m.width)

	return b.String()
}

func (m model) viewSyncConfigs() string {
	var b strings.Builder

	if m.server.client == nil {
		b.WriteString("\n  No server configured.\n")
		b.WriteString("  Run \033[36mmmcli server add\033[0m to register one.\n\n")
		b.WriteString("  \033[2m` mode • q quit\033[0m\n\n")
		return b.String()
	}

	// Config push confirmation modal
	if m.sync.confirmConfigPush {
		renderConfigPushConfirm(&b, m.server.serverName, m.sync.configItems)
		return b.String()
	}

	// Config push busy
	if m.sync.configPushBusy {
		b.WriteString("\n  \033[33mPushing configs...\033[0m\n\n")
		return b.String()
	}

	if m.sync.configFetching {
		b.WriteString("\n  \033[2mFetching config diffs...\033[0m\n\n")
		return b.String()
	}

	b.WriteString("\n")

	// Header
	fmt.Fprintf(&b, "  \033[1mConfig sync: %s → %s\033[0m\n\n", m.cfg.ActiveProfile, m.server.serverName)

	if m.sync.configErr != nil {
		fmt.Fprintf(&b, "  \033[31mError: %v\033[0m\n\n", m.sync.configErr)
	}

	renderConfigList(&b, m.sync.configItems, m.sync.configCursor, listVisible(m.height, 12))

	b.WriteString("\n")

	// Last config push result
	if m.sync.lastConfigPush != nil {
		fmt.Fprintf(&b, "  \033[2mLast push: %s\033[0m\n", m.sync.lastConfigPush.Message)
	}

	var hotkeys []string
	if m.server.role == agentapi.RoleAdmin {
		hotkeys = []string{"p push", "r refresh", "` mode", "tab next", "q quit"}
	} else {
		hotkeys = []string{"r refresh", "` mode", "tab next", "q quit"}
	}
	renderHotkeyBar(&b, hotkeys, m.width)

	return b.String()
}

func renderConfigList(b *strings.Builder, items []configListItem, cursor, visible int) {
	if len(items) == 0 {
		b.WriteString("  No config files.\n")
		return
	}

	maxName := 0
	for _, item := range items {
		if l := len(item.Filename); l > maxName {
			maxName = l
		}
	}

	start, end := listWindow(len(items), cursor, visible)

	if start > 0 {
		fmt.Fprintf(b, "  \033[2m  ↑ %d more\033[0m\n", start)
	}

	for i := start; i < end; i++ {
		item := items[i]
		cur := "  "
		if i == cursor {
			cur = "\033[36m>\033[0m "
		}

		pad := strings.Repeat(" ", maxName-len(item.Filename)+2)

		var status string
		switch item.Status {
		case "modified":
			if item.DiffCount > 0 {
				status = fmt.Sprintf("\033[33mmodified (%d entries)\033[0m", item.DiffCount)
			} else {
				status = "\033[33mmodified\033[0m"
			}
		case "unchanged":
			status = "\033[32m✓\033[0m"
		case "local only":
			status = "\033[36mlocal only\033[0m"
		case "server only":
			status = "\033[35mserver only\033[0m"
		default:
			status = item.Status
		}

		fmt.Fprintf(b, "  %s%s%s%s\n", cur, item.Filename, pad, status)
	}

	if end < len(items) {
		fmt.Fprintf(b, "  \033[2m  ↓ %d more\033[0m\n", len(items)-end)
	}
}

func renderConfigPushConfirm(b *strings.Builder, serverName string, items []configListItem) {
	fmt.Fprintf(b, "\n  \033[1mPush configs to %s?\033[0m\n\n", serverName)

	modified := 0
	localOnly := 0
	unchanged := 0
	for _, item := range items {
		switch item.Status {
		case "modified":
			modified++
		case "local only":
			localOnly++
		case "unchanged":
			unchanged++
		}
	}

	if modified > 0 {
		fmt.Fprintf(b, "  \033[33m%d modified\033[0m", modified)
	}
	if localOnly > 0 {
		if modified > 0 {
			b.WriteString(", ")
		} else {
			b.WriteString("  ")
		}
		fmt.Fprintf(b, "\033[36m%d local only\033[0m", localOnly)
	}
	if unchanged > 0 {
		if modified > 0 || localOnly > 0 {
			b.WriteString(", ")
		} else {
			b.WriteString("  ")
		}
		fmt.Fprintf(b, "%d unchanged (skipped)", unchanged)
	}
	b.WriteString("\n")

	b.WriteString("\n  \033[33my push • any key cancel\033[0m\n\n")
}

// --- Async commands ---

func fetchConfigDiffs(c *client.AgentClient, paths config.Paths, cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		configDir := paths.ProfileConfigDir(cfg.ActiveProfile)

		localFiles, err := cfgfile.ListConfigFiles(configDir)
		if err != nil {
			return syncConfigListMsg{err: fmt.Errorf("failed to list local configs: %w", err)}
		}

		remoteResp, err := c.ListConfigs()
		if err != nil {
			return syncConfigListMsg{err: fmt.Errorf("failed to list server configs: %w", err)}
		}

		localSet := make(map[string]bool)
		for _, f := range localFiles {
			localSet[f] = true
		}
		remoteSet := make(map[string]bool)
		for _, f := range remoteResp.Files {
			remoteSet[f] = true
		}

		var items []configListItem

		// Files on both sides — diff them
		for _, f := range localFiles {
			if !remoteSet[f] {
				continue
			}

			localData, err := os.ReadFile(filepath.Join(configDir, f))
			if err != nil {
				continue
			}

			resp, err := c.GetConfig(f)
			if err != nil {
				continue
			}

			item := configListItem{Filename: f, Status: "unchanged"}

			if cfgfile.IsCfg(f) {
				local, err := cfgfile.ParseBytes(localData)
				if err == nil {
					remote, err := cfgfile.ParseBytes([]byte(resp.Content))
					if err == nil {
						diffs := cfgfile.Diff(local, remote)
						changed := 0
						for _, d := range diffs {
							if d.Status == cfgfile.Changed {
								changed++
							}
						}
						if changed > 0 || len(diffs) > 0 {
							item.Status = "modified"
							item.DiffCount = changed
						}
					}
				}
			} else {
				diff := cfgfile.TextDiff("local", "server", localData, []byte(resp.Content))
				if diff != "" {
					item.Status = "modified"
				}
			}

			items = append(items, item)
		}

		// Local-only files
		for _, f := range localFiles {
			if !remoteSet[f] {
				items = append(items, configListItem{Filename: f, Status: "local only"})
			}
		}

		// Server-only files
		for _, f := range remoteResp.Files {
			if !localSet[f] {
				items = append(items, configListItem{Filename: f, Status: "server only"})
			}
		}

		// Sort: modified first, then local only, then server only, then unchanged
		statusOrder := map[string]int{"modified": 0, "local only": 1, "server only": 2, "unchanged": 3}
		sort.SliceStable(items, func(i, j int) bool {
			return statusOrder[items[i].Status] < statusOrder[items[j].Status]
		})

		return syncConfigListMsg{items: items}
	}
}

func pushAllConfigs(c *client.AgentClient, paths config.Paths, cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		configDir := paths.ProfileConfigDir(cfg.ActiveProfile)

		localFiles, err := cfgfile.ListConfigFiles(configDir)
		if err != nil {
			return syncConfigPushMsg{err: err}
		}

		if len(localFiles) == 0 {
			return syncConfigPushMsg{err: fmt.Errorf("no config files to push")}
		}

		var cfgPatches []agentapi.ConfigPatch
		var wholeFiles []agentapi.ConfigFile

		for _, f := range localFiles {
			localData, err := os.ReadFile(filepath.Join(configDir, f))
			if err != nil {
				continue
			}

			resp, remoteErr := c.GetConfig(f)

			// .cfg files with a server copy: entry-level patch for changed values
			if cfgfile.IsCfg(f) && remoteErr == nil {
				local, err := cfgfile.ParseBytes(localData)
				if err != nil {
					continue
				}
				remote, err := cfgfile.ParseBytes([]byte(resp.Content))
				if err != nil {
					continue
				}
				for _, d := range cfgfile.Diff(local, remote) {
					if d.Status == cfgfile.Changed {
						cfgPatches = append(cfgPatches, agentapi.ConfigPatch{
							Filename: f,
							Section:  d.Section,
							Key:      d.Key,
							Value:    d.LocalValue,
						})
					}
				}
				continue
			}

			// Non-.cfg files: only push if different
			if !cfgfile.IsCfg(f) && remoteErr == nil {
				diff := cfgfile.TextDiff("server", "local", []byte(resp.Content), localData)
				if diff == "" {
					continue
				}
			}

			wholeFiles = append(wholeFiles, agentapi.ConfigFile{
				Filename: f,
				Content:  string(localData),
			})
		}

		if len(cfgPatches) == 0 && len(wholeFiles) == 0 {
			return syncConfigPushMsg{err: fmt.Errorf("no differences to push")}
		}

		pushResp, err := c.PushConfigs(agentapi.ConfigPushRequest{
			Patches: cfgPatches,
			Files:   wholeFiles,
		})
		return syncConfigPushMsg{resp: pushResp, err: err}
	}
}

// renderSyncPushing shows the mod list with a "syncing" indicator while the push is in progress.
func renderSyncPushing(b *strings.Builder, items []modListItem) {
	fmt.Fprintf(b, "\n  \033[1mSyncing mods...\033[0m\n\n")

	maxName := 0
	for _, item := range items {
		if l := len(item.Name); l > maxName {
			maxName = l
		}
	}

	for _, item := range items {
		if item.Status == "" {
			continue // skip unchanged
		}
		pad := strings.Repeat(" ", maxName-len(item.Name)+2)
		version := item.Version
		if version == "" {
			version = "-"
		}

		switch item.Status {
		case "added":
			fmt.Fprintf(b, "    \033[33m⟳\033[0m %s%s%s  \033[33mdownloading...\033[0m\n", item.Name, pad, version)
		case "changed":
			fmt.Fprintf(b, "    \033[33m⟳\033[0m %s%s%s  \033[33mupdating...\033[0m\n", item.Name, pad, version)
		case "removed":
			fmt.Fprintf(b, "    \033[33m⟳\033[0m %s%s     \033[33mremoving...\033[0m\n", item.Name, pad)
		}
	}

	b.WriteString("\n  \033[2mWaiting for server...\033[0m\n\n")
}

// renderSyncPushResult shows the push results and stays on screen until dismissed.
func renderSyncPushResult(b *strings.Builder, lp *agentapi.SyncResponse, pushTime time.Time, scroll int, role string) {
	if lp == nil {
		return
	}

	ago := time.Since(pushTime).Truncate(time.Second)
	fmt.Fprintf(b, "\n  \033[1mPush Complete\033[0m  \033[2m%s ago\033[0m\n", ago)

	total := lp.Downloaded + lp.Uploaded + lp.Cached + lp.Skipped
	fmt.Fprintf(b, "  %d mods synced", total)
	if lp.Downloaded > 0 {
		fmt.Fprintf(b, ", \033[32m%d downloaded\033[0m", lp.Downloaded)
	}
	if lp.Uploaded > 0 {
		fmt.Fprintf(b, ", \033[32m%d uploaded\033[0m", lp.Uploaded)
	}
	if lp.Cached > 0 {
		fmt.Fprintf(b, ", \033[36m%d cached\033[0m", lp.Cached)
	}
	if lp.Skipped > 0 {
		fmt.Fprintf(b, ", %d unchanged", lp.Skipped)
	}
	if lp.Removed > 0 {
		fmt.Fprintf(b, ", \033[31m%d removed\033[0m", lp.Removed)
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
			case "uploaded":
				fmt.Fprintf(b, "    \033[32m↑\033[0m %s%s%s  \033[32muploaded\033[0m\n", r.Mod, pad, r.Version)
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

	fmt.Fprintf(b, "\n  \033[2mesc/enter done\033[0m\n\n")
}

// --- Last push persistence ---

type savedPush struct {
	Response agentapi.SyncResponse `json:"response"`
	PushedAt time.Time             `json:"pushed_at"`
}

func lastPushPath(paths config.Paths) string {
	return filepath.Join(paths.ConfigDir, "last_push.json")
}

func saveLastPush(paths config.Paths, resp *agentapi.SyncResponse, t time.Time) {
	data, err := json.Marshal(savedPush{Response: *resp, PushedAt: t})
	if err != nil {
		return
	}
	_ = os.WriteFile(lastPushPath(paths), data, 0644)
}

func loadLastPush(paths config.Paths) (*agentapi.SyncResponse, time.Time) {
	data, err := os.ReadFile(lastPushPath(paths))
	if err != nil {
		return nil, time.Time{}
	}
	var sp savedPush
	if err := json.Unmarshal(data, &sp); err != nil {
		return nil, time.Time{}
	}
	return &sp.Response, sp.PushedAt
}

// --- Audit tab ---

type auditRow struct {
	Name           string
	LocalVersion   string
	ServerVersion  string
	ModpackVersion string
	Target         string // "client", "server", "both"
	Anticheat      string // "whitelist", "greylist", "adminonly", ""
}

func (m model) buildAuditRows() []auditRow {
	seen := make(map[string]*auditRow)
	var order []string

	ensure := func(name string) *auditRow {
		if r, ok := seen[name]; ok {
			return r
		}
		r := &auditRow{Name: name, LocalVersion: "-", ServerVersion: "-", ModpackVersion: "-", Target: "both"}
		seen[name] = r
		order = append(order, name)
		return r
	}

	// Local mods
	localMods := m.reg.ListAllMods(m.cfg.ActiveProfile, m.paths.ProfilePluginsDir(m.cfg.ActiveProfile))
	for _, mod := range localMods {
		r := ensure(mod.FullName())
		r.LocalVersion = syncVersionStr(mod.Version)
		r.Target = mod.ResolvedTarget()
		r.Anticheat = mod.Anticheat
	}

	// Server mods
	for _, sm := range m.server.mods {
		r := ensure(sm.Name)
		r.ServerVersion = syncVersionStr(sm.Version)
	}

	// Modpack mods
	for _, dep := range m.modpack.deps {
		key := fmt.Sprintf("%s-%s", dep.Owner, dep.Name)
		r := ensure(key)
		r.ModpackVersion = syncVersionStr(dep.Version)
	}

	rows := make([]auditRow, len(order))
	for i, name := range order {
		rows[i] = *seen[name]
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].Name < rows[j].Name
	})
	return rows
}

func (m model) handleSyncAuditKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.sync.auditRows)
	if m.sync.auditCursor >= n && n > 0 {
		m.sync.auditCursor = n - 1
	}
	switch msg.String() {
	case "up", "k":
		if m.sync.auditCursor > 0 {
			m.sync.auditCursor--
		}
	case "down", "j":
		if m.sync.auditCursor < n-1 {
			m.sync.auditCursor++
		}
	case "t":
		if n == 0 {
			return m, nil
		}
		row := m.sync.auditRows[m.sync.auditCursor]
		mod, ok := m.reg.GetMod(m.cfg.ActiveProfile, row.Name)
		if !ok {
			return m, nil
		}

		oldTarget := mod.ResolvedTarget()
		// Cycle: both → client → server → both
		switch oldTarget {
		case "both":
			mod.Target = "client"
		case "client":
			mod.Target = "server"
		case "server":
			mod.Target = "both"
		}

		// Auto-disable/enable for server target transitions
		if mod.Target == "server" && !mod.Disabled {
			_ = installer.Disable(m.paths, m.cfg, m.reg, mod.FullName())
		} else if oldTarget == "server" && mod.Target != "server" && mod.Disabled {
			_ = installer.Enable(m.paths, m.cfg, m.reg, mod.FullName())
		}

		m.reg.SetMod(m.cfg.ActiveProfile, mod)
		config.SaveRegistry(m.paths, *m.reg)
		m.sync.auditRows = m.buildAuditRows()
	}
	return m, nil
}

func (m model) viewSyncAudit() string {
	var b strings.Builder

	b.WriteString("\n")
	fmt.Fprintf(&b, "  \033[1mMod Audit\033[0m  \033[2m%s\033[0m\n\n", m.cfg.ActiveProfile)

	cursor := m.sync.auditCursor
	if cursor >= len(m.sync.auditRows) {
		cursor = max(0, len(m.sync.auditRows)-1)
	}

	renderAuditList(&b, m.sync.auditRows, cursor, listVisible(m.height, 12), m.anticheatSystem)

	b.WriteString("\n")
	renderHotkeyBar(&b, []string{"t target", "` mode", "tab next", "q quit"}, m.width)
	return b.String()
}

func renderAuditList(b *strings.Builder, rows []auditRow, cursor, visible int, anticheatSystem string) {
	if len(rows) == 0 {
		b.WriteString("  No mods.\n")
		return
	}

	// Resolve moderation labels for width calculation.
	modLabels := make([]string, len(rows))
	for i, r := range rows {
		modLabels[i] = auditModerationText(r.Anticheat, anticheatSystem)
	}

	// Compute column widths.
	colName, colLocal, colServer, colModpack, colTarget, colMod := len("Name"), len("Local"), len("Server"), len("Modpack"), len("Target"), len("Moderation")
	for i, r := range rows {
		if w := displayWidth(r.Name); w > colName {
			colName = w
		}
		if w := displayWidth(r.LocalVersion); w > colLocal {
			colLocal = w
		}
		if w := displayWidth(r.ServerVersion); w > colServer {
			colServer = w
		}
		if w := displayWidth(r.ModpackVersion); w > colModpack {
			colModpack = w
		}
		if w := displayWidth(r.Target); w > colTarget {
			colTarget = w
		}
		if w := displayWidth(modLabels[i]); w > colMod {
			colMod = w
		}
	}
	colName += 2
	colLocal += 2
	colServer += 2
	colModpack += 2
	colTarget += 2

	// Header
	b.WriteString("  \033[2m  ")
	b.WriteString(padRight("Name", colName))
	b.WriteString(padRight("Local", colLocal))
	b.WriteString(padRight("Server", colServer))
	b.WriteString(padRight("Modpack", colModpack))
	b.WriteString(padRight("Target", colTarget))
	b.WriteString("Moderation\033[0m\n")

	start, end := listWindow(len(rows), cursor, visible)

	if start > 0 {
		fmt.Fprintf(b, "  \033[2m  ↑ %d more\033[0m\n", start)
	}

	for i := start; i < end; i++ {
		r := rows[i]
		cur := "  "
		if i == cursor {
			cur = "\033[36m>\033[0m "
		}

		targetColored := auditTargetColor(r.Target, padRight(r.Target, colTarget))

		fmt.Fprintf(b, "  %s%s%s%s%s%s%s\n", cur,
			padRight(r.Name, colName),
			padRight(r.LocalVersion, colLocal),
			padRight(r.ServerVersion, colServer),
			padRight(r.ModpackVersion, colModpack),
			targetColored,
			auditModerationColor(r.Anticheat, modLabels[i]),
		)
	}

	if end < len(rows) {
		fmt.Fprintf(b, "  \033[2m  ↓ %d more\033[0m\n", len(rows)-end)
	}
}

// auditTargetColor wraps a pre-padded target string with ANSI color.
func auditTargetColor(target, text string) string {
	switch target {
	case "client":
		return "\033[36m" + text + "\033[0m"
	case "server":
		return "\033[35m" + text + "\033[0m"
	default:
		return "\033[2m" + text + "\033[0m"
	}
}

// auditModerationText returns the plain-text moderation label.
func auditModerationText(anticheat, system string) string {
	switch anticheat {
	case "whitelist":
		if system == "enforcer" {
			return "required"
		}
		return "whitelist"
	case "greylist":
		if system == "enforcer" {
			return "optional"
		}
		return "greylist"
	case "adminonly":
		return "admin only"
	default:
		return "-"
	}
}

// auditModerationColor wraps a moderation label with ANSI color.
func auditModerationColor(anticheat, text string) string {
	switch anticheat {
	case "whitelist":
		return "\033[32m" + text + "\033[0m"
	case "greylist":
		return "\033[33m" + text + "\033[0m"
	case "adminonly":
		return "\033[35m" + text + "\033[0m"
	default:
		return "\033[2m" + text + "\033[0m"
	}
}
