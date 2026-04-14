package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestModEntryFullName(t *testing.T) {
	tests := []struct {
		name     string
		mod      ModEntry
		expected string
	}{
		{
			name:     "thunderstore mod",
			mod:      ModEntry{Owner: "RandyKnapp", Name: "EpicLoot"},
			expected: "RandyKnapp-EpicLoot",
		},
		{
			name:     "local mod",
			mod:      ModEntry{Name: "MyMod", IsLocal: true},
			expected: "MyMod",
		},
		{
			name:     "local mod with owner set",
			mod:      ModEntry{Owner: "local", Name: "TestMod", IsLocal: true},
			expected: "TestMod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.mod.FullName()
			if got != tt.expected {
				t.Errorf("FullName() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestModEntryResolvedTarget(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		expected string
	}{
		{"empty defaults to both", "", "both"},
		{"client stays client", "client", "client"},
		{"server stays server", "server", "server"},
		{"both stays both", "both", "both"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mod := ModEntry{Target: tt.target}
			got := mod.ResolvedTarget()
			if got != tt.expected {
				t.Errorf("ResolvedTarget() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestRegistrySetGetMod(t *testing.T) {
	reg := NewRegistry()
	mod := ModEntry{Owner: "Test", Name: "Mod", Version: "1.0.0"}
	reg.SetMod("default", mod)

	got, ok := reg.GetMod("default", "Test-Mod")
	if !ok {
		t.Fatal("GetMod returned false, want true")
	}
	if got.Owner != "Test" || got.Name != "Mod" || got.Version != "1.0.0" {
		t.Errorf("GetMod returned %+v, want matching mod", got)
	}
}

func TestRegistryGetModNotFound(t *testing.T) {
	reg := NewRegistry()
	_, ok := reg.GetMod("default", "NonExistent")
	if ok {
		t.Error("GetMod returned true for non-existent mod")
	}

	// Also test missing profile
	_, ok = reg.GetMod("no-such-profile", "anything")
	if ok {
		t.Error("GetMod returned true for non-existent profile")
	}
}

func TestRegistryRemoveMod(t *testing.T) {
	reg := NewRegistry()
	mod := ModEntry{Owner: "Test", Name: "Mod", Version: "1.0.0"}
	reg.SetMod("default", mod)
	reg.RemoveMod("default", "Test-Mod")

	_, ok := reg.GetMod("default", "Test-Mod")
	if ok {
		t.Error("GetMod returned true after RemoveMod")
	}
}

func TestRegistryRemoveModNonExistentProfile(t *testing.T) {
	reg := NewRegistry()
	// Should not panic
	reg.RemoveMod("no-such-profile", "anything")
}

func TestRegistryListMods(t *testing.T) {
	reg := NewRegistry()
	reg.SetMod("default", ModEntry{Owner: "A", Name: "Mod1", Version: "1.0.0"})
	reg.SetMod("default", ModEntry{Owner: "B", Name: "Mod2", Version: "2.0.0"})
	reg.SetMod("other", ModEntry{Owner: "C", Name: "Mod3", Version: "3.0.0"})

	mods := reg.ListMods("default")
	if len(mods) != 2 {
		t.Fatalf("ListMods returned %d mods, want 2", len(mods))
	}

	// Non-existent profile
	mods = reg.ListMods("missing")
	if mods != nil {
		t.Errorf("ListMods for missing profile returned %v, want nil", mods)
	}
}

func TestRegistryIsDependent(t *testing.T) {
	reg := NewRegistry()
	reg.SetMod("default", ModEntry{
		Owner:        "A",
		Name:         "MainMod",
		Dependencies: []string{"B-DepMod"},
	})
	reg.SetMod("default", ModEntry{
		Owner:        "B",
		Name:         "DepMod",
		IsDependency: true,
	})

	if !reg.IsDependent("default", "B-DepMod") {
		t.Error("IsDependent returned false, want true")
	}
	if reg.IsDependent("default", "C-NoDep") {
		t.Error("IsDependent returned true for non-dependent mod")
	}
	if reg.IsDependent("missing", "B-DepMod") {
		t.Error("IsDependent returned true for missing profile")
	}
}

func TestRegistryEnsureProfile(t *testing.T) {
	reg := NewRegistry()
	reg.EnsureProfile("new-profile")

	if reg.Profiles["new-profile"] == nil {
		t.Error("EnsureProfile did not create profile map")
	}

	// Calling again should not reset existing data
	reg.SetMod("new-profile", ModEntry{Owner: "A", Name: "Mod"})
	reg.EnsureProfile("new-profile")
	if len(reg.Profiles["new-profile"]) != 1 {
		t.Error("EnsureProfile reset existing profile data")
	}
}

func TestPathHelpers(t *testing.T) {
	p := Paths{
		ProfilesDir: "/home/user/.config/mmcli/profiles",
		ValheimDir:  "/home/user/Valheim",
	}

	tests := []struct {
		name     string
		got      string
		expected string
	}{
		{"ProfileDir", p.ProfileDir("test"), "/home/user/.config/mmcli/profiles/test"},
		{"ProfilePluginsDir", p.ProfilePluginsDir("test"), "/home/user/.config/mmcli/profiles/test/plugins"},
		{"ProfileConfigDir", p.ProfileConfigDir("test"), "/home/user/.config/mmcli/profiles/test/config"},
		{"ProfilePatchersDir", p.ProfilePatchersDir("test"), "/home/user/.config/mmcli/profiles/test/patchers"},
		{"ProfileMonomodDir", p.ProfileMonomodDir("test"), "/home/user/.config/mmcli/profiles/test/monomod"},
		{"BepInExDir", p.BepInExDir(), "/home/user/Valheim/BepInEx"},
		{"BepInExPluginsDir", p.BepInExPluginsDir(), "/home/user/Valheim/BepInEx/plugins"},
		{"BepInExConfigDir", p.BepInExConfigDir(), "/home/user/Valheim/BepInEx/config"},
		{"BepInExPatchersDir", p.BepInExPatchersDir(), "/home/user/Valheim/BepInEx/patchers"},
		{"BepInExMonomodDir", p.BepInExMonomodDir(), "/home/user/Valheim/BepInEx/monomod"},
		{"BepInExCoreDir", p.BepInExCoreDir(), "/home/user/Valheim/BepInEx/core"},
		{"BepInExLogFile", p.BepInExLogFile(), "/home/user/Valheim/BepInEx/LogOutput.log"},
		{"RunBepInExScript", p.RunBepInExScript(), "/home/user/Valheim/run_bepinex.sh"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("got %q, want %q", tt.got, tt.expected)
			}
		})
	}
}

func TestLoadSaveConfig(t *testing.T) {
	tmp := t.TempDir()
	p := Paths{
		ConfigDir:  tmp,
		ConfigFile: filepath.Join(tmp, "config.json"),
	}

	cfg := Config{
		ActiveProfile: "default",
		ValheimPath:   "/path/to/valheim",
		Initialized:   true,
		ActiveServer:  "myserver",
		Servers: map[string]ServerEntry{
			"myserver": {Host: "1.2.3.4", Port: 9877, Secret: "abc"},
		},
	}

	if err := Save(p, cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(p)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.ActiveProfile != cfg.ActiveProfile {
		t.Errorf("ActiveProfile = %q, want %q", loaded.ActiveProfile, cfg.ActiveProfile)
	}
	if loaded.ValheimPath != cfg.ValheimPath {
		t.Errorf("ValheimPath = %q, want %q", loaded.ValheimPath, cfg.ValheimPath)
	}
	if !loaded.Initialized {
		t.Error("Initialized = false, want true")
	}
	if loaded.ActiveServer != "myserver" {
		t.Errorf("ActiveServer = %q, want %q", loaded.ActiveServer, "myserver")
	}
	srv, ok := loaded.Servers["myserver"]
	if !ok {
		t.Fatal("Server 'myserver' not found")
	}
	if srv.Host != "1.2.3.4" || srv.Port != 9877 || srv.Secret != "abc" {
		t.Errorf("Server = %+v, want host=1.2.3.4 port=9877 secret=abc", srv)
	}
}

func TestLoadSaveRegistry(t *testing.T) {
	tmp := t.TempDir()
	p := Paths{
		ConfigDir:    tmp,
		RegistryFile: filepath.Join(tmp, "registry.json"),
	}

	reg := NewRegistry()
	reg.SetMod("default", ModEntry{Owner: "A", Name: "Mod1", Version: "1.0.0"})
	reg.SetMod("default", ModEntry{Owner: "B", Name: "Mod2", Version: "2.0.0", Target: "server"})

	if err := SaveRegistry(p, reg); err != nil {
		t.Fatalf("SaveRegistry failed: %v", err)
	}

	loaded, err := LoadRegistry(p)
	if err != nil {
		t.Fatalf("LoadRegistry failed: %v", err)
	}

	mods := loaded.ListMods("default")
	if len(mods) != 2 {
		t.Fatalf("loaded %d mods, want 2", len(mods))
	}
}

func TestLoadRegistryNotExist(t *testing.T) {
	p := Paths{RegistryFile: "/nonexistent/path/registry.json"}
	reg, err := LoadRegistry(p)
	if err != nil {
		t.Fatalf("LoadRegistry should return empty registry for missing file, got error: %v", err)
	}
	if reg.Profiles == nil {
		t.Error("Profiles map should be initialized")
	}
}

func TestLoadConfigCorrupt(t *testing.T) {
	tmp := t.TempDir()
	p := Paths{ConfigFile: filepath.Join(tmp, "config.json")}
	os.WriteFile(p.ConfigFile, []byte("{invalid json"), 0644)

	_, err := Load(p)
	if err == nil {
		t.Error("Load should fail on corrupt JSON")
	}
}

func TestRegistryJSONRoundTrip(t *testing.T) {
	reg := NewRegistry()
	reg.SetMod("default", ModEntry{
		Owner: "A", Name: "Mod", Version: "1.0.0",
		IsDependency: true, Disabled: true,
		Files: []string{"/path/to/file.dll"},
		Dependencies: []string{"B-Dep"},
		Target: "server", Anticheat: "whitelist",
	})

	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var loaded Registry
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	mod, ok := loaded.Profiles["default"]["A-Mod"]
	if !ok {
		t.Fatal("mod not found after round trip")
	}
	if mod.Target != "server" {
		t.Errorf("Target = %q, want %q", mod.Target, "server")
	}
	if mod.Anticheat != "whitelist" {
		t.Errorf("Anticheat = %q, want %q", mod.Anticheat, "whitelist")
	}
	if !mod.IsDependency {
		t.Error("IsDependency = false, want true")
	}
	if !mod.Disabled {
		t.Error("Disabled = false, want true")
	}
}

func TestDetectLocalMods(t *testing.T) {
	tmp := t.TempDir()

	// Create a tracked mod directory (should be skipped)
	os.MkdirAll(filepath.Join(tmp, "TrackedMod"), 0755)
	os.WriteFile(filepath.Join(tmp, "TrackedMod", "mod.dll"), []byte("dll"), 0644)

	// Create an untracked mod directory with a DLL
	os.MkdirAll(filepath.Join(tmp, "UntrackedMod"), 0755)
	os.WriteFile(filepath.Join(tmp, "UntrackedMod", "plugin.dll"), []byte("dll"), 0644)

	// Create a disabled untracked mod directory
	os.MkdirAll(filepath.Join(tmp, "DisabledMod"), 0755)
	os.WriteFile(filepath.Join(tmp, "DisabledMod", "plugin.dll.old"), []byte("dll"), 0644)

	// Create a loose DLL file
	os.WriteFile(filepath.Join(tmp, "LooseMod.dll"), []byte("dll"), 0644)

	// Create a disabled loose DLL
	os.WriteFile(filepath.Join(tmp, "DisabledLoose.dll.old"), []byte("dll"), 0644)

	// Create a directory without DLLs (should be skipped)
	os.MkdirAll(filepath.Join(tmp, "NoDLLDir"), 0755)
	os.WriteFile(filepath.Join(tmp, "NoDLLDir", "readme.txt"), []byte("text"), 0644)

	registered := map[string]ModEntry{
		"TrackedMod": {Name: "TrackedMod", IsLocal: true},
	}

	locals := DetectLocalMods(tmp, registered)

	// Should find: UntrackedMod (enabled), DisabledMod (disabled), LooseMod (enabled), DisabledLoose (disabled)
	if len(locals) != 4 {
		t.Fatalf("DetectLocalMods returned %d mods, want 4. Got: %+v", len(locals), locals)
	}

	found := make(map[string]ModEntry)
	for _, m := range locals {
		found[m.Name] = m
	}

	if m, ok := found["UntrackedMod"]; !ok {
		t.Error("missing UntrackedMod")
	} else if m.Disabled {
		t.Error("UntrackedMod should be enabled")
	} else if !m.IsLocal {
		t.Error("UntrackedMod should be local")
	}

	if m, ok := found["DisabledMod"]; !ok {
		t.Error("missing DisabledMod")
	} else if !m.Disabled {
		t.Error("DisabledMod should be disabled")
	}

	if m, ok := found["LooseMod"]; !ok {
		t.Error("missing LooseMod")
	} else if m.Disabled {
		t.Error("LooseMod should be enabled")
	}

	if m, ok := found["DisabledLoose"]; !ok {
		t.Error("missing DisabledLoose")
	} else if !m.Disabled {
		t.Error("DisabledLoose should be disabled")
	}
}

func TestDetectLocalModsSkipsByBareName(t *testing.T) {
	tmp := t.TempDir()

	// Create a directory using just the mod name (e.g. "FastLink")
	os.MkdirAll(filepath.Join(tmp, "FastLink"), 0755)
	os.WriteFile(filepath.Join(tmp, "FastLink", "FastLink.dll"), []byte("dll"), 0644)

	// Register the mod under Owner-Name format
	registered := map[string]ModEntry{
		"Azumatt-FastLink": {Owner: "Azumatt", Name: "FastLink", Version: "1.4.8"},
	}

	locals := DetectLocalMods(tmp, registered)

	// "FastLink" directory should be skipped because it matches the registered mod's Name
	if len(locals) != 0 {
		t.Fatalf("DetectLocalMods returned %d mods, want 0 (FastLink should be skipped). Got: %+v", len(locals), locals)
	}
}

func TestProfileSettingsHelpers(t *testing.T) {
	// Default (nil) means enabled
	ps := ProfileSettings{}
	if !ps.ServerManagementEnabled() {
		t.Error("ServerManagementEnabled should default to true")
	}
	if !ps.ModpackManagementEnabled() {
		t.Error("ModpackManagementEnabled should default to true")
	}

	// Explicitly disabled
	f := false
	ps.ServerManagement = &f
	ps.ModpackManagement = &f
	if ps.ServerManagementEnabled() {
		t.Error("ServerManagementEnabled should be false when set to false")
	}
	if ps.ModpackManagementEnabled() {
		t.Error("ModpackManagementEnabled should be false when set to false")
	}

	// Explicitly enabled
	tr := true
	ps.ServerManagement = &tr
	ps.ModpackManagement = &tr
	if !ps.ServerManagementEnabled() {
		t.Error("ServerManagementEnabled should be true when set to true")
	}
	if !ps.ModpackManagementEnabled() {
		t.Error("ModpackManagementEnabled should be true when set to true")
	}
}

func TestRegistrySettingsRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	p := Paths{
		ConfigDir:    tmp,
		RegistryFile: filepath.Join(tmp, "registry.json"),
	}

	reg := NewRegistry()
	f := false
	reg.SetSettings("myprofile", ProfileSettings{
		Server:           "praetoris",
		ServerManagement: &f,
		ModpackPath:      "/some/path",
		AnticheatSystem:  "azu",
	})

	if err := SaveRegistry(p, reg); err != nil {
		t.Fatalf("SaveRegistry failed: %v", err)
	}

	loaded, err := LoadRegistry(p)
	if err != nil {
		t.Fatalf("LoadRegistry failed: %v", err)
	}

	ps := loaded.GetSettings("myprofile")
	if ps.Server != "praetoris" {
		t.Errorf("Server = %q, want %q", ps.Server, "praetoris")
	}
	if ps.ServerManagementEnabled() {
		t.Error("ServerManagement should be disabled")
	}
	if ps.ModpackPath != "/some/path" {
		t.Errorf("ModpackPath = %q, want %q", ps.ModpackPath, "/some/path")
	}
	if ps.AnticheatSystem != "azu" {
		t.Errorf("AnticheatSystem = %q, want %q", ps.AnticheatSystem, "azu")
	}
}

func TestMigrateProfileSettings(t *testing.T) {
	f := false
	cfg := Config{
		ActiveProfile:    "default",
		ActiveServer:     "myserver",
		ServerManagement: &f,
		ModpackPath:      "/modpack",
		AnticheatSystem:  "enforcer",
	}
	reg := NewRegistry()
	reg.EnsureProfile("default")
	reg.EnsureProfile("other")

	cfgDirty, regDirty := MigrateProfileSettings(&cfg, &reg, "default")
	if !cfgDirty || !regDirty {
		t.Error("migration should mark both as dirty")
	}

	// Config fields should be cleared
	if cfg.ActiveServer != "" {
		t.Errorf("ActiveServer should be empty after migration, got %q", cfg.ActiveServer)
	}
	if cfg.ServerManagement != nil {
		t.Error("ServerManagement should be nil after migration")
	}
	if cfg.ModpackPath != "" {
		t.Errorf("ModpackPath should be empty after migration, got %q", cfg.ModpackPath)
	}
	if cfg.AnticheatSystem != "" {
		t.Errorf("AnticheatSystem should be empty after migration, got %q", cfg.AnticheatSystem)
	}

	// All profiles should have the values
	for _, name := range []string{"default", "other"} {
		ps := reg.GetSettings(name)
		if ps.Server != "myserver" {
			t.Errorf("%s: Server = %q, want %q", name, ps.Server, "myserver")
		}
		if ps.ServerManagementEnabled() {
			t.Errorf("%s: ServerManagement should be disabled after migration", name)
		}
		if ps.ModpackPath != "/modpack" {
			t.Errorf("%s: ModpackPath = %q, want %q", name, ps.ModpackPath, "/modpack")
		}
		if ps.AnticheatSystem != "enforcer" {
			t.Errorf("%s: AnticheatSystem = %q, want %q", name, ps.AnticheatSystem, "enforcer")
		}
	}
}

func TestMigrateProfileSettingsIdempotent(t *testing.T) {
	cfg := Config{ActiveProfile: "default"}
	reg := NewRegistry()

	// No old fields set — migration should be a no-op
	cfgDirty, regDirty := MigrateProfileSettings(&cfg, &reg, "default")
	if cfgDirty || regDirty {
		t.Error("migration should be no-op when config has no old fields")
	}
}

func TestMigrateProfileSettingsPreservesExisting(t *testing.T) {
	cfg := Config{
		ActiveProfile: "default",
		ActiveServer:  "newserver",
	}
	reg := NewRegistry()
	reg.EnsureProfile("default")
	reg.SetSettings("default", ProfileSettings{Server: "existingserver"})

	MigrateProfileSettings(&cfg, &reg, "default")

	ps := reg.GetSettings("default")
	if ps.Server != "existingserver" {
		t.Errorf("Server = %q, want %q — migration should not overwrite existing profile settings", ps.Server, "existingserver")
	}
}

func TestEnsureProfileCreatesSettings(t *testing.T) {
	reg := NewRegistry()
	reg.EnsureProfile("newprofile")

	if _, ok := reg.Settings["newprofile"]; !ok {
		t.Error("EnsureProfile should create a settings entry")
	}
}
