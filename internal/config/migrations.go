package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mmcli/internal/games"
)

// MigrationResult describes what RunMigrations actually did, so the
// caller can decide whether to re-activate the active profile (to fix
// Mac symlinks or Windows doorstop_config.ini that point at pre-migration
// paths).
type MigrationResult struct {
	ConfigChanged   bool
	RegistryChanged bool
	DirsMovedNames  []string // profile names that were moved into <game>/
}

// AnyChange reports whether any migration step actually mutated state.
func (r MigrationResult) AnyChange() bool {
	return r.ConfigChanged || r.RegistryChanged || len(r.DirsMovedNames) > 0
}

const (
	migrationSentinelName = ".migrating"
	migrationBackupDir    = ".pre-multigame-backup"
)

// RunMigrations performs all pending on-disk migrations to bring an
// existing mmcli setup up to the current shape. Safe when nothing needs
// migrating; supports retry after interruption (a leftover sentinel
// triggers a retry on the next call). Migrations are: legacy
// Config.ValheimPath → Config.GameInstalls["valheim"]; legacy
// top-level Registry.Profiles/Settings → Registry.Games["valheim"];
// legacy profile dirs at <AllProfilesRoot>/<name>/ → <AllProfilesRoot>/valheim/<name>/.
//
// On the first migration run, config.json and registry.json are copied
// into <ConfigDir>/.pre-multigame-backup/ before any mutation, so a
// failed migration can be recovered by restoring those files manually.
func RunMigrations(p Paths) (MigrationResult, error) {
	var result MigrationResult

	if p.ConfigDir == "" {
		return result, fmt.Errorf("RunMigrations: Paths.ConfigDir is empty")
	}

	sentinelPath := filepath.Join(p.ConfigDir, migrationSentinelName)
	sentinelExists := false
	if _, err := os.Stat(sentinelPath); err == nil {
		sentinelExists = true
	}

	cfg, cfgExists, err := loadConfigRaw(p.ConfigFile)
	if err != nil {
		return result, err
	}
	reg, regExists, err := loadRegistryRaw(p.RegistryFile)
	if err != nil {
		return result, err
	}

	legacyDirs, err := detectLegacyProfileDirs(p)
	if err != nil {
		return result, err
	}

	needsConfigMigration := cfgExists && cfg.ValheimPath != ""
	needsRegistryMigration := regExists && (len(reg.Profiles) > 0 || len(reg.Settings) > 0)
	needsDirMigration := len(legacyDirs) > 0

	if !needsConfigMigration && !needsRegistryMigration && !needsDirMigration && !sentinelExists {
		return result, nil
	}

	if sentinelExists {
		fmt.Fprintln(os.Stderr, "mmcli: previous migration was interrupted, retrying")
	}

	if err := os.WriteFile(sentinelPath, []byte(time.Now().Format(time.RFC3339)+"\n"), 0644); err != nil {
		return result, fmt.Errorf("write migration sentinel: %w", err)
	}

	if err := backupForMigration(p); err != nil {
		return result, fmt.Errorf("backup config and registry: %w", err)
	}

	moved, err := migrateLegacyProfileDirs(p, legacyDirs)
	result.DirsMovedNames = moved
	if err != nil {
		return result, fmt.Errorf("move profile dirs: %w", err)
	}

	if needsConfigMigration {
		migrateGameInstalls(&cfg)
		if err := Save(p, cfg); err != nil {
			return result, fmt.Errorf("save migrated config: %w", err)
		}
		result.ConfigChanged = true
	}

	if needsRegistryMigration {
		migrateRegistryToGames(&reg)
		if err := SaveRegistry(p, reg); err != nil {
			return result, fmt.Errorf("save migrated registry: %w", err)
		}
		result.RegistryChanged = true
	}

	if err := os.Remove(sentinelPath); err != nil && !os.IsNotExist(err) {
		return result, fmt.Errorf("clear migration sentinel: %w", err)
	}
	return result, nil
}

// loadConfigRaw reads config.json without running the in-memory
// migration that Load() applies, so RunMigrations can inspect the legacy
// shape and decide what to do.
func loadConfigRaw(path string) (Config, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, false, nil
		}
		return Config{}, false, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, false, fmt.Errorf("parse config: %w", err)
	}
	return cfg, true, nil
}

func loadRegistryRaw(path string) (Registry, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Registry{}, false, nil
		}
		return Registry{}, false, fmt.Errorf("read registry: %w", err)
	}
	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return Registry{}, false, fmt.Errorf("parse registry: %w", err)
	}
	return reg, true, nil
}

// detectLegacyProfileDirs returns the names of directories directly under
// AllProfilesRoot that are not registered game IDs. These are
// pre-multigame profile dirs that need to be moved under valheim/.
func detectLegacyProfileDirs(p Paths) ([]string, error) {
	if p.AllProfilesRoot == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(p.AllProfilesRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	knownGames := map[string]bool{}
	for _, id := range games.IDs() {
		knownGames[id] = true
	}

	var legacy []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if knownGames[e.Name()] {
			continue
		}
		legacy = append(legacy, e.Name())
	}
	return legacy, nil
}

func migrateLegacyProfileDirs(p Paths, legacyNames []string) ([]string, error) {
	if len(legacyNames) == 0 {
		return nil, nil
	}
	targetGameDir := filepath.Join(p.AllProfilesRoot, defaultActiveGame)
	if err := os.MkdirAll(targetGameDir, 0755); err != nil {
		return nil, fmt.Errorf("create %s: %w", targetGameDir, err)
	}
	var moved []string
	for _, name := range legacyNames {
		src := filepath.Join(p.AllProfilesRoot, name)
		dst := filepath.Join(targetGameDir, name)
		if _, err := os.Stat(dst); err == nil {
			// Already moved (retry case after interruption). Don't merge
			// blindly — a stray src here likely indicates a half-finished
			// previous attempt; leave it alone for the user to inspect.
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			return moved, fmt.Errorf("rename %s -> %s: %w", src, dst, err)
		}
		moved = append(moved, name)
	}
	return moved, nil
}

func backupForMigration(p Paths) error {
	backupDir := filepath.Join(p.ConfigDir, migrationBackupDir)
	// If the backup dir already exists (a previous interrupted migration
	// already wrote one), don't overwrite — the original pre-migration
	// state is what we want to preserve.
	if _, err := os.Stat(backupDir); err == nil {
		return nil
	}
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return err
	}
	for _, src := range []string{p.ConfigFile, p.RegistryFile} {
		data, err := os.ReadFile(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		dst := filepath.Join(backupDir, filepath.Base(src))
		if err := os.WriteFile(dst, data, 0644); err != nil {
			return err
		}
	}
	return nil
}
