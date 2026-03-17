package thunderstore

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

const profileCodeAPI = baseURL + "/api/experimental/legacyprofile/get/"

var profileCodeRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ProfileMod represents a mod entry from a profile code's export.r2x.
type ProfileMod struct {
	Name    string // "Owner-ModName"
	Version string // "1.2.3"
	Enabled bool
}

// IsProfileCode returns true if the string looks like a profile code UUID.
func IsProfileCode(s string) bool {
	return profileCodeRegex.MatchString(s)
}

// FetchProfileCode downloads a profile code and returns the profile name,
// list of mods, and the raw zip data (for config extraction).
func FetchProfileCode(code string) (string, []ProfileMod, []byte, error) {
	resp, err := http.Get(profileCodeAPI + code + "/")
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to fetch profile code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", nil, nil, fmt.Errorf("profile code not found or expired (HTTP %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to read profile code response: %w", err)
	}

	data := string(body)
	if !strings.HasPrefix(data, "#r2modman") {
		return "", nil, nil, fmt.Errorf("invalid profile code data: missing #r2modman header")
	}

	b64 := strings.TrimSpace(data[len("#r2modman"):])
	zipData, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to decode profile data: %w", err)
	}

	// Read export.r2x from the zip
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to open profile zip: %w", err)
	}

	var r2xData []byte
	for _, f := range zr.File {
		if f.Name == "export.r2x" {
			rc, err := f.Open()
			if err != nil {
				return "", nil, nil, fmt.Errorf("failed to open export.r2x: %w", err)
			}
			r2xData, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return "", nil, nil, fmt.Errorf("failed to read export.r2x: %w", err)
			}
			break
		}
	}
	if r2xData == nil {
		return "", nil, nil, fmt.Errorf("profile zip missing export.r2x")
	}

	profileName, mods, err := parseR2X(string(r2xData))
	if err != nil {
		return "", nil, nil, err
	}

	return profileName, mods, zipData, nil
}

// parseR2X parses the YAML-like export.r2x format.
func parseR2X(data string) (string, []ProfileMod, error) {
	var profileName string
	var mods []ProfileMod

	lines := strings.Split(data, "\n")
	i := 0
	for i < len(lines) {
		line := strings.TrimRight(lines[i], "\r")

		if strings.HasPrefix(line, "profileName:") {
			profileName = trimYAMLString(line[len("profileName:"):])
			i++
			continue
		}

		if strings.TrimSpace(line) == "mods:" {
			i++
			// Parse mod entries
			for i < len(lines) {
				line = strings.TrimRight(lines[i], "\r")
				trimmed := strings.TrimSpace(line)

				if !strings.HasPrefix(trimmed, "- name:") && !strings.HasPrefix(trimmed, "name:") {
					if strings.HasPrefix(trimmed, "-") || (!strings.HasPrefix(trimmed, " ") && trimmed != "") {
						// We've either hit a new list item without a name (shouldn't happen)
						// or left the mods block
						if !strings.HasPrefix(trimmed, "-") {
							break
						}
					}
				}

				if strings.HasPrefix(trimmed, "- name:") {
					mod := ProfileMod{Enabled: true}
					mod.Name = trimYAMLString(trimmed[len("- name:"):])
					i++

					var major, minor, patch string
					// Read nested fields
					for i < len(lines) {
						nested := strings.TrimRight(lines[i], "\r")
						nestedTrimmed := strings.TrimSpace(nested)

						if nestedTrimmed == "" || strings.HasPrefix(nestedTrimmed, "- ") || (!strings.HasPrefix(nested, " ") && !strings.HasPrefix(nested, "\t")) {
							break
						}

						if strings.HasPrefix(nestedTrimmed, "major:") {
							major = strings.TrimSpace(nestedTrimmed[len("major:"):])
						} else if strings.HasPrefix(nestedTrimmed, "minor:") {
							minor = strings.TrimSpace(nestedTrimmed[len("minor:"):])
						} else if strings.HasPrefix(nestedTrimmed, "patch:") {
							patch = strings.TrimSpace(nestedTrimmed[len("patch:"):])
						} else if strings.HasPrefix(nestedTrimmed, "enabled:") {
							val := strings.TrimSpace(nestedTrimmed[len("enabled:"):])
							mod.Enabled = val != "false"
						}
						i++
					}

					if major != "" || minor != "" || patch != "" {
						mod.Version = fmt.Sprintf("%s.%s.%s", defZero(major), defZero(minor), defZero(patch))
					}
					mods = append(mods, mod)
					continue
				}
				i++
			}
			continue
		}
		i++
	}

	if profileName == "" {
		return "", nil, fmt.Errorf("export.r2x missing profileName")
	}

	return profileName, mods, nil
}

func trimYAMLString(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
	}
	return s
}

func defZero(s string) string {
	if s == "" {
		return "0"
	}
	// Validate it's a number
	if _, err := strconv.Atoi(s); err != nil {
		return "0"
	}
	return s
}
