package profile

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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

	return nil
}
