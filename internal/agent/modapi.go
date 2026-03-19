package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"mmcli/internal/agentapi"
)

// ModAPIPlugin represents a single plugin from the MMCLIServerMod HTTP API.
type ModAPIPlugin struct {
	GUID         string   `json:"guid"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Dependencies []string `json:"dependencies"`
}

// ModAPIResponse is the JSON shape returned by GET /plugins on the mod API.
type ModAPIResponse struct {
	Plugins []ModAPIPlugin `json:"plugins"`
}

// QueryModAPI queries the MMCLIServerMod HTTP API for loaded plugins.
// Returns nil, nil if the server is unreachable (not an error condition).
func QueryModAPI(port int) ([]ModAPIPlugin, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/plugins", port))
	if err != nil {
		return nil, nil // unreachable — graceful fallback
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	var apiResp ModAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, nil
	}

	return apiResp.Plugins, nil
}

// ModAPIStatus is the JSON shape returned by GET /status on the mod API.
type ModAPIStatus struct {
	ServerRunning bool   `json:"server_running"`
	World         string `json:"world"`
	IsDedicated   bool   `json:"is_dedicated"`
	PlayerCount   int    `json:"player_count"`
	Day           int    `json:"day"`
	GameTime      string `json:"game_time"`
	IsDay         bool   `json:"is_day"`
}

// ModAPIPlayer is a single player from the mod API.
type ModAPIPlayer struct {
	Name        string `json:"name"`
	Host        string `json:"host"`
	UID         int64  `json:"uid"`
	CharacterID string `json:"character_id"`
}

// ModAPIPlayersResponse is the JSON shape returned by GET /players on the mod API.
type ModAPIPlayersResponse struct {
	Players []ModAPIPlayer `json:"players"`
}

// QueryModStatus queries the mod API for server game state.
func QueryModStatus(port int) (*ModAPIStatus, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/status", port))
	if err != nil {
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	var status ModAPIStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, nil
	}
	return &status, nil
}

// QueryModPlayers queries the mod API for connected players.
func QueryModPlayers(port int) ([]ModAPIPlayer, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/players", port))
	if err != nil {
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	var playersResp ModAPIPlayersResponse
	if err := json.NewDecoder(resp.Body).Decode(&playersResp); err != nil {
		return nil, nil
	}
	return playersResp.Players, nil
}

// modCandidate pairs a modMap directory name with normalized names to match against.
type modCandidate struct {
	dirName string
	names   []string
}

// MatchAPIToMods matches mod API plugins to entries in modMap.
// It uses manifestNames for high-quality matches and falls back to directory name matching.
// Returns matched (dirName -> plugin) and unmatched (plugins with no modMap entry).
func MatchAPIToMods(plugins []ModAPIPlugin, modMap map[string]*agentapi.ModInfo, manifestNames map[string]string) (matched map[string]ModAPIPlugin, unmatched []ModAPIPlugin) {
	matched = make(map[string]ModAPIPlugin)
	candidates := buildCandidates(modMap, manifestNames)

	for _, plugin := range plugins {
		// Extract name portion from GUID (after last dot)
		guidName := plugin.GUID
		if idx := strings.LastIndex(plugin.GUID, "."); idx >= 0 {
			guidName = plugin.GUID[idx+1:]
		}

		if dirName, ok := findMatch(normalize(guidName), normalize(plugin.Name), candidates); ok {
			matched[dirName] = plugin
		} else {
			unmatched = append(unmatched, plugin)
		}
	}

	return matched, unmatched
}

// buildCandidates constructs normalized match names for every modMap entry.
func buildCandidates(modMap map[string]*agentapi.ModInfo, manifestNames map[string]string) []modCandidate {
	candidates := make([]modCandidate, 0, len(modMap))
	for dirName := range modMap {
		var names []string

		// Best source: manifest name (e.g., "EpicLoot")
		if modName, ok := manifestNames[dirName]; ok {
			names = append(names, normalize(modName))
		}

		// Directory name itself (e.g., "RandyKnapp-EpicLoot")
		names = append(names, normalize(dirName))

		// Name portion after first hyphen (e.g., "EpicLoot" from "RandyKnapp-EpicLoot")
		if idx := strings.Index(dirName, "-"); idx >= 0 {
			names = append(names, normalize(dirName[idx+1:]))
		}

		candidates = append(candidates, modCandidate{dirName: dirName, names: names})
	}
	return candidates
}

// findMatch tries to match a plugin's normalized GUID suffix and display name
// against the candidate list. Returns the matching dirName and true, or ("", false).
func findMatch(normGUID, normDisplay string, candidates []modCandidate) (string, bool) {
	for _, c := range candidates {
		for _, name := range c.names {
			if normGUID == name || normDisplay == name {
				return c.dirName, true
			}
			if strings.Contains(normGUID, name) || strings.Contains(name, normGUID) {
				return c.dirName, true
			}
			if strings.Contains(normDisplay, name) || strings.Contains(name, normDisplay) {
				return c.dirName, true
			}
		}
	}
	return "", false
}
