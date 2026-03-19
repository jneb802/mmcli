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

// ParseStartScript reads a start script and extracts Valheim server settings
// from the exec/binary invocation line.
func ParseStartScript(path string) (*agentapi.SettingsResponse, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var execLine string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Skip comments and blank lines
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Match the binary invocation line
		if strings.HasPrefix(trimmed, "exec ") || strings.Contains(trimmed, "valheim_server") {
			execLine = trimmed
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if execLine == "" {
		return nil, fmt.Errorf("no valheim_server invocation found in %s", path)
	}

	tokens := tokenizeExecLine(execLine)

	// Skip leading tokens until we hit one starting with "-"
	// (skips "exec", binary path, etc.)
	startIdx := 0
	for startIdx < len(tokens) && !strings.HasPrefix(tokens[startIdx], "-") {
		startIdx++
	}

	settings := parseExecArgs(tokens[startIdx:])

	// Read permission files from savedir
	if settings.SaveDir != "" {
		settings.Admins = readPermissionFile(filepath.Join(settings.SaveDir, "adminlist.txt"))
		settings.Banned = readPermissionFile(filepath.Join(settings.SaveDir, "bannedlist.txt"))
		settings.Permitted = readPermissionFile(filepath.Join(settings.SaveDir, "permittedlist.txt"))
	}

	return settings, nil
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
