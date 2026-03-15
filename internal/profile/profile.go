package profile

import (
	"fmt"
	"os"
	"path/filepath"

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

	// Copy BepInEx.cfg from BepInEx/config if it exists (follow symlinks)
	srcCfg := filepath.Join(paths.BepInExConfigDir(), "BepInEx.cfg")
	if data, err := os.ReadFile(srcCfg); err == nil {
		dstCfg := filepath.Join(paths.ProfileConfigDir(name), "BepInEx.cfg")
		os.WriteFile(dstCfg, data, 0644)
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

// Switch changes the active profile and re-points symlinks.
func Switch(paths config.Paths, cfg *config.Config, name string) error {
	profileDir := paths.ProfileDir(name)
	if _, err := os.Stat(profileDir); os.IsNotExist(err) {
		return fmt.Errorf("profile '%s' does not exist", name)
	}

	if err := ActivateSymlinks(paths, name); err != nil {
		return fmt.Errorf("failed to activate symlinks: %w", err)
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

// ActivateSymlinks makes the given profile's directories the active symlink targets.
func ActivateSymlinks(paths config.Paths, name string) error {
	profilePlugins := paths.ProfilePluginsDir(name)
	profileConfig := paths.ProfileConfigDir(name)
	bepPlugins := paths.BepInExPluginsDir()
	bepConfig := paths.BepInExConfigDir()

	if err := replaceWithSymlink(bepPlugins, profilePlugins, name, paths); err != nil {
		return fmt.Errorf("failed to symlink plugins: %w", err)
	}
	if err := replaceWithSymlink(bepConfig, profileConfig, name, paths); err != nil {
		return fmt.Errorf("failed to symlink config: %w", err)
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
