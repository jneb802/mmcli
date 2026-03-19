package thunderstore

import (
	"testing"
)

func TestParseDep(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected DepRef
	}{
		{
			name:     "simple dep",
			input:    "RandyKnapp-EpicLoot-0.12.11",
			expected: DepRef{Owner: "RandyKnapp", Name: "EpicLoot", Version: "0.12.11"},
		},
		{
			name:     "name with dashes",
			input:    "denikson-BepInExPack_Valheim-5.4.2200",
			expected: DepRef{Owner: "denikson", Name: "BepInExPack_Valheim", Version: "5.4.2200"},
		},
		{
			name:     "name with multiple dashes",
			input:    "Author-My-Cool-Mod-1.0.0",
			expected: DepRef{Owner: "Author", Name: "My-Cool-Mod", Version: "1.0.0"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: DepRef{},
		},
		{
			name:     "no dashes",
			input:    "nodashes",
			expected: DepRef{},
		},
		{
			name:     "single dash",
			input:    "Owner-Name",
			expected: DepRef{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseDep(tt.input)
			if got != tt.expected {
				t.Errorf("ParseDep(%q) = %+v, want %+v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSplitDep(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantNil  bool
		wantLen  int
		wantParts []string
	}{
		{
			name:      "standard",
			input:     "Owner-Name-1.0.0",
			wantLen:   3,
			wantParts: []string{"Owner", "Name", "1.0.0"},
		},
		{
			name:      "name with dash",
			input:     "Author-Multi-Part-Name-2.1.0",
			wantLen:   3,
			wantParts: []string{"Author", "Multi-Part-Name", "2.1.0"},
		},
		{
			name:    "empty",
			input:   "",
			wantNil: true,
		},
		{
			name:    "no dash",
			input:   "nope",
			wantNil: true,
		},
		{
			name:    "single dash at start",
			input:   "-foo",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitDep(tt.input)
			if tt.wantNil {
				if got != nil {
					t.Errorf("splitDep(%q) = %v, want nil", tt.input, got)
				}
				return
			}
			if len(got) != tt.wantLen {
				t.Fatalf("splitDep(%q) returned %d parts, want %d", tt.input, len(got), tt.wantLen)
			}
			for i, want := range tt.wantParts {
				if got[i] != want {
					t.Errorf("splitDep(%q)[%d] = %q, want %q", tt.input, i, got[i], want)
				}
			}
		})
	}
}

func TestIsProfileCode(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"valid UUID", "550e8400-e29b-41d4-a716-446655440000", true},
		{"valid UUID uppercase", "550E8400-E29B-41D4-A716-446655440000", true},
		{"not a UUID", "hello-world", false},
		{"empty", "", false},
		{"owner-name format", "RandyKnapp-EpicLoot", false},
		{"almost UUID - too short", "550e8400-e29b-41d4-a716-44665544000", false},
		{"almost UUID - too long", "550e8400-e29b-41d4-a716-4466554400000", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsProfileCode(tt.input)
			if got != tt.expected {
				t.Errorf("IsProfileCode(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestParseR2X(t *testing.T) {
	input := `profileName: "My Modpack"
mods:
  - name: "Author-ModOne"
    major: 1
    minor: 2
    patch: 3
    enabled: true
  - name: "Author-ModTwo"
    major: 0
    minor: 5
    patch: 0
    enabled: false
  - name: "Author-ModThree"
    major: 2
    minor: 0
    patch: 1
`

	profileName, mods, err := parseR2X(input)
	if err != nil {
		t.Fatalf("parseR2X failed: %v", err)
	}

	if profileName != "My Modpack" {
		t.Errorf("profileName = %q, want %q", profileName, "My Modpack")
	}

	if len(mods) != 3 {
		t.Fatalf("got %d mods, want 3", len(mods))
	}

	// Mod 1
	if mods[0].Name != "Author-ModOne" {
		t.Errorf("mods[0].Name = %q, want %q", mods[0].Name, "Author-ModOne")
	}
	if mods[0].Version != "1.2.3" {
		t.Errorf("mods[0].Version = %q, want %q", mods[0].Version, "1.2.3")
	}
	if !mods[0].Enabled {
		t.Error("mods[0].Enabled = false, want true")
	}

	// Mod 2
	if mods[1].Name != "Author-ModTwo" {
		t.Errorf("mods[1].Name = %q, want %q", mods[1].Name, "Author-ModTwo")
	}
	if mods[1].Version != "0.5.0" {
		t.Errorf("mods[1].Version = %q, want %q", mods[1].Version, "0.5.0")
	}
	if mods[1].Enabled {
		t.Error("mods[1].Enabled = true, want false")
	}

	// Mod 3
	if mods[2].Version != "2.0.1" {
		t.Errorf("mods[2].Version = %q, want %q", mods[2].Version, "2.0.1")
	}
}

func TestParseR2XMissingProfileName(t *testing.T) {
	input := `mods:
  - name: "Author-Mod"
    major: 1
    minor: 0
    patch: 0
`
	_, _, err := parseR2X(input)
	if err == nil {
		t.Error("parseR2X should fail when profileName is missing")
	}
}

func TestParseR2XNoQuotes(t *testing.T) {
	input := `profileName: TestProfile
mods:
  - name: Author-Mod
    major: 1
    minor: 0
    patch: 0
`
	name, mods, err := parseR2X(input)
	if err != nil {
		t.Fatalf("parseR2X failed: %v", err)
	}
	if name != "TestProfile" {
		t.Errorf("profileName = %q, want %q", name, "TestProfile")
	}
	if len(mods) != 1 {
		t.Fatalf("got %d mods, want 1", len(mods))
	}
	if mods[0].Name != "Author-Mod" {
		t.Errorf("Name = %q, want %q", mods[0].Name, "Author-Mod")
	}
}

func TestTrimYAMLString(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`"hello"`, "hello"},
		{`'hello'`, "hello"},
		{`  "hello"  `, "hello"},
		{`hello`, "hello"},
		{`""`, ""},
		{`  spaces  `, "spaces"},
	}

	for _, tt := range tests {
		got := trimYAMLString(tt.input)
		if got != tt.expected {
			t.Errorf("trimYAMLString(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestDefZero(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "0"},
		{"5", "5"},
		{"0", "0"},
		{"123", "123"},
		{"abc", "0"},
		{"-1", "-1"}, // Atoi accepts negative numbers
	}

	for _, tt := range tests {
		got := defZero(tt.input)
		if got != tt.expected {
			t.Errorf("defZero(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
