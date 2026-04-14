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
// target is "client", "server", or "both" (empty string defaults to "both").
// Dependencies always get target "both".
func Install(paths config.Paths, cfg config.Config, reg *config.Registry, query string, target string) error {
	return InstallVersion(paths, cfg, reg, query, target, "")
}

// InstallVersion works like Install but pins the main mod to a specific version.
// When version is empty it behaves identically to Install (fetches latest).
func InstallVersion(paths config.Paths, cfg config.Config, reg *config.Registry, query string, target string, version string) error {
	if target == "" {
		target = "both"
	}

	var pkg *thunderstore.Package
	var err error
	if version != "" {
		// Resolve owner/name from query, then fetch the pinned version.
		pkg, err = thunderstore.FindPackageByQuery(query)
		if err != nil {
			return err
		}
		fmt.Printf("Resolving %s-%s v%s...\n", pkg.Owner, pkg.Name, version)
		pkg, err = thunderstore.GetPackageVersion(pkg.Owner, pkg.Name, version)
		if err != nil {
			return err
		}
	} else {
		fmt.Printf("Resolving package '%s'...\n", query)
		pkg, err = thunderstore.FindPackageByQuery(query)
		if err != nil {
			return err
		}
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

	installedVersion := ""
	depNames := []string{}
	if len(pkg.Versions) > 0 {
		installedVersion = pkg.Versions[0].VersionNumber
		for _, d := range deps {
			depNames = append(depNames, fmt.Sprintf("%s-%s", d.Owner, d.Name))
		}
	}

	reg.SetMod(profile, config.ModEntry{
		Owner:        pkg.Owner,
		Name:         pkg.Name,
		Version:      installedVersion,
		IsDependency: false,
		Files:        files,
		Dependencies: depNames,
		Target:       target,
	})

	// Auto-disable server-only mods locally (they'd error in the local game)
	if target == "server" {
		modSubdir := fmt.Sprintf("%s-%s", pkg.Owner, pkg.Name)
		for _, dir := range modDirs(paths, profile, modSubdir) {
			renameDLLs(dir, ".dll", ".dll.old")
		}
		mod, _ := reg.GetMod(profile, fullName)
		mod.Disabled = true
		reg.SetMod(profile, mod)
		fmt.Printf("Successfully installed %s v%s (target: server, disabled locally)\n", fullName, installedVersion)
	} else {
		fmt.Printf("Successfully installed %s v%s\n", fullName, installedVersion)
	}
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

// Enable re-enables a disabled mod. Returns an error if the mod is already enabled.
func Enable(paths config.Paths, cfg config.Config, reg *config.Registry, modName string) error {
	profile := cfg.ActiveProfile
	mod, exists := findMod(reg, profile, modName)
	if !exists {
		return fmt.Errorf("mod '%s' not found", modName)
	}
	if !mod.Disabled {
		return fmt.Errorf("mod '%s' is already enabled", modName)
	}

	modSubdir := fmt.Sprintf("%s-%s", mod.Owner, mod.Name)
	for _, dir := range modDirs(paths, profile, modSubdir) {
		if err := renameDLLs(dir, ".dll.old", ".dll"); err != nil {
			return err
		}
	}
	mod.Disabled = false
	reg.SetMod(profile, mod)
	return nil
}

// Disable disables an enabled mod. Returns an error if the mod is already disabled.
func Disable(paths config.Paths, cfg config.Config, reg *config.Registry, modName string) error {
	profile := cfg.ActiveProfile
	mod, exists := findMod(reg, profile, modName)
	if !exists {
		return fmt.Errorf("mod '%s' not found", modName)
	}
	if mod.Disabled {
		return fmt.Errorf("mod '%s' is already disabled", modName)
	}

	modSubdir := fmt.Sprintf("%s-%s", mod.Owner, mod.Name)
	for _, dir := range modDirs(paths, profile, modSubdir) {
		if err := renameDLLs(dir, ".dll", ".dll.old"); err != nil {
			return err
		}
	}
	mod.Disabled = true
	reg.SetMod(profile, mod)
	return nil
}

// Toggle flips a mod between enabled and disabled without printing output.
func Toggle(paths config.Paths, cfg config.Config, reg *config.Registry, modName string) error {
	profile := cfg.ActiveProfile
	mod, exists := findMod(reg, profile, modName)
	if !exists {
		return fmt.Errorf("mod '%s' not found", modName)
	}

	modSubdir := fmt.Sprintf("%s-%s", mod.Owner, mod.Name)
	if mod.Disabled {
		for _, dir := range modDirs(paths, profile, modSubdir) {
			if err := renameDLLs(dir, ".dll.old", ".dll"); err != nil {
				return err
			}
		}
		mod.Disabled = false
	} else {
		for _, dir := range modDirs(paths, profile, modSubdir) {
			if err := renameDLLs(dir, ".dll", ".dll.old"); err != nil {
				return err
			}
		}
		mod.Disabled = true
	}

	reg.SetMod(profile, mod)
	return nil
}

// ToggleLocalMod enables or disables a local (untracked) mod by renaming its DLLs.
func ToggleLocalMod(pluginsDir string, mod config.ModEntry) error {
	oldSuffix, newSuffix := ".dll", ".dll.old"
	if mod.Disabled {
		oldSuffix, newSuffix = ".dll.old", ".dll"
	}

	dirPath := filepath.Join(pluginsDir, mod.Name)
	if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
		return renameDLLs(dirPath, oldSuffix, newSuffix)
	}

	// Loose DLL file
	oldPath := filepath.Join(pluginsDir, mod.Name+oldSuffix)
	newPath := filepath.Join(pluginsDir, mod.Name+newSuffix)
	if _, err := os.Stat(oldPath); err == nil {
		return os.Rename(oldPath, newPath)
	}
	return fmt.Errorf("local mod '%s' not found", mod.Name)
}

// RemoveLocalMod deletes a local (untracked) mod from the plugins directory.
func RemoveLocalMod(pluginsDir string, mod config.ModEntry) error {
	dirPath := filepath.Join(pluginsDir, mod.Name)
	if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
		return os.RemoveAll(dirPath)
	}

	// Loose DLL file
	for _, suffix := range []string{".dll", ".dll.old"} {
		p := filepath.Join(pluginsDir, mod.Name+suffix)
		if _, err := os.Stat(p); err == nil {
			return os.Remove(p)
		}
	}
	return fmt.Errorf("local mod '%s' not found", mod.Name)
}

// modDirs returns all directories where a mod may have files installed.
func modDirs(paths config.Paths, profile, modSubdir string) []string {
	return []string{
		filepath.Join(paths.ProfilePluginsDir(profile), modSubdir),
		filepath.Join(paths.ProfilePatchersDir(profile), modSubdir),
		filepath.Join(paths.ProfileMonomodDir(profile), modSubdir),
		filepath.Join(paths.ProfileCoreDir(profile), modSubdir),
	}
}

// renameDLLs recursively walks a directory and renames files matching oldSuffix to newSuffix.
func renameDLLs(dir, oldSuffix, newSuffix string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), oldSuffix) {
			neu := strings.TrimSuffix(path, oldSuffix) + newSuffix
			if err := os.Rename(path, neu); err != nil {
				return err
			}
		}
		return nil
	})
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

// extractMod extracts a mod zip using r2modman-compatible override folder rules.
// Recognized override folders (plugins/, patchers/, monomod/, core/, config/) route
// files to the corresponding BepInEx subdirectory. Files not in any override folder
// default to plugins/<Author-Name>/. Subdirectory structure is preserved.
func extractMod(paths config.Paths, cfg config.Config, owner, name, zipPath string) ([]string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open zip: %w", err)
	}
	defer r.Close()

	modSubdir := fmt.Sprintf("%s-%s", owner, name)
	profile := cfg.ActiveProfile

	// Override folder prefix → target directory.
	// Author subfolder is baked into dir for all except config.
	type override struct {
		prefix string
		dir    string
	}
	overrides := []override{
		{"plugins/", filepath.Join(paths.ProfilePluginsDir(profile), modSubdir)},
		{"patchers/", filepath.Join(paths.ProfilePatchersDir(profile), modSubdir)},
		{"monomod/", filepath.Join(paths.ProfileMonomodDir(profile), modSubdir)},
		{"core/", filepath.Join(paths.ProfileCoreDir(profile), modSubdir)},
		{"config/", paths.ProfileConfigDir(profile)},
	}

	var files []string

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
			// .mm.dll files auto-route to monomod
			if strings.HasSuffix(strings.ToLower(fname), ".mm.dll") {
				destPath = filepath.Join(paths.ProfileMonomodDir(profile), modSubdir, baseName)
			} else {
				// Default: preserve subdirectory structure in plugins
				destPath = filepath.Join(paths.ProfilePluginsDir(profile), modSubdir, fname)
			}
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

// removeModFilesKeepConfig removes a mod's plugin/patcher/monomod/core files but preserves configs.
func removeModFilesKeepConfig(paths config.Paths, cfg config.Config, mod config.ModEntry) {
	modSubdir := fmt.Sprintf("%s-%s", mod.Owner, mod.Name)
	profile := cfg.ActiveProfile

	for _, dir := range []string{
		paths.ProfilePluginsDir(profile),
		paths.ProfilePatchersDir(profile),
		paths.ProfileMonomodDir(profile),
		paths.ProfileCoreDir(profile),
	} {
		os.RemoveAll(filepath.Join(dir, modSubdir))
	}
}

// Update removes a mod's non-config files, removes its registry entry, and reinstalls the latest version.
// Config files are preserved so user customizations are not lost.
func Update(paths config.Paths, cfg config.Config, reg *config.Registry, modName string) error {
	profile := cfg.ActiveProfile
	mod, exists := findMod(reg, profile, modName)
	if !exists {
		return fmt.Errorf("mod '%s' not found", modName)
	}

	fullName := mod.FullName()
	existingTarget := mod.ResolvedTarget()
	removeModFilesKeepConfig(paths, cfg, mod)
	reg.RemoveMod(profile, fullName)

	return Install(paths, cfg, reg, fullName, existingTarget)
}

// IsLocalPath returns true if the string refers to an existing file or directory.
func IsLocalPath(s string) bool {
	_, err := os.Stat(expandHome(s))
	return err == nil
}

func expandHome(s string) string {
	if s == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	} else if strings.HasPrefix(s, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, s[2:])
		}
	}
	return s
}

// InstallLocal copies a locally-developed mod from srcPath into the active profile's plugins directory.
// If the mod was previously installed from a local path, it is overwritten in place.
func InstallLocal(paths config.Paths, cfg config.Config, reg *config.Registry, srcPath string, target string) error {
	if target == "" {
		target = "both"
	}

	absPath, err := filepath.Abs(expandHome(srcPath))
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("path not found: %w", err)
	}

	profile := cfg.ActiveProfile
	modName := filepath.Base(absPath)
	if !info.IsDir() {
		modName = strings.TrimSuffix(modName, filepath.Ext(modName))
	}

	fullName := fmt.Sprintf("local-%s", modName)

	// If already installed as a local mod, overwrite
	if existing, exists := reg.GetMod(profile, fullName); exists {
		if existing.Owner == "local" {
			removeModFiles(paths, cfg, existing)
			reg.RemoveMod(profile, fullName)
		} else {
			return fmt.Errorf("%s is already installed as a Thunderstore mod — remove it first", fullName)
		}
	}

	destDir := filepath.Join(paths.ProfilePluginsDir(profile), fullName)
	os.MkdirAll(destDir, 0755)

	var files []string
	if info.IsDir() {
		files, err = copyDir(absPath, destDir)
	} else {
		destFile := filepath.Join(destDir, filepath.Base(absPath))
		err = copyFile(absPath, destFile)
		files = []string{destFile}
	}
	if err != nil {
		return fmt.Errorf("failed to copy mod: %w", err)
	}

	reg.SetMod(profile, config.ModEntry{
		Owner:   "local",
		Name:    modName,
		Version: "dev",
		Files:   files,
		Target:  target,
	})

	if target == "server" {
		for _, dir := range modDirs(paths, profile, fullName) {
			renameDLLs(dir, ".dll", ".dll.old")
		}
		mod, _ := reg.GetMod(profile, fullName)
		mod.Disabled = true
		reg.SetMod(profile, mod)
		fmt.Printf("Successfully installed %s from %s (target: server, disabled locally)\n", fullName, absPath)
	} else {
		fmt.Printf("Successfully installed %s from %s\n", fullName, absPath)
	}

	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	os.MkdirAll(filepath.Dir(dst), 0755)
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func copyDir(src, dst string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(src, path)
		destPath := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		if err := copyFile(path, destPath); err != nil {
			return err
		}
		files = append(files, destPath)
		return nil
	})
	return files, err
}

func removeModFiles(paths config.Paths, cfg config.Config, mod config.ModEntry) {
	modSubdir := fmt.Sprintf("%s-%s", mod.Owner, mod.Name)
	profile := cfg.ActiveProfile

	// Remove the mod's subdirectory from all target directories
	for _, dir := range []string{
		paths.ProfilePluginsDir(profile),
		paths.ProfilePatchersDir(profile),
		paths.ProfileMonomodDir(profile),
		paths.ProfileCoreDir(profile),
	} {
		os.RemoveAll(filepath.Join(dir, modSubdir))
	}

	// Remove individually tracked files (config files without author subfolder)
	for _, f := range mod.Files {
		if strings.Contains(f, "/config/") {
			os.Remove(f)
		}
	}
}
