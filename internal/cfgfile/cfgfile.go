package cfgfile

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"
)

// Entry represents a single config setting with its metadata.
type Entry struct {
	Section          string // [SectionName]
	Key              string // setting name
	Value            string // current value
	Description      string // from ## comments
	SettingType      string // from "# Setting type: X"
	DefaultValue     string // from "# Default value: X"
	AcceptableValues string // from "# Acceptable values: ..." or range
}

// CfgFile represents a parsed BepInEx .cfg file.
type CfgFile struct {
	Header  string  // preamble comments before first section
	Entries []Entry // all entries in file order
}

// DiffStatus indicates how an entry differs between two files.
type DiffStatus int

const (
	Changed    DiffStatus = iota // value differs
	LocalOnly                    // exists only in local
	RemoteOnly                   // exists only in remote
)

// DiffEntry represents a difference between two config files.
type DiffEntry struct {
	Entry                  // base info (section, key, metadata from whichever side has it)
	LocalValue  string     // value in local file ("" if missing)
	RemoteValue string     // value in remote file ("" if missing)
	Status      DiffStatus // Changed, LocalOnly, RemoteOnly
}

// Patch describes a single key=value change to apply to a .cfg file.
type Patch struct {
	Section string
	Key     string
	Value   string
}

// ParseFile reads and parses a .cfg file from disk.
func ParseFile(path string) (*CfgFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseBytes(data)
}

// ParseBytes parses .cfg content from bytes.
func ParseBytes(data []byte) (*CfgFile, error) {
	cfg := &CfgFile{}
	scanner := bufio.NewScanner(bytes.NewReader(data))

	var currentSection string
	var description strings.Builder
	var settingType, defaultValue, acceptableValues string
	inHeader := true

	var headerLines []string

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Section header
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inHeader = false
			currentSection = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			// Reset accumulated metadata
			description.Reset()
			settingType = ""
			defaultValue = ""
			acceptableValues = ""
			continue
		}

		// Before any section, accumulate header
		if inHeader {
			headerLines = append(headerLines, line)
			continue
		}

		// Description comment (## )
		if strings.HasPrefix(trimmed, "## ") {
			if description.Len() > 0 {
				description.WriteString("\n")
			}
			description.WriteString(strings.TrimPrefix(trimmed, "## "))
			continue
		}
		// Description comment (just ##)
		if trimmed == "##" {
			if description.Len() > 0 {
				description.WriteString("\n")
			}
			continue
		}

		// Metadata comments
		if strings.HasPrefix(trimmed, "# Setting type: ") {
			settingType = strings.TrimPrefix(trimmed, "# Setting type: ")
			continue
		}
		if strings.HasPrefix(trimmed, "# Default value: ") {
			defaultValue = strings.TrimPrefix(trimmed, "# Default value: ")
			continue
		}
		if strings.HasPrefix(trimmed, "# Acceptable values: ") {
			acceptableValues = strings.TrimPrefix(trimmed, "# Acceptable values: ")
			continue
		}
		if strings.HasPrefix(trimmed, "# Acceptable value range: ") {
			acceptableValues = strings.TrimPrefix(trimmed, "# Acceptable value range: ")
			continue
		}

		// Skip other comments and the flags hint line
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}

		// Blank line resets accumulated metadata
		if trimmed == "" {
			description.Reset()
			settingType = ""
			defaultValue = ""
			acceptableValues = ""
			continue
		}

		// Key = Value line
		if idx := strings.Index(trimmed, "="); idx > 0 {
			key := strings.TrimSpace(trimmed[:idx])
			value := strings.TrimSpace(trimmed[idx+1:])

			cfg.Entries = append(cfg.Entries, Entry{
				Section:          currentSection,
				Key:              key,
				Value:            value,
				Description:      description.String(),
				SettingType:      settingType,
				DefaultValue:     defaultValue,
				AcceptableValues: acceptableValues,
			})

			// Reset metadata for next entry
			description.Reset()
			settingType = ""
			defaultValue = ""
			acceptableValues = ""
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	cfg.Header = strings.Join(headerLines, "\n")
	return cfg, nil
}

// entryKey returns a composite key for map lookups.
func entryKey(section, key string) string {
	return section + "\x00" + key
}

// EntryMap returns entries indexed by "Section\x00Key" for efficient lookup.
func (c *CfgFile) EntryMap() map[string]Entry {
	m := make(map[string]Entry, len(c.Entries))
	for _, e := range c.Entries {
		m[entryKey(e.Section, e.Key)] = e
	}
	return m
}

// Diff compares two CfgFiles and returns entries that differ.
// local is the source of truth for metadata when both sides have an entry.
func Diff(local, remote *CfgFile) []DiffEntry {
	localMap := local.EntryMap()
	remoteMap := remote.EntryMap()

	var diffs []DiffEntry

	// Check local entries against remote
	for _, e := range local.Entries {
		k := entryKey(e.Section, e.Key)
		if re, ok := remoteMap[k]; ok {
			if e.Value != re.Value {
				diffs = append(diffs, DiffEntry{
					Entry:       e,
					LocalValue:  e.Value,
					RemoteValue: re.Value,
					Status:      Changed,
				})
			}
		} else {
			diffs = append(diffs, DiffEntry{
				Entry:      e,
				LocalValue: e.Value,
				Status:     LocalOnly,
			})
		}
	}

	// Check remote entries that don't exist locally
	for _, e := range remote.Entries {
		k := entryKey(e.Section, e.Key)
		if _, ok := localMap[k]; !ok {
			diffs = append(diffs, DiffEntry{
				Entry:       e,
				RemoteValue: e.Value,
				Status:      RemoteOnly,
			})
		}
	}

	return diffs
}

// PatchFile applies patches to a .cfg file in place.
// It reads the file line by line, finds the correct [Section] and Key = ... line,
// and replaces only the value portion. Preserves all comments and formatting.
func PatchFile(path string, patches []Patch) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	// Index patches by section+key for fast lookup
	patchMap := make(map[string]string)
	for _, p := range patches {
		patchMap[entryKey(p.Section, p.Key)] = p.Value
	}

	lines := strings.Split(string(data), "\n")
	var currentSection string
	applied := 0

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track section changes
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			currentSection = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			continue
		}

		// Skip comments and blank lines
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}

		// Key = Value line
		if idx := strings.Index(trimmed, "="); idx > 0 {
			key := strings.TrimSpace(trimmed[:idx])
			k := entryKey(currentSection, key)

			if newValue, ok := patchMap[k]; ok {
				// Preserve the original key and spacing style
				eqIdx := strings.Index(line, "=")
				lines[i] = line[:eqIdx+1] + " " + newValue
				applied++
			}
		}
	}

	if applied > 0 {
		err = os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)
		if err != nil {
			return 0, fmt.Errorf("failed to write patched file: %w", err)
		}
	}

	return applied, nil
}
