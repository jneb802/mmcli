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
	WorldLoaded   bool   `json:"world_loaded"`
	ServerReady   bool   `json:"server_ready"`
	SaveCount     int    `json:"save_count"`
	LastSave      string `json:"last_save,omitempty"`
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

// ModAPIEvent is a game event from the mod API event log.
type ModAPIEvent struct {
	Seq    int    `json:"seq"`
	Type   string `json:"type"`
	Player string `json:"player"`
	UID    int64  `json:"uid,omitempty"`
	Time   string `json:"time"`
}

// ModAPIEventsResponse is the JSON shape returned by GET /events on the mod API.
type ModAPIEventsResponse struct {
	Events []ModAPIEvent `json:"events"`
}

// QueryModEvents queries the mod API for game events after a given sequence number.
func QueryModEvents(port int, afterSeq int) ([]ModAPIEvent, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/events?after=%d", port, afterSeq))
	if err != nil {
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	var eventsResp ModAPIEventsResponse
	if err := json.NewDecoder(resp.Body).Decode(&eventsResp); err != nil {
		return nil, nil
	}
	return eventsResp.Events, nil
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

// ModMatch pairs a matched plugin with whether the match was exact.
type ModMatch struct {
	Plugin ModAPIPlugin
	Exact  bool // true = safe to overwrite version; false = fuzzy substring match
}

// MatchAPIToMods matches mod API plugins to entries in modMap.
// It uses manifestNames for high-quality matches and falls back to directory name matching.
// Returns matched (dirName -> ModMatch) and unmatched (plugins with no modMap entry).
func MatchAPIToMods(plugins []ModAPIPlugin, modMap map[string]*agentapi.ModInfo, manifestNames map[string]string) (matched map[string]ModMatch, unmatched []ModAPIPlugin) {
	matched = make(map[string]ModMatch)
	candidates := buildCandidates(modMap, manifestNames)

	for _, plugin := range plugins {
		// Extract name portion from GUID (after last dot)
		guidName := plugin.GUID
		if idx := strings.LastIndex(plugin.GUID, "."); idx >= 0 {
			guidName = plugin.GUID[idx+1:]
		}

		if dirName, exact, ok := findMatch(normalize(guidName), normalize(plugin.Name), candidates); ok {
			matched[dirName] = ModMatch{Plugin: plugin, Exact: exact}
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
// against the candidate list. Uses three tiers: exact, contains, then token-overlap.
// Returns the matching dirName, whether the match was exact, and whether any match was found.
func findMatch(normGUID, normDisplay string, candidates []modCandidate) (dirName string, exact bool, found bool) {
	// Tier 1: Exact match
	for _, c := range candidates {
		for _, name := range c.names {
			if normGUID == name || normDisplay == name {
				return c.dirName, true, true
			}
		}
	}

	// Tier 2: Contains match
	for _, c := range candidates {
		for _, name := range c.names {
			if strings.Contains(normGUID, name) || strings.Contains(name, normGUID) {
				return c.dirName, false, true
			}
			if strings.Contains(normDisplay, name) || strings.Contains(name, normDisplay) {
				return c.dirName, false, true
			}
		}
	}

	// Tier 3: Token-overlap match — accept only unambiguous single match
	var tokenMatches []string
	for _, c := range candidates {
		for _, name := range c.names {
			if tokenMatch(normDisplay, name) {
				tokenMatches = append(tokenMatches, c.dirName)
				break
			}
		}
	}
	if len(tokenMatches) == 1 {
		return tokenMatches[0], false, true
	}

	return "", false, false
}
