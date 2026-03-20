package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"mmcli/internal/agentapi"
	"mmcli/internal/config"
)

// --- Settings tab state ---

type settingsTabState struct {
	cursor  int
	section int // 0=local, 1=server, 2=modpack

	// Local editing
	editingPath bool
	pathInput   string

	// Modpack editing
	editingField int // -1=none, 0=token, 1=author, 2=path
	fieldInput   string
}

// --- Settings item used by this tab ---

type settingsTabItem struct {
	label           string
	value           string
	tooltip         string
	editable        bool
	action          string // "anticheat", "path", "server-editor", "modpack-field-0/1/2", "open-readme", "open-manifest"
	isSectionHeader bool
}

// --- View ---

func (m model) viewSettingsTab() string {
	var b strings.Builder

	// Local path editing modal
	if m.settingsTab.editingPath {
		b.WriteString("\n  Valheim Path:\n\n")
		fmt.Fprintf(&b, "  > %s\033[7m \033[0m\n", m.settingsTab.pathInput)
		b.WriteString("\n  \033[2menter save • esc cancel\033[0m\n\n")
		return b.String()
	}

	// Modpack field editing modal
	if m.settingsTab.editingField >= 0 {
		var label string
		switch m.settingsTab.editingField {
		case 0:
			label = "Thunderstore Token"
		case 1:
			label = "Thunderstore Author"
		case 2:
			label = "Modpack Path"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "\n  %s:\n\n", label)
		fmt.Fprintf(&b, "  > %s\033[7m \033[0m\n", m.settingsTab.fieldInput)
		b.WriteString("\n  \033[2menter save • esc cancel\033[0m\n\n")
		return b.String()
	}

	// Server settings editor (takes over the whole view)
	if m.server.editor.active {
		var sb strings.Builder
		renderSettingsEdit(&sb, &m.server.editor, m.width)
		return sb.String()
	}

	items := m.buildSettingsTabItems()
	b.WriteString("\n")

	for i, item := range items {
		if item.isSectionHeader {
			if i > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "  \033[1m%s\033[0m\n", item.label)
			continue
		}
		cursor := "    "
		if i == m.settingsTab.cursor {
			cursor = "  \033[36m>\033[0m "
		}
		label := fmt.Sprintf("%-22s", item.label+":")
		val := item.value
		if item.editable && i == m.settingsTab.cursor {
			val = fmt.Sprintf("< %s >", val)
		}
		fmt.Fprintf(&b, "%s%s %s\n", cursor, label, val)
	}

	// Tooltip
	b.WriteString("\n")
	if m.settingsTab.cursor < len(items) && items[m.settingsTab.cursor].tooltip != "" {
		fmt.Fprintf(&b, "  \033[2m%s\033[0m\n", items[m.settingsTab.cursor].tooltip)
	}

	b.WriteString("\n")
	hotkeys := []string{"↑/↓ navigate", "enter/space change"}
	hotkeys = append(hotkeys, "tab next", "q quit")
	renderHotkeyBar(&b, hotkeys, m.width)

	return b.String()
}

// --- Items builder ---

func (m model) buildSettingsTabItems() []settingsTabItem {
	var items []settingsTabItem

	// --- Local section ---
	items = append(items, settingsTabItem{label: "Local", isSectionHeader: true})

	// Anticheat
	pref := m.cfg.AnticheatSystem
	if pref == "" {
		pref = "auto"
	}
	acValue := pref
	if pref == "auto" {
		acValue = fmt.Sprintf("%s \033[2m(resolved: %s)\033[0m", pref, m.anticheatSystem)
	}
	items = append(items, settingsTabItem{
		label:    "Anticheat",
		value:    acValue,
		tooltip:  "Which anticheat mod to configure on push. Auto detects from installed mods.",
		editable: true,
		action:   "anticheat",
	})

	// Valheim path
	items = append(items, settingsTabItem{
		label:    "Valheim Path",
		value:    m.cfg.ValheimPath,
		tooltip:  "Local Valheim installation directory.",
		editable: true,
		action:   "path",
	})

	// Profile (read-only)
	items = append(items, settingsTabItem{
		label:   "Profile",
		value:   m.cfg.ActiveProfile,
		tooltip: "Active mod profile. Switch with 'p' on the Mods tab.",
	})

	if !m.isFullMode() {
		return items
	}

	// --- Server section ---
	items = append(items, settingsTabItem{label: "Server", isSectionHeader: true})

	// Server settings editor entry
	items = append(items, settingsTabItem{
		label:    "World Settings",
		value:    "press to edit",
		tooltip:  "Edit world settings, manage launch configs. Opens the server settings editor.",
		editable: true,
		action:   "server-editor",
	})

	// --- Modpack section ---
	if m.cfg.ModpackPath != "" {
		items = append(items, settingsTabItem{label: "Modpack", isSectionHeader: true})

		// Token
		tokenVal := m.cfg.ThunderstoreToken
		if tokenVal == "" {
			tokenVal = "\033[2m(not set)\033[0m"
		} else if len(tokenVal) > 4 {
			tokenVal = strings.Repeat("*", len(tokenVal)-4) + tokenVal[len(tokenVal)-4:]
		}
		items = append(items, settingsTabItem{
			label:    "Thunderstore Token",
			value:    tokenVal,
			tooltip:  "API token for publishing to Thunderstore.",
			editable: true,
			action:   "modpack-field-0",
		})

		// Author
		authorVal := m.cfg.ThunderstoreAuthor
		if authorVal == "" {
			authorVal = "\033[2m(not set)\033[0m"
		}
		items = append(items, settingsTabItem{
			label:    "Thunderstore Author",
			value:    authorVal,
			tooltip:  "Author name for Thunderstore publishing.",
			editable: true,
			action:   "modpack-field-1",
		})

		// Modpack path
		items = append(items, settingsTabItem{
			label:    "Modpack Path",
			value:    m.cfg.ModpackPath,
			tooltip:  "Local directory containing modpack files (manifest.json, README.md, icon.png).",
			editable: true,
			action:   "modpack-field-2",
		})

		// README (open action)
		readmeStatus := "\033[2mnot found\033[0m"
		if len(m.modpack.readmeLines) > 0 {
			readmeStatus = "\033[32mfound\033[0m"
		}
		items = append(items, settingsTabItem{
			label:    "README",
			value:    readmeStatus,
			tooltip:  "Open README.md in your editor.",
			editable: true,
			action:   "open-readme",
		})

		// Manifest (open action)
		manifestStatus := "\033[2mnot found\033[0m"
		if m.modpack.manifest != nil {
			manifestStatus = fmt.Sprintf("\033[32m%s v%s\033[0m", m.modpack.manifest.Name, m.modpack.manifest.VersionNumber)
		}
		items = append(items, settingsTabItem{
			label:    "Manifest",
			value:    manifestStatus,
			tooltip:  "Open manifest.json in your editor.",
			editable: true,
			action:   "open-manifest",
		})

		// Image
		iconStatus := "\033[31mnot found\033[0m"
		if m.modpack.iconFile != "" {
			iconStatus = fmt.Sprintf("\033[32m%s\033[0m", m.modpack.iconFile)
		}
		items = append(items, settingsTabItem{
			label:   "Image",
			value:   iconStatus,
			tooltip: "Expected: icon.png (256x256) in modpack directory.",
		})
	}

	return items
}

// --- Key handler ---

func (m model) handleSettingsTabKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Local path editing
	if m.settingsTab.editingPath {
		switch msg.String() {
		case "esc":
			m.settingsTab.editingPath = false
		case "enter":
			if m.settingsTab.pathInput != "" {
				m.cfg.ValheimPath = m.settingsTab.pathInput
				m.paths.ValheimDir = m.settingsTab.pathInput
				config.Save(m.paths, m.cfg)
			}
			m.settingsTab.editingPath = false
		case "backspace":
			if len(m.settingsTab.pathInput) > 0 {
				m.settingsTab.pathInput = m.settingsTab.pathInput[:len(m.settingsTab.pathInput)-1]
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				m.settingsTab.pathInput += string(msg.Runes)
			}
		}
		return m, nil
	}

	// Modpack field editing
	if m.settingsTab.editingField >= 0 {
		switch msg.String() {
		case "esc":
			m.settingsTab.editingField = -1
		case "enter":
			switch m.settingsTab.editingField {
			case 0:
				m.cfg.ThunderstoreToken = m.settingsTab.fieldInput
			case 1:
				m.cfg.ThunderstoreAuthor = m.settingsTab.fieldInput
			case 2:
				m.cfg.ModpackPath = m.settingsTab.fieldInput
				m.modpack.loadFromDisk(m.settingsTab.fieldInput)
			}
			config.Save(m.paths, m.cfg)
			m.settingsTab.editingField = -1
		case "backspace":
			if len(m.settingsTab.fieldInput) > 0 {
				m.settingsTab.fieldInput = m.settingsTab.fieldInput[:len(m.settingsTab.fieldInput)-1]
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				m.settingsTab.fieldInput += string(msg.Runes)
			}
		}
		return m, nil
	}

	// Server settings editor
	if m.server.editor.active {
		return m.handleSettingsEditor(msg)
	}

	items := m.buildSettingsTabItems()

	switch msg.String() {
	case "up", "k":
		m.settingsTab.cursor--
		for m.settingsTab.cursor >= 0 && items[m.settingsTab.cursor].isSectionHeader {
			m.settingsTab.cursor--
		}
		if m.settingsTab.cursor < 0 {
			for i, item := range items {
				if !item.isSectionHeader {
					m.settingsTab.cursor = i
					break
				}
			}
		}
	case "down", "j":
		m.settingsTab.cursor++
		for m.settingsTab.cursor < len(items) && items[m.settingsTab.cursor].isSectionHeader {
			m.settingsTab.cursor++
		}
		if m.settingsTab.cursor >= len(items) {
			m.settingsTab.cursor = len(items) - 1
		}
	case "enter", " ":
		if m.settingsTab.cursor >= len(items) || !items[m.settingsTab.cursor].editable {
			return m, nil
		}
		item := items[m.settingsTab.cursor]
		switch item.action {
		case "anticheat":
			switch m.cfg.AnticheatSystem {
			case "", "auto":
				m.cfg.AnticheatSystem = "azu"
			case "azu":
				m.cfg.AnticheatSystem = "enforcer"
			case "enforcer":
				m.cfg.AnticheatSystem = "auto"
			}
			config.Save(m.paths, m.cfg)
			m.anticheatSystem = resolveAnticheatSystem(m.cfg, m.local.mods)
		case "path":
			m.settingsTab.editingPath = true
			m.settingsTab.pathInput = m.cfg.ValheimPath
		case "server-editor":
			if m.server.role == agentapi.RoleAdmin && m.server.client != nil {
				if m.server.settings == nil {
					return m, fetchSettings(m.server.client)
				}
				m.server.editor = settingsEditor{
					active: true,
					fields: buildEditorFields(m.server.settings),
					cursor: 0,
				}
				return m, fetchLaunchConfigsForEditor(m.server.client)
			}
		case "modpack-field-0":
			m.settingsTab.editingField = 0
			m.settingsTab.fieldInput = m.cfg.ThunderstoreToken
		case "modpack-field-1":
			m.settingsTab.editingField = 1
			m.settingsTab.fieldInput = m.cfg.ThunderstoreAuthor
		case "modpack-field-2":
			m.settingsTab.editingField = 2
			m.settingsTab.fieldInput = m.cfg.ModpackPath
		case "open-readme":
			if m.cfg.ModpackPath != "" {
				return m, openFile(filepath.Join(m.cfg.ModpackPath, "README.md"))
			}
		case "open-manifest":
			if m.cfg.ModpackPath != "" {
				return m, openFile(filepath.Join(m.cfg.ModpackPath, "manifest.json"))
			}
		}
	}
	return m, nil
}
