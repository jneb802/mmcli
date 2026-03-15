package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type ModEntry struct {
	Owner        string   `json:"owner"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	IsDependency bool     `json:"is_dependency"`
	Files        []string `json:"files"`
	Dependencies []string `json:"dependencies"`
}

func (m ModEntry) FullName() string {
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
