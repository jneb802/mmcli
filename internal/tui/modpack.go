package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"mmcli/internal/config"
	"mmcli/internal/thunderstore"
)

// Async messages for modpack actions.
type modpackPublishDoneMsg struct{ err error }

type syncDiffItem struct {
	Name   string
	Status string // "added", "removed", "changed"
	Old    string // old version (for changed)
	New    string // new version (for changed/added)
}

type modpackManifest struct {
	Name          string   `json:"name"`
	VersionNumber string   `json:"version_number"`
	Description   string   `json:"description"`
	WebsiteURL    string   `json:"website_url"`
	Dependencies  []string `json:"dependencies"`
}

type modpackDep struct {
	Owner   string
	Name    string
	Version string
	Raw     string
}

type modpackModel struct {
	manifest     *modpackManifest
	deps         []modpackDep
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

	// Sync state
	confirmSync bool
	syncDiff    []syncDiffItem // preview of what sync will change

	// Publish state
	confirmPublish bool
	publishing     bool
	publishErr     error
	publishDone    bool
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
	mp.loadErr = nil
	mp.statusMsg = ""

	if modpackPath == "" {
		return
	}

	// Read manifest.json
	data, err := os.ReadFile(filepath.Join(modpackPath, "manifest.json"))
	if err != nil {
		mp.loadErr = fmt.Errorf("cannot read manifest.json: %w", err)
		return
	}
	var manifest modpackManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		mp.loadErr = fmt.Errorf("invalid manifest.json: %w", err)
		return
	}
	mp.manifest = &manifest

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

// buildSyncDiff compares profile mods to current manifest dependencies.
func buildSyncDiff(reg *config.Registry, profileName string, manifest *modpackManifest) []syncDiffItem {
	// Build current dependency set from manifest
	currentDeps := make(map[string]string) // Owner-Name -> Version
	for _, dep := range manifest.Dependencies {
		ref := thunderstore.ParseDep(dep)
		if ref.Owner != "" && ref.Name != "" {
			currentDeps[fmt.Sprintf("%s-%s", ref.Owner, ref.Name)] = ref.Version
		}
	}

	// Build new dependency set from profile
	newDeps := make(map[string]string)
	for _, mod := range reg.ListMods(profileName) {
		if mod.IsLocal || mod.Owner == "" {
			continue
		}
		newDeps[mod.FullName()] = mod.Version
	}

	var diff []syncDiffItem

	// Added or changed
	for name, ver := range newDeps {
		oldVer, exists := currentDeps[name]
		if !exists {
			diff = append(diff, syncDiffItem{Name: name, Status: "added", New: ver})
		} else if oldVer != ver {
			diff = append(diff, syncDiffItem{Name: name, Status: "changed", Old: oldVer, New: ver})
		}
	}

	// Removed
	for name := range currentDeps {
		if _, exists := newDeps[name]; !exists {
			diff = append(diff, syncDiffItem{Name: name, Status: "removed"})
		}
	}

	return diff
}

// syncManifestDeps overwrites manifest.json dependencies from the profile.
func syncManifestDeps(modpackPath string, reg *config.Registry, profileName string) error {
	manifestPath := filepath.Join(modpackPath, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest modpackManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}

	var deps []string
	for _, mod := range reg.ListMods(profileName) {
		if mod.IsLocal || mod.Owner == "" {
			continue
		}
		deps = append(deps, fmt.Sprintf("%s-%s-%s", mod.Owner, mod.Name, mod.Version))
	}
	manifest.Dependencies = deps

	out, err := json.MarshalIndent(manifest, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(manifestPath, out, 0644)
}

func publishModpack(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		err := thunderstore.Publish(cfg.ThunderstoreToken, cfg.ThunderstoreAuthor, cfg.ModpackPath)
		return modpackPublishDoneMsg{err: err}
	}
}

// --- Key handlers ---

func (m model) handleModpackNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

	// Sync confirmation modal
	if m.modpack.confirmSync {
		switch msg.String() {
		case "y":
			m.modpack.confirmSync = false
			if err := syncManifestDeps(m.cfg.ModpackPath, m.reg, m.cfg.ActiveProfile); err != nil {
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
		// no interactive keys
	case contentModpackImage:
		return m.handleModpackImageKeys(msg)
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
	case "s":
		if m.modpack.manifest != nil {
			diff := buildSyncDiff(m.reg, m.cfg.ActiveProfile, m.modpack.manifest)
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
			if version != "" {
				fmt.Fprintf(&b, "  %s%s%s%s\n", cur, name, pad, version)
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
	hotkeys := []string{"↑/↓ navigate", "s sync from profile", "p publish", "tab next", "` mode", "q quit"}
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
	hotkeys := []string{"tab next", "` mode", "q quit"}
	renderHotkeyBar(&b, hotkeys, m.width)
	return b.String()
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
