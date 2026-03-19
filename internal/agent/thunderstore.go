package agent

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"mmcli/internal/agentapi"
)

const thunderstoreDownloadURL = "https://thunderstore.io/package/download/%s/%s/%s/"

// downloadModZip downloads a mod from Thunderstore into the cache directory.
// Returns the path to the cached zip. Skips download if already cached.
func downloadModZip(cacheDir, owner, name, version string) (zipPath string, cached bool, err error) {
	os.MkdirAll(cacheDir, 0755)
	filename := fmt.Sprintf("%s-%s-%s.zip", owner, name, version)
	zipPath = filepath.Join(cacheDir, filename)

	// Skip if cached
	if _, err := os.Stat(zipPath); err == nil {
		return zipPath, true, nil
	}

	url := fmt.Sprintf(thunderstoreDownloadURL, owner, name, version)
	resp, err := http.Get(url)
	if err != nil {
		return "", false, fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", false, fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(zipPath)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(zipPath)
		return "", false, err
	}

	return zipPath, false, nil
}

// extractModZip extracts a Thunderstore mod zip into the BepInEx directory.
// Uses r2modman-compatible override folder rules. Skips config/ to match
// current push behavior (mods generate defaults on first run).
func extractModZip(zipPath, bepDir, owner, name string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip: %w", err)
	}
	defer r.Close()

	modSubdir := fmt.Sprintf("%s-%s", owner, name)

	type override struct {
		prefix string
		dir    string
	}
	overrides := []override{
		{"plugins/", filepath.Join(bepDir, "plugins", modSubdir)},
		{"patchers/", filepath.Join(bepDir, "patchers", modSubdir)},
		{"monomod/", filepath.Join(bepDir, "monomod", modSubdir)},
		{"core/", filepath.Join(bepDir, "core", modSubdir)},
		// config/ intentionally omitted — mods generate defaults on first run
	}

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}

		fname := filepath.ToSlash(f.Name)
		baseName := filepath.Base(fname)

		// Skip metadata
		if baseName == "icon.png" || baseName == "manifest.json" || baseName == "README.md" || baseName == "CHANGELOG.md" {
			continue
		}

		// Skip config files
		if len(fname) > len("config/") && strings.EqualFold(fname[:len("config/")], "config/") {
			continue
		}

		var destPath string
		matched := false

		for _, ov := range overrides {
			if len(fname) > len(ov.prefix) && strings.EqualFold(fname[:len(ov.prefix)], ov.prefix) {
				relPath := fname[len(ov.prefix):]
				destPath = filepath.Join(ov.dir, relPath)
				matched = true
				break
			}
		}

		if !matched {
			if strings.HasSuffix(strings.ToLower(fname), ".mm.dll") {
				destPath = filepath.Join(bepDir, "monomod", modSubdir, baseName)
			} else {
				destPath = filepath.Join(bepDir, "plugins", modSubdir, fname)
			}
		}

		if err := extractZipFile(f, destPath); err != nil {
			return fmt.Errorf("failed to extract %s: %w", fname, err)
		}
	}

	return nil
}

func extractZipFile(f *zip.File, destPath string) error {
	os.MkdirAll(filepath.Dir(destPath), 0755)

	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc)
	return err
}

// removeModDirs removes a mod's directories from all BepInEx locations.
func removeModDirs(bepDir, dirName string) {
	for _, sub := range []string{"plugins", "patchers", "monomod", "core"} {
		os.RemoveAll(filepath.Join(bepDir, sub, dirName))
	}
}

// setupAnticheat rebuilds the AzuAntiCheat whitelist/greylist folders by
// copying DLLs from the mod plugin directories of classified mods.
func setupAnticheat(bepDir string, mods []agentapi.ManifestMod) error {
	// Clean existing anticheat folders
	os.RemoveAll(filepath.Join(bepDir, "config", "AzuAntiCheat_Whitelist"))
	os.RemoveAll(filepath.Join(bepDir, "config", "AzuAntiCheat_Greylist"))

	type classifiedMod struct {
		dirName string
		folder  string
	}

	var classified []classifiedMod
	for _, mod := range mods {
		if mod.Anticheat == "" {
			continue
		}
		folder := filepath.Join(bepDir, "config", "AzuAntiCheat_Whitelist")
		if mod.Anticheat == "greylist" {
			folder = filepath.Join(bepDir, "config", "AzuAntiCheat_Greylist")
		}
		classified = append(classified, classifiedMod{
			dirName: mod.DirName,
			folder:  folder,
		})
	}

	if len(classified) == 0 {
		return nil
	}

	// Create directories
	dirs := map[string]bool{}
	for _, cm := range classified {
		dirs[cm.folder] = true
	}
	for dir := range dirs {
		os.MkdirAll(dir, 0755)
	}

	// Copy DLLs from each classified mod's plugins dir
	for _, cm := range classified {
		modDir := filepath.Join(bepDir, "plugins", cm.dirName)
		err := filepath.Walk(modDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(strings.ToLower(info.Name()), ".dll") {
				return nil
			}

			destPath := filepath.Join(cm.folder, info.Name())
			return copyFile(path, destPath)
		})
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("anticheat: failed to walk %s: %w", cm.dirName, err)
		}
	}

	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// agentCacheDir returns the cache directory for the agent.
func agentCacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/mmcli-agent-cache"
	}
	return filepath.Join(home, ".cache", "mmcli-agent")
}
