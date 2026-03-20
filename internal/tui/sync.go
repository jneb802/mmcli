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

	// Push state (mods)
	pushResult       bool // showing push result screen
	pushResultScroll int

	// Push state (configs)
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
			if mod.IsLocal || mod.Owner == "" || mod.Owner == "local" {
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
		var body strings.Builder
		renderPushConfirm(&body, m.server.serverName, m.cfg.ActiveProfile, items, 0, m.server.status)
		m.confirm = confirmModal{
			Active: true,
			Prompt: fmt.Sprintf("Push mods to %s?", m.server.serverName),
			Body:   body.String(),
			OnYes: func(m model) (tea.Model, tea.Cmd) {
				m.server.actionBusy = true
				m.server.actionMsg = "Pushing mods..."
				return m, pushMods(m.server.client, m.paths, m.cfg, *m.reg)
			},
		}
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
		m.confirm = confirmModal{
			Active: true,
			Prompt: "Push all config changes to server?",
			OnYes: func(m model) (tea.Model, tea.Cmd) {
				m.sync.configPushBusy = true
				return m, pushAllConfigs(m.server.client, m.paths, m.cfg)
			},
		}
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

		maxName, maxVer := 0, 0
		for _, r := range lp.Results {
			if l := len(r.Mod); l > maxName {
				maxName = l
			}
			if l := len(r.Version); l > maxVer {
				maxVer = l
			}
		}

		for _, r := range lp.Results[scroll:end] {
			name := padRight(r.Mod, maxName+2)
			ver := padRight(r.Version, maxVer+2)
			switch r.Status {
			case "downloaded":
				fmt.Fprintf(b, "    \033[32m↓\033[0m %s%s\033[32mdownloaded\033[0m\n", name, ver)
			case "uploaded":
				fmt.Fprintf(b, "    \033[32m↑\033[0m %s%s\033[32muploaded\033[0m\n", name, ver)
			case "cached":
				fmt.Fprintf(b, "    \033[36m↓\033[0m %s%s\033[36mcached\033[0m\n", name, ver)
			case "skipped":
				fmt.Fprintf(b, "    \033[2m· %s%sunchanged\033[0m\n", name, ver)
			case "removed":
				fmt.Fprintf(b, "    \033[31m✗ %s%sremoved\033[0m\n", name, padRight("", maxVer+2))
			case "failed":
				fmt.Fprintf(b, "    \033[31m✗ %s%s%s\033[0m\n", name, padRight("", maxVer+2), r.Reason)
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

// Audit tab types and functions moved to mods.go
