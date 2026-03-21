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

// tokenize splits a string into lowercase word tokens by splitting on
// non-alphanumeric boundaries and camelCase boundaries.
// "More_and_Modified_Player_Cloth_Colliders" → ["more","and","modified","player","cloth","colliders"]
// "YamlDotNetDetector" → ["yaml","dot","net","detector"]
// "MMCLIServerMod" → ["mmcli","server","mod"]
func tokenize(s string) []string {
	// Step 1: split on non-alphanumeric
	parts := regexp.MustCompile(`[^a-zA-Z0-9]+`).Split(s, -1)

	// Step 2: split each part on camelCase boundaries
	seen := make(map[string]bool)
	var tokens []string
	for _, part := range parts {
		if part == "" {
			continue
		}
		for _, w := range splitCamelCase(part) {
			w = strings.ToLower(w)
			if w != "" && !seen[w] {
				seen[w] = true
				tokens = append(tokens, w)
			}
		}
	}
	return tokens
}

// splitCamelCase splits a string on camelCase boundaries, correctly handling
// acronyms: "MMCLIServerMod" → ["MMCLI","Server","Mod"].
func splitCamelCase(s string) []string {
	var words []string
	start := 0
	for i := 1; i < len(s); i++ {
		prev := s[i-1]
		cur := s[i]
		// Break between lowercase/digit and uppercase: "serverM" → "server","M..."
		if (isLow(prev) || isDig(prev)) && isUp(cur) {
			words = append(words, s[start:i])
			start = i
		}
		// Break at end of acronym: "CLISe" → "CLI","Se..."
		if i+1 < len(s) && isUp(prev) && isUp(cur) && isLow(s[i+1]) {
			words = append(words, s[start:i])
			start = i
		}
	}
	if start < len(s) {
		words = append(words, s[start:])
	}
	return words
}

func isUp(b byte) bool  { return b >= 'A' && b <= 'Z' }
func isLow(b byte) bool { return b >= 'a' && b <= 'z' }
func isDig(b byte) bool { return b >= '0' && b <= '9' }

// tokenMatch returns true if all tokens from the shorter token set appear
// in the longer token set. Requires the shorter set to have at least 3 tokens
// to prevent false positives on common words.
func tokenMatch(a, b string) bool {
	tokA := tokenize(a)
	tokB := tokenize(b)
	if len(tokA) == 0 || len(tokB) == 0 {
		return false
	}
	// shorter = the set we check is a subset of longer
	shorter, longer := tokA, tokB
	if len(tokA) > len(tokB) {
		shorter, longer = tokB, tokA
	}
	if len(shorter) < 3 {
		return false
	}
	longerSet := make(map[string]bool, len(longer))
	for _, t := range longer {
		longerSet[t] = true
	}
	for _, t := range shorter {
		if !longerSet[t] {
			return false
		}
	}
	return true
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
