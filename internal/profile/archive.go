package profile

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mmcli/internal/agentapi"
	"mmcli/internal/config"
)

// BuildManifest creates a PushManifest from the registry, excluding client-only mods.
// Mods with Owner=="local" get Source="upload"; all others get Source="thunderstore".
func BuildManifest(profileName string, reg config.Registry) agentapi.PushManifest {
	var mods []agentapi.ManifestMod
	for _, mod := range reg.ListMods(profileName) {
		if mod.ResolvedTarget() == "client" {
			continue
		}
		// Skip non-Thunderstore mods with no owner — these are server-detected
		// mods (e.g. loose DLLs) that are already on the server and can't be
		// downloaded or uploaded.
		if mod.Owner == "" {
			continue
		}
		source := "thunderstore"
		if mod.Owner == "local" {
			source = "upload"
		}
		mods = append(mods, agentapi.ManifestMod{
			DirName: mod.FullName(),
			Owner:   mod.Owner,
			Name:    mod.Name,
			Version: mod.Version,
			Target:  mod.ResolvedTarget(),
			Source:  source,
			GUID:    mod.GUID,
			// Anticheat intentionally omitted — server is source of truth.
		})
	}
	return agentapi.PushManifest{
		PushedAt: time.Now().UTC().Format(time.RFC3339),
		Profile:  profileName,
		Mods:     mods,
	}
}

// BuildUploads packages each upload-source mod into an in-memory zip.
// Returns a map from DirName to the zip data reader.
func BuildUploads(paths config.Paths, profileName string, manifest agentapi.PushManifest, reg config.Registry) (map[string]io.Reader, error) {
	uploads := make(map[string]io.Reader)

	// Build server mod set for .dll.old → .dll renaming
	serverMods := make(map[string]bool)
	for _, mod := range reg.ListMods(profileName) {
		if mod.ResolvedTarget() == "server" {
			serverMods[mod.FullName()] = true
		}
	}

	for _, mod := range manifest.Mods {
		if mod.Source != "upload" {
			continue
		}
		r, err := packageUploadMod(paths, profileName, mod.DirName, serverMods[mod.DirName])
		if err != nil {
			return nil, fmt.Errorf("failed to package %s: %w", mod.DirName, err)
		}
		uploads[mod.DirName] = r
	}
	return uploads, nil
}

// packageUploadMod creates an in-memory zip of a local mod's files from the profile.
// Files are stored with prefixes (plugins/, patchers/, monomod/) so the server knows
// where to extract them. For server-targeted mods, .dll.old files are renamed to .dll.
func packageUploadMod(paths config.Paths, profileName, dirName string, isServerMod bool) (io.Reader, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	dirs := map[string]string{
		"plugins/":  filepath.Join(paths.ProfilePluginsDir(profileName), dirName),
		"patchers/": filepath.Join(paths.ProfilePatchersDir(profileName), dirName),
		"monomod/":  filepath.Join(paths.ProfileMonomodDir(profileName), dirName),
	}

	for prefix, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}

			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return nil
			}

			archivePath := prefix + rel

			// For server-targeted mods: rename .dll.old → .dll so they arrive active
			if isServerMod && strings.HasSuffix(archivePath, ".dll.old") {
				archivePath = strings.TrimSuffix(archivePath, ".old")
			}

			w, err := zw.Create(archivePath)
			if err != nil {
				return err
			}

			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()

			_, err = io.Copy(w, f)
			return err
		})

		if err != nil {
			return nil, fmt.Errorf("failed to walk %s: %w", prefix, err)
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}

	return &buf, nil
}
