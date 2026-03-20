package agent

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"mmcli/internal/agentapi"
)

// ParsedScript holds the start script decomposed into preamble, binary, and settings.
type ParsedScript struct {
	Preamble []string // all lines before the exec line
	Prefix   string   // "exec " or ""
	Binary   string   // e.g. "./valheim_server.x86_64"
}

// ParseStartScript reads a start script and extracts Valheim server settings.
func ParseStartScript(path string) (*agentapi.SettingsResponse, error) {
	_, settings, err := ParseStartScriptFull(path)
	return settings, err
}

// ParseStartScriptFull reads a start script, returning the parsed structure
// (preamble + binary) and the extracted settings.
func ParseStartScriptFull(path string) (*ParsedScript, *agentapi.SettingsResponse, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	var preamble []string
	var execLine string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Skip comments and blank lines — they go into preamble
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			preamble = append(preamble, line)
			continue
		}

		// Match the binary invocation line
		if strings.HasPrefix(trimmed, "exec ") || strings.Contains(trimmed, "valheim_server") {
			execLine = trimmed
			break
		}

		// Non-comment, non-exec lines go into preamble (e.g. export statements)
		preamble = append(preamble, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}

	if execLine == "" {
		return nil, nil, fmt.Errorf("no valheim_server invocation found in %s", path)
	}

	tokens := tokenizeExecLine(execLine)

	// Extract prefix and binary from leading tokens
	ps := &ParsedScript{Preamble: preamble}
	startIdx := 0
	for startIdx < len(tokens) && !strings.HasPrefix(tokens[startIdx], "-") {
		tok := tokens[startIdx]
		if tok == "exec" {
			ps.Prefix = "exec "
		} else if strings.Contains(tok, "valheim_server") {
			ps.Binary = tok
		}
		startIdx++
	}

	settings := parseExecArgs(tokens[startIdx:])

	// Read permission files from savedir
	if settings.SaveDir != "" {
		settings.Admins = readPermissionFile(filepath.Join(settings.SaveDir, "adminlist.txt"))
		settings.Banned = readPermissionFile(filepath.Join(settings.SaveDir, "bannedlist.txt"))
		settings.Permitted = readPermissionFile(filepath.Join(settings.SaveDir, "permittedlist.txt"))
	}

	return ps, settings, nil
}

// RebuildStartScript produces the new file content from a parsed script and settings.
func RebuildStartScript(ps *ParsedScript, s *agentapi.SettingsResponse) string {
	var b strings.Builder

	// Write preamble
	for _, line := range ps.Preamble {
		b.WriteString(line)
		b.WriteByte('\n')
	}

	// Build exec line
	b.WriteString(ps.Prefix)
	b.WriteString(ps.Binary)

	// Core string settings — always quote
	if s.Name != "" {
		fmt.Fprintf(&b, " -name %q", s.Name)
	}
	fmt.Fprintf(&b, " -port %d", s.Port)
	if s.World != "" {
		fmt.Fprintf(&b, " -world %q", s.World)
	}
	if s.Password != "" {
		fmt.Fprintf(&b, " -password %q", s.Password)
	}
	if s.Public != 0 {
		fmt.Fprintf(&b, " -public %d", s.Public)
	} else {
		b.WriteString(" -public 0")
	}
	if s.SaveDir != "" {
		fmt.Fprintf(&b, " -savedir %s", s.SaveDir)
	}
	if s.LogFile != "" {
		fmt.Fprintf(&b, " -logFile %q", s.LogFile)
	}
	if s.InstanceID != "" {
		fmt.Fprintf(&b, " -instanceid %q", s.InstanceID)
	}

	// Backup settings — only emit if non-zero
	if s.SaveInterval != 0 {
		fmt.Fprintf(&b, " -saveinterval %d", s.SaveInterval)
	}
	if s.Backups != 0 {
		fmt.Fprintf(&b, " -backups %d", s.Backups)
	}
	if s.BackupShort != 0 {
		fmt.Fprintf(&b, " -backupshort %d", s.BackupShort)
	}
	if s.BackupLong != 0 {
		fmt.Fprintf(&b, " -backuplong %d", s.BackupLong)
	}

	// World modifiers
	if s.Crossplay {
		b.WriteString(" -crossplay")
	}
	if s.Preset != "" {
		fmt.Fprintf(&b, " -preset %s", s.Preset)
	}
	for k, v := range s.Modifiers {
		fmt.Fprintf(&b, " -modifier %s %s", k, v)
	}
	for _, k := range s.SetKeys {
		fmt.Fprintf(&b, " -setkey %s", k)
	}

	b.WriteByte('\n')
	return b.String()
}

// ApplySettingsUpdate merges non-nil fields from the update request into current settings.
func ApplySettingsUpdate(current *agentapi.SettingsResponse, req *agentapi.SettingsUpdateRequest) {
	if req.Name != nil {
		current.Name = *req.Name
	}
	if req.Port != nil {
		current.Port = *req.Port
	}
	if req.World != nil {
		current.World = *req.World
	}
	if req.Password != nil {
		current.Password = *req.Password
	}
	if req.SaveDir != nil {
		current.SaveDir = *req.SaveDir
	}
	if req.Public != nil {
		current.Public = *req.Public
	}
	if req.LogFile != nil {
		current.LogFile = *req.LogFile
	}
	if req.InstanceID != nil {
		current.InstanceID = *req.InstanceID
	}
	if req.SaveInterval != nil {
		current.SaveInterval = *req.SaveInterval
	}
	if req.Backups != nil {
		current.Backups = *req.Backups
	}
	if req.BackupShort != nil {
		current.BackupShort = *req.BackupShort
	}
	if req.BackupLong != nil {
		current.BackupLong = *req.BackupLong
	}
	if req.Crossplay != nil {
		current.Crossplay = *req.Crossplay
	}
	if req.Preset != nil {
		current.Preset = *req.Preset
	}
	if req.Modifiers != nil {
		current.Modifiers = req.Modifiers
	}
	if req.SetKeys != nil {
		current.SetKeys = req.SetKeys
	}
	if req.Admins != nil {
		current.Admins = req.Admins
	}
}

// tokenizeExecLine splits a command line respecting quoted strings.
func tokenizeExecLine(line string) []string {
	var tokens []string
	var current strings.Builder
	inQuote := false

	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case ch == '"':
			inQuote = !inQuote
		case ch == ' ' && !inQuote:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// parseExecArgs walks the tokenized arguments and populates a SettingsResponse.
func parseExecArgs(args []string) *agentapi.SettingsResponse {
	s := &agentapi.SettingsResponse{}

	for i := 0; i < len(args); i++ {
		flag := strings.ToLower(args[i])

		switch flag {
		case "-crossplay":
			s.Crossplay = true

		case "-modifier":
			// -modifier <key> <value>
			if i+2 < len(args) {
				key := strings.ToLower(args[i+1])
				val := strings.ToLower(args[i+2])
				if s.Modifiers == nil {
					s.Modifiers = make(map[string]string)
				}
				s.Modifiers[key] = val
				i += 2
			}

		case "-setkey":
			// -setkey <key>
			if i+1 < len(args) {
				s.SetKeys = append(s.SetKeys, strings.ToLower(args[i+1]))
				i++
			}

		default:
			// All other flags take a single value
			if i+1 >= len(args) {
				break
			}
			val := args[i+1]
			i++

			switch flag {
			case "-name":
				s.Name = val
			case "-port":
				s.Port, _ = strconv.Atoi(val)
			case "-world":
				s.World = val
			case "-password":
				s.Password = val
			case "-savedir":
				s.SaveDir = val
			case "-public":
				s.Public, _ = strconv.Atoi(val)
			case "-logfile":
				s.LogFile = val
			case "-instanceid":
				s.InstanceID = val
			case "-saveinterval":
				s.SaveInterval, _ = strconv.Atoi(val)
			case "-backups":
				s.Backups, _ = strconv.Atoi(val)
			case "-backupshort":
				s.BackupShort, _ = strconv.Atoi(val)
			case "-backuplong":
				s.BackupLong, _ = strconv.Atoi(val)
			case "-preset":
				s.Preset = val
			}
		}
	}

	return s
}

// readPermissionFile reads a Valheim permission file (adminlist.txt etc.)
// writePermissionFile writes a Valheim permission file (adminlist.txt etc.).
func writePermissionFile(path string, ids []string) error {
	var b strings.Builder
	b.WriteString("// List of Steam IDs\n")
	for _, id := range ids {
		b.WriteString(id)
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// readPermissionFile reads a Valheim permission file (adminlist.txt etc.)
// and returns the list of platform user IDs.
func readPermissionFile(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var ids []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		ids = append(ids, line)
	}
	return ids
}
