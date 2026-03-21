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

func saveManifest(modpackPath string, manifest *Manifest) error {
	out, err := json.MarshalIndent(manifest, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(modpackPath, "manifest.json"), out, 0644)
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

// AddDep adds a dependency string to manifest.json. If the mod already exists,
// it updates the version instead.
func AddDep(modpackPath, depString string) error {
	manifest, err := LoadManifest(modpackPath)
	if err != nil {
		return err
	}

	ref := thunderstore.ParseDep(depString)
	key := fmt.Sprintf("%s-%s", ref.Owner, ref.Name)

	// Check if already present — update version if so
	for i, dep := range manifest.Dependencies {
		existing := thunderstore.ParseDep(dep)
		existingKey := fmt.Sprintf("%s-%s", existing.Owner, existing.Name)
		if existingKey == key {
			manifest.Dependencies[i] = depString
			return saveManifest(modpackPath, manifest)
		}
	}

	manifest.Dependencies = append(manifest.Dependencies, depString)
	return saveManifest(modpackPath, manifest)
}

// RemoveDep removes a dependency from manifest.json by Owner-Name key.
func RemoveDep(modpackPath, ownerName string) error {
	manifest, err := LoadManifest(modpackPath)
	if err != nil {
		return err
	}

	var kept []string
	for _, dep := range manifest.Dependencies {
		ref := thunderstore.ParseDep(dep)
		key := fmt.Sprintf("%s-%s", ref.Owner, ref.Name)
		if key != ownerName {
			kept = append(kept, dep)
		}
	}
	manifest.Dependencies = kept
	return saveManifest(modpackPath, manifest)
}

// UpdateDep updates a single dependency version in manifest.json.
func UpdateDep(modpackPath, ownerName, newVersion string) error {
	manifest, err := LoadManifest(modpackPath)
	if err != nil {
		return err
	}

	for i, dep := range manifest.Dependencies {
		ref := thunderstore.ParseDep(dep)
		key := fmt.Sprintf("%s-%s", ref.Owner, ref.Name)
		if key == ownerName {
			manifest.Dependencies[i] = fmt.Sprintf("%s-%s-%s", ref.Owner, ref.Name, newVersion)
			break
		}
	}
	return saveManifest(modpackPath, manifest)
}

// SyncManifestDeps overwrites manifest.json dependencies from the profile.
func SyncManifestDeps(modpackPath string, reg *config.Registry, profileName string) error {
	manifest, err := LoadManifest(modpackPath)
	if err != nil {
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
	return saveManifest(modpackPath, manifest)
}
