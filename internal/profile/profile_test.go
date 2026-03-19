package profile

import (
	"os"
	"path/filepath"
	"testing"

	"mmcli/internal/agentapi"
	"mmcli/internal/config"
)

func testPaths(t *testing.T) config.Paths {
	t.Helper()
	tmp := t.TempDir()
	valheim := filepath.Join(tmp, "Valheim")
	os.MkdirAll(filepath.Join(valheim, "BepInEx", "config"), 0755)
	os.MkdirAll(filepath.Join(valheim, "BepInEx", "plugins"), 0755)

	return config.Paths{
		ConfigDir:   filepath.Join(tmp, "config"),
		ProfilesDir: filepath.Join(tmp, "config", "profiles"),
		ValheimDir:  valheim,
	}
}

func TestCreateProfile(t *testing.T) {
	paths := testPaths(t)

	if err := Create(paths, "test"); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Verify directories were created
	for _, sub := range []string{"plugins", "config", "patchers", "monomod"} {
		dir := filepath.Join(paths.ProfilesDir, "test", sub)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Errorf("missing directory: %s", sub)
		}
	}
}

func TestCreateProfileDuplicate(t *testing.T) {
	paths := testPaths(t)

	Create(paths, "test")
	err := Create(paths, "test")
	if err == nil {
		t.Error("Create should fail for duplicate profile")
	}
}

func TestListProfiles(t *testing.T) {
	paths := testPaths(t)

	Create(paths, "alpha")
	Create(paths, "beta")

	names, err := List(paths)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(names) != 2 {
		t.Fatalf("got %d profiles, want 2", len(names))
	}

	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}
	if !nameSet["alpha"] || !nameSet["beta"] {
		t.Errorf("expected alpha and beta, got %v", names)
	}
}

func TestListProfilesEmpty(t *testing.T) {
	paths := testPaths(t)
	names, err := List(paths)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if names != nil {
		t.Errorf("expected nil for non-existent dir, got %v", names)
	}
}

func TestDeleteProfile(t *testing.T) {
	paths := testPaths(t)
	Create(paths, "todelete")

	cfg := config.Config{ActiveProfile: "other"}
	if err := Delete(paths, cfg, "todelete"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if _, err := os.Stat(paths.ProfileDir("todelete")); !os.IsNotExist(err) {
		t.Error("profile directory should be deleted")
	}
}

func TestDeleteActiveProfile(t *testing.T) {
	paths := testPaths(t)
	Create(paths, "active")

	cfg := config.Config{ActiveProfile: "active"}
	err := Delete(paths, cfg, "active")
	if err == nil {
		t.Error("Delete should refuse to delete active profile")
	}
}

func TestDeleteNonExistentProfile(t *testing.T) {
	paths := testPaths(t)
	cfg := config.Config{ActiveProfile: "other"}
	err := Delete(paths, cfg, "nonexistent")
	if err == nil {
		t.Error("Delete should fail for non-existent profile")
	}
}

func TestSwitchProfile(t *testing.T) {
	paths := testPaths(t)
	Create(paths, "first")
	Create(paths, "second")

	cfg := &config.Config{ActiveProfile: "first"}
	if err := Switch(paths, cfg, "second"); err != nil {
		t.Fatalf("Switch failed: %v", err)
	}

	if cfg.ActiveProfile != "second" {
		t.Errorf("ActiveProfile = %q, want %q", cfg.ActiveProfile, "second")
	}

	// Verify symlinks point to the right profile
	pluginsLink := paths.BepInExPluginsDir()
	target, err := os.Readlink(pluginsLink)
	if err != nil {
		t.Fatalf("Readlink failed: %v", err)
	}
	if target != paths.ProfilePluginsDir("second") {
		t.Errorf("plugins symlink = %q, want %q", target, paths.ProfilePluginsDir("second"))
	}
}

func TestSwitchNonExistentProfile(t *testing.T) {
	paths := testPaths(t)
	cfg := &config.Config{ActiveProfile: "first"}
	err := Switch(paths, cfg, "nonexistent")
	if err == nil {
		t.Error("Switch should fail for non-existent profile")
	}
}

func TestBuildManifest(t *testing.T) {
	reg := config.NewRegistry()

	// Server mod - should be included
	reg.SetMod("default", config.ModEntry{
		Owner: "A", Name: "ServerMod", Version: "1.0.0", Target: "server",
		Anticheat: "whitelist",
	})

	// Both mod - should be included
	reg.SetMod("default", config.ModEntry{
		Owner: "B", Name: "BothMod", Version: "2.0.0", Target: "both",
	})

	// Client-only mod - should be EXCLUDED
	reg.SetMod("default", config.ModEntry{
		Owner: "C", Name: "ClientMod", Version: "3.0.0", Target: "client",
	})

	// Local upload mod - should have Source="upload"
	reg.SetMod("default", config.ModEntry{
		Owner: "local", Name: "LocalMod", Version: "dev",
	})

	// No target (defaults to both) - should be included
	reg.SetMod("default", config.ModEntry{
		Owner: "D", Name: "DefaultMod", Version: "1.0.0",
	})

	manifest := BuildManifest("default", reg)

	if manifest.Profile != "default" {
		t.Errorf("Profile = %q, want %q", manifest.Profile, "default")
	}
	if manifest.PushedAt == "" {
		t.Error("PushedAt should not be empty")
	}

	// Should have 4 mods (everything except client-only)
	if len(manifest.Mods) != 4 {
		t.Fatalf("got %d mods, want 4", len(manifest.Mods))
	}

	modMap := make(map[string]agentapi.ManifestMod)
	for _, m := range manifest.Mods {
		modMap[m.DirName] = m
	}

	// Client mod should not be present
	if _, ok := modMap["C-ClientMod"]; ok {
		t.Error("client-only mod should be excluded from manifest")
	}

	// Server mod
	if m, ok := modMap["A-ServerMod"]; ok {
		if m.Source != "thunderstore" {
			t.Errorf("ServerMod source = %q, want %q", m.Source, "thunderstore")
		}
		if m.Anticheat != "whitelist" {
			t.Errorf("ServerMod anticheat = %q, want %q", m.Anticheat, "whitelist")
		}
		if m.Target != "server" {
			t.Errorf("ServerMod target = %q, want %q", m.Target, "server")
		}
	} else {
		t.Error("missing A-ServerMod in manifest")
	}

	// Local mod should have source=upload (FullName is "local-LocalMod" since IsLocal is not persisted)
	if m, ok := modMap["local-LocalMod"]; ok {
		if m.Source != "upload" {
			t.Errorf("LocalMod source = %q, want %q", m.Source, "upload")
		}
	} else {
		t.Error("missing local-LocalMod in manifest")
	}

	// Default target mod
	if m, ok := modMap["D-DefaultMod"]; ok {
		if m.Target != "both" {
			t.Errorf("DefaultMod target = %q, want %q", m.Target, "both")
		}
	} else {
		t.Error("missing D-DefaultMod in manifest")
	}
}

func TestBuildManifestEmptyProfile(t *testing.T) {
	reg := config.NewRegistry()
	manifest := BuildManifest("empty", reg)

	if manifest.Profile != "empty" {
		t.Errorf("Profile = %q, want %q", manifest.Profile, "empty")
	}
	if len(manifest.Mods) != 0 {
		t.Errorf("got %d mods, want 0", len(manifest.Mods))
	}
}

func TestCreateProfileCopiesBepInExCfg(t *testing.T) {
	paths := testPaths(t)

	// Create a BepInEx.cfg in the config dir
	bepCfg := filepath.Join(paths.BepInExConfigDir(), "BepInEx.cfg")
	os.WriteFile(bepCfg, []byte("[Logging]\nEnabled = true\n"), 0644)

	if err := Create(paths, "withcfg"); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Check the config was copied
	copiedCfg := filepath.Join(paths.ProfileConfigDir("withcfg"), "BepInEx.cfg")
	data, err := os.ReadFile(copiedCfg)
	if err != nil {
		t.Fatalf("BepInEx.cfg not copied: %v", err)
	}
	if string(data) != "[Logging]\nEnabled = true\n" {
		t.Error("copied BepInEx.cfg content mismatch")
	}
}
