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
func Install(paths config.Paths, zipPath string) error {
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

// PatchRunScript patches run_bepinex.sh for macOS compatibility.
func PatchRunScript(paths config.Paths) error {
	scriptPath := paths.RunBepInExScript()
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("run_bepinex.sh not found: %w", err)
	}

	content := string(data)

	// Set executable_name to valheim.app
	content = replaceScriptVar(content, "executable_name", "valheim.app")

	// Ensure ARCHPREFERENCE is set for x86_64 (Rosetta)
	if !strings.Contains(content, "ARCHPREFERENCE") {
		// Add before the exec line
		content = strings.Replace(content,
			"exec",
			"export ARCHPREFERENCE=\"x86_64\"\nexec",
			1)
	}

	return os.WriteFile(scriptPath, []byte(content), 0755)
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
