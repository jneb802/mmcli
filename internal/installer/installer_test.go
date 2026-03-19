package installer

import (
	"os"
	"path/filepath"
	"testing"

	"mmcli/internal/config"
)

func TestFindMod(t *testing.T) {
	reg := config.NewRegistry()
	reg.SetMod("default", config.ModEntry{Owner: "RandyKnapp", Name: "EpicLoot", Version: "1.0.0"})
	reg.SetMod("default", config.ModEntry{Owner: "Smoothbrain", Name: "Jewelcrafting", Version: "2.0.0"})

	tests := []struct {
		name      string
		query     string
		wantFound bool
		wantName  string
	}{
		{"exact full name", "RandyKnapp-EpicLoot", true, "EpicLoot"},
		{"just mod name", "EpicLoot", true, "EpicLoot"},
		{"case insensitive name", "epicloot", true, "EpicLoot"},
		{"case insensitive full name", "randyknapp-epicloot", true, "EpicLoot"},
		{"not found", "NonExistent", false, ""},
		{"partial match should not work", "Epic", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mod, found := findMod(&reg, "default", tt.query)
			if found != tt.wantFound {
				t.Errorf("findMod(%q) found = %v, want %v", tt.query, found, tt.wantFound)
			}
			if found && mod.Name != tt.wantName {
				t.Errorf("findMod(%q) name = %q, want %q", tt.query, mod.Name, tt.wantName)
			}
		})
	}
}

func TestFindModEmptyProfile(t *testing.T) {
	reg := config.NewRegistry()
	_, found := findMod(&reg, "empty", "anything")
	if found {
		t.Error("findMod should return false for empty profile")
	}
}

func TestIsLocalPath(t *testing.T) {
	// Create a temp file and directory to test with
	tmp := t.TempDir()
	tmpFile := filepath.Join(tmp, "test.dll")
	os.WriteFile(tmpFile, []byte("data"), 0644)

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"existing directory", tmp, true},
		{"existing file", tmpFile, true},
		{"non-existent path", "/nonexistent/path/to/file", false},
		{"thunderstore query", "RandyKnapp-EpicLoot", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsLocalPath(tt.input)
			if got != tt.expected {
				t.Errorf("IsLocalPath(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"tilde only", "~", home},
		{"tilde with path", "~/Documents", filepath.Join(home, "Documents")},
		{"no tilde", "/absolute/path", "/absolute/path"},
		{"relative", "relative/path", "relative/path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandHome(tt.input)
			if got != tt.expected {
				t.Errorf("expandHome(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestDetectLocalModsIntegration(t *testing.T) {
	tmp := t.TempDir()

	// Set up a plugins directory structure
	os.MkdirAll(filepath.Join(tmp, "TrackedMod"), 0755)
	os.WriteFile(filepath.Join(tmp, "TrackedMod", "mod.dll"), []byte("dll"), 0644)
	os.MkdirAll(filepath.Join(tmp, "UnknownMod"), 0755)
	os.WriteFile(filepath.Join(tmp, "UnknownMod", "plugin.dll"), []byte("dll"), 0644)

	registered := map[string]config.ModEntry{
		"TrackedMod": {Name: "TrackedMod", IsLocal: true},
	}

	locals := config.DetectLocalMods(tmp, registered)

	if len(locals) != 1 {
		t.Fatalf("got %d local mods, want 1", len(locals))
	}
	if locals[0].Name != "UnknownMod" {
		t.Errorf("local mod name = %q, want %q", locals[0].Name, "UnknownMod")
	}
	if !locals[0].IsLocal {
		t.Error("detected mod should be marked as local")
	}
}
