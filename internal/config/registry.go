package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ModEntry struct {
	Owner        string   `json:"owner"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	IsDependency bool     `json:"is_dependency"`
	IsLocal      bool     `json:"-"`
	Disabled     bool     `json:"disabled,omitempty"`
	Files        []string `json:"files"`
	Dependencies []string `json:"dependencies"`
	Target       string   `json:"target,omitempty"`    // "client", "server", "both" (default/"" = both)
	Anticheat    string   `json:"anticheat,omitempty"` // vestigial — server is source of truth; kept for CLI compat
	GUID         string   `json:"guid,omitempty"`      // BepInEx plugin GUID, persisted after first match
}

func (m ModEntry) ResolvedTarget() string {
	if m.Target == "" {
		return "both"
	}
	return m.Target
}

func (m ModEntry) FullName() string {
	if m.IsLocal || m.Owner == "" {
		return m.Name
	}
	return fmt.Sprintf("%s-%s", m.Owner, m.Name)
}

// ProfileSettings stores per-profile configuration (server, modpack, anticheat).
type ProfileSettings struct {
	Server            string `json:"server,omitempty"`             // key into Config.Servers
	ServerManagement  *bool  `json:"server_management,omitempty"`  // nil = enabled
	ModpackPath       string `json:"modpack_path,omitempty"`
	ModpackManagement *bool  `json:"modpack_management,omitempty"` // nil = enabled
	AnticheatSystem   string `json:"anticheat_system,omitempty"`   // "auto", "azu", "enforcer", ""
}

// ServerManagementEnabled returns true if server management is enabled (default: true).
func (ps ProfileSettings) ServerManagementEnabled() bool {
	return ps.ServerManagement == nil || *ps.ServerManagement
}

// ModpackManagementEnabled returns true if modpack management is enabled (default: true).
func (ps ProfileSettings) ModpackManagementEnabled() bool {
	return ps.ModpackManagement == nil || *ps.ModpackManagement
}

// GameRegistry holds the per-game mod and settings data.
type GameRegistry struct {
	Profiles map[string]map[string]ModEntry `json:"profiles"`
	Settings map[string]ProfileSettings     `json:"settings,omitempty"`
}

func newGameRegistry() *GameRegistry {
	return &GameRegistry{
		Profiles: make(map[string]map[string]ModEntry),
		Settings: make(map[string]ProfileSettings),
	}
}

// Registry holds mod registration data segmented per game. Methods operate
// on the active game's data; SetActiveGame must be called once after
// loading or constructing a Registry before any mod or settings methods
// are invoked.
type Registry struct {
	Games map[string]*GameRegistry `json:"games,omitempty"`

	// Profiles and Settings are legacy fields (pre-multigame). They are
	// populated when unmarshaling an old registry.json; LoadRegistry
	// migrates their contents into Games["valheim"] and clears them so
	// they do not appear on subsequent saves.
	Profiles map[string]map[string]ModEntry `json:"profiles,omitempty"`
	Settings map[string]ProfileSettings     `json:"settings,omitempty"`

	activeGame string
}

// defaultActiveGame is used when an empty active game is passed in. Until
// PR-2 adds Risk of Rain 2, "valheim" is the only registered game, so
// defaulting to it preserves pre-multigame behavior for callers that
// haven't been updated yet.
const defaultActiveGame = "valheim"

// NewRegistry returns a Registry with the default active game selected.
func NewRegistry() Registry {
	r := Registry{
		Games: make(map[string]*GameRegistry),
	}
	r.SetActiveGame(defaultActiveGame)
	return r
}

// LoadRegistry reads the registry file, migrates legacy shape if present,
// and sets the active game so subsequent method calls operate on the
// correct slice of registry data. An empty activeGame falls back to the
// default ("valheim").
func LoadRegistry(p Paths, activeGame string) (Registry, error) {
	if activeGame == "" {
		activeGame = defaultActiveGame
	}
	data, err := os.ReadFile(p.RegistryFile)
	if err != nil {
		if os.IsNotExist(err) {
			r := Registry{Games: make(map[string]*GameRegistry)}
			r.SetActiveGame(activeGame)
			return r, nil
		}
		return Registry{}, err
	}
	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return Registry{}, fmt.Errorf("corrupt registry.json: %w", err)
	}
	migrateRegistryToGames(&reg)
	reg.SetActiveGame(activeGame)
	return reg, nil
}

func SaveRegistry(p Paths, reg Registry) error {
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.RegistryFile, data, 0644)
}

// migrateRegistryToGames folds legacy top-level Profiles/Settings into
// Games["valheim"]. Returns true if the registry was modified.
func migrateRegistryToGames(r *Registry) bool {
	hasLegacy := len(r.Profiles) > 0 || len(r.Settings) > 0
	if !hasLegacy {
		if r.Games == nil {
			r.Games = make(map[string]*GameRegistry)
		}
		return false
	}
	if r.Games == nil {
		r.Games = make(map[string]*GameRegistry)
	}
	if r.Games["valheim"] == nil {
		r.Games["valheim"] = newGameRegistry()
	}
	v := r.Games["valheim"]
	for name, mods := range r.Profiles {
		if v.Profiles[name] == nil {
			v.Profiles[name] = mods
		}
	}
	for name, ps := range r.Settings {
		if _, exists := v.Settings[name]; !exists {
			v.Settings[name] = ps
		}
	}
	r.Profiles = nil
	r.Settings = nil
	return true
}

// SetActiveGame selects which game's registry data the receiver methods
// operate on. Creates an empty GameRegistry for the game if one doesn't
// already exist.
func (r *Registry) SetActiveGame(gameID string) {
	r.activeGame = gameID
	if gameID == "" {
		return
	}
	if r.Games == nil {
		r.Games = make(map[string]*GameRegistry)
	}
	if r.Games[gameID] == nil {
		r.Games[gameID] = newGameRegistry()
	}
}

// ActiveGame returns the currently selected game ID, or "" if none has
// been set.
func (r *Registry) ActiveGame() string {
	return r.activeGame
}

// active returns the GameRegistry for the active game. Lazily defaults
// the active game to "valheim" when the receiver was constructed without
// SetActiveGame being called — the only situation this happens in
// practice is direct struct literals in tests or zero-value Registry
// values, which predate multigame support.
func (r *Registry) active() *GameRegistry {
	if r.activeGame == "" {
		r.activeGame = defaultActiveGame
	}
	if r.Games == nil {
		r.Games = make(map[string]*GameRegistry)
	}
	if r.Games[r.activeGame] == nil {
		r.Games[r.activeGame] = newGameRegistry()
	}
	return r.Games[r.activeGame]
}

func (r *Registry) EnsureProfile(name string) {
	g := r.active()
	if g.Profiles[name] == nil {
		g.Profiles[name] = make(map[string]ModEntry)
	}
	if _, ok := g.Settings[name]; !ok {
		g.Settings[name] = ProfileSettings{}
	}
}

// ProfileNames returns the profile names registered for the active game.
func (r *Registry) ProfileNames() []string {
	g := r.active()
	out := make([]string, 0, len(g.Profiles))
	for name := range g.Profiles {
		out = append(out, name)
	}
	return out
}

// ProfileMods returns the registered mods map for the given profile in the
// active game. Returns nil if the profile is not registered.
func (r *Registry) ProfileMods(profile string) map[string]ModEntry {
	return r.active().Profiles[profile]
}

// DeleteProfile removes a profile's mod and settings entries from the
// active game.
func (r *Registry) DeleteProfile(name string) {
	g := r.active()
	delete(g.Profiles, name)
	delete(g.Settings, name)
}

// GetSettings returns the profile settings for the given profile in the
// active game.
func (r *Registry) GetSettings(profile string) ProfileSettings {
	return r.active().Settings[profile]
}

// SetSettings stores the profile settings for the given profile in the
// active game.
func (r *Registry) SetSettings(profile string, ps ProfileSettings) {
	g := r.active()
	if g.Settings == nil {
		g.Settings = make(map[string]ProfileSettings)
	}
	g.Settings[profile] = ps
}

func (r *Registry) GetMod(profile, fullName string) (ModEntry, bool) {
	mods, ok := r.active().Profiles[profile]
	if !ok {
		return ModEntry{}, false
	}
	mod, ok := mods[fullName]
	return mod, ok
}

func (r *Registry) SetMod(profile string, mod ModEntry) {
	r.EnsureProfile(profile)
	r.active().Profiles[profile][mod.FullName()] = mod
}

func (r *Registry) RemoveMod(profile, fullName string) {
	if mods, ok := r.active().Profiles[profile]; ok {
		delete(mods, fullName)
	}
}

func (r *Registry) ListMods(profile string) []ModEntry {
	mods, ok := r.active().Profiles[profile]
	if !ok {
		return nil
	}
	result := make([]ModEntry, 0, len(mods))
	for _, mod := range mods {
		result = append(result, mod)
	}
	return result
}

// ListAllMods returns registry mods plus locally-detected mods for a profile.
func (r *Registry) ListAllMods(profile, pluginsDir string) []ModEntry {
	mods := r.ListMods(profile)
	registered := r.active().Profiles[profile]
	if registered == nil {
		registered = make(map[string]ModEntry)
	}
	return append(mods, DetectLocalMods(pluginsDir, registered)...)
}

// IsDependent returns true if any non-dependency mod in the profile depends on fullName.
func (r *Registry) IsDependent(profile, fullName string) bool {
	mods, ok := r.active().Profiles[profile]
	if !ok {
		return false
	}
	for _, mod := range mods {
		for _, dep := range mod.Dependencies {
			if dep == fullName {
				return true
			}
		}
	}
	return false
}

// DetectLocalMods scans the plugins directory for mods not tracked in the registry.
func DetectLocalMods(pluginsDir string, registered map[string]ModEntry) []ModEntry {
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return nil
	}

	knownDirs := make(map[string]bool)
	knownNames := make(map[string]bool) // bare Name without owner prefix
	for _, mod := range registered {
		knownDirs[mod.FullName()] = true
		if mod.Owner != "" && mod.Name != "" {
			knownNames[mod.Name] = true
		}
	}

	isKnown := func(name string) bool {
		return knownDirs[name] || knownNames[name]
	}

	var locals []ModEntry
	for _, entry := range entries {
		name := entry.Name()

		if isKnown(name) {
			continue
		}

		if entry.IsDir() {
			hasDLL, hasDisabledDLL := scanForDLLs(filepath.Join(pluginsDir, name))
			if hasDLL || hasDisabledDLL {
				locals = append(locals, ModEntry{
					Name:     name,
					IsLocal:  true,
					Disabled: !hasDLL && hasDisabledDLL,
				})
			}
		} else if strings.HasSuffix(strings.ToLower(name), ".dll.old") {
			modName := strings.TrimSuffix(name, ".dll.old")
			if isKnown(modName) {
				continue
			}
			locals = append(locals, ModEntry{
				Name:     modName,
				IsLocal:  true,
				Disabled: true,
			})
		} else if strings.HasSuffix(strings.ToLower(name), ".dll") {
			modName := strings.TrimSuffix(name, filepath.Ext(name))
			if isKnown(modName) {
				continue
			}
			locals = append(locals, ModEntry{
				Name:    modName,
				IsLocal: true,
			})
		}
	}

	return locals
}

func scanForDLLs(dir string) (hasDLL bool, hasDisabledDLL bool) {
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		lower := strings.ToLower(d.Name())
		if strings.HasSuffix(lower, ".dll.old") {
			hasDisabledDLL = true
		} else if strings.HasSuffix(lower, ".dll") {
			hasDLL = true
		}
		return nil
	})
	return
}
