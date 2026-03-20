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
	Target    string   `json:"target,omitempty"`    // "client", "server", "both" (default/"" = both)
	Anticheat string   `json:"anticheat,omitempty"` // "whitelist", "greylist", "adminonly", or "" (none)
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

type Registry struct {
	Profiles map[string]map[string]ModEntry `json:"profiles"`
}

func NewRegistry() Registry {
	return Registry{
		Profiles: make(map[string]map[string]ModEntry),
	}
}

func LoadRegistry(p Paths) (Registry, error) {
	data, err := os.ReadFile(p.RegistryFile)
	if err != nil {
		if os.IsNotExist(err) {
			return NewRegistry(), nil
		}
		return Registry{}, err
	}
	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return Registry{}, fmt.Errorf("corrupt registry.json: %w", err)
	}
	if reg.Profiles == nil {
		reg.Profiles = make(map[string]map[string]ModEntry)
	}
	return reg, nil
}

func SaveRegistry(p Paths, reg Registry) error {
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.RegistryFile, data, 0644)
}

func (r *Registry) EnsureProfile(name string) {
	if r.Profiles[name] == nil {
		r.Profiles[name] = make(map[string]ModEntry)
	}
}

func (r *Registry) GetMod(profile, fullName string) (ModEntry, bool) {
	mods, ok := r.Profiles[profile]
	if !ok {
		return ModEntry{}, false
	}
	mod, ok := mods[fullName]
	return mod, ok
}

func (r *Registry) SetMod(profile string, mod ModEntry) {
	r.EnsureProfile(profile)
	r.Profiles[profile][mod.FullName()] = mod
}

func (r *Registry) RemoveMod(profile, fullName string) {
	if mods, ok := r.Profiles[profile]; ok {
		delete(mods, fullName)
	}
}

func (r *Registry) ListMods(profile string) []ModEntry {
	mods, ok := r.Profiles[profile]
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
	registered := r.Profiles[profile]
	if registered == nil {
		registered = make(map[string]ModEntry)
	}
	return append(mods, DetectLocalMods(pluginsDir, registered)...)
}

// IsDependent returns true if any non-dependency mod in the profile depends on fullName.
func (r *Registry) IsDependent(profile, fullName string) bool {
	mods, ok := r.Profiles[profile]
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
	for _, mod := range registered {
		knownDirs[mod.FullName()] = true
	}

	var locals []ModEntry
	for _, entry := range entries {
		name := entry.Name()

		if knownDirs[name] {
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
			locals = append(locals, ModEntry{
				Name:     strings.TrimSuffix(name, ".dll.old"),
				IsLocal:  true,
				Disabled: true,
			})
		} else if strings.HasSuffix(strings.ToLower(name), ".dll") {
			locals = append(locals, ModEntry{
				Name:    strings.TrimSuffix(name, filepath.Ext(name)),
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
