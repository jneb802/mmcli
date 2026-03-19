package agent

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

// LogPlugin represents a plugin found in the BepInEx log.
type LogPlugin struct {
	DisplayName string // "Epic Loot"
	Version     string // "0.12.11"
}

var pluginLoadRe = regexp.MustCompile(`\[Info\s+:\s+BepInEx\] Loading \[(.+) (\S+)\]`)

// ParseBepInExLog reads the log file and returns all loaded plugins.
// Returns nil, nil if the file doesn't exist (not an error).
func ParseBepInExLog(logPath string) ([]LogPlugin, error) {
	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var plugins []LogPlugin
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		m := pluginLoadRe.FindStringSubmatch(scanner.Text())
		if m != nil {
			plugins = append(plugins, LogPlugin{
				DisplayName: strings.TrimSpace(m[1]),
				Version:     m[2],
			})
		}
	}
	return plugins, scanner.Err()
}

// normalize strips spaces, underscores, dots, and hyphens, then lowercases.
func normalize(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer(" ", "", "_", "", ".", "", "-", "").Replace(s)
	return s
}

// MatchLogToManifest matches log plugin entries to manifest mod names.
// Returns a map from manifest DirName to the matched LogPlugin.
// Uses normalized substring matching — only confirms matches, never claims non-matches.
func MatchLogToManifest(logPlugins []LogPlugin, manifestNames map[string]string) map[string]LogPlugin {
	matched := make(map[string]LogPlugin)

	// Build normalized log lookup
	type normLog struct {
		norm   string
		plugin LogPlugin
	}
	var normLogs []normLog
	for _, lp := range logPlugins {
		normLogs = append(normLogs, normLog{norm: normalize(lp.DisplayName), plugin: lp})
	}

	// For each manifest entry (dirName -> modName), try to match a log entry
	for dirName, modName := range manifestNames {
		normName := normalize(modName)
		for _, nl := range normLogs {
			// Exact match after normalization
			if nl.norm == normName {
				matched[dirName] = nl.plugin
				break
			}
			// Manifest name is substring of log name
			if strings.Contains(nl.norm, normName) {
				matched[dirName] = nl.plugin
				break
			}
			// Log name is substring of manifest name
			if strings.Contains(normName, nl.norm) {
				matched[dirName] = nl.plugin
				break
			}
		}
	}

	return matched
}
