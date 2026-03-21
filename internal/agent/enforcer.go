package agent

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"mmcli/internal/agentapi"

	"gopkg.in/yaml.v3"
)

// EnforcerModEntry represents a single mod entry in ValheimEnforcer's Mods.yaml.
type EnforcerModEntry struct {
	PluginID string `yaml:"pluginID"`
	Version  string `yaml:"version"`
	Name     string `yaml:"name"`
}

// EnforcerModsConfig represents the full ValheimEnforcer Mods.yaml structure.
type EnforcerModsConfig struct {
	ActiveMods     map[string]EnforcerModEntry `yaml:"activeMods"`
	RequiredMods   map[string]EnforcerModEntry `yaml:"requiredMods"`
	OptionalMods   map[string]EnforcerModEntry `yaml:"optionalMods"`
	AdminOnlyMods  map[string]EnforcerModEntry `yaml:"adminOnlyMods"`
	ServerOnlyMods map[string]EnforcerModEntry `yaml:"serverOnlyMods"`
}

// enforcerGUIDEntry holds a resolved GUID and display name for a mod.
type enforcerGUIDEntry struct {
	guid    string
	name    string
	version string
}

// detectAnticheatSystems checks whether AzuAntiCheat and/or ValheimEnforcer are
// installed. It checks manifest DirNames first, then falls back to scanning plugins/.
func detectAnticheatSystems(bepDir string, mods []agentapi.ManifestMod) (hasAzu, hasEnforcer bool) {
	for _, mod := range mods {
		lower := strings.ToLower(mod.DirName)
		if strings.Contains(lower, "azuanticheat") {
			hasAzu = true
		}
		if strings.Contains(lower, "valheimenforcer") {
			hasEnforcer = true
		}
	}

	if hasAzu && hasEnforcer {
		return
	}

	// Fallback: scan plugins/ directories
	pluginsDir := filepath.Join(bepDir, "plugins")
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		lower := strings.ToLower(e.Name())
		if strings.Contains(lower, "azuanticheat") {
			hasAzu = true
		}
		if strings.Contains(lower, "valheimenforcer") {
			hasEnforcer = true
		}
	}

	return
}

// loadEnforcerConfig reads the existing ValheimEnforcer Mods.yaml from disk.
// Returns nil, nil if the file does not exist (not an error condition).
func loadEnforcerConfig(bepDir string) (*EnforcerModsConfig, error) {
	path := filepath.Join(bepDir, "config", "ValheimEnforcer", "Mods.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read Mods.yaml: %w", err)
	}

	var cfg EnforcerModsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse Mods.yaml: %w", err)
	}
	return &cfg, nil
}

// buildGUIDIndex builds a normalizedName -> enforcerGUIDEntry map from an
// existing Mods.yaml and/or mod API plugin list. This is used to resolve
// Thunderstore mod names to BepInEx plugin GUIDs.
func buildGUIDIndex(existing *EnforcerModsConfig, apiPlugins []ModAPIPlugin) map[string]enforcerGUIDEntry {
	index := make(map[string]enforcerGUIDEntry)

	// Index entries from existing Mods.yaml (all categories)
	if existing != nil {
		for _, bucket := range []map[string]EnforcerModEntry{
			existing.ActiveMods,
			existing.RequiredMods,
			existing.OptionalMods,
			existing.AdminOnlyMods,
			existing.ServerOnlyMods,
		} {
			for guid, entry := range bucket {
				norm := normalize(entry.Name)
				if norm != "" {
					index[norm] = enforcerGUIDEntry{guid: guid, name: entry.Name, version: entry.Version}
				}
				// Also index by GUID suffix (name after last dot)
				if idx := strings.LastIndex(guid, "."); idx >= 0 {
					normSuffix := normalize(guid[idx+1:])
					if normSuffix != "" {
						index[normSuffix] = enforcerGUIDEntry{guid: guid, name: entry.Name, version: entry.Version}
					}
				}
			}
		}
	}

	// Index entries from mod API (overwrites stale data from Mods.yaml)
	for _, plugin := range apiPlugins {
		entry := enforcerGUIDEntry{guid: plugin.GUID, name: plugin.Name, version: plugin.Version}
		norm := normalize(plugin.Name)
		if norm != "" {
			index[norm] = entry
		}
		if idx := strings.LastIndex(plugin.GUID, "."); idx >= 0 {
			normSuffix := normalize(plugin.GUID[idx+1:])
			if normSuffix != "" {
				index[normSuffix] = entry
			}
		}
	}

	return index
}

// resolveGUID tries to match a manifest mod to a BepInEx GUID using the index.
// Uses three tiers: exact lookup, contains matching, then token-overlap matching.
// Returns the GUID entry and true on success, or zero value and false on failure.
func resolveGUID(mod agentapi.ManifestMod, index map[string]enforcerGUIDEntry) (enforcerGUIDEntry, bool) {
	// Build the set of normalized names to try for this mod
	modNames := []string{normalize(mod.Name)}
	if norm := normalize(mod.DirName); norm != modNames[0] {
		modNames = append(modNames, norm)
	}
	if idx := strings.Index(mod.DirName, "-"); idx >= 0 {
		if norm := normalize(mod.DirName[idx+1:]); norm != modNames[0] {
			modNames = append(modNames, norm)
		}
	}

	// Tier 1: Exact map lookups
	for _, name := range modNames {
		if entry, ok := index[name]; ok {
			return entry, true
		}
	}

	// Tier 2: Contains matching — iterate index, accept only unambiguous single match
	candidates := make(map[string]enforcerGUIDEntry) // keyed by GUID for dedup
	for key, entry := range index {
		for _, name := range modNames {
			if strings.Contains(key, name) || strings.Contains(name, key) {
				candidates[entry.guid] = entry
				break
			}
		}
	}
	if len(candidates) == 1 {
		for _, entry := range candidates {
			return entry, true
		}
	}
	if len(candidates) > 1 {
		log.Printf("Enforcer: ambiguous contains match for %s (%d candidates), skipping", mod.DirName, len(candidates))
	}

	// Tier 3: Token-overlap matching — compare against entry display names
	candidates = make(map[string]enforcerGUIDEntry)
	for _, entry := range index {
		for _, name := range modNames {
			if tokenMatch(name, entry.name) {
				candidates[entry.guid] = entry
				break
			}
		}
	}
	if len(candidates) == 1 {
		for _, entry := range candidates {
			return entry, true
		}
	}
	if len(candidates) > 1 {
		log.Printf("Enforcer: ambiguous token match for %s (%d candidates), skipping", mod.DirName, len(candidates))
	}

	return enforcerGUIDEntry{}, false
}

// patchEnforcerModeration updates a single mod's classification in the existing Mods.yaml.
// It resolves the Thunderstore mod name to a BepInEx GUID, removes the mod from all
// category buckets, then re-adds it to the correct one.
// Returns the resolved GUID so callers can persist it.
func patchEnforcerModeration(bepDir string, modName, anticheat, guid, version string, modAPIPort int) (string, error) {
	existing, err := loadEnforcerConfig(bepDir)
	if err != nil || existing == nil {
		return "", fmt.Errorf("enforcer: cannot read Mods.yaml: %w", err)
	}

	apiPlugins, _ := QueryModAPI(modAPIPort)
	index := buildGUIDIndex(existing, apiPlugins)

	// Resolve mod name to GUID — parse DirName format (Owner-Name)
	mod := agentapi.ManifestMod{DirName: modName}
	if idx := strings.Index(modName, "-"); idx >= 0 {
		mod.Owner = modName[:idx]
		mod.Name = modName[idx+1:]
	} else {
		mod.Name = modName
	}
	entry, found := resolveGUID(mod, index)
	if !found && guid != "" {
		// Use provided GUID (e.g. client-only mod not on server)
		name := mod.Name
		if name == "" {
			name = modName
		}
		entry = enforcerGUIDEntry{guid: guid, name: name, version: version}
		found = true
	}
	if !found {
		return "", fmt.Errorf("enforcer: cannot resolve GUID for %s", modName)
	}

	modEntry := EnforcerModEntry{
		PluginID: entry.guid,
		Version:  entry.version,
		Name:     entry.name,
	}

	// Remove from all category buckets
	for _, bucket := range []map[string]EnforcerModEntry{
		existing.RequiredMods, existing.OptionalMods, existing.AdminOnlyMods, existing.ServerOnlyMods,
	} {
		delete(bucket, entry.guid)
	}

	// Add to correct bucket
	switch anticheat {
	case "whitelist":
		existing.RequiredMods[entry.guid] = modEntry
	case "greylist":
		existing.OptionalMods[entry.guid] = modEntry
	case "adminonly":
		existing.AdminOnlyMods[entry.guid] = modEntry
	case "serveronly":
		existing.ServerOnlyMods[entry.guid] = modEntry
	}
	// Always keep in activeMods
	existing.ActiveMods[entry.guid] = modEntry

	// Write back
	data, err := yaml.Marshal(existing)
	if err != nil {
		return "", fmt.Errorf("enforcer: marshal: %w", err)
	}
	path := filepath.Join(bepDir, "config", "ValheimEnforcer", "Mods.yaml")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("enforcer: write: %w", err)
	}

	log.Printf("Enforcer: patched %s (%s) → %s", modName, entry.guid, anticheat)
	return entry.guid, nil
}

// readEnforcerClassifications reads Mods.yaml and returns a GUID → anticheat map.
// This allows the handler to overlay classifications from manual Mods.yaml edits.
func readEnforcerClassifications(bepDir string) map[string]string {
	cfg, err := loadEnforcerConfig(bepDir)
	if err != nil || cfg == nil {
		return nil
	}
	result := make(map[string]string)
	for guid := range cfg.RequiredMods {
		result[guid] = "whitelist"
	}
	for guid := range cfg.OptionalMods {
		result[guid] = "greylist"
	}
	for guid := range cfg.AdminOnlyMods {
		result[guid] = "adminonly"
	}
	for guid := range cfg.ServerOnlyMods {
		result[guid] = "serveronly"
	}
	return result
}

