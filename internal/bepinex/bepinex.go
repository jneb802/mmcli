package bepinex

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"mmcli/internal/config"
)

const bepinexPackage = "denikson/BepInExPack_Valheim"

type tsVersion struct {
	VersionNumber string `json:"version_number"`
	DownloadURL   string `json:"download_url"`
}

type tsPackage struct {
	Versions []tsVersion `json:"versions"`
}

// LatestVersion fetches the latest BepInExPack_Valheim version info.
func LatestVersion() (version string, downloadURL string, err error) {
	url := fmt.Sprintf("https://thunderstore.io/api/experimental/package/%s/", bepinexPackage)
	resp, err := http.Get(url)
	if err != nil {
		return "", "", fmt.Errorf("failed to query Thunderstore: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("Thunderstore returned HTTP %d", resp.StatusCode)
	}

	var pkg struct {
		Latest struct {
			VersionNumber string `json:"version_number"`
			DownloadURL   string `json:"download_url"`
		} `json:"latest"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pkg); err != nil {
		return "", "", fmt.Errorf("failed to parse response: %w", err)
	}

	return pkg.Latest.VersionNumber, pkg.Latest.DownloadURL, nil
}

// Download downloads the BepInEx zip to the cache directory.
func Download(paths config.Paths, downloadURL, version string) (string, error) {
	os.MkdirAll(paths.CacheDir, 0755)
	zipPath := filepath.Join(paths.CacheDir, fmt.Sprintf("BepInExPack_Valheim-%s.zip", version))

	// Skip download if already cached
	if _, err := os.Stat(zipPath); err == nil {
		return zipPath, nil
	}

	resp, err := http.Get(downloadURL)
	if err != nil {
		return "", fmt.Errorf("failed to download BepInEx: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download failed with HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(zipPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(zipPath)
		return "", fmt.Errorf("download interrupted: %w", err)
	}

	return zipPath, nil
}

// Install extracts the BepInEx zip into the Valheim directory.
// Strips the "BepInExPack_Valheim/" prefix and skips metadata files.
// Removes any dangling symlinks from a previous mmcli install first.
func Install(paths config.Paths, zipPath string) error {
	// Clean up dangling symlinks left from previous profile symlinks
	removeDanglingSymlinks(paths.BepInExDir())

	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip: %w", err)
	}
	defer r.Close()

	skipFiles := map[string]bool{
		"icon.png":      true,
		"manifest.json": true,
		"README.md":     true,
		"CHANGELOG.md":  true,
	}

	for _, f := range r.File {
		name := f.Name

		// Strip the BepInExPack_Valheim/ prefix
		if idx := strings.Index(name, "/"); idx >= 0 {
			prefix := name[:idx]
			if strings.HasPrefix(prefix, "BepInExPack") {
				name = name[idx+1:]
			}
		}

		if name == "" {
			continue
		}

		// Skip metadata files
		baseName := filepath.Base(name)
		if skipFiles[baseName] {
			continue
		}

		destPath := filepath.Join(paths.ValheimDir, name)

		if f.FileInfo().IsDir() {
			os.MkdirAll(destPath, 0755)
			continue
		}

		if err := extractFile(f, destPath); err != nil {
			return fmt.Errorf("failed to extract %s: %w", name, err)
		}
	}

	return nil
}

// PatchRunScript patches run_bepinex.sh for macOS Apple Silicon compatibility.
// The stock BepInExPack script fails on ARM Macs for two reasons:
//  1. The arch detection case block rejects non-x86/x64 binaries
//  2. The launch uses bare `exec` instead of forcing x86_64 via Rosetta
//
// This function applies the same patches as a known-working macOS install.
func PatchRunScript(paths config.Paths) error {
	scriptPath := paths.RunBepInExScript()
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("run_bepinex.sh not found: %w", err)
	}

	content := string(data)

	// Set executable_name to valheim.app
	content = replaceScriptVar(content, "executable_name", "valheim.app")

	// Comment out the arch detection case block — it rejects Apple Silicon binaries.
	// The block starts with `case "${file_out}" in` and ends with `esac`.
	content = commentOutArchCheck(content)

	// Replace the stock `exec "$executable_path" $rest_args` launch with an
	// `arch -x86_64 zsh` wrapper that re-exports the doorstop env vars and
	// forces Rosetta. This is required because libdoorstop has no ARM build.
	content = replaceExecWithArchWrapper(content)

	return os.WriteFile(scriptPath, []byte(content), 0755)
}

// commentOutArchCheck comments out the `case "${file_out}" in ... esac` block
// that checks binary architecture, since it rejects Apple Silicon executables.
func commentOutArchCheck(content string) string {
	lines := strings.Split(content, "\n")
	inBlock := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Already commented out — nothing to do
		if strings.HasPrefix(trimmed, "# case \"${file_out}\"") {
			return content
		}

		if strings.HasPrefix(trimmed, "case \"${file_out}\"") {
			inBlock = true
		}
		if inBlock && !strings.HasPrefix(trimmed, "#") {
			lines[i] = "# " + line
		}
		if inBlock && trimmed == "esac" {
			lines[i] = "# " + line
			break
		}
	}
	return strings.Join(lines, "\n")
}

// replaceExecWithArchWrapper replaces the stock exec launch line with an
// `arch -x86_64 zsh -c` wrapper that re-exports doorstop environment variables
// and forces the game to run under Rosetta 2.
func replaceExecWithArchWrapper(content string) string {
	// If already patched (contains "arch -x86_64 zsh"), skip
	if strings.Contains(content, "arch -x86_64 zsh") {
		return content
	}

	wrapper := `# Wrap the launch command in arch -x86_64 (libdoorstop has no Apple Silicon build)
current_path=$(pwd)
exports="export LD_LIBRARY_PATH=\"$LD_LIBRARY_PATH\";export LD_PRELOAD=$LD_PRELOAD;export DYLD_LIBRARY_PATH=\"$DYLD_LIBRARY_PATH\";export DYLD_INSERT_LIBRARIES=\"$DYLD_INSERT_LIBRARIES\""
cdir="cd \"$current_path\""
exec="\"${executable_path}\""
a="\"$rest_args\""
launch="arch;$cdir;pwd;$exports;$exec $a"
echo "Wrapping x86_64 Launch Command: $launch"
arch -x86_64 zsh -c "$launch"`

	// Replace the stock exec line: `exec "$executable_path" $rest_args`
	lines := strings.Split(content, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Match both active and shellcheck-disabled exec lines
		if strings.HasPrefix(trimmed, "exec \"$executable_path\"") ||
			strings.HasPrefix(trimmed, "exec \"${executable_path}\"") {
			// Comment out the stock exec and append wrapper
			result = append(result, "# "+line)
			result = append(result, "")
			result = append(result, wrapper)
			continue
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

// MakeExecutable sets executable permissions on required files.
func MakeExecutable(paths config.Paths) error {
	files := []string{
		paths.RunBepInExScript(),
		filepath.Join(paths.ValheimDir, "libdoorstop.dylib"),
	}
	for _, f := range files {
		if _, err := os.Stat(f); err == nil {
			if err := os.Chmod(f, 0755); err != nil {
				return fmt.Errorf("failed to chmod %s: %w", filepath.Base(f), err)
			}
		}
	}
	return nil
}

func extractFile(f *zip.File, destPath string) error {
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

// removeDanglingSymlinks removes symlinks in a directory that point to non-existent targets.
func removeDanglingSymlinks(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if _, err := os.Stat(path); err != nil {
				os.Remove(path)
			}
		}
	}
}

func replaceScriptVar(content, varName, value string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, varName+"=") {
			lines[i] = fmt.Sprintf(`%s="%s"`, varName, value)
		}
	}
	return strings.Join(lines, "\n")
}
