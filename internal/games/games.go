// Package games defines the registry of games that mmcli supports.
//
// A Game describes everything mmcli needs to find, launch, and mod a game:
// its Steam identity, per-OS executable names, the BepInEx pack required to
// load mods into it, and capability flags for game-specific subsystems
// (dedicated-server agent, anti-cheat tooling). Games are registered
// statically in this package; adding a new game is a code change.
package games

import (
	"fmt"
	"sort"
)

// LoaderPack identifies a BepInEx-pack package on Thunderstore. The owner is
// the Thunderstore namespace, the name is the package name (e.g.
// "denikson"/"BepInExPack_Valheim", "bbepis"/"BepInExPack").
type LoaderPack struct {
	Owner string
	Name  string
}

// FullName returns "owner/name" for use with Thunderstore APIs.
func (p LoaderPack) FullName() string {
	return p.Owner + "/" + p.Name
}

// Capabilities describes optional subsystems a game supports. These gate
// features that are Valheim-specific today (and will gain other games over
// time): the dedicated-server agent flow on praetoris, the anti-cheat
// classification commands.
type Capabilities struct {
	// SupportsAgent is true if the game can be managed by mmcli-agent on a
	// remote dedicated server.
	SupportsAgent bool
	// SupportsAnticheat is true if the game's modding ecosystem has
	// anti-cheat plugins (AzuAntiCheat, ValheimEnforcer) that mmcli
	// classifies and enforces.
	SupportsAnticheat bool
}

// Game describes a single supported game.
type Game struct {
	// ID is the short identifier used in config and command line
	// (e.g. "valheim", "riskofrain2"). Lowercase, no spaces.
	ID string
	// DisplayName is the human-readable name shown in UI strings.
	DisplayName string
	// SteamAppID is the Steam application id (used on Windows registry
	// lookup paths and reserved for future Steam integration).
	SteamAppID string
	// SteamFolderName is the directory name under steamapps/common/.
	SteamFolderName string
	// ExecutableNames maps GOOS to the executable filename inside the
	// game install dir. A missing GOOS means the game is unsupported on
	// that OS.
	ExecutableNames map[string]string
	// ProcessName is the OS-level process name used by IsGameRunning
	// (pgrep on Unix, tasklist /FI IMAGENAME on Windows).
	ProcessName map[string]string
	// LoaderPack is the BepInEx pack required to mod this game.
	LoaderPack LoaderPack
	// Capabilities flags optional mmcli subsystems for this game.
	Capabilities Capabilities
}

// ExecutableFor returns the executable name for the given GOOS, or "" if
// the game is unsupported on that OS.
func (g Game) ExecutableFor(goos string) string {
	return g.ExecutableNames[goos]
}

// ProcessNameFor returns the process name used by IsGameRunning on the
// given GOOS, or "" if no name is configured (the caller should treat that
// as "running detection unsupported on this platform").
func (g Game) ProcessNameFor(goos string) string {
	return g.ProcessName[goos]
}

// SupportedOn reports whether the game has an executable registered for
// the given GOOS.
func (g Game) SupportedOn(goos string) bool {
	return g.ExecutableNames[goos] != ""
}

var registry = map[string]Game{
	"valheim": {
		ID:              "valheim",
		DisplayName:     "Valheim",
		SteamAppID:      "892970",
		SteamFolderName: "Valheim",
		ExecutableNames: map[string]string{
			"darwin":  "valheim.app",
			"linux":   "valheim.x86_64",
			"windows": "valheim.exe",
		},
		ProcessName: map[string]string{
			"darwin":  "Valheim",
			"linux":   "valheim.x86_64",
			"windows": "valheim.exe",
		},
		LoaderPack: LoaderPack{Owner: "denikson", Name: "BepInExPack_Valheim"},
		Capabilities: Capabilities{
			SupportsAgent:     true,
			SupportsAnticheat: true,
		},
	},
}

// Get returns the game with the given ID. Returns an error if no such game
// is registered.
func Get(id string) (Game, error) {
	g, ok := registry[id]
	if !ok {
		return Game{}, fmt.Errorf("unknown game %q (known: %s)", id, listIDs())
	}
	return g, nil
}

// MustGet is like Get but panics if the game is not registered. Use only
// for static known IDs (e.g. "valheim" from migration code).
func MustGet(id string) Game {
	g, err := Get(id)
	if err != nil {
		panic(err)
	}
	return g
}

// All returns every registered game, sorted by ID for stable iteration.
func All() []Game {
	out := make([]Game, 0, len(registry))
	for _, g := range registry {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// IDs returns every registered game ID, sorted.
func IDs() []string {
	out := make([]string, 0, len(registry))
	for id := range registry {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func listIDs() string {
	ids := IDs()
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ", "
		}
		out += id
	}
	return out
}
