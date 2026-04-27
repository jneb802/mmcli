package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func setupLegacyLayout(t *testing.T) Paths {
	t.Helper()
	tmp := t.TempDir()
	p := Paths{
		ConfigDir:       tmp,
		ConfigFile:      filepath.Join(tmp, "config.json"),
		RegistryFile:    filepath.Join(tmp, "registry.json"),
		AllProfilesRoot: filepath.Join(tmp, "profiles"),
	}
	if err := os.MkdirAll(p.AllProfilesRoot, 0755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunMigrationsMovesLegacyProfileDirs(t *testing.T) {
	p := setupLegacyLayout(t)

	// Create legacy profile dirs directly under AllProfilesRoot.
	for _, name := range []string{"default", "develop"} {
		if err := os.MkdirAll(filepath.Join(p.AllProfilesRoot, name, "plugins"), 0755); err != nil {
			t.Fatal(err)
		}
		// Sentinel file inside the profile so we can verify the contents
		// survived the move.
		if err := os.WriteFile(filepath.Join(p.AllProfilesRoot, name, "plugins", "marker.txt"), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := RunMigrations(p)
	if err != nil {
		t.Fatalf("RunMigrations failed: %v", err)
	}
	if len(result.DirsMovedNames) != 2 {
		t.Errorf("expected 2 moved dirs, got %v", result.DirsMovedNames)
	}

	for _, name := range []string{"default", "develop"} {
		newPath := filepath.Join(p.AllProfilesRoot, "valheim", name, "plugins", "marker.txt")
		data, err := os.ReadFile(newPath)
		if err != nil {
			t.Errorf("expected marker at %s: %v", newPath, err)
			continue
		}
		if string(data) != name {
			t.Errorf("marker contents = %q, want %q", string(data), name)
		}
		oldPath := filepath.Join(p.AllProfilesRoot, name)
		if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
			t.Errorf("old path %s should not exist after migration", oldPath)
		}
	}
}

func TestRunMigrationsMigratesConfigShape(t *testing.T) {
	p := setupLegacyLayout(t)

	legacy := []byte(`{"active_profile":"default","valheim_path":"/games/valheim","initialized":true}`)
	if err := os.WriteFile(p.ConfigFile, legacy, 0644); err != nil {
		t.Fatal(err)
	}

	result, err := RunMigrations(p)
	if err != nil {
		t.Fatalf("RunMigrations failed: %v", err)
	}
	if !result.ConfigChanged {
		t.Error("ConfigChanged should be true")
	}

	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.ActiveGame != "valheim" {
		t.Errorf("ActiveGame = %q, want valheim", cfg.ActiveGame)
	}
	if cfg.GamePath() != "/games/valheim" {
		t.Errorf("GamePath = %q, want /games/valheim", cfg.GamePath())
	}
}

func TestRunMigrationsMigratesRegistryShape(t *testing.T) {
	p := setupLegacyLayout(t)

	// Legacy top-level registry shape.
	legacy := map[string]any{
		"profiles": map[string]any{
			"default": map[string]any{
				"A-Mod": map[string]any{
					"owner": "A", "name": "Mod", "version": "1.0.0",
					"is_dependency": false,
				},
			},
		},
		"settings": map[string]any{
			"default": map[string]any{"server": "praetoris"},
		},
	}
	data, _ := json.Marshal(legacy)
	if err := os.WriteFile(p.RegistryFile, data, 0644); err != nil {
		t.Fatal(err)
	}

	result, err := RunMigrations(p)
	if err != nil {
		t.Fatalf("RunMigrations failed: %v", err)
	}
	if !result.RegistryChanged {
		t.Error("RegistryChanged should be true")
	}

	reg, err := LoadRegistry(p, "valheim")
	if err != nil {
		t.Fatalf("LoadRegistry failed: %v", err)
	}
	mod, ok := reg.GetMod("default", "A-Mod")
	if !ok {
		t.Fatal("mod missing after migration")
	}
	if mod.Owner != "A" {
		t.Errorf("Owner = %q, want A", mod.Owner)
	}
	ps := reg.GetSettings("default")
	if ps.Server != "praetoris" {
		t.Errorf("Server = %q, want praetoris", ps.Server)
	}
}

func TestRunMigrationsBackupsLegacyFiles(t *testing.T) {
	p := setupLegacyLayout(t)
	if err := os.WriteFile(p.ConfigFile, []byte(`{"valheim_path":"/x","initialized":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.RegistryFile, []byte(`{"profiles":{"default":{}}}`), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := RunMigrations(p); err != nil {
		t.Fatalf("RunMigrations failed: %v", err)
	}

	for _, name := range []string{"config.json", "registry.json"} {
		backup := filepath.Join(p.ConfigDir, ".pre-multigame-backup", name)
		if _, err := os.Stat(backup); err != nil {
			t.Errorf("backup %s missing: %v", backup, err)
		}
	}
}

func TestRunMigrationsIsSafeToRunTwice(t *testing.T) {
	p := setupLegacyLayout(t)
	if err := os.WriteFile(p.ConfigFile, []byte(`{"valheim_path":"/x","initialized":true}`), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := RunMigrations(p); err != nil {
		t.Fatalf("first RunMigrations failed: %v", err)
	}

	// Second call must be a no-op — no errors, no changes reported.
	result, err := RunMigrations(p)
	if err != nil {
		t.Fatalf("second RunMigrations failed: %v", err)
	}
	if result.AnyChange() {
		t.Errorf("expected no changes on second run, got %+v", result)
	}
}

func TestRunMigrationsResumesAfterInterruption(t *testing.T) {
	p := setupLegacyLayout(t)
	if err := os.MkdirAll(filepath.Join(p.AllProfilesRoot, "leftover"), 0755); err != nil {
		t.Fatal(err)
	}
	// Plant a stale sentinel as if a previous run was interrupted.
	if err := os.WriteFile(filepath.Join(p.ConfigDir, ".migrating"), []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := RunMigrations(p)
	if err != nil {
		t.Fatalf("RunMigrations failed: %v", err)
	}
	if len(result.DirsMovedNames) != 1 {
		t.Errorf("expected 1 moved dir, got %v", result.DirsMovedNames)
	}
	if _, err := os.Stat(filepath.Join(p.ConfigDir, ".migrating")); !os.IsNotExist(err) {
		t.Errorf("sentinel should be cleared after successful retry")
	}
}

func TestRunMigrationsDoesNothingForFreshSetup(t *testing.T) {
	tmp := t.TempDir()
	p := Paths{
		ConfigDir:       tmp,
		ConfigFile:      filepath.Join(tmp, "config.json"),
		RegistryFile:    filepath.Join(tmp, "registry.json"),
		AllProfilesRoot: filepath.Join(tmp, "profiles"),
	}

	result, err := RunMigrations(p)
	if err != nil {
		t.Fatalf("RunMigrations failed: %v", err)
	}
	if result.AnyChange() {
		t.Errorf("fresh setup should require no migration, got %+v", result)
	}
}
