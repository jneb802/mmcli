package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"mmcli/internal/config"
	"mmcli/internal/installer"
	"mmcli/internal/modpack"
	"mmcli/internal/profile"
)

// --- Filter ---

type modFilter int

const (
	filterAll modFilter = iota
	filterLocal
	filterServer
	filterModpack
)

func modFilterName(f modFilter) string {
	switch f {
	case filterAll:
		return "All"
	case filterLocal:
		return "Local"
	case filterServer:
		return "Server"
	case filterModpack:
		return "Modpack"
	default:
		return "?"
	}
}

// --- Mods tab state ---

type modsState struct {
	filter modFilter
	cursor int

	// Audit rows (full mode)
	auditRows []auditRow

	// --- Migrated modal states from localModel ---
	installing  bool
	installInput string
	installBusy  bool

	pickProfile     bool
	profiles        []string
	profileCursor   int
	creatingProfile bool
	newProfileInput string

	preflightFetching bool

	// Scope picker (remove only)
	scopePicker                          bool
	scopeLocal, scopeServer, scopeModpack bool
	scopeCursor                          int // 0=local, 1=server, 2=modpack

	err       error
	statusMsg string // transient success message (cleared on next action)
}

// --- Audit row (moved from sync.go) ---

type auditRow struct {
	Name           string
	LocalVersion      string
	ServerVersion     string
	ServerRuntimeVer  string // BepInEx-reported version (may differ from manifest)
	ModpackVersion    string
	Target            string
	Anticheat         string
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

	// Local mods — only registered (managed) mods, not filesystem detection.
	// Untracked directories show in the Local filter view only.
	localMods := m.reg.ListMods(m.cfg.ActiveProfile)
	for _, mod := range localMods {
		r := ensure(mod.FullName())
		r.LocalVersion = syncVersionStr(mod.Version)
		r.Target = mod.ResolvedTarget()
	}

	// Server mods — server is source of truth for anticheat
	for _, sm := range m.server.mods {
		r := ensure(sm.Name)
		r.ServerVersion = syncVersionStr(sm.Version)
		r.ServerRuntimeVer = sm.RuntimeVersion
		r.Anticheat = sm.Anticheat
	}

	// Server manifest — fill anticheat for mods not in server mod list (e.g. client-only)
	if m.server.manifest != nil {
		for _, mm := range m.server.manifest.Mods {
			if r, ok := seen[mm.DirName]; ok && r.Anticheat == "" && mm.Anticheat != "" {
				r.Anticheat = mm.Anticheat
			}
		}
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

	// Reconcile duplicates: merge bare-name rows into Owner-Name counterparts.
	// E.g. "FastLink" merges into "Azumatt-FastLink".
	canonical := make(map[string]int) // lowercased suffix after first hyphen → row index
	for i, r := range rows {
		if idx := strings.Index(r.Name, "-"); idx >= 0 {
			suffix := strings.ToLower(r.Name[idx+1:])
			canonical[suffix] = i
		}
	}
	skip := make(map[int]bool)
	for i, r := range rows {
		if skip[i] {
			continue
		}
		if !strings.Contains(r.Name, "-") {
			if ci, ok := canonical[strings.ToLower(r.Name)]; ok && ci != i {
				mergeAuditRow(&rows[ci], &r)
				skip[i] = true
			}
		}
	}
	if len(skip) > 0 {
		merged := make([]auditRow, 0, len(rows)-len(skip))
		for i, r := range rows {
			if !skip[i] {
				merged = append(merged, r)
			}
		}
		rows = merged
	}

	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].Name < rows[j].Name
	})
	return rows
}

// mergeAuditRow merges src data into dst, filling in empty/placeholder fields.
func mergeAuditRow(dst, src *auditRow) {
	if dst.LocalVersion == "-" && src.LocalVersion != "-" {
		dst.LocalVersion = src.LocalVersion
	}
	if dst.ServerVersion == "-" && src.ServerVersion != "-" {
		dst.ServerVersion = src.ServerVersion
	}
	if dst.ServerRuntimeVer == "" && src.ServerRuntimeVer != "" {
		dst.ServerRuntimeVer = src.ServerRuntimeVer
	}
	if dst.ModpackVersion == "-" && src.ModpackVersion != "-" {
		dst.ModpackVersion = src.ModpackVersion
	}
	if dst.Anticheat == "" && src.Anticheat != "" {
		dst.Anticheat = src.Anticheat
	}
	if dst.Target == "both" && src.Target != "both" {
		dst.Target = src.Target
	}
}

// getOrRegisterMod returns the mod entry from the registry, or if not found,
// looks it up in locally-detected mods or server mods and registers it so
// metadata like target and anticheat can be persisted.
func (m model) getOrRegisterMod(name string) (config.ModEntry, bool) {
	mod, ok := m.reg.GetMod(m.cfg.ActiveProfile, name)
	if ok {
		return mod, true
	}
	// Check locally-detected mods
	pluginsDir := m.paths.ProfilePluginsDir(m.cfg.ActiveProfile)
	for _, local := range config.DetectLocalMods(pluginsDir, m.reg.Profiles[m.cfg.ActiveProfile]) {
		if local.FullName() == name {
			m.reg.SetMod(m.cfg.ActiveProfile, local)
			return local, true
		}
	}
	// Check server-only mods
	for _, sm := range m.server.mods {
		if sm.Name == name {
			var entry config.ModEntry
			if i := strings.Index(name, "-"); i >= 0 {
				entry = config.ModEntry{Owner: name[:i], Name: name[i+1:], Version: sm.Version, Target: "server"}
			} else {
				entry = config.ModEntry{Name: name, Version: sm.Version, Target: "server"}
			}
			m.reg.SetMod(m.cfg.ActiveProfile, entry)
			return entry, true
		}
	}
	return config.ModEntry{}, false
}

// filterAuditRows returns only rows matching the current filter.
func filterAuditRows(rows []auditRow, f modFilter) []auditRow {
	if f == filterAll {
		return rows
	}
	var out []auditRow
	for _, r := range rows {
		switch f {
		case filterLocal:
			if r.LocalVersion != "-" {
				out = append(out, r)
			}
		case filterServer:
			if r.ServerVersion != "-" {
				out = append(out, r)
			}
		case filterModpack:
			if r.ModpackVersion != "-" {
				out = append(out, r)
			}
		}
	}
	return out
}

// --- View ---

func (m model) viewMods() string {
	if m.isFullMode() {
		return m.viewModsFull()
	}
	return m.viewModsLocal()
}

func (m model) viewModsFull() string {
	var b strings.Builder

	// Preflight fetching
	if m.mods.preflightFetching {
		b.WriteString("\n  \033[33mChecking server mods...\033[0m\n\n")
		return b.String()
	}

	// Profile picker
	if m.mods.pickProfile {
		return m.viewProfilePicker()
	}

	// Install input
	if m.mods.installing && !m.mods.installBusy {
		target := "local"
		switch m.mods.filter {
		case filterServer:
			target = "server"
		case filterModpack:
			target = "modpack"
		}
		fmt.Fprintf(&b, "\n  Install mod → \033[36m%s\033[0m (Owner-Name, URL, or local path):\n\n", target)
		fmt.Fprintf(&b, "  > %s\033[7m \033[0m\n", m.mods.installInput)
		b.WriteString("\n  \033[2menter install • esc cancel\033[0m\n\n")
		return b.String()
	}

	// Installing/updating busy
	if m.mods.installBusy {
		query := m.mods.installInput
		if query == "" {
			query = "mod"
		}
		target := "locally"
		switch m.mods.filter {
		case filterServer:
			target = "to server"
		case filterModpack:
			target = "in modpack"
		}
		fmt.Fprintf(&b, "\n  \033[33mUpdating %s %s...\033[0m\n\n", query, target)
		return b.String()
	}

	// Scope picker
	if m.mods.scopePicker {
		return m.viewScopePicker()
	}

	// Filter header
	filterLabel := modFilterName(m.mods.filter)
	b.WriteString("\n")
	fmt.Fprintf(&b, "  \033[1mMods\033[0m  \033[2m[%s]\033[0m  \033[2m%s\033[0m\n", filterLabel, m.cfg.ActiveProfile)

	// Version mismatch banner (server filter only, selectable)
	pendingMods := pendingRestartMods(m.mods.auditRows)
	if m.mods.filter == filterServer && len(pendingMods) > 0 {
		if m.mods.cursor == -1 {
			fmt.Fprintf(&b, "  \033[36m>\033[0m \033[33m%d mod(s) out of sync\033[0m    \033[2menter to view\033[0m\n", len(pendingMods))
		} else {
			fmt.Fprintf(&b, "  \033[33m%d mod(s) out of sync\033[0m\n", len(pendingMods))
		}
	}
	b.WriteString("\n")

	// Audit list
	rows := filterAuditRows(m.mods.auditRows, m.mods.filter)
	cursor := m.mods.cursor
	if cursor >= len(rows) {
		cursor = max(0, len(rows)-1)
	}
	renderAuditList(&b, rows, cursor, listVisible(m.height, 12), m.anticheatSystem)

	// Status bar
	b.WriteString("\n")
	if m.mods.err != nil {
		fmt.Fprintf(&b, "  \033[31mError: %v\033[0m\n", m.mods.err)
	} else if m.mods.statusMsg != "" {
		fmt.Fprintf(&b, "  \033[32m%s\033[0m\n", m.mods.statusMsg)
	}
	hotkeys := []string{"↑/↓ navigate", "f filter", "x remove", "i install", "u update", "a moderation", "c config"}
	if m.mods.filter == filterLocal {
		hotkeys = append(hotkeys, "p profile")
	}
	if m.mods.filter == filterServer {
		hotkeys = append(hotkeys, "r restart server")
	}
	hotkeys = append(hotkeys, "s start", "tab next", "q quit")
	renderHotkeyBar(&b, hotkeys, m.width)

	return b.String()
}

func (m model) viewModsLocal() string {
	var b strings.Builder

	// Preflight fetching
	if m.mods.preflightFetching {
		b.WriteString("\n  \033[33mChecking server mods...\033[0m\n\n")
		return b.String()
	}

	// Profile picker
	if m.mods.pickProfile {
		return m.viewProfilePicker()
	}

	// Install input mode
	if m.mods.installing && !m.mods.installBusy {
		b.WriteString("\n  Install mod (Owner-Name, URL, or local path):\n\n")
		fmt.Fprintf(&b, "  > %s\033[7m \033[0m\n", m.mods.installInput)
		b.WriteString("\n  \033[2menter install • esc cancel\033[0m\n\n")
		return b.String()
	}

	// Installing busy
	if m.mods.installBusy {
		query := m.mods.installInput
		if query == "" {
			query = "mod"
		}
		fmt.Fprintf(&b, "\n  \033[33mInstalling %s...\033[0m\n\n", query)
		return b.String()
	}

	updateCount := len(m.local.updates)
	if m.local.checkingUpdates {
		b.WriteString("\n  \033[2mchecking for updates...\033[0m\n")
	} else if updateCount > 0 {
		if m.mods.cursor == -1 {
			fmt.Fprintf(&b, "\n  \033[36m>\033[0m \033[33m%d update(s) available\033[0m    \033[2menter to update all\033[0m\n", updateCount)
		} else {
			fmt.Fprintf(&b, "\n    \033[33m%d update(s) available\033[0m\n", updateCount)
		}
	}
	b.WriteString("\n")

	// Mod list
	items := make([]modListItem, len(m.local.mods))
	for i, mod := range m.local.mods {
		items[i] = modListItem{
			Name:     mod.FullName(),
			Version:  mod.Version,
			Disabled: mod.Disabled,
			Update:   m.local.updates[mod.FullName()],
		}
	}
	renderModList(&b, items, m.mods.cursor, listVisible(m.height, 11), false, m.anticheatSystem)

	// Status bar
	b.WriteString("\n")
	if m.mods.err != nil {
		fmt.Fprintf(&b, "  \033[31mError: %v\033[0m\n", m.mods.err)
	} else if m.mods.statusMsg != "" {
		fmt.Fprintf(&b, "  \033[32m%s\033[0m\n", m.mods.statusMsg)
	}
	hotkeys := []string{"↑/↓ navigate", "space toggle", "x remove", "u update", "i install", "c config", "o open folder", "s start", "p profile"}
	hotkeys = append(hotkeys, "tab next", "q quit")
	renderHotkeyBar(&b, hotkeys, m.width)

	return b.String()
}

// --- Shared modal views ---

func (m model) viewProfilePicker() string {
	var b strings.Builder
	if m.mods.creatingProfile {
		b.WriteString("\n  New profile name:\n\n")
		fmt.Fprintf(&b, "  > %s\033[7m \033[0m\n", m.mods.newProfileInput)
		b.WriteString("\n  \033[2menter create • esc cancel\033[0m\n\n")
		return b.String()
	}
	b.WriteString("\n  Switch profile:\n\n")
	for i, name := range m.mods.profiles {
		cursor := "  "
		if i == m.mods.profileCursor {
			cursor = "\033[36m>\033[0m "
		}
		active := ""
		if name == m.cfg.ActiveProfile {
			active = " \033[32m(active)\033[0m"
		}
		fmt.Fprintf(&b, "  %s%s%s\n", cursor, name, active)
	}
	b.WriteString("\n  \033[2m↑/↓ navigate • enter select • n new profile • esc back\033[0m\n\n")
	return b.String()
}

func (m model) viewScopePicker() string {
	var b strings.Builder
	row := m.filteredAuditRow()
	if row == nil {
		return ""
	}

	fmt.Fprintf(&b, "\n  \033[33mRemove %s\033[0m\n\n", row.Name)

	scopes := []struct {
		label   string
		checked bool
		exists  bool
	}{
		{"Local", m.mods.scopeLocal, row.LocalVersion != "-"},
		{"Server", m.mods.scopeServer, row.ServerVersion != "-"},
		{"Modpack", m.mods.scopeModpack, row.ModpackVersion != "-"},
	}

	b.WriteString("  ")
	for i, s := range scopes {
		if !s.exists {
			continue
		}
		cursor := " "
		if i == m.mods.scopeCursor {
			cursor = "\033[36m>\033[0m"
		}
		check := " "
		if s.checked {
			check = "x"
		}
		fmt.Fprintf(&b, " %s[%s] %s  ", cursor, check, s.label)
	}
	b.WriteString("\n\n")
	b.WriteString("  \033[2mspace toggle • h/l move • enter confirm • esc cancel\033[0m\n\n")

	return b.String()
}

// --- Key handlers ---

func (m model) handleModsKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Modal stack (checked before global keys in tui.go Update)

	// Install input
	if m.mods.installing {
		return m.handleModsInstallInput(msg)
	}
	// Profile picker
	if m.mods.pickProfile {
		return m.handleModsProfilePicker(msg)
	}
	// Scope picker
	if m.mods.scopePicker {
		return m.handleModsScopePicker(msg)
	}

	// Clear transient status on any key press
	m.mods.statusMsg = ""

	// Shared actions (same in full and local-only mode)
	switch msg.String() {
	case "i":
		m.mods.installing = true
		m.mods.installInput = ""
		m.mods.err = nil
		return m, nil
	case "p":
		return m.openProfilePicker()
	case "s":
		return m.modsStartGame()
	case "o":
		return m, openFile(m.paths.ProfileDir(m.cfg.ActiveProfile))
	}

	if m.isFullMode() {
		return m.handleModsKeysFull(msg)
	}
	return m.handleModsKeysLocal(msg)
}

func (m model) handleModsKeysFull(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := filterAuditRows(m.mods.auditRows, m.mods.filter)
	n := len(rows)
	if m.mods.cursor >= n && n > 0 {
		m.mods.cursor = n - 1
	}

	hasBanner := m.mods.filter == filterServer && len(pendingRestartMods(m.mods.auditRows)) > 0

	switch msg.String() {
	case "up", "k":
		if m.mods.cursor > 0 {
			m.mods.cursor--
		} else if m.mods.cursor == 0 && hasBanner {
			m.mods.cursor = -1
		}
	case "down", "j":
		if m.mods.cursor == -1 {
			m.mods.cursor = 0
		} else if m.mods.cursor < n-1 {
			m.mods.cursor++
		}

	case "f":
		// Cycle filter
		switch m.mods.filter {
		case filterAll:
			m.mods.filter = filterLocal
		case filterLocal:
			m.mods.filter = filterServer
		case filterServer:
			m.mods.filter = filterModpack
		case filterModpack:
			m.mods.filter = filterAll
		}
		m.mods.cursor = 0

	case "x":
		// Remove — context-sensitive based on active filter
		if n == 0 {
			return m, nil
		}
		row := rows[m.mods.cursor]
		if m.mods.filter == filterServer {
			if row.ServerVersion == "-" {
				m.mods.err = fmt.Errorf("not on server")
				return m, nil
			}
			modName := row.Name
			m.confirm = confirmModal{
				Active: true,
				Prompt: fmt.Sprintf("Remove %s from server?", modName),
				OnYes: func(m model) (tea.Model, tea.Cmd) {
					m.reg.RemoveMod(m.cfg.ActiveProfile, modName)
					config.SaveRegistry(m.paths, *m.reg)
					m.refreshMods()
					m.mods.auditRows = m.buildAuditRows()
					if m.server.client != nil {
						return m, removeModFromServer(m.server.client, modName)
					}
					return m, nil
				},
			}
			m.mods.err = nil
			return m, nil
		}
		if m.mods.filter == filterModpack {
			if row.ModpackVersion == "-" {
				m.mods.err = fmt.Errorf("not in modpack")
				return m, nil
			}
			modName := row.Name
			m.confirm = confirmModal{
				Active: true,
				Prompt: fmt.Sprintf("Remove %s from modpack?", modName),
				OnYes: func(m model) (tea.Model, tea.Cmd) {
					if m.cfg.ModpackPath != "" {
						if err := modpack.RemoveDep(m.cfg.ModpackPath, modName); err != nil {
							m.mods.err = err
						} else {
							m.modpack.loadFromDisk(m.cfg.ModpackPath)
							m.mods.auditRows = m.buildAuditRows()
						}
					}
					return m, nil
				},
			}
			m.mods.err = nil
			return m, nil
		}
		if row.LocalVersion == "-" {
			m.mods.err = fmt.Errorf("not installed locally")
			return m, nil
		}
		places := 0
		if row.LocalVersion != "-" {
			places++
		}
		if row.ServerVersion != "-" {
			places++
		}
		if row.ModpackVersion != "-" {
			places++
		}
		if places > 1 && m.mods.filter == filterAll {
			// Open scope picker
			m.mods.scopePicker = true
			m.mods.scopeLocal = row.LocalVersion != "-"
			m.mods.scopeServer = false
			m.mods.scopeModpack = false
			m.mods.scopeCursor = 0
		} else {
			modName := row.Name
			m.confirm = confirmModal{
				Active: true,
				Prompt: fmt.Sprintf("Remove %s locally?", modName),
				OnYes: func(m model) (tea.Model, tea.Cmd) {
					m.modsRemoveMod(modName)
					return m, nil
				},
			}
		}
		m.mods.err = nil

	case "u":
		// Update selected mod — context-sensitive based on active filter
		if n == 0 {
			return m, nil
		}
		row := rows[m.mods.cursor]
		if m.mods.filter == filterServer {
			// Update on server: push local version if local differs from server
			if row.ServerVersion != "-" && row.LocalVersion != "-" && row.ServerVersion != row.LocalVersion {
				m.mods.installBusy = true
				m.mods.err = nil
				return m, updateModToServer(m.paths, m.cfg, m.reg, row.Name, m.server.client)
			}
			// Otherwise check for Thunderstore update
			if _, ok := m.local.updates[row.Name]; !ok {
				m.mods.err = fmt.Errorf("no update available")
				return m, nil
			}
			m.mods.installBusy = true
			m.mods.err = nil
			return m, updateModToServer(m.paths, m.cfg, m.reg, row.Name, m.server.client)
		}
		if m.mods.filter == filterModpack {
			// Update modpack dep to match local version
			mod, ok := m.reg.GetMod(m.cfg.ActiveProfile, row.Name)
			if !ok || mod.Version == "" {
				m.mods.err = fmt.Errorf("not in local registry")
				return m, nil
			}
			if row.ModpackVersion == mod.Version {
				m.mods.err = fmt.Errorf("modpack already up to date")
				return m, nil
			}
			if m.cfg.ModpackPath == "" {
				m.mods.err = fmt.Errorf("no modpack configured")
				return m, nil
			}
			m.mods.installBusy = true
			m.mods.err = nil
			return m, updateModpackDep(m.cfg.ModpackPath, row.Name, mod.Version)
		}
		// Default: update locally
		if _, ok := m.local.updates[row.Name]; ok {
			m.mods.installBusy = true
			m.mods.err = nil
			return m, updateMod(m.paths, m.cfg, m.reg, row.Name)
		}

	case "a":
		// Cycle anticheat — server is source of truth, send directly
		if n == 0 {
			return m, nil
		}
		row := rows[m.mods.cursor]
		if m.server.client == nil {
			m.mods.err = fmt.Errorf("no server connection")
			return m, nil
		}
		newAC := nextAnticheatValue(row.Anticheat, m.anticheatSystem)
		// Optimistic update — show new value immediately
		for i := range m.mods.auditRows {
			if m.mods.auditRows[i].Name == row.Name {
				m.mods.auditRows[i].Anticheat = newAC
				break
			}
		}
		// Include GUID and version for mods not on server (e.g. client-only)
		guid, version := "", ""
		if mod, ok := m.reg.GetMod(m.cfg.ActiveProfile, row.Name); ok {
			guid = mod.GUID
			version = mod.Version
		} else {
			// Fallback: match by Name field across all registered mods
			for _, mod := range m.reg.ListMods(m.cfg.ActiveProfile) {
				if strings.EqualFold(mod.Name, row.Name) {
					guid = mod.GUID
					version = mod.Version
					break
				}
			}
		}
		return m, updateServerModerationFull(m.server.client, row.Name, newAC, guid, version)

	case "c":
		// Open config file for selected mod
		if n == 0 {
			return m, nil
		}
		row := rows[m.mods.cursor]
		mod, ok := m.reg.GetMod(m.cfg.ActiveProfile, row.Name)
		if !ok {
			// Try to open config dir as fallback
			return m, openFile(m.paths.ProfileConfigDir(m.cfg.ActiveProfile))
		}
		path := findConfigFile(m.paths, m.cfg.ActiveProfile, mod)
		return m, openFile(path)

	case "r":
		// Restart server (server filter only)
		if m.mods.filter == filterServer && m.server.client != nil && m.server.role == "admin" {
			m.confirm = confirmModal{
				Active: true, Prompt: "Restart server?",
				OnYes: func(m model) (tea.Model, tea.Cmd) {
					m.server.actionBusy = true
					m.server.actionMsg = "Restarting server..."
					return m, serverAction(m.server.client, "restart")
				},
			}
		}

	case "enter":
		if m.mods.cursor == -1 && hasBanner {
			m.confirm = buildOutOfSyncModal(pendingRestartMods(m.mods.auditRows))
			return m, nil
		}
	}
	return m, nil
}

func (m model) handleModsKeysLocal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.mods.cursor > 0 {
			m.mods.cursor--
		} else if m.mods.cursor == 0 && len(m.local.updates) > 0 {
			m.mods.cursor = -1
		}
	case "down", "j":
		if m.mods.cursor == -1 {
			m.mods.cursor = 0
		} else if m.mods.cursor < len(m.local.mods)-1 {
			m.mods.cursor++
		}
	case "enter":
		if m.mods.cursor == -1 && len(m.local.updates) > 0 {
			m.confirm = buildUpdateAllConfirm(m)
			return m, nil
		}
	case " ":
		if len(m.local.mods) > 0 && m.mods.cursor >= 0 {
			mod := m.local.mods[m.mods.cursor]
			if mod.IsLocal {
				pluginsDir := m.paths.ProfilePluginsDir(m.cfg.ActiveProfile)
				if err := installer.ToggleLocalMod(pluginsDir, mod); err != nil {
					m.mods.err = err
				} else {
					m.local.mods[m.mods.cursor].Disabled = !m.local.mods[m.mods.cursor].Disabled
					m.mods.err = nil
				}
			} else if err := installer.Toggle(m.paths, m.cfg, m.reg, mod.FullName()); err != nil {
				m.mods.err = err
			} else {
				m.local.mods[m.mods.cursor].Disabled = !m.local.mods[m.mods.cursor].Disabled
				m.mods.err = nil
				config.SaveRegistry(m.paths, *m.reg)
			}
		}
	case "x":
		if len(m.local.mods) > 0 && m.mods.cursor >= 0 {
			modName := m.local.mods[m.mods.cursor].FullName()
			m.confirm = confirmModal{
				Active: true,
				Prompt: fmt.Sprintf("Remove %s locally?", modName),
				OnYes: func(m model) (tea.Model, tea.Cmd) {
					m.modsRemoveMod(modName)
					return m, nil
				},
			}
			m.mods.err = nil
		}
	case "u":
		if len(m.local.mods) > 0 && m.mods.cursor >= 0 {
			mod := m.local.mods[m.mods.cursor]
			if _, ok := m.local.updates[mod.FullName()]; ok {
				m.mods.installBusy = true
				m.mods.err = nil
				return m, updateMod(m.paths, m.cfg, m.reg, mod.FullName())
			}
		}
	case "c":
		if len(m.local.mods) > 0 && m.mods.cursor >= 0 {
			mod := m.local.mods[m.mods.cursor]
			path := findConfigFile(m.paths, m.cfg.ActiveProfile, mod)
			return m, openFile(path)
		}
	}
	return m, nil
}

// --- Modal handlers ---

func (m model) handleModsInstallInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mods.installing = false
	case "enter":
		if m.mods.installInput != "" {
			m.mods.installBusy = true
			if m.mods.filter == filterServer {
				return m, installModToServer(m.mods.installInput, m.server.client)
			}
			if m.mods.filter == filterModpack {
				return m, installModToModpack(m.cfg.ModpackPath, m.mods.installInput)
			}
			return m, installMod(m.paths, m.cfg, m.reg, m.mods.installInput, "both")
		}
	case "backspace":
		if len(m.mods.installInput) > 0 {
			m.mods.installInput = m.mods.installInput[:len(m.mods.installInput)-1]
		}
	case "ctrl+c":
		return m, tea.Quit
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			m.mods.installInput += string(msg.Runes)
		}
	}
	return m, nil
}

func (m model) handleModsProfilePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mods.creatingProfile {
		switch msg.String() {
		case "esc":
			m.mods.creatingProfile = false
		case "enter":
			if m.mods.newProfileInput != "" {
				if err := profile.Create(m.paths, m.mods.newProfileInput); err != nil {
					m.mods.err = err
				} else {
					m.reg.EnsureProfile(m.mods.newProfileInput)
					config.SaveRegistry(m.paths, *m.reg)
					if err := profile.Switch(m.paths, &m.cfg, m.mods.newProfileInput); err != nil {
						m.mods.err = err
					} else {
						config.Save(m.paths, m.cfg)
						m.mods.cursor = 0
						m.refreshMods()
						m.anticheatSystem = resolveAnticheatSystem(m.cfg, m.local.mods)
						m.local.updates = make(map[string]string)
						m.mods.err = nil
						m.mods.pickProfile = false
						m.mods.creatingProfile = false
						if m.isFullMode() {
							m.mods.auditRows = m.buildAuditRows()
						}
						return m, nil
					}
				}
				m.mods.creatingProfile = false
			}
		case "backspace":
			if len(m.mods.newProfileInput) > 0 {
				m.mods.newProfileInput = m.mods.newProfileInput[:len(m.mods.newProfileInput)-1]
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				m.mods.newProfileInput += string(msg.Runes)
			}
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "esc":
		m.mods.pickProfile = false
	case "up", "k":
		if m.mods.profileCursor > 0 {
			m.mods.profileCursor--
		}
	case "down", "j":
		if m.mods.profileCursor < len(m.mods.profiles)-1 {
			m.mods.profileCursor++
		}
	case "enter":
		name := m.mods.profiles[m.mods.profileCursor]
		if name != m.cfg.ActiveProfile {
			if err := profile.Switch(m.paths, &m.cfg, name); err != nil {
				m.mods.err = err
			} else {
				config.Save(m.paths, m.cfg)
				m.mods.cursor = 0
				m.refreshMods()
				m.anticheatSystem = resolveAnticheatSystem(m.cfg, m.local.mods)
				m.local.updates = make(map[string]string)
				m.local.checkingUpdates = true
				m.mods.err = nil
				m.mods.pickProfile = false
				if m.isFullMode() {
					m.mods.auditRows = m.buildAuditRows()
				}
				return m, checkUpdates(m.local.mods)
			}
		}
		m.mods.pickProfile = false
	case "n":
		m.mods.creatingProfile = true
		m.mods.newProfileInput = ""
		m.mods.err = nil
	}
	return m, nil
}

func (m model) handleModsScopePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	row := m.filteredAuditRow()
	if row == nil {
		m.mods.scopePicker = false
		return m, nil
	}

	// Count which scopes exist
	type scopeInfo struct {
		exists  bool
		checked *bool
	}
	scopes := []scopeInfo{
		{row.LocalVersion != "-", &m.mods.scopeLocal},
		{row.ServerVersion != "-", &m.mods.scopeServer},
		{row.ModpackVersion != "-", &m.mods.scopeModpack},
	}

	switch msg.String() {
	case "esc", "q":
		m.mods.scopePicker = false
	case "h", "left":
		for i := m.mods.scopeCursor - 1; i >= 0; i-- {
			if scopes[i].exists {
				m.mods.scopeCursor = i
				break
			}
		}
	case "l", "right", "tab":
		for i := m.mods.scopeCursor + 1; i < 3; i++ {
			if scopes[i].exists {
				m.mods.scopeCursor = i
				break
			}
		}
	case " ":
		if m.mods.scopeCursor < 3 && scopes[m.mods.scopeCursor].exists {
			*scopes[m.mods.scopeCursor].checked = !*scopes[m.mods.scopeCursor].checked
		}
	case "enter":
		m.mods.scopePicker = false
		// Execute scoped removal — local is immediate, server/modpack via Changes tab
		if m.mods.scopeLocal && row.LocalVersion != "-" {
			m.modsRemoveMod(row.Name)
		} else if m.isFullMode() {
			// Rebuild audit rows even without local removal (scope may have changed)
			m.mods.auditRows = m.buildAuditRows()
		}
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// --- Shared action helpers ---

func (m model) openProfilePicker() (tea.Model, tea.Cmd) {
	profiles, err := profile.List(m.paths)
	if err != nil {
		m.mods.err = err
		return m, nil
	}
	m.mods.profiles = profiles
	m.mods.profileCursor = 0
	for i, name := range profiles {
		if name == m.cfg.ActiveProfile {
			m.mods.profileCursor = i
			break
		}
	}
	m.mods.pickProfile = true
	m.mods.err = nil
	return m, nil
}

func (m model) modsStartGame() (tea.Model, tea.Cmd) {
	if m.local.gameRunning {
		return m, nil
	}
	if m.server.client != nil {
		if len(m.server.mods) == 0 {
			m.mods.preflightFetching = true
			return m, fetchServerStatus(m.server.client)
		}
		warnings := preflightCheck(m.local.mods, m.server.mods)
		if len(warnings) > 0 {
			m.confirm = buildPreflightConfirm(warnings)
			return m, nil
		}
	}
	return m, startGame(m.paths, m.cfg)
}

// modsRemoveMod removes a mod by name from the local profile.
func (m *model) modsRemoveMod(modName string) {
	mod, ok := m.reg.GetMod(m.cfg.ActiveProfile, modName)
	if !ok {
		return
	}
	var err error
	if mod.IsLocal {
		pluginsDir := m.paths.ProfilePluginsDir(m.cfg.ActiveProfile)
		err = installer.RemoveLocalMod(pluginsDir, mod)
	} else {
		err = installer.Remove(m.paths, m.cfg, m.reg, mod.FullName())
	}
	if err != nil {
		m.mods.err = err
	} else {
		m.mods.err = nil
		config.SaveRegistry(m.paths, *m.reg)
		m.refreshMods()
		if m.isFullMode() {
			m.mods.auditRows = m.buildAuditRows()
		}
	}
}

// --- Confirm modal builders ---

func buildPreflightConfirm(warnings []string) confirmModal {
	var body strings.Builder
	for _, w := range warnings {
		fmt.Fprintf(&body, "    %s\n", w)
	}
	return confirmModal{
		Active: true,
		Prompt: "Mod mismatch with server — start anyway?",
		Body:   body.String(),
		OnYes: func(m model) (tea.Model, tea.Cmd) {
			return m, startGame(m.paths, m.cfg)
		},
	}
}

func buildUpdateAllConfirm(m model) confirmModal {
	var body strings.Builder
	for name, latest := range m.local.updates {
		cur := ""
		for _, mod := range m.local.mods {
			if mod.FullName() == name {
				cur = mod.Version
				break
			}
		}
		fmt.Fprintf(&body, "    \033[33m%s\033[0m  %s → %s\n", name, cur, latest)
	}
	count := len(m.local.updates)
	return confirmModal{
		Active: true,
		Prompt: fmt.Sprintf("Update %d mod(s)?", count),
		Body:   body.String(),
		OnYes: func(m model) (tea.Model, tea.Cmd) {
			m.mods.installBusy = true
			m.mods.err = nil
			return m, updateAllMods(m.paths, m.cfg, m.reg, m.local.updates)
		},
	}
}

// pendingRestartMods returns audit rows where local and server versions differ.
func pendingRestartMods(rows []auditRow) []auditRow {
	var out []auditRow
	for _, r := range rows {
		if r.LocalVersion != "-" && r.ServerVersion != "-" && r.LocalVersion != r.ServerVersion {
			out = append(out, r)
		}
	}
	return out
}

func buildOutOfSyncModal(mods []auditRow) confirmModal {
	var body strings.Builder
	for _, r := range mods {
		fmt.Fprintf(&body, "    \033[33m%s\033[0m  local %s → server %s\n", r.Name, r.LocalVersion, r.ServerVersion)
	}
	return confirmModal{
		Active: true,
		Prompt: fmt.Sprintf("%d mod(s) out of sync with server", len(mods)),
		Body:   body.String(),
	}
}

// --- Helpers ---

// filteredAuditRow returns the audit row at the current cursor position in the filtered view.
func (m model) filteredAuditRow() *auditRow {
	rows := filterAuditRows(m.mods.auditRows, m.mods.filter)
	if m.mods.cursor < 0 || m.mods.cursor >= len(rows) {
		return nil
	}
	return &rows[m.mods.cursor]
}

// --- Audit list rendering (moved from sync.go) ---

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

	// Hide target column when enforcer handles server-only classification.
	showTarget := anticheatSystem != "enforcer"

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
		if showTarget {
			if w := displayWidth(r.Target); w > colTarget {
				colTarget = w
			}
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
	if showTarget {
		b.WriteString(padRight("Target", colTarget))
	}
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

		namePad := padRight(r.Name, colName)
		if i == cursor {
			nameWidth := displayWidth(r.Name)
			namePad = r.Name + "\033[2m" + strings.Repeat("─", colName-nameWidth-1) + "\033[0m "
		}

		targetCol := ""
		if showTarget {
			targetCol = auditTargetColor(r.Target, padRight(r.Target, colTarget))
		}

		fmt.Fprintf(b, "  %s%s%s%s%s%s%s\n", cur,
			namePad,
			padRight(r.LocalVersion, colLocal),
			padRight(r.ServerVersion, colServer),
			padRight(r.ModpackVersion, colModpack),
			targetCol,
			auditModerationColor(r.Anticheat, modLabels[i]),
		)
	}

	if end < len(rows) {
		fmt.Fprintf(b, "  \033[2m  ↓ %d more\033[0m\n", len(rows)-end)
	}
}

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
	case "serveronly":
		return "server only"
	default:
		return "-"
	}
}

func auditModerationColor(anticheat, text string) string {
	switch anticheat {
	case "whitelist":
		return "\033[32m" + text + "\033[0m"
	case "greylist":
		return "\033[33m" + text + "\033[0m"
	case "adminonly":
		return "\033[35m" + text + "\033[0m"
	case "serveronly":
		return "\033[34m" + text + "\033[0m"
	default:
		return "\033[2m" + text + "\033[0m"
	}
}
