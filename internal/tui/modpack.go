package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"mmcli/internal/config"
	"mmcli/internal/modpack"
	"mmcli/internal/thunderstore"
)

// Async messages for modpack dep actions.
type modpackUpdateCheckDoneMsg struct {
	updates map[string]string // Owner-Name -> latest version
}
type modpackUpdateDoneMsg struct{ err error }

// Async messages for modpack actions.
type modpackPublishDoneMsg struct{ err error }

type modpackDep struct {
	Owner   string
	Name    string
	Version string
	Raw     string
}

type modpackModel struct {
	manifest     *modpack.Manifest
	deps         []modpackDep
	versionMap   map[string]string // Owner-Name -> Version, for sync view
	depCursor    int
	readmeLines  []string
	readmeScroll int
	iconFile     string   // "icon.png" or ""
	configFiles  []string // files in config/ subdir
	configCursor int
	loadErr      error  // fatal: can't read manifest — blocks all tabs
	statusMsg    string // transient info/error shown on Mods tab only
	editingPath  bool
	pathInput    string

	// Mod actions state
	confirmRemove    bool
	depUpdates       map[string]string // Owner-Name -> latest version
	checkingUpdates  bool
	updatingDep      bool

	// Sync state
	confirmSync bool
	syncDiff    []modpack.SyncDiffItem // preview of what sync will change

	// Publish state
	confirmPublish bool
	publishing     bool
	publishErr     error
	publishDone    bool

	// Settings state
	settingsCursor int    // 0=token, 1=author, 2=path
	editingField   int    // -1=none, 0=token, 1=author, 2=path
	fieldInput     string // current input buffer
}

const modpackReadmeVisible = 30

func (mp *modpackModel) loadFromDisk(modpackPath string) {
	// Reset state
	mp.manifest = nil
	mp.deps = nil
	mp.depCursor = 0
	mp.readmeScroll = 0
	mp.readmeLines = nil
	mp.iconFile = ""
	mp.configFiles = nil
	mp.configCursor = 0
	mp.versionMap = nil
	mp.loadErr = nil
	mp.statusMsg = ""
	mp.editingField = -1
	mp.depUpdates = nil
	mp.checkingUpdates = false

	if modpackPath == "" {
		return
	}

	manifest, err := modpack.LoadManifest(modpackPath)
	if err != nil {
		mp.loadErr = err
		return
	}
	mp.manifest = manifest

	// Parse dependencies
	for _, dep := range manifest.Dependencies {
		ref := thunderstore.ParseDep(dep)
		mp.deps = append(mp.deps, modpackDep{
			Owner:   ref.Owner,
			Name:    ref.Name,
			Version: ref.Version,
			Raw:     dep,
		})
	}

	// Sort deps alphabetically by Owner-Name
	sort.Slice(mp.deps, func(i, j int) bool {
		ni := fmt.Sprintf("%s-%s", mp.deps[i].Owner, mp.deps[i].Name)
		nj := fmt.Sprintf("%s-%s", mp.deps[j].Owner, mp.deps[j].Name)
		return ni < nj
	})

	// Build modpack version map for sync view
	mp.versionMap = make(map[string]string)
	for _, dep := range mp.deps {
		mp.versionMap[fmt.Sprintf("%s-%s", dep.Owner, dep.Name)] = dep.Version
	}

	// Read README.md (optional)
	if readmeData, err := os.ReadFile(filepath.Join(modpackPath, "README.md")); err == nil {
		mp.readmeLines = strings.Split(string(readmeData), "\n")
	}

	// Check icon.png
	if _, err := os.Stat(filepath.Join(modpackPath, "icon.png")); err == nil {
		mp.iconFile = "icon.png"
	}

	// List config files
	configDir := filepath.Join(modpackPath, "config")
	if entries, err := os.ReadDir(configDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				mp.configFiles = append(mp.configFiles, e.Name())
			}
		}
	}
}

func checkModpackUpdates(deps []modpackDep) tea.Cmd {
	return func() tea.Msg {
		updates := make(map[string]string)
		for _, dep := range deps {
			if dep.Owner == "" || dep.Name == "" {
				continue
			}
			pkg, err := thunderstore.GetPackage(dep.Owner, dep.Name)
			if err != nil || len(pkg.Versions) == 0 {
				continue
			}
			latest := pkg.Versions[0].VersionNumber
			if latest != dep.Version {
				updates[fmt.Sprintf("%s-%s", dep.Owner, dep.Name)] = latest
			}
		}
		return modpackUpdateCheckDoneMsg{updates: updates}
	}
}

func updateModpackDep(modpackPath, ownerName, newVersion string) tea.Cmd {
	return func() tea.Msg {
		return modpackUpdateDoneMsg{err: modpack.UpdateDep(modpackPath, ownerName, newVersion)}
	}
}

func publishModpack(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		err := thunderstore.Publish(cfg.ThunderstoreToken, cfg.ThunderstoreAuthor, cfg.ModpackPath)
		return modpackPublishDoneMsg{err: err}
	}
}

// --- Key handlers ---

func (m model) handleModpackNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Settings field editing modal
	if m.modpack.editingField >= 0 {
		switch msg.String() {
		case "esc":
			m.modpack.editingField = -1
		case "enter":
			switch m.modpack.editingField {
			case 0:
				m.cfg.ThunderstoreToken = m.modpack.fieldInput
			case 1:
				m.cfg.ThunderstoreAuthor = m.modpack.fieldInput
			case 2:
				m.cfg.ModpackPath = m.modpack.fieldInput
				m.modpack.loadFromDisk(m.cfg.ModpackPath)
			}
			config.Save(m.paths, m.cfg)
			m.modpack.editingField = -1
		case "backspace":
			if len(m.modpack.fieldInput) > 0 {
				m.modpack.fieldInput = m.modpack.fieldInput[:len(m.modpack.fieldInput)-1]
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				m.modpack.fieldInput += string(msg.Runes)
			}
		}
		return m, nil
	}

	// Path input modal
	if m.modpack.editingPath {
		switch msg.String() {
		case "esc":
			m.modpack.editingPath = false
		case "enter":
			if m.modpack.pathInput != "" {
				m.cfg.ModpackPath = m.modpack.pathInput
				config.Save(m.paths, m.cfg)
				m.modpack.editingPath = false
				m.modpack.loadFromDisk(m.cfg.ModpackPath)
			}
		case "backspace":
			if len(m.modpack.pathInput) > 0 {
				m.modpack.pathInput = m.modpack.pathInput[:len(m.modpack.pathInput)-1]
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				m.modpack.pathInput += string(msg.Runes)
			}
		}
		return m, nil
	}

	// Remove confirmation modal
	if m.modpack.confirmRemove {
		switch msg.String() {
		case "y":
			m.modpack.confirmRemove = false
			dep := m.modpack.deps[m.modpack.depCursor]
			ownerName := fmt.Sprintf("%s-%s", dep.Owner, dep.Name)
			if err := modpack.RemoveDep(m.cfg.ModpackPath, ownerName); err != nil {
				m.modpack.statusMsg = err.Error()
			} else {
				m.modpack.loadFromDisk(m.cfg.ModpackPath)
				if m.modpack.depCursor >= len(m.modpack.deps) {
					m.modpack.depCursor = max(0, len(m.modpack.deps)-1)
				}
				m.modpack.statusMsg = "dependency removed"
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			m.modpack.confirmRemove = false
		}
		return m, nil
	}

	// Updating dep busy
	if m.modpack.updatingDep {
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}

	// Sync confirmation modal
	if m.modpack.confirmSync {
		switch msg.String() {
		case "y":
			m.modpack.confirmSync = false
			if err := modpack.SyncManifestDeps(m.cfg.ModpackPath, m.reg, m.cfg.ActiveProfile); err != nil {
				m.modpack.statusMsg = err.Error()
			} else {
				m.modpack.loadFromDisk(m.cfg.ModpackPath)
				m.modpack.statusMsg = "dependencies synced"
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			m.modpack.confirmSync = false
		}
		return m, nil
	}

	// Publish confirmation modal
	if m.modpack.confirmPublish {
		switch msg.String() {
		case "y":
			m.modpack.confirmPublish = false
			m.modpack.publishing = true
			m.modpack.publishErr = nil
			return m, publishModpack(m.cfg)
		case "ctrl+c":
			return m, tea.Quit
		default:
			m.modpack.confirmPublish = false
		}
		return m, nil
	}

	// Publishing busy
	if m.modpack.publishing {
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}

	// Publish done acknowledgment
	if m.modpack.publishDone {
		m.modpack.publishDone = false
		m.modpack.publishErr = nil
		return m, nil
	}

	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "`":
		return m, m.enterSyncMode()
	case "1":
		return m, m.enterLocalMode()
	case "2":
		return m, m.enterServerMode()
	case "4":
		return m, m.enterSyncMode()
	case "d":
		m.modpack.editingPath = true
		m.modpack.pathInput = m.cfg.ModpackPath
	case "tab":
		cmd := m.cycleModpackTab(1)
		return m, cmd
	case "shift+tab":
		cmd := m.cycleModpackTab(-1)
		return m, cmd
	}

	// Tab-specific keys
	switch m.activeModpackTab {
	case contentModpackMods:
		return m.handleModpackModsKeys(msg)
	case contentModpackConfig:
		return m.handleModpackConfigKeys(msg)
	case contentModpackReadme:
		return m.handleModpackReadmeKeys(msg)
	case contentModpackManifest:
		return m.handleModpackManifestKeys(msg)
	case contentModpackImage:
		return m.handleModpackImageKeys(msg)
	case contentModpackSettings:
		return m.handleModpackSettingsKeys(msg)
	}
	return m, nil
}

func (m model) handleModpackModsKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.modpack.depCursor > 0 {
			m.modpack.depCursor--
		}
	case "down", "j":
		if m.modpack.depCursor < len(m.modpack.deps)-1 {
			m.modpack.depCursor++
		}
	case "x":
		if len(m.modpack.deps) > 0 {
			m.modpack.confirmRemove = true
			m.modpack.statusMsg = ""
		}
	case "u":
		if len(m.modpack.deps) > 0 {
			dep := m.modpack.deps[m.modpack.depCursor]
			ownerName := fmt.Sprintf("%s-%s", dep.Owner, dep.Name)
			if latest, ok := m.modpack.depUpdates[ownerName]; ok {
				m.modpack.updatingDep = true
				m.modpack.statusMsg = ""
				return m, updateModpackDep(m.cfg.ModpackPath, ownerName, latest)
			}
		}
	case "c":
		if len(m.modpack.deps) > 0 && !m.modpack.checkingUpdates {
			m.modpack.checkingUpdates = true
			m.modpack.statusMsg = "checking for updates..."
			return m, checkModpackUpdates(m.modpack.deps)
		}
	case "s":
		if m.modpack.manifest != nil {
			diff := modpack.BuildSyncDiff(m.reg, m.cfg.ActiveProfile, m.modpack.manifest)
			if len(diff) == 0 {
				m.modpack.statusMsg = "dependencies already match profile"
				return m, nil
			}
			m.modpack.syncDiff = diff
			m.modpack.confirmSync = true
			m.modpack.statusMsg = ""
		}
	case "p":
		if m.modpack.manifest == nil {
			return m, nil
		}
		if m.cfg.ThunderstoreToken == "" {
			m.modpack.statusMsg = "set thunderstore_token in config.json first"
			return m, nil
		}
		if m.cfg.ThunderstoreAuthor == "" {
			m.modpack.statusMsg = "set thunderstore_author in config.json first"
			return m, nil
		}
		if m.modpack.iconFile == "" {
			m.modpack.statusMsg = "icon.png is required to publish"
			return m, nil
		}
		m.modpack.confirmPublish = true
		m.modpack.statusMsg = ""
	}
	return m, nil
}

func (m model) handleModpackConfigKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.modpack.configCursor > 0 {
			m.modpack.configCursor--
		}
	case "down", "j":
		if m.modpack.configCursor < len(m.modpack.configFiles)-1 {
			m.modpack.configCursor++
		}
	case "o":
		if len(m.modpack.configFiles) > 0 && m.modpack.configCursor < len(m.modpack.configFiles) {
			path := filepath.Join(m.cfg.ModpackPath, "config", m.modpack.configFiles[m.modpack.configCursor])
			return m, openFile(path)
		}
	}
	return m, nil
}

func (m model) handleModpackReadmeKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	maxScroll := max(0, len(m.modpack.readmeLines)-modpackReadmeVisible)
	switch msg.String() {
	case "up", "k":
		if m.modpack.readmeScroll > 0 {
			m.modpack.readmeScroll--
		}
	case "down", "j":
		if m.modpack.readmeScroll < maxScroll {
			m.modpack.readmeScroll++
		}
	case "o":
		return m, openFile(filepath.Join(m.cfg.ModpackPath, "README.md"))
	}
	return m, nil
}

func (m model) handleModpackImageKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "o":
		if m.cfg.ModpackPath != "" {
			return m, openFile(m.cfg.ModpackPath)
		}
	}
	return m, nil
}

// --- Views ---

func (m model) viewModpackPathInput() string {
	var b strings.Builder
	b.WriteString("\n  Modpack directory path:\n\n")
	fmt.Fprintf(&b, "  > %s\033[7m \033[0m\n", m.modpack.pathInput)
	b.WriteString("\n  \033[2menter save • esc cancel\033[0m\n\n")
	return b.String()
}

func (m model) modpackNotConfigured() (string, bool) {
	if m.cfg.ModpackPath == "" {
		var b strings.Builder
		b.WriteString("\n  No modpack path configured.\n\n")
		hotkeys := []string{"d set path", "` mode", "q quit"}
		renderHotkeyBar(&b, hotkeys, m.width)
		return b.String(), true
	}
	if m.modpack.loadErr != nil {
		var b strings.Builder
		fmt.Fprintf(&b, "\n  \033[31mError: %v\033[0m\n\n", m.modpack.loadErr)
		hotkeys := []string{"d set path", "` mode", "tab next", "q quit"}
		renderHotkeyBar(&b, hotkeys, m.width)
		return b.String(), true
	}
	return "", false
}

func (m model) viewModpackMods() string {
	if s, notReady := m.modpackNotConfigured(); notReady {
		return s
	}

	var b strings.Builder

	// Remove confirmation modal
	if m.modpack.confirmRemove && m.modpack.depCursor < len(m.modpack.deps) {
		dep := m.modpack.deps[m.modpack.depCursor]
		fmt.Fprintf(&b, "\n  \033[1mRemove %s-%s from modpack?\033[0m\n\n", dep.Owner, dep.Name)
		b.WriteString("  \033[33my confirm • any key cancel\033[0m\n\n")
		return b.String()
	}

	// Updating dep busy
	if m.modpack.updatingDep {
		b.WriteString("\n  \033[33mUpdating dependency...\033[0m\n\n")
		return b.String()
	}

	// Sync confirmation modal
	if m.modpack.confirmSync {
		b.WriteString("\n  \033[1mSync dependencies from profile?\033[0m\n\n")
		for _, d := range m.modpack.syncDiff {
			switch d.Status {
			case "added":
				fmt.Fprintf(&b, "    \033[32m+ %s %s\033[0m\n", d.Name, d.New)
			case "removed":
				fmt.Fprintf(&b, "    \033[31m- %s\033[0m\n", d.Name)
			case "changed":
				fmt.Fprintf(&b, "    \033[33m~ %s %s → %s\033[0m\n", d.Name, d.Old, d.New)
			}
		}
		b.WriteString("\n  \033[33my confirm • any key cancel\033[0m\n\n")
		return b.String()
	}

	// Publish confirmation modal
	if m.modpack.confirmPublish {
		man := m.modpack.manifest
		fmt.Fprintf(&b, "\n  \033[1mPublish %s v%s to Thunderstore?\033[0m\n\n", man.Name, man.VersionNumber)
		fmt.Fprintf(&b, "    %d dependencies\n", len(man.Dependencies))
		b.WriteString("\n  \033[33my confirm • any key cancel\033[0m\n\n")
		return b.String()
	}

	// Publishing busy
	if m.modpack.publishing {
		b.WriteString("\n  \033[33mPublishing to Thunderstore...\033[0m\n\n")
		return b.String()
	}

	// Publish done
	if m.modpack.publishDone {
		b.WriteString("\n  \033[32mPublished successfully!\033[0m\n\n")
		b.WriteString("  \033[2many key to continue\033[0m\n\n")
		return b.String()
	}

	b.WriteString("\n")

	if len(m.modpack.deps) == 0 {
		b.WriteString("  No dependencies.\n")
	} else {
		// Calculate column widths
		maxName := 0
		for _, dep := range m.modpack.deps {
			name := dep.Raw
			if dep.Owner != "" && dep.Name != "" {
				name = fmt.Sprintf("%s-%s", dep.Owner, dep.Name)
			}
			if len(name) > maxName {
				maxName = len(name)
			}
		}

		vis := listVisible(m.height, 11)
		start, end := listWindow(len(m.modpack.deps), m.modpack.depCursor, vis)

		if start > 0 {
			fmt.Fprintf(&b, "  \033[2m  ↑ %d more\033[0m\n", start)
		}

		for i := start; i < end; i++ {
			dep := m.modpack.deps[i]
			cur := "  "
			if i == m.modpack.depCursor {
				cur = "\033[36m>\033[0m "
			}
			name := dep.Raw
			version := ""
			if dep.Owner != "" && dep.Name != "" {
				name = fmt.Sprintf("%s-%s", dep.Owner, dep.Name)
				version = dep.Version
			}
			pad := strings.Repeat(" ", maxName-len(name)+2)
			updateTag := ""
			if dep.Owner != "" && dep.Name != "" {
				ownerName := fmt.Sprintf("%s-%s", dep.Owner, dep.Name)
				if latest, ok := m.modpack.depUpdates[ownerName]; ok {
					updateTag = fmt.Sprintf("  \033[33m→ %s\033[0m", latest)
				}
			}
			if version != "" {
				fmt.Fprintf(&b, "  %s%s%s%s%s\n", cur, name, pad, version, updateTag)
			} else {
				fmt.Fprintf(&b, "  %s%s\n", cur, name)
			}
		}

		if end < len(m.modpack.deps) {
			fmt.Fprintf(&b, "  \033[2m  ↓ %d more\033[0m\n", len(m.modpack.deps)-end)
		}
	}

	b.WriteString("\n")
	if m.modpack.publishErr != nil {
		fmt.Fprintf(&b, "  \033[31mPublish error: %v\033[0m\n", m.modpack.publishErr)
	}
	if m.modpack.statusMsg != "" {
		fmt.Fprintf(&b, "  \033[33m%s\033[0m\n", m.modpack.statusMsg)
	}
	hotkeys := []string{"↑/↓ navigate", "x remove", "u update", "c check updates", "s sync", "p publish", "tab next", "` mode", "q quit"}
	renderHotkeyBar(&b, hotkeys, m.width)
	return b.String()
}

func (m model) viewModpackConfig() string {
	if s, notReady := m.modpackNotConfigured(); notReady {
		return s
	}

	var b strings.Builder
	b.WriteString("\n")

	if len(m.modpack.configFiles) == 0 {
		b.WriteString("  No config files.\n")
	} else {
		vis := listVisible(m.height, 9)
		start, end := listWindow(len(m.modpack.configFiles), m.modpack.configCursor, vis)

		if start > 0 {
			fmt.Fprintf(&b, "  \033[2m  ↑ %d more\033[0m\n", start)
		}
		for i := start; i < end; i++ {
			cur := "  "
			if i == m.modpack.configCursor {
				cur = "\033[36m>\033[0m "
			}
			fmt.Fprintf(&b, "  %s%s\n", cur, m.modpack.configFiles[i])
		}
		if end < len(m.modpack.configFiles) {
			fmt.Fprintf(&b, "  \033[2m  ↓ %d more\033[0m\n", len(m.modpack.configFiles)-end)
		}
	}

	b.WriteString("\n")
	hotkeys := []string{"↑/↓ navigate", "o open", "tab next", "` mode", "q quit"}
	renderHotkeyBar(&b, hotkeys, m.width)
	return b.String()
}

func (m model) viewModpackReadme() string {
	if s, notReady := m.modpackNotConfigured(); notReady {
		return s
	}

	var b strings.Builder
	b.WriteString("\n")

	if len(m.modpack.readmeLines) == 0 {
		b.WriteString("  No README.md found.\n")
	} else {
		end := m.modpack.readmeScroll + modpackReadmeVisible
		if end > len(m.modpack.readmeLines) {
			end = len(m.modpack.readmeLines)
		}
		for _, line := range m.modpack.readmeLines[m.modpack.readmeScroll:end] {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}

	b.WriteString("\n")
	hotkeys := []string{"↑/↓ scroll", "o open", "tab next", "` mode", "q quit"}
	renderHotkeyBar(&b, hotkeys, m.width)
	return b.String()
}

func (m model) viewModpackManifest() string {
	if s, notReady := m.modpackNotConfigured(); notReady {
		return s
	}

	var b strings.Builder
	b.WriteString("\n")

	man := m.modpack.manifest
	fmt.Fprintf(&b, "  Name:          \033[36m%s\033[0m\n", man.Name)
	fmt.Fprintf(&b, "  Version:       \033[36m%s\033[0m\n", man.VersionNumber)
	fmt.Fprintf(&b, "  Description:   %s\n", man.Description)
	if man.WebsiteURL != "" {
		fmt.Fprintf(&b, "  Website:       %s\n", man.WebsiteURL)
	} else {
		fmt.Fprintf(&b, "  Website:       \033[2m–\033[0m\n")
	}
	fmt.Fprintf(&b, "  Dependencies:  %d\n", len(man.Dependencies))

	b.WriteString("\n")
	hotkeys := []string{"o open", "tab next", "` mode", "q quit"}
	renderHotkeyBar(&b, hotkeys, m.width)
	return b.String()
}

func (m model) handleModpackManifestKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "o":
		if m.cfg.ModpackPath != "" {
			return m, openFile(filepath.Join(m.cfg.ModpackPath, "manifest.json"))
		}
	}
	return m, nil
}

func (m model) viewModpackImage() string {
	if s, notReady := m.modpackNotConfigured(); notReady {
		return s
	}

	var b strings.Builder
	b.WriteString("\n")

	if m.modpack.iconFile != "" {
		fmt.Fprintf(&b, "  Icon:          \033[32m%s\033[0m\n", m.modpack.iconFile)
	} else {
		fmt.Fprintf(&b, "  Icon:          \033[31mnot found\033[0m  \033[2m(expected icon.png, 256x256)\033[0m\n")
	}

	b.WriteString("\n")
	hotkeys := []string{"o open folder", "tab next", "` mode", "q quit"}
	renderHotkeyBar(&b, hotkeys, m.width)
	return b.String()
}

func (m model) handleModpackSettingsKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	settings := []struct{ field int }{{0}, {1}, {2}}
	switch msg.String() {
	case "up", "k":
		if m.modpack.settingsCursor > 0 {
			m.modpack.settingsCursor--
		}
	case "down", "j":
		if m.modpack.settingsCursor < len(settings)-1 {
			m.modpack.settingsCursor++
		}
	case "enter", "e":
		m.modpack.editingField = m.modpack.settingsCursor
		switch m.modpack.settingsCursor {
		case 0:
			m.modpack.fieldInput = m.cfg.ThunderstoreToken
		case 1:
			m.modpack.fieldInput = m.cfg.ThunderstoreAuthor
		case 2:
			m.modpack.fieldInput = m.cfg.ModpackPath
		}
	}
	return m, nil
}

func (m model) viewModpackSettings() string {
	// Show the field editing modal if active
	if m.modpack.editingField >= 0 {
		var label string
		switch m.modpack.editingField {
		case 0:
			label = "Thunderstore Token"
		case 1:
			label = "Thunderstore Author"
		case 2:
			label = "Modpack Path"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "\n  %s:\n\n", label)
		fmt.Fprintf(&b, "  > %s\033[7m \033[0m\n", m.modpack.fieldInput)
		b.WriteString("\n  \033[2menter save • esc cancel\033[0m\n\n")
		return b.String()
	}

	var b strings.Builder
	b.WriteString("\n")

	type setting struct {
		label string
		value string
		mask  bool
	}
	items := []setting{
		{"Thunderstore Token", m.cfg.ThunderstoreToken, true},
		{"Thunderstore Author", m.cfg.ThunderstoreAuthor, false},
		{"Modpack Path", m.cfg.ModpackPath, false},
	}

	for i, s := range items {
		cur := "  "
		if i == m.modpack.settingsCursor {
			cur = "\033[36m>\033[0m "
		}
		val := s.value
		if val == "" {
			val = "\033[2m(not set)\033[0m"
		} else if s.mask {
			// Show only last 4 chars of token
			if len(val) > 4 {
				val = strings.Repeat("*", len(val)-4) + val[len(val)-4:]
			}
		}
		fmt.Fprintf(&b, "  %s%-22s %s\n", cur, s.label, val)
	}

	b.WriteString("\n")
	hotkeys := []string{"↑/↓ navigate", "e edit", "tab next", "` mode", "q quit"}
	renderHotkeyBar(&b, hotkeys, m.width)
	return b.String()
}
