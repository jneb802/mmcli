package modpack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"mmcli/internal/config"
	"mmcli/internal/thunderstore"
)

type Manifest struct {
	Name          string   `json:"name"`
	VersionNumber string   `json:"version_number"`
	Description   string   `json:"description"`
	WebsiteURL    string   `json:"website_url"`
	Dependencies  []string `json:"dependencies"`
}

type SyncDiffItem struct {
	Name   string
	Status string // "added", "removed", "changed"
	Old    string // old version (for changed)
	New    string // new version (for changed/added)
}

// LoadManifest reads and parses the manifest.json from a modpack directory.
func LoadManifest(modpackPath string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(modpackPath, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("cannot read manifest.json: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("invalid manifest.json: %w", err)
	}
	return &m, nil
}

// BuildSyncDiff compares profile mods to current manifest dependencies.
func BuildSyncDiff(reg *config.Registry, profileName string, manifest *Manifest) []SyncDiffItem {
	// Build current dependency set from manifest
	currentDeps := make(map[string]string) // Owner-Name -> Version
	for _, dep := range manifest.Dependencies {
		ref := thunderstore.ParseDep(dep)
		if ref.Owner != "" && ref.Name != "" {
			currentDeps[fmt.Sprintf("%s-%s", ref.Owner, ref.Name)] = ref.Version
		}
	}

	// Build new dependency set from profile
	newDeps := make(map[string]string)
	for _, mod := range reg.ListMods(profileName) {
		if mod.IsLocal || mod.Owner == "" {
			continue
		}
		newDeps[mod.FullName()] = mod.Version
	}

	var diff []SyncDiffItem

	// Added or changed
	for name, ver := range newDeps {
		oldVer, exists := currentDeps[name]
		if !exists {
			diff = append(diff, SyncDiffItem{Name: name, Status: "added", New: ver})
		} else if oldVer != ver {
			diff = append(diff, SyncDiffItem{Name: name, Status: "changed", Old: oldVer, New: ver})
		}
	}

	// Removed
	for name := range currentDeps {
		if _, exists := newDeps[name]; !exists {
			diff = append(diff, SyncDiffItem{Name: name, Status: "removed"})
		}
	}

	return diff
}

// SyncManifestDeps overwrites manifest.json dependencies from the profile.
func SyncManifestDeps(modpackPath string, reg *config.Registry, profileName string) error {
	manifestPath := filepath.Join(modpackPath, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}

	var deps []string
	for _, mod := range reg.ListMods(profileName) {
		if mod.IsLocal || mod.Owner == "" {
			continue
		}
		deps = append(deps, fmt.Sprintf("%s-%s-%s", mod.Owner, mod.Name, mod.Version))
	}
	manifest.Dependencies = deps

	out, err := json.MarshalIndent(manifest, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(manifestPath, out, 0644)
}
