package installer

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"mmcli/internal/config"
	"mmcli/internal/thunderstore"
)

// Install resolves a mod query, downloads + installs it and its dependencies.
func Install(paths config.Paths, cfg config.Config, reg *config.Registry, query string) error {
	fmt.Printf("Resolving package '%s'...\n", query)
	pkg, err := thunderstore.FindPackageByQuery(query, paths.CacheDir)
	if err != nil {
		return err
	}

	fullName := fmt.Sprintf("%s-%s", pkg.Owner, pkg.Name)
	profile := cfg.ActiveProfile

	// Check if already installed
	if _, exists := reg.GetMod(profile, fullName); exists {
		fmt.Printf("%s is already installed in profile '%s'\n", fullName, profile)
		return nil
	}

	// Resolve dependencies
	installed := make(map[string]bool)
	for name := range reg.Profiles[profile] {
		installed[name] = true
	}

	deps, err := thunderstore.ResolveDependencies(pkg, installed)
	if err != nil {
		return fmt.Errorf("failed to resolve dependencies: %w", err)
	}

	// Install dependencies first
	for _, dep := range deps {
		depFullName := fmt.Sprintf("%s-%s", dep.Owner, dep.Name)
		if _, exists := reg.GetMod(profile, depFullName); exists {
			continue
		}

		fmt.Printf("  Installing dependency %s...\n", depFullName)
		depPkg, err := thunderstore.GetPackage(dep.Owner, dep.Name)
		if err != nil {
			fmt.Printf("  Warning: could not fetch %s: %v\n", depFullName, err)
			continue
		}

		files, err := downloadAndExtract(paths, cfg, depPkg)
		if err != nil {
			fmt.Printf("  Warning: could not install %s: %v\n", depFullName, err)
			continue
		}

		version := dep.Version
		if len(depPkg.Versions) > 0 {
			version = depPkg.Versions[0].VersionNumber
		}

		reg.SetMod(profile, config.ModEntry{
			Owner:        dep.Owner,
			Name:         dep.Name,
			Version:      version,
			IsDependency: true,
			Files:        files,
		})
	}

	// Install the main mod
	fmt.Printf("Installing %s...\n", fullName)
	files, err := downloadAndExtract(paths, cfg, pkg)
	if err != nil {
		return fmt.Errorf("failed to install %s: %w", fullName, err)
	}

	version := ""
	depNames := []string{}
	if len(pkg.Versions) > 0 {
		version = pkg.Versions[0].VersionNumber
		for _, d := range deps {
			depNames = append(depNames, fmt.Sprintf("%s-%s", d.Owner, d.Name))
		}
	}

	reg.SetMod(profile, config.ModEntry{
		Owner:        pkg.Owner,
		Name:         pkg.Name,
		Version:      version,
		IsDependency: false,
		Files:        files,
		Dependencies: depNames,
	})

	fmt.Printf("Successfully installed %s v%s\n", fullName, version)
	return nil
}

// Remove removes a mod and any orphaned dependencies.
func Remove(paths config.Paths, cfg config.Config, reg *config.Registry, modName string) error {
	profile := cfg.ActiveProfile

	// Find the mod in registry
	mod, exists := findMod(reg, profile, modName)
	if !exists {
		return fmt.Errorf("mod '%s' is not installed in profile '%s'", modName, profile)
	}

	fullName := mod.FullName()

	// Remove the mod's files
	fmt.Printf("Removing %s...\n", fullName)
	removeModFiles(paths, cfg, mod)
	reg.RemoveMod(profile, fullName)

	// Find and remove orphaned dependencies
	for _, depName := range mod.Dependencies {
		dep, depExists := reg.GetMod(profile, depName)
		if !depExists {
			continue
		}
		if !dep.IsDependency {
			continue
		}
		// Check if any other mod still depends on this
		if !reg.IsDependent(profile, depName) {
			fmt.Printf("  Removing orphaned dependency %s...\n", depName)
			removeModFiles(paths, cfg, dep)
			reg.RemoveMod(profile, depName)
		}
	}

	fmt.Printf("Successfully removed %s\n", fullName)
	return nil
}

func findMod(reg *config.Registry, profile, query string) (config.ModEntry, bool) {
	// Try exact full name match first
	if mod, ok := reg.GetMod(profile, query); ok {
		return mod, true
	}
	// Try matching just the mod name
	queryLower := strings.ToLower(query)
	for _, mod := range reg.ListMods(profile) {
		if strings.ToLower(mod.Name) == queryLower {
			return mod, true
		}
		if strings.ToLower(mod.FullName()) == queryLower {
			return mod, true
		}
	}
	return config.ModEntry{}, false
}

func downloadAndExtract(paths config.Paths, cfg config.Config, pkg *thunderstore.Package) ([]string, error) {
	if len(pkg.Versions) == 0 {
		return nil, fmt.Errorf("package %s has no versions", pkg.FullName)
	}

	ver := pkg.Versions[0]
	zipPath, err := downloadMod(paths, pkg.Owner, pkg.Name, ver.VersionNumber, ver.DownloadURL)
	if err != nil {
		return nil, err
	}

	return extractMod(paths, cfg, pkg.Owner, pkg.Name, zipPath)
}

func downloadMod(paths config.Paths, owner, name, version, downloadURL string) (string, error) {
	os.MkdirAll(paths.CacheDir, 0755)
	filename := fmt.Sprintf("%s-%s-%s.zip", owner, name, version)
	zipPath := filepath.Join(paths.CacheDir, filename)

	// Skip if cached
	if _, err := os.Stat(zipPath); err == nil {
		return zipPath, nil
	}

	resp, err := http.Get(downloadURL)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(zipPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(zipPath)
		return "", err
	}

	return zipPath, nil
}

// extractMod extracts a mod zip into the active profile's plugins and config directories.
// DLLs go into plugins/<Owner>-<Name>/, configs go into config/.
func extractMod(paths config.Paths, cfg config.Config, owner, name, zipPath string) ([]string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open zip: %w", err)
	}
	defer r.Close()

	modSubdir := fmt.Sprintf("%s-%s", owner, name)
	pluginsDir := filepath.Join(paths.ProfilePluginsDir(cfg.ActiveProfile), modSubdir)
	configDir := paths.ProfileConfigDir(cfg.ActiveProfile)
	os.MkdirAll(pluginsDir, 0755)

	var files []string

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}

		fname := f.Name
		baseName := filepath.Base(fname)
		lowerName := strings.ToLower(baseName)

		// Skip metadata
		if baseName == "icon.png" || baseName == "manifest.json" || baseName == "README.md" || baseName == "CHANGELOG.md" {
			continue
		}

		var destPath string

		// Config files go to config dir
		if strings.HasSuffix(lowerName, ".cfg") {
			destPath = filepath.Join(configDir, baseName)
		} else if strings.Contains(strings.ToLower(fname), "config/") || strings.Contains(strings.ToLower(fname), "config\\") {
			// Files inside a config/ folder in the zip
			destPath = filepath.Join(configDir, baseName)
		} else {
			// Everything else goes to the mod's plugin subfolder
			destPath = filepath.Join(pluginsDir, baseName)
		}

		if err := extractZipFile(f, destPath); err != nil {
			return nil, fmt.Errorf("failed to extract %s: %w", fname, err)
		}
		files = append(files, destPath)
	}

	return files, nil
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

func removeModFiles(paths config.Paths, cfg config.Config, mod config.ModEntry) {
	// Remove the mod's plugin subfolder
	modSubdir := fmt.Sprintf("%s-%s", mod.Owner, mod.Name)
	pluginDir := filepath.Join(paths.ProfilePluginsDir(cfg.ActiveProfile), modSubdir)
	os.RemoveAll(pluginDir)

	// Remove individual tracked files that might be in config/
	for _, f := range mod.Files {
		// Only remove files in the config dir (plugin files are handled by RemoveAll above)
		if strings.Contains(f, "/config/") {
			os.Remove(f)
		}
	}
}
