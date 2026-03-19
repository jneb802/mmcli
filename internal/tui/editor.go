package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"mmcli/internal/agentapi"
	"mmcli/internal/client"
)

// Field types for the settings editor.
const (
	fieldText   = "text"
	fieldInt    = "int"
	fieldBool   = "bool"
	fieldSelect = "select"
	fieldWorld  = "world"
)

type editField struct {
	Key           string
	Label         string
	Type          string
	Value         string
	OriginalValue string
	Options       []string
	Section       string
}

type settingsEditor struct {
	active      bool
	fields      []editField
	cursor      int
	editing     bool // typing into text/int field
	scroll      int
	dirty       bool
	confirmSave bool // restart confirmation before save
	saving      bool
	err         string

	// World picker sub-modal
	worldPicker    bool
	worlds         []agentapi.WorldInfo
	worldCursor    int
	worldMode      int // 0=select, 1=new, 2=upload
	worldInput     string
	worldFetching  bool
	worldUploading bool
	worldErr       string

	// Launch config manager sub-modal
	lcManager       bool
	lcConfigs       []agentapi.LaunchConfigSummary
	lcActive        string
	lcCursor        int
	lcCreating      bool
	lcCreateInput   string
	lcConfirmDelete bool
	lcFetching      bool
	lcErr           string
}

// Async message types for editor.
type settingsUpdateMsg struct{ err error }
type worldListMsg struct {
	worlds []agentapi.WorldInfo
	err    error
}
type worldUploadMsg struct {
	name string
	err  error
}
type lcListMsg struct {
	configs []agentapi.LaunchConfigSummary
	active  string
	err     error
}
type lcActionMsg struct{ err error }

// --- Field construction ---

func buildEditorFields(s *agentapi.SettingsResponse) []editField {
	publicVal := "false"
	if s.Public == 1 {
		publicVal = "true"
	}

	fields := []editField{
		// Core
		{Key: "name", Label: "Name", Type: fieldText, Section: "Core", Value: s.Name},
		{Key: "port", Label: "Port", Type: fieldInt, Section: "Core", Value: strconv.Itoa(s.Port)},
		{Key: "world", Label: "World", Type: fieldWorld, Section: "Core", Value: s.World},
		{Key: "password", Label: "Password", Type: fieldText, Section: "Core", Value: s.Password},
		{Key: "public", Label: "Public", Type: fieldBool, Section: "Core", Value: publicVal},

		// Backup
		{Key: "saveinterval", Label: "Save Interval", Type: fieldInt, Section: "Backup", Value: intStr(s.SaveInterval)},
		{Key: "backups", Label: "Backups", Type: fieldInt, Section: "Backup", Value: intStr(s.Backups)},
		{Key: "backupshort", Label: "Short Interval", Type: fieldInt, Section: "Backup", Value: intStr(s.BackupShort)},
		{Key: "backuplong", Label: "Long Interval", Type: fieldInt, Section: "Backup", Value: intStr(s.BackupLong)},

		// Modifiers
		{Key: "crossplay", Label: "Crossplay", Type: fieldBool, Section: "Modifiers", Value: boolStr(s.Crossplay)},
		{Key: "preset", Label: "Preset", Type: fieldSelect, Section: "Modifiers",
			Value: s.Preset, Options: []string{"", "normal", "casual", "hard", "hardcore", "immersive", "hammer"}},
		{Key: "combat", Label: "Combat", Type: fieldSelect, Section: "Modifiers",
			Value: modVal(s.Modifiers, "combat"), Options: []string{"", "veryeasy", "easy", "hard", "veryhard"}},
		{Key: "deathpenalty", Label: "Death Penalty", Type: fieldSelect, Section: "Modifiers",
			Value: modVal(s.Modifiers, "deathpenalty"), Options: []string{"", "casual", "veryeasy", "easy", "hard", "hardcore"}},
		{Key: "resources", Label: "Resources", Type: fieldSelect, Section: "Modifiers",
			Value: modVal(s.Modifiers, "resources"), Options: []string{"", "muchless", "less", "more", "muchmore", "most"}},
		{Key: "raids", Label: "Raids", Type: fieldSelect, Section: "Modifiers",
			Value: modVal(s.Modifiers, "raids"), Options: []string{"", "none", "muchless", "less", "more", "muchmore"}},
		{Key: "portals", Label: "Portals", Type: fieldSelect, Section: "Modifiers",
			Value: modVal(s.Modifiers, "portals"), Options: []string{"", "casual", "hard", "veryhard"}},

		// Set Keys
		{Key: "nomap", Label: "No Map", Type: fieldBool, Section: "Keys", Value: boolStr(hasKey(s.SetKeys, "nomap"))},
		{Key: "playerevents", Label: "Player Events", Type: fieldBool, Section: "Keys", Value: boolStr(hasKey(s.SetKeys, "playerevents"))},
		{Key: "passivemobs", Label: "Passive Mobs", Type: fieldBool, Section: "Keys", Value: boolStr(hasKey(s.SetKeys, "passivemobs"))},
		{Key: "nobuildcost", Label: "No Build Cost", Type: fieldBool, Section: "Keys", Value: boolStr(hasKey(s.SetKeys, "nobuildcost"))},
	}

	for i := range fields {
		fields[i].OriginalValue = fields[i].Value
	}
	return fields
}

func buildSettingsFromFields(fields []editField) agentapi.SettingsResponse {
	s := agentapi.SettingsResponse{
		Modifiers: make(map[string]string),
	}
	for _, f := range fields {
		switch f.Key {
		case "name":
			s.Name = f.Value
		case "port":
			s.Port, _ = strconv.Atoi(f.Value)
		case "world":
			s.World = f.Value
		case "password":
			s.Password = f.Value
		case "public":
			if f.Value == "true" {
				s.Public = 1
			}
		case "saveinterval":
			s.SaveInterval, _ = strconv.Atoi(f.Value)
		case "backups":
			s.Backups, _ = strconv.Atoi(f.Value)
		case "backupshort":
			s.BackupShort, _ = strconv.Atoi(f.Value)
		case "backuplong":
			s.BackupLong, _ = strconv.Atoi(f.Value)
		case "crossplay":
			s.Crossplay = f.Value == "true"
		case "preset":
			s.Preset = f.Value
		case "combat", "deathpenalty", "resources", "raids", "portals":
			if f.Value != "" {
				s.Modifiers[f.Key] = f.Value
			}
		case "nomap", "playerevents", "passivemobs", "nobuildcost":
			if f.Value == "true" {
				s.SetKeys = append(s.SetKeys, f.Key)
			}
		}
	}
	if len(s.Modifiers) == 0 {
		s.Modifiers = nil
	}
	return s
}

// --- Key handling ---

func (m model) handleSettingsEditor(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ed := &m.server.editor

	// World picker sub-modal
	if ed.worldPicker {
		return m.handleWorldPicker(msg)
	}

	// Launch config manager sub-modal
	if ed.lcManager {
		return m.handleLCManager(msg)
	}

	// Save confirmation modal
	if ed.confirmSave {
		switch msg.String() {
		case "y":
			ed.confirmSave = false
			ed.saving = true
			ed.err = ""
			settings := buildSettingsFromFields(ed.fields)
			if m.server.settings != nil {
				settings.SaveDir = m.server.settings.SaveDir
			}
			return m, saveSettings(m.server.client, m.server.editor.lcActive, &settings)
		case "ctrl+c":
			return m, tea.Quit
		default:
			ed.confirmSave = false
		}
		return m, nil
	}

	// Saving in progress
	if ed.saving {
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}

	// Text/int editing mode
	if ed.editing {
		return m.handleEditorTyping(msg)
	}

	// Normal navigation
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		ed.active = false
		ed.err = ""
		return m, nil
	case "up", "k":
		if ed.cursor > 0 {
			ed.cursor--

		}
	case "down", "j":
		if ed.cursor < len(ed.fields)-1 {
			ed.cursor++

		}
	case "tab":
		m.editorNextSection()
	case "shift+tab":
		m.editorPrevSection()
	case "enter", " ":
		f := &ed.fields[ed.cursor]
		switch f.Type {
		case fieldText, fieldInt:
			ed.editing = true
		case fieldBool:
			m.toggleEditorBool()
		case fieldSelect:
			m.editorCycleSelect(1)
		case fieldWorld:
			ed.worldPicker = true
			ed.worldMode = 0
			ed.worldCursor = 0
			ed.worldInput = ""
			ed.worldErr = ""
			ed.worldFetching = true
			return m, fetchWorlds(m.server.client)
		}
	case "left", "h":
		f := &ed.fields[ed.cursor]
		if f.Type == fieldSelect {
			m.editorCycleSelect(-1)
		} else if f.Type == fieldBool {
			m.toggleEditorBool()
		}
	case "right", "l":
		f := &ed.fields[ed.cursor]
		if f.Type == fieldSelect {
			m.editorCycleSelect(1)
		} else if f.Type == fieldBool {
			m.toggleEditorBool()
		}
	case "s", "ctrl+s":
		if !ed.dirty {
			return m, nil
		}
		ed.confirmSave = true
	case "c":
		ed.lcManager = true
		ed.lcFetching = true
		ed.lcCursor = 0
		ed.lcErr = ""
		return m, fetchLaunchConfigs(m.server.client)
	}
	return m, nil
}

func (m model) handleEditorTyping(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ed := &m.server.editor
	f := &ed.fields[ed.cursor]

	switch msg.String() {
	case "enter":
		ed.editing = false
		m.updateEditorDirty()
	case "esc":
		f.Value = f.OriginalValue
		ed.editing = false
	case "backspace":
		if len(f.Value) > 0 {
			f.Value = f.Value[:len(f.Value)-1]
		}
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			input := string(msg.Runes)
			if f.Type == fieldInt {
				// Only allow digits
				for _, r := range input {
					if r >= '0' && r <= '9' {
						f.Value += string(r)
					}
				}
			} else {
				f.Value += input
			}
		}
	}
	return m, nil
}

func (m model) handleWorldPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ed := &m.server.editor

	if ed.worldFetching || ed.worldUploading {
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}

	// Typing mode (new world name or upload path)
	if ed.worldMode == 1 || ed.worldMode == 2 {
		switch msg.String() {
		case "esc":
			ed.worldMode = 0
			ed.worldInput = ""
			ed.worldErr = ""
		case "enter":
			if ed.worldInput == "" {
				return m, nil
			}
			if ed.worldMode == 1 {
				// Create new — just set the world name
				m.setWorldField(ed.worldInput)
				ed.worldPicker = false
				ed.worldInput = ""
				return m, nil
			}
			// Upload
			ed.worldUploading = true
			ed.worldErr = ""
			return m, uploadWorld(m.server.client, ed.worldInput)
		case "backspace":
			if len(ed.worldInput) > 0 {
				ed.worldInput = ed.worldInput[:len(ed.worldInput)-1]
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				ed.worldInput += string(msg.Runes)
			}
		}
		return m, nil
	}

	// Select existing mode
	switch msg.String() {
	case "esc", "q":
		ed.worldPicker = false
		ed.worldErr = ""
	case "up", "k":
		if ed.worldCursor > 0 {
			ed.worldCursor--
		}
	case "down", "j":
		if ed.worldCursor < len(ed.worlds)-1 {
			ed.worldCursor++
		}
	case "enter":
		if len(ed.worlds) > 0 {
			m.setWorldField(ed.worlds[ed.worldCursor].Name)
			ed.worldPicker = false
		}
	case "left", "h":
		if ed.worldMode > 0 {
			ed.worldMode--
		}
	case "right", "l":
		if ed.worldMode < 2 {
			ed.worldMode++
		}
	case "n":
		ed.worldMode = 1
		ed.worldInput = ""
	case "u":
		ed.worldMode = 2
		ed.worldInput = ""
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m model) handleLCManager(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ed := &m.server.editor

	if ed.lcFetching {
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}

	if ed.lcConfirmDelete {
		switch msg.String() {
		case "y":
			ed.lcConfirmDelete = false
			if ed.lcCursor < len(ed.lcConfigs) {
				name := ed.lcConfigs[ed.lcCursor].Name
				ed.lcFetching = true
				return m, deleteLaunchConfig(m.server.client, name)
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			ed.lcConfirmDelete = false
		}
		return m, nil
	}

	if ed.lcCreating {
		switch msg.String() {
		case "esc":
			ed.lcCreating = false
			ed.lcCreateInput = ""
		case "enter":
			if ed.lcCreateInput != "" {
				ed.lcFetching = true
				return m, createLaunchConfig(m.server.client, ed.lcCreateInput, ed.lcActive)
			}
		case "backspace":
			if len(ed.lcCreateInput) > 0 {
				ed.lcCreateInput = ed.lcCreateInput[:len(ed.lcCreateInput)-1]
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				ed.lcCreateInput += string(msg.Runes)
			}
		}
		return m, nil
	}

	switch msg.String() {
	case "esc", "q":
		ed.lcManager = false
		ed.lcErr = ""
	case "up", "k":
		if ed.lcCursor > 0 {
			ed.lcCursor--
		}
	case "down", "j":
		if ed.lcCursor < len(ed.lcConfigs)-1 {
			ed.lcCursor++
		}
	case "enter":
		if len(ed.lcConfigs) > 0 {
			name := ed.lcConfigs[ed.lcCursor].Name
			if name != ed.lcActive {
				ed.lcFetching = true
				return m, activateLaunchConfig(m.server.client, name)
			}
		}
	case "n":
		ed.lcCreating = true
		ed.lcCreateInput = ""
	case "d":
		if len(ed.lcConfigs) > 0 {
			name := ed.lcConfigs[ed.lcCursor].Name
			if name != ed.lcActive {
				ed.lcConfirmDelete = true
			}
		}
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// --- Rendering ---

func renderSettingsEdit(b *strings.Builder, ed *settingsEditor, width int) {
	// World picker overlay
	if ed.worldPicker {
		renderWorldPicker(b, ed)
		return
	}

	// Launch config manager overlay
	if ed.lcManager {
		renderLCManager(b, ed)
		return
	}

	if ed.confirmSave {
		b.WriteString("\n  \033[33mThese changes require a server restart.\033[0m\n")
		b.WriteString("  \033[33mSave and restart server now? (y/n)\033[0m\n\n")
		return
	}

	if ed.saving {
		b.WriteString("\n  \033[33mSaving settings...\033[0m\n\n")
		return
	}

	// Header
	b.WriteString("\n  \033[1mEdit Server Settings\033[0m")
	if ed.lcActive != "" {
		fmt.Fprintf(b, "  \033[2m(%s)\033[0m", ed.lcActive)
	}
	b.WriteString("\n")

	if ed.err != "" {
		fmt.Fprintf(b, "  \033[31m%s\033[0m\n", ed.err)
	}
	b.WriteString("\n")

	// Build display lines with field indices
	type displayLine struct {
		text     string
		fieldIdx int // -1 for section headers
	}
	var lines []displayLine
	lastSection := ""

	for i, f := range ed.fields {
		if f.Section != lastSection {
			lastSection = f.Section
			lines = append(lines, displayLine{
				text:     fmt.Sprintf("  \033[36m%s\033[0m", f.Section),
				fieldIdx: -1,
			})
		}
		lines = append(lines, displayLine{
			text:     renderEditField(f, i == ed.cursor, ed.editing && i == ed.cursor),
			fieldIdx: i,
		})
	}

	// Scroll
	visible := 25
	if len(lines) <= visible {
		for _, line := range lines {
			fmt.Fprintf(b, "%s\n", line.text)
		}
	} else {
		// Find the line containing the cursor field
		cursorLine := 0
		for i, line := range lines {
			if line.fieldIdx == ed.cursor {
				cursorLine = i
				break
			}
		}
		// Center scroll around cursor
		start := cursorLine - visible/2
		if start < 0 {
			start = 0
		}
		if start > len(lines)-visible {
			start = len(lines) - visible
		}
		end := start + visible
		if end > len(lines) {
			end = len(lines)
		}
		for _, line := range lines[start:end] {
			fmt.Fprintf(b, "%s\n", line.text)
		}
	}

	// Hint bar
	b.WriteString("\n")
	var hints []string
	hints = append(hints, "↑/↓ navigate")
	if ed.editing {
		hints = append(hints, "enter confirm", "esc revert")
	} else {
		hints = append(hints, "enter edit", "←/→ toggle")
		if ed.dirty {
			hints = append(hints, "s save")
		}
		hints = append(hints, "c configs", "esc cancel")
	}
	renderHotkeyBar(b, hints, width)
}

func renderEditField(f editField, focused, editing bool) string {
	cursor := "    "
	if focused {
		cursor = "  \033[36m>\033[0m "
	}

	label := fmt.Sprintf("%-16s", f.Label+":")
	changed := f.Value != f.OriginalValue

	var val string
	switch f.Type {
	case fieldText:
		if editing {
			val = fmt.Sprintf("%s\033[7m \033[0m", f.Value)
		} else if f.Value == "" {
			val = "\033[2m(empty)\033[0m"
		} else {
			val = f.Value
		}
	case fieldInt:
		if editing {
			val = fmt.Sprintf("%s\033[7m \033[0m", f.Value)
		} else if f.Value == "" || f.Value == "0" {
			val = "\033[2m0\033[0m"
		} else {
			val = f.Value
		}
	case fieldBool:
		if f.Value == "true" {
			val = "\033[32mon\033[0m"
		} else {
			val = "\033[2moff\033[0m"
		}
	case fieldSelect:
		if f.Value == "" {
			val = "\033[2m(default)\033[0m"
		} else {
			val = f.Value
		}
		if focused {
			val = fmt.Sprintf("< %s >", val)
		}
	case fieldWorld:
		if f.Value == "" {
			val = "\033[2m(none)\033[0m"
		} else {
			val = f.Value
		}
		if focused {
			val += "  \033[2menter to change\033[0m"
		}
	}

	if changed && !editing {
		val = "\033[33m" + stripANSI(val) + "\033[0m"
	}

	return fmt.Sprintf("%s%s %s", cursor, label, val)
}

func renderWorldPicker(b *strings.Builder, ed *settingsEditor) {
	b.WriteString("\n  \033[1m── World ──────────────────────────────\033[0m\n")

	if ed.worldFetching {
		b.WriteString("  \033[33mFetching worlds...\033[0m\n\n")
		return
	}
	if ed.worldUploading {
		b.WriteString("  \033[33mUploading world...\033[0m\n\n")
		return
	}
	if ed.worldErr != "" {
		fmt.Fprintf(b, "  \033[31m%s\033[0m\n", ed.worldErr)
	}

	// Mode tabs
	modes := []string{"Select Existing", "Create New", "Upload"}
	b.WriteString("  ")
	for i, mode := range modes {
		if i > 0 {
			b.WriteString("  ")
		}
		if i == ed.worldMode {
			fmt.Fprintf(b, "\033[1;36m[%s]\033[0m", mode)
		} else {
			fmt.Fprintf(b, "\033[2m%s\033[0m", mode)
		}
	}
	b.WriteString("\n\n")

	switch ed.worldMode {
	case 0: // Select existing
		if len(ed.worlds) == 0 {
			b.WriteString("  \033[2mNo worlds found\033[0m\n")
		} else {
			for i, w := range ed.worlds {
				cursor := "  "
				if i == ed.worldCursor {
					cursor = "\033[36m>\033[0m "
				}
				size := formatBytes(w.SizeDB)
				fmt.Fprintf(b, "  %s%-20s %8s\n", cursor, w.Name, size)
			}
		}
	case 1: // Create new
		fmt.Fprintf(b, "  World name:\n")
		fmt.Fprintf(b, "  > %s\033[7m \033[0m\n", ed.worldInput)
	case 2: // Upload
		fmt.Fprintf(b, "  Path to .db file:\n")
		fmt.Fprintf(b, "  > %s\033[7m \033[0m\n", ed.worldInput)
		b.WriteString("  \033[2m(.fwl loaded from same directory)\033[0m\n")
	}

	b.WriteString("\n  \033[2m")
	switch ed.worldMode {
	case 0:
		b.WriteString("enter select • n new • u upload • ←/→ mode • esc cancel")
	case 1:
		b.WriteString("enter create • esc back")
	case 2:
		b.WriteString("enter upload • esc back")
	}
	b.WriteString("\033[0m\n\n")
}

func renderLCManager(b *strings.Builder, ed *settingsEditor) {
	b.WriteString("\n  \033[1m── Launch Configs ─────────────────────\033[0m\n\n")

	if ed.lcFetching {
		b.WriteString("  \033[33mLoading...\033[0m\n\n")
		return
	}
	if ed.lcErr != "" {
		fmt.Fprintf(b, "  \033[31m%s\033[0m\n", ed.lcErr)
	}
	if ed.lcConfirmDelete {
		if ed.lcCursor < len(ed.lcConfigs) {
			fmt.Fprintf(b, "  \033[33mDelete '%s'? (y/n)\033[0m\n\n", ed.lcConfigs[ed.lcCursor].Name)
		}
		return
	}
	if ed.lcCreating {
		b.WriteString("  New config name:\n")
		fmt.Fprintf(b, "  > %s\033[7m \033[0m\n", ed.lcCreateInput)
		b.WriteString("\n  \033[2menter create • esc cancel\033[0m\n\n")
		return
	}

	if len(ed.lcConfigs) == 0 {
		b.WriteString("  \033[2mNo launch configs\033[0m\n")
	} else {
		maxName := 0
		for _, c := range ed.lcConfigs {
			if len(c.Name) > maxName {
				maxName = len(c.Name)
			}
		}
		for i, c := range ed.lcConfigs {
			cursor := "  "
			if i == ed.lcCursor {
				cursor = "\033[36m>\033[0m "
			}
			active := " "
			if c.Name == ed.lcActive {
				active = "\033[32m*\033[0m"
			}
			pad := strings.Repeat(" ", maxName-len(c.Name)+2)
			preset := c.Preset
			if preset == "" {
				preset = "-"
			}
			fmt.Fprintf(b, "  %s%s %-s%s%-12s %s\n", cursor, active, c.Name, pad, c.World, preset)
		}
	}

	b.WriteString("\n  \033[2menter activate • n new • d delete • esc back\033[0m\n\n")
}

// --- Helpers ---

func (m *model) toggleEditorBool() {
	f := &m.server.editor.fields[m.server.editor.cursor]
	if f.Value == "true" {
		f.Value = "false"
	} else {
		f.Value = "true"
	}
	m.updateEditorDirty()
}

func (m *model) setWorldField(name string) {
	for i := range m.server.editor.fields {
		if m.server.editor.fields[i].Key == "world" {
			m.server.editor.fields[i].Value = name
			break
		}
	}
	m.updateEditorDirty()
}

func (m *model) updateEditorDirty() {
	dirty := false
	for _, f := range m.server.editor.fields {
		if f.Value != f.OriginalValue {
			dirty = true
			break
		}
	}
	m.server.editor.dirty = dirty
}

func (m *model) editorCycleSelect(dir int) {
	f := &m.server.editor.fields[m.server.editor.cursor]
	if len(f.Options) == 0 {
		return
	}
	idx := 0
	for i, opt := range f.Options {
		if opt == f.Value {
			idx = i
			break
		}
	}
	idx += dir
	if idx < 0 {
		idx = len(f.Options) - 1
	}
	if idx >= len(f.Options) {
		idx = 0
	}
	f.Value = f.Options[idx]
	m.updateEditorDirty()
}

func (m *model) editorNextSection() {
	ed := &m.server.editor
	currentSection := ed.fields[ed.cursor].Section
	for i := ed.cursor + 1; i < len(ed.fields); i++ {
		if ed.fields[i].Section != currentSection {
			ed.cursor = i

			return
		}
	}
}

func (m *model) editorPrevSection() {
	ed := &m.server.editor
	currentSection := ed.fields[ed.cursor].Section
	// Find start of current section
	sectionStart := ed.cursor
	for sectionStart > 0 && ed.fields[sectionStart-1].Section == currentSection {
		sectionStart--
	}
	if sectionStart == 0 {
		ed.cursor = 0
		return
	}
	// Go to start of previous section
	prevSection := ed.fields[sectionStart-1].Section
	for sectionStart > 0 && ed.fields[sectionStart-1].Section == prevSection {
		sectionStart--
	}
	ed.cursor = sectionStart
}

func intStr(v int) string {
	return strconv.Itoa(v)
}

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func modVal(m map[string]string, key string) string {
	if m == nil {
		return ""
	}
	return m[key]
}

func hasKey(keys []string, key string) bool {
	for _, k := range keys {
		if k == key {
			return true
		}
	}
	return false
}

func formatBytes(b int64) string {
	if b < 0 {
		return "-"
	}
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	if b < 1024*1024 {
		return fmt.Sprintf("%.0f KB", float64(b)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
}

func stripANSI(s string) string {
	var out strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\033' {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// --- Async commands ---

func fetchWorlds(c *client.AgentClient) tea.Cmd {
	return func() tea.Msg {
		resp, err := c.ListWorlds()
		if err != nil {
			return worldListMsg{err: err}
		}
		return worldListMsg{worlds: resp.Worlds}
	}
}

func uploadWorld(c *client.AgentClient, dbPath string) tea.Cmd {
	return func() tea.Msg {
		dbPath = strings.TrimSpace(dbPath)
		// Derive world name and fwl path
		ext := filepath.Ext(dbPath)
		var worldName, fwlPath string
		if ext == ".db" {
			worldName = strings.TrimSuffix(filepath.Base(dbPath), ".db")
			fwlPath = strings.TrimSuffix(dbPath, ".db") + ".fwl"
		} else {
			// Assume it's a path without extension or a directory
			worldName = filepath.Base(dbPath)
			fwlPath = dbPath + ".fwl"
			dbPath = dbPath + ".db"
		}

		dbFile, err := os.Open(dbPath)
		if err != nil {
			return worldUploadMsg{err: fmt.Errorf("cannot open .db file: %w", err)}
		}
		defer dbFile.Close()

		fwlFile, err := os.Open(fwlPath)
		if err != nil {
			return worldUploadMsg{err: fmt.Errorf("cannot open .fwl file: %w", err)}
		}
		defer fwlFile.Close()

		_, err = c.UploadWorld(worldName, dbFile, fwlFile)
		if err != nil {
			return worldUploadMsg{err: err}
		}
		return worldUploadMsg{name: worldName}
	}
}

func saveSettings(c *client.AgentClient, activeLCName string, settings *agentapi.SettingsResponse) tea.Cmd {
	return func() tea.Msg {
		if activeLCName != "" {
			_, err := c.UpdateLaunchConfig(activeLCName, settings)
			return settingsUpdateMsg{err: err}
		}
		// Fallback: use direct settings update
		req := settingsToUpdateReq(settings)
		_, err := c.UpdateSettings(req)
		return settingsUpdateMsg{err: err}
	}
}

func fetchLaunchConfigs(c *client.AgentClient) tea.Cmd {
	return func() tea.Msg {
		resp, err := c.ListLaunchConfigs()
		if err != nil {
			return lcListMsg{err: err}
		}
		return lcListMsg{configs: resp.Configs, active: resp.Active}
	}
}

func createLaunchConfig(c *client.AgentClient, name, copyFrom string) tea.Cmd {
	return func() tea.Msg {
		_, err := c.CreateLaunchConfig(agentapi.LaunchConfigCreateRequest{
			Name:     name,
			CopyFrom: copyFrom,
		})
		if err != nil {
			return lcActionMsg{err: err}
		}
		// Re-fetch list
		resp, err := c.ListLaunchConfigs()
		if err != nil {
			return lcListMsg{err: err}
		}
		return lcListMsg{configs: resp.Configs, active: resp.Active}
	}
}

func deleteLaunchConfig(c *client.AgentClient, name string) tea.Cmd {
	return func() tea.Msg {
		_, err := c.DeleteLaunchConfig(name)
		if err != nil {
			return lcActionMsg{err: err}
		}
		resp, err := c.ListLaunchConfigs()
		if err != nil {
			return lcListMsg{err: err}
		}
		return lcListMsg{configs: resp.Configs, active: resp.Active}
	}
}

func activateLaunchConfig(c *client.AgentClient, name string) tea.Cmd {
	return func() tea.Msg {
		_, err := c.ActivateLaunchConfig(name)
		if err != nil {
			return lcActionMsg{err: err}
		}
		// Re-fetch to get updated active + reload settings
		resp, err := c.ListLaunchConfigs()
		if err != nil {
			return lcListMsg{err: err}
		}
		return lcListMsg{configs: resp.Configs, active: resp.Active}
	}
}

func settingsToUpdateReq(s *agentapi.SettingsResponse) *agentapi.SettingsUpdateRequest {
	return &agentapi.SettingsUpdateRequest{
		Name:         &s.Name,
		Port:         &s.Port,
		World:        &s.World,
		Password:     &s.Password,
		SaveDir:      &s.SaveDir,
		Public:       &s.Public,
		LogFile:      &s.LogFile,
		InstanceID:   &s.InstanceID,
		SaveInterval: &s.SaveInterval,
		Backups:      &s.Backups,
		BackupShort:  &s.BackupShort,
		BackupLong:   &s.BackupLong,
		Crossplay:    &s.Crossplay,
		Preset:       &s.Preset,
		Modifiers:    s.Modifiers,
		SetKeys:      s.SetKeys,
	}
}
