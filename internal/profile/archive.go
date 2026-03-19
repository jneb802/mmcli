package profile

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mmcli/internal/agentapi"
	"mmcli/internal/config"
)

// BuildProfileArchive creates a tar.gz of the profile's mod directories.
// Paths in the archive are relative to BepInEx/ (e.g., plugins/ModName/mod.dll).
// Excludes client-only mods. Skips config/ entirely (mods generate defaults on first run).
// For server-targeted mods (locally disabled), renames .dll.old → .dll in the archive
// so they arrive active on the server.
func BuildProfileArchive(w io.Writer, paths config.Paths, profileName string, reg config.Registry) error {
	gw := gzip.NewWriter(w)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	// Build exclude set: client-only mod directory names
	clientMods := make(map[string]bool)
	// Build server mod set: for .dll.old → .dll renaming
	serverMods := make(map[string]bool)
	for _, mod := range reg.ListMods(profileName) {
		dirName := mod.FullName()
		switch mod.ResolvedTarget() {
		case "client":
			clientMods[dirName] = true
		case "server":
			serverMods[dirName] = true
		}
	}

	// Write push manifest as first tar entry
	if err := writeManifest(tw, profileName, reg, clientMods); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	// Only push plugins, patchers, monomod — skip config
	dirs := map[string]string{
		"plugins":  paths.ProfilePluginsDir(profileName),
		"patchers": paths.ProfilePatchersDir(profileName),
		"monomod":  paths.ProfileMonomodDir(profileName),
	}

	for prefix, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // skip errors
			}

			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return nil
			}

			// Check if this is inside a client-only mod directory — skip it
			topDir := strings.SplitN(rel, string(filepath.Separator), 2)[0]
			if clientMods[topDir] {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			// Build archive path: prefix/relative (e.g., plugins/ModName/mod.dll)
			archivePath := filepath.Join(prefix, rel)

			if archivePath == prefix {
				return tw.WriteHeader(&tar.Header{
					Name:     archivePath + "/",
					Typeflag: tar.TypeDir,
					Mode:     0755,
				})
			}

			if info.IsDir() {
				return tw.WriteHeader(&tar.Header{
					Name:     archivePath + "/",
					Typeflag: tar.TypeDir,
					Mode:     0755,
				})
			}

			// For server-targeted mods: rename .dll.old → .dll in the archive
			// so the mod arrives active on the server
			if serverMods[topDir] && strings.HasSuffix(archivePath, ".dll.old") {
				archivePath = strings.TrimSuffix(archivePath, ".old")
			}

			header := &tar.Header{
				Name:     archivePath,
				Size:     info.Size(),
				Mode:     int64(info.Mode()),
				Typeflag: tar.TypeReg,
			}
			if err := tw.WriteHeader(header); err != nil {
				return err
			}

			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		})

		if err != nil {
			return fmt.Errorf("failed to archive %s: %w", prefix, err)
		}
	}

	// Add AzuAntiCheat whitelist/greylist DLL entries
	if err := addAnticheatEntries(tw, paths.ProfilePluginsDir(profileName), reg, profileName, serverMods); err != nil {
		return fmt.Errorf("failed to add anticheat entries: %w", err)
	}

	return nil
}

// addAnticheatEntries collects DLLs from classified mods and writes them into
// config/AzuAntiCheat_Whitelist/ and config/AzuAntiCheat_Greylist/ in the tar.
// These folders are used by AzuAntiCheat for hash comparison only (not loaded).
func addAnticheatEntries(tw *tar.Writer, pluginsDir string, reg config.Registry,
	profileName string, serverMods map[string]bool) error {

	type classifiedMod struct {
		dirName string
		folder  string // "config/AzuAntiCheat_Whitelist" or "config/AzuAntiCheat_Greylist"
	}

	var classified []classifiedMod
	for _, mod := range reg.ListMods(profileName) {
		if mod.ResolvedTarget() == "client" || mod.Anticheat == "" {
			continue
		}
		folder := "config/AzuAntiCheat_Whitelist"
		if mod.Anticheat == "greylist" {
			folder = "config/AzuAntiCheat_Greylist"
		}
		classified = append(classified, classifiedMod{
			dirName: mod.FullName(),
			folder:  folder,
		})
	}

	if len(classified) == 0 {
		return nil
	}

	// Write directory entries
	dirs := map[string]bool{}
	for _, cm := range classified {
		dirs[cm.folder] = true
	}
	for dir := range dirs {
		if err := tw.WriteHeader(&tar.Header{
			Name:     dir + "/",
			Typeflag: tar.TypeDir,
			Mode:     0755,
		}); err != nil {
			return err
		}
	}

	// Collect DLLs from each classified mod
	seen := map[string]bool{} // track archive paths to warn on collisions
	for _, cm := range classified {
		modDir := filepath.Join(pluginsDir, cm.dirName)
		isServerMod := serverMods[cm.dirName]

		err := filepath.Walk(modDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			lower := strings.ToLower(info.Name())
			isDLL := strings.HasSuffix(lower, ".dll")
			isDisabledDLL := strings.HasSuffix(lower, ".dll.old")
			if !isDLL && !isDisabledDLL {
				return nil
			}

			// Skip genuinely disabled DLLs (not server-targeted)
			if isDisabledDLL && !isServerMod {
				return nil
			}

			baseName := info.Name()
			if isDisabledDLL && isServerMod {
				baseName = strings.TrimSuffix(baseName, ".old")
			}

			archivePath := cm.folder + "/" + baseName

			if seen[archivePath] {
				fmt.Fprintf(os.Stderr, "Warning: anticheat DLL collision: %s (overwritten)\n", archivePath)
			}
			seen[archivePath] = true

			header := &tar.Header{
				Name:     archivePath,
				Size:     info.Size(),
				Mode:     int64(info.Mode()),
				Typeflag: tar.TypeReg,
			}
			if err := tw.WriteHeader(header); err != nil {
				return err
			}

			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		})

		if err != nil {
			return fmt.Errorf("anticheat: failed to walk %s: %w", cm.dirName, err)
		}
	}

	return nil
}

// BuildManifest creates a PushManifest from the registry, excluding client-only mods.
func BuildManifest(profileName string, reg config.Registry) agentapi.PushManifest {
	var mods []agentapi.ManifestMod
	for _, mod := range reg.ListMods(profileName) {
		if mod.ResolvedTarget() == "client" {
			continue
		}
		mods = append(mods, agentapi.ManifestMod{
			DirName:   mod.FullName(),
			Owner:     mod.Owner,
			Name:      mod.Name,
			Version:   mod.Version,
			Target:    mod.ResolvedTarget(),
			Anticheat: mod.Anticheat,
		})
	}
	return agentapi.PushManifest{
		PushedAt: time.Now().UTC().Format(time.RFC3339),
		Profile:  profileName,
		Mods:     mods,
	}
}

// writeManifest serializes the manifest and writes it as a JSON tar entry.
func writeManifest(tw *tar.Writer, profileName string, reg config.Registry, clientMods map[string]bool) error {
	manifest := BuildManifest(profileName, reg)

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}

	header := &tar.Header{
		Name:     agentapi.ManifestFileName,
		Size:     int64(len(data)),
		Mode:     0644,
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	_, err = tw.Write(data)
	return err
}
