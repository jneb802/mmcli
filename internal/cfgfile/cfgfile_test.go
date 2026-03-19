package cfgfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBytes(t *testing.T) {
	input := []byte(`## Configuration file

[General]

## Enable debug mode
# Setting type: Boolean
# Default value: false
Enabled = true

## Log level (0-3)
# Setting type: Int32
# Default value: 1
# Acceptable values: 0, 1, 2, 3
LogLevel = 2

[Advanced]

## Timeout in seconds
# Setting type: Single
# Default value: 30
Timeout = 60.5
`)

	cfg, err := ParseBytes(input)
	if err != nil {
		t.Fatalf("ParseBytes failed: %v", err)
	}

	if len(cfg.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(cfg.Entries))
	}

	// Entry 0: Enabled
	e := cfg.Entries[0]
	if e.Section != "General" {
		t.Errorf("entry 0 section = %q, want %q", e.Section, "General")
	}
	if e.Key != "Enabled" {
		t.Errorf("entry 0 key = %q, want %q", e.Key, "Enabled")
	}
	if e.Value != "true" {
		t.Errorf("entry 0 value = %q, want %q", e.Value, "true")
	}
	if e.Description != "Enable debug mode" {
		t.Errorf("entry 0 description = %q, want %q", e.Description, "Enable debug mode")
	}
	if e.SettingType != "Boolean" {
		t.Errorf("entry 0 setting type = %q, want %q", e.SettingType, "Boolean")
	}
	if e.DefaultValue != "false" {
		t.Errorf("entry 0 default = %q, want %q", e.DefaultValue, "false")
	}

	// Entry 1: LogLevel
	e = cfg.Entries[1]
	if e.Key != "LogLevel" {
		t.Errorf("entry 1 key = %q, want %q", e.Key, "LogLevel")
	}
	if e.AcceptableValues != "0, 1, 2, 3" {
		t.Errorf("entry 1 acceptable values = %q, want %q", e.AcceptableValues, "0, 1, 2, 3")
	}

	// Entry 2: Timeout (different section)
	e = cfg.Entries[2]
	if e.Section != "Advanced" {
		t.Errorf("entry 2 section = %q, want %q", e.Section, "Advanced")
	}
	if e.Key != "Timeout" {
		t.Errorf("entry 2 key = %q, want %q", e.Key, "Timeout")
	}
	if e.Value != "60.5" {
		t.Errorf("entry 2 value = %q, want %q", e.Value, "60.5")
	}
}

func TestParseBytesHeader(t *testing.T) {
	input := []byte(`## Settings file was created by plugin TestMod v1.0.0
## Plugin GUID: com.test.mod

[Main]

Setting = value
`)

	cfg, err := ParseBytes(input)
	if err != nil {
		t.Fatalf("ParseBytes failed: %v", err)
	}

	if !strings.Contains(cfg.Header, "Settings file was created by plugin TestMod") {
		t.Errorf("header = %q, expected to contain plugin info", cfg.Header)
	}
}

func TestParseBytesEmpty(t *testing.T) {
	cfg, err := ParseBytes([]byte(""))
	if err != nil {
		t.Fatalf("ParseBytes failed: %v", err)
	}
	if len(cfg.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(cfg.Entries))
	}
}

func TestEntryMap(t *testing.T) {
	cfg := &CfgFile{
		Entries: []Entry{
			{Section: "A", Key: "foo", Value: "1"},
			{Section: "A", Key: "bar", Value: "2"},
			{Section: "B", Key: "foo", Value: "3"},
		},
	}

	m := cfg.EntryMap()
	if len(m) != 3 {
		t.Fatalf("EntryMap has %d entries, want 3", len(m))
	}

	// Same key name in different sections should be separate
	if m["A\x00foo"].Value != "1" {
		t.Error("A.foo should be 1")
	}
	if m["B\x00foo"].Value != "3" {
		t.Error("B.foo should be 3")
	}
}

func TestDiff(t *testing.T) {
	local := &CfgFile{
		Entries: []Entry{
			{Section: "Main", Key: "SharedKey", Value: "localValue"},
			{Section: "Main", Key: "LocalOnly", Value: "exists"},
			{Section: "Main", Key: "SameValue", Value: "same"},
		},
	}

	remote := &CfgFile{
		Entries: []Entry{
			{Section: "Main", Key: "SharedKey", Value: "remoteValue"},
			{Section: "Main", Key: "RemoteOnly", Value: "exists"},
			{Section: "Main", Key: "SameValue", Value: "same"},
		},
	}

	diffs := Diff(local, remote)

	if len(diffs) != 3 {
		t.Fatalf("got %d diffs, want 3 (changed + local-only + remote-only)", len(diffs))
	}

	diffMap := make(map[string]DiffEntry)
	for _, d := range diffs {
		diffMap[d.Key] = d
	}

	// Changed entry
	if d, ok := diffMap["SharedKey"]; !ok {
		t.Error("missing SharedKey diff")
	} else {
		if d.Status != Changed {
			t.Errorf("SharedKey status = %d, want Changed(%d)", d.Status, Changed)
		}
		if d.LocalValue != "localValue" {
			t.Errorf("SharedKey local = %q, want %q", d.LocalValue, "localValue")
		}
		if d.RemoteValue != "remoteValue" {
			t.Errorf("SharedKey remote = %q, want %q", d.RemoteValue, "remoteValue")
		}
	}

	// Local only
	if d, ok := diffMap["LocalOnly"]; !ok {
		t.Error("missing LocalOnly diff")
	} else if d.Status != LocalOnly {
		t.Errorf("LocalOnly status = %d, want LocalOnly(%d)", d.Status, LocalOnly)
	}

	// Remote only
	if d, ok := diffMap["RemoteOnly"]; !ok {
		t.Error("missing RemoteOnly diff")
	} else if d.Status != RemoteOnly {
		t.Errorf("RemoteOnly status = %d, want RemoteOnly(%d)", d.Status, RemoteOnly)
	}
}

func TestDiffNoDifferences(t *testing.T) {
	cfg := &CfgFile{
		Entries: []Entry{
			{Section: "A", Key: "key", Value: "val"},
		},
	}

	diffs := Diff(cfg, cfg)
	if len(diffs) != 0 {
		t.Errorf("got %d diffs for identical files, want 0", len(diffs))
	}
}

func TestPatchFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.cfg")

	content := `[General]

## Some setting
Enabled = false

## Another setting
Count = 10

[Advanced]

Timeout = 30
`
	os.WriteFile(path, []byte(content), 0644)

	patches := []Patch{
		{Section: "General", Key: "Enabled", Value: "true"},
		{Section: "Advanced", Key: "Timeout", Value: "60"},
	}

	applied, err := PatchFile(path, patches)
	if err != nil {
		t.Fatalf("PatchFile failed: %v", err)
	}

	if applied != 2 {
		t.Errorf("applied = %d, want 2", applied)
	}

	// Verify the patched file
	data, _ := os.ReadFile(path)
	result := string(data)

	if !strings.Contains(result, "Enabled = true") {
		t.Error("Enabled should be patched to true")
	}
	if !strings.Contains(result, "Timeout = 60") {
		t.Error("Timeout should be patched to 60")
	}
	// Count should be unchanged
	if !strings.Contains(result, "Count = 10") {
		t.Error("Count should remain 10")
	}
}

func TestPatchFileNoMatches(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.cfg")
	os.WriteFile(path, []byte("[Main]\nKey = val\n"), 0644)

	applied, err := PatchFile(path, []Patch{{Section: "Other", Key: "Missing", Value: "x"}})
	if err != nil {
		t.Fatalf("PatchFile failed: %v", err)
	}
	if applied != 0 {
		t.Errorf("applied = %d, want 0", applied)
	}
}

func TestIsCfg(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"plugin.cfg", true},
		{"Plugin.CFG", true},
		{"config.yaml", false},
		{"data.json", false},
		{"readme.txt", false},
		{"file.cfg.bak", false},
	}

	for _, tt := range tests {
		got := IsCfg(tt.input)
		if got != tt.expected {
			t.Errorf("IsCfg(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestTextDiff(t *testing.T) {
	a := []byte("line1\nline2\nline3\n")
	b := []byte("line1\nmodified\nline3\n")

	result := TextDiff("local", "remote", a, b)

	if result == "" {
		t.Error("TextDiff returned empty for different inputs")
	}
	if !strings.Contains(result, "--- local") {
		t.Error("missing --- header")
	}
	if !strings.Contains(result, "+++ remote") {
		t.Error("missing +++ header")
	}
	if !strings.Contains(result, "-line2") {
		t.Error("missing removed line")
	}
	if !strings.Contains(result, "+modified") {
		t.Error("missing added line")
	}
}

func TestTextDiffIdentical(t *testing.T) {
	a := []byte("same\ncontent\n")
	result := TextDiff("a", "b", a, a)
	if result != "" {
		t.Errorf("TextDiff returned non-empty for identical inputs: %q", result)
	}
}

func TestTextDiffEmpty(t *testing.T) {
	result := TextDiff("a", "b", []byte(""), []byte("new content\n"))
	if result == "" {
		t.Error("TextDiff returned empty for added content")
	}
}

func TestListConfigFiles(t *testing.T) {
	tmp := t.TempDir()

	// Create various files
	os.WriteFile(filepath.Join(tmp, "mod.cfg"), []byte("cfg"), 0644)
	os.WriteFile(filepath.Join(tmp, "settings.yaml"), []byte("yaml"), 0644)
	os.WriteFile(filepath.Join(tmp, "data.json"), []byte("json"), 0644)
	os.WriteFile(filepath.Join(tmp, "readme.txt"), []byte("txt"), 0644)
	os.WriteFile(filepath.Join(tmp, "backup.bak"), []byte("bak"), 0644)
	os.WriteFile(filepath.Join(tmp, ".DS_Store"), []byte("ds"), 0644)

	os.MkdirAll(filepath.Join(tmp, "subdir"), 0755)
	os.WriteFile(filepath.Join(tmp, "subdir", "nested.cfg"), []byte("cfg"), 0644)
	os.WriteFile(filepath.Join(tmp, "subdir", "file.yml"), []byte("yml"), 0644)

	files, err := ListConfigFiles(tmp)
	if err != nil {
		t.Fatalf("ListConfigFiles failed: %v", err)
	}

	fileSet := make(map[string]bool)
	for _, f := range files {
		fileSet[f] = true
	}

	// Should include
	for _, want := range []string{"mod.cfg", "settings.yaml", "data.json", filepath.Join("subdir", "nested.cfg"), filepath.Join("subdir", "file.yml")} {
		if !fileSet[want] {
			t.Errorf("missing expected file: %s. Got: %v", want, files)
		}
	}

	// Should exclude
	for _, unwanted := range []string{"readme.txt", "backup.bak", ".DS_Store"} {
		if fileSet[unwanted] {
			t.Errorf("should not include: %s", unwanted)
		}
	}
}

func TestParseBytesAcceptableValueRange(t *testing.T) {
	input := []byte(`[Settings]

## Distance in meters
# Setting type: Single
# Default value: 10
# Acceptable value range: From 0 to 100
Distance = 50
`)

	cfg, err := ParseBytes(input)
	if err != nil {
		t.Fatalf("ParseBytes failed: %v", err)
	}

	if len(cfg.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(cfg.Entries))
	}

	if cfg.Entries[0].AcceptableValues != "From 0 to 100" {
		t.Errorf("acceptable values = %q, want %q", cfg.Entries[0].AcceptableValues, "From 0 to 100")
	}
}

func TestParseBytesMultilineDescription(t *testing.T) {
	input := []byte(`[Main]

## First line of description
## Second line of description
# Setting type: String
# Default value: hello
Greeting = hi
`)

	cfg, err := ParseBytes(input)
	if err != nil {
		t.Fatalf("ParseBytes failed: %v", err)
	}

	expected := "First line of description\nSecond line of description"
	if cfg.Entries[0].Description != expected {
		t.Errorf("description = %q, want %q", cfg.Entries[0].Description, expected)
	}
}
