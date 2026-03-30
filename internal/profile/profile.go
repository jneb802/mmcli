package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"mmcli/internal/config"
)

// Create creates a new profile directory with plugins/ and config/ subdirectories.
// If a source profile exists, it copies BepInEx.cfg to the new profile.
func Create(paths config.Paths, name string) error {
	profileDir := paths.ProfileDir(name)
	if _, err := os.Stat(profileDir); err == nil {
		return fmt.Errorf("profile '%s' already exists", name)
	}

	if err := os.MkdirAll(paths.ProfilePluginsDir(name), 0755); err != nil {
		return fmt.Errorf("failed to create plugins dir: %w", err)
	}
	if err := os.MkdirAll(paths.ProfileConfigDir(name), 0755); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}
	if err := os.MkdirAll(paths.ProfilePatchersDir(name), 0755); err != nil {
		return fmt.Errorf("failed to create patchers dir: %w", err)
	}
	if err := os.MkdirAll(paths.ProfileMonomodDir(name), 0755); err != nil {
		return fmt.Errorf("failed to create monomod dir: %w", err)
	}
	if runtime.GOOS == "windows" {
		if err := os.MkdirAll(paths.ProfileCoreDir(name), 0755); err != nil {
			return fmt.Errorf("failed to create core dir: %w", err)
		}
	}

	srcCfg := filepath.Join(paths.BepInExConfigDir(), "BepInEx.cfg")
	templateProfile, hasTemplate := findTemplateProfile(paths, name)
	if runtime.GOOS == "windows" {
		srcCfg = filepath.Join(paths.ProfileConfigDir(templateProfile), "BepInEx.cfg")
	}
	if data, err := os.ReadFile(srcCfg); err == nil {
		dstCfg := filepath.Join(paths.ProfileConfigDir(name), "BepInEx.cfg")
		_ = os.WriteFile(dstCfg, data, 0644)
	}

	if runtime.GOOS == "windows" && hasTemplate {
		if err := copyDirContents(paths.ProfileCoreDir(templateProfile), paths.ProfileCoreDir(name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to copy core dir: %w", err)
		}
	}

	return nil
}

// List returns the names of all profiles.
func List(paths config.Paths) ([]string, error) {
	entries, err := os.ReadDir(paths.ProfilesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// Switch changes the active profile and activates its BepInEx paths.
func Switch(paths config.Paths, cfg *config.Config, name string) error {
	profileDir := paths.ProfileDir(name)
	if _, err := os.Stat(profileDir); os.IsNotExist(err) {
		return fmt.Errorf("profile '%s' does not exist", name)
	}

	if err := Activate(paths, name); err != nil {
		return fmt.Errorf("failed to activate profile: %w", err)
	}

	cfg.ActiveProfile = name
	return nil
}

// Delete removes a profile. Refuses to delete the active profile.
func Delete(paths config.Paths, cfg config.Config, name string) error {
	if cfg.ActiveProfile == name {
		return fmt.Errorf("cannot delete active profile '%s'. Switch to another profile first", name)
	}
	profileDir := paths.ProfileDir(name)
	if _, err := os.Stat(profileDir); os.IsNotExist(err) {
		return fmt.Errorf("profile '%s' does not exist", name)
	}
	return os.RemoveAll(profileDir)
}

// Activate makes the given profile's directories the active BepInEx targets.
func Activate(paths config.Paths, name string) error {
	if runtime.GOOS == "windows" {
		return writeDoorstopConfig(paths, name)
	}
	return ActivateSymlinks(paths, name)
}

// ActivateSymlinks makes the given profile's directories the active symlink targets.
func ActivateSymlinks(paths config.Paths, name string) error {
	// Ensure all profile target directories exist (handles upgrades from older mmcli)
	for _, dir := range []string{
		paths.ProfilePluginsDir(name),
		paths.ProfileConfigDir(name),
		paths.ProfilePatchersDir(name),
		paths.ProfileMonomodDir(name),
	} {
		os.MkdirAll(dir, 0755)
	}

	symlinks := []struct {
		bepDir     string
		profileDir string
		label      string
	}{
		{paths.BepInExPluginsDir(), paths.ProfilePluginsDir(name), "plugins"},
		{paths.BepInExConfigDir(), paths.ProfileConfigDir(name), "config"},
		{paths.BepInExPatchersDir(), paths.ProfilePatchersDir(name), "patchers"},
		{paths.BepInExMonomodDir(), paths.ProfileMonomodDir(name), "monomod"},
	}

	for _, s := range symlinks {
		if err := replaceWithSymlink(s.bepDir, s.profileDir, name, paths); err != nil {
			return fmt.Errorf("failed to symlink %s: %w", s.label, err)
		}
	}
	return nil
}

// replaceWithSymlink replaces a directory (or existing symlink) at linkPath with a symlink to target.
func replaceWithSymlink(linkPath, target, profileName string, paths config.Paths) error {
	info, err := os.Lstat(linkPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			// It's already a symlink — just remove it
			if err := os.Remove(linkPath); err != nil {
				return fmt.Errorf("failed to remove existing symlink: %w", err)
			}
		} else if info.IsDir() {
			// It's a real directory — move contents into the profile, then remove
			if err := migrateContents(linkPath, target); err != nil {
				return fmt.Errorf("failed to migrate contents: %w", err)
			}
			if err := os.RemoveAll(linkPath); err != nil {
				return fmt.Errorf("failed to remove directory: %w", err)
			}
		}
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(linkPath), 0755); err != nil {
		return err
	}

	return os.Symlink(target, linkPath)
}

// migrateContents moves all files from src into dst.
func migrateContents(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())
		// Skip if already exists in destination
		if _, err := os.Stat(dstPath); err == nil {
			continue
		}
		if err := os.Rename(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

func writeDoorstopConfig(paths config.Paths, name string) error {
	preloader := filepath.Join(paths.ProfileCoreDir(name), "BepInEx.Preloader.dll")
	if _, err := os.Stat(preloader); err != nil {
		return fmt.Errorf("BepInEx preloader not found at %s", preloader)
	}

	content := fmt.Sprintf(`[UnityDoorstop]
enabled=true
targetAssembly=%s
redirectOutputLog=false
ignoreDisableSwitch=false
dllSearchPathOverride=unstripped_corlib
`, preloader)

	return os.WriteFile(filepath.Join(paths.ValheimDir, "doorstop_config.ini"), []byte(content), 0644)
}

func copyDirContents(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0755); err != nil {
				return err
			}
			if err := copyDirContents(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, 0644); err != nil {
			return err
		}
	}
	return nil
}

func findTemplateProfile(paths config.Paths, exclude string) (string, bool) {
	profiles, err := List(paths)
	if err != nil {
		return "", false
	}
	for _, name := range profiles {
		if name == exclude {
			continue
		}
		if _, err := os.Stat(filepath.Join(paths.ProfileCoreDir(name), "BepInEx.Preloader.dll")); err == nil {
			return name, true
		}
	}
	return "", false
}
