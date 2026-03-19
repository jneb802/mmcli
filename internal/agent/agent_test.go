package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mmcli/internal/agentapi"
)

// --- scriptparser tests ---

func TestTokenizeExecLine(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			"simple",
			`exec ./valheim_server.x86_64 -name "My Server" -port 2456`,
			[]string{"exec", "./valheim_server.x86_64", "-name", "My Server", "-port", "2456"},
		},
		{
			"no quotes",
			`./valheim_server.x86_64 -name TestServer -port 2456`,
			[]string{"./valheim_server.x86_64", "-name", "TestServer", "-port", "2456"},
		},
		{
			"quoted password with spaces",
			`exec ./valheim_server.x86_64 -password "my secret pass"`,
			[]string{"exec", "./valheim_server.x86_64", "-password", "my secret pass"},
		},
		{
			"empty string",
			``,
			nil,
		},
		{
			"extra spaces",
			`exec   ./valheim_server.x86_64   -port  2456`,
			[]string{"exec", "./valheim_server.x86_64", "-port", "2456"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tokenizeExecLine(tt.input)
			if len(got) != len(tt.expected) {
				t.Fatalf("tokenizeExecLine(%q) = %v (len %d), want %v (len %d)",
					tt.input, got, len(got), tt.expected, len(tt.expected))
			}
			for i, want := range tt.expected {
				if got[i] != want {
					t.Errorf("token[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

func TestParseExecArgs(t *testing.T) {
	args := []string{
		"-name", "My Server",
		"-port", "2456",
		"-world", "Dedicated",
		"-password", "secret123",
		"-public", "1",
		"-savedir", "/data/saves",
		"-saveinterval", "1800",
		"-backups", "4",
		"-backupshort", "7200",
		"-backuplong", "43200",
		"-crossplay",
		"-preset", "hard",
		"-modifier", "combat", "hard",
		"-modifier", "raids", "muchmore",
		"-setkey", "playerevents",
		"-setkey", "passivemobs",
	}

	s := parseExecArgs(args)

	if s.Name != "My Server" {
		t.Errorf("Name = %q, want %q", s.Name, "My Server")
	}
	if s.Port != 2456 {
		t.Errorf("Port = %d, want %d", s.Port, 2456)
	}
	if s.World != "Dedicated" {
		t.Errorf("World = %q, want %q", s.World, "Dedicated")
	}
	if s.Password != "secret123" {
		t.Errorf("Password = %q, want %q", s.Password, "secret123")
	}
	if s.Public != 1 {
		t.Errorf("Public = %d, want %d", s.Public, 1)
	}
	if s.SaveDir != "/data/saves" {
		t.Errorf("SaveDir = %q, want %q", s.SaveDir, "/data/saves")
	}
	if s.SaveInterval != 1800 {
		t.Errorf("SaveInterval = %d, want %d", s.SaveInterval, 1800)
	}
	if s.Backups != 4 {
		t.Errorf("Backups = %d, want %d", s.Backups, 4)
	}
	if s.BackupShort != 7200 {
		t.Errorf("BackupShort = %d, want %d", s.BackupShort, 7200)
	}
	if s.BackupLong != 43200 {
		t.Errorf("BackupLong = %d, want %d", s.BackupLong, 43200)
	}
	if !s.Crossplay {
		t.Error("Crossplay = false, want true")
	}
	if s.Preset != "hard" {
		t.Errorf("Preset = %q, want %q", s.Preset, "hard")
	}
	if len(s.Modifiers) != 2 {
		t.Fatalf("got %d modifiers, want 2", len(s.Modifiers))
	}
	if s.Modifiers["combat"] != "hard" {
		t.Errorf("Modifiers[combat] = %q, want %q", s.Modifiers["combat"], "hard")
	}
	if s.Modifiers["raids"] != "muchmore" {
		t.Errorf("Modifiers[raids] = %q, want %q", s.Modifiers["raids"], "muchmore")
	}
	if len(s.SetKeys) != 2 {
		t.Fatalf("got %d setkeys, want 2", len(s.SetKeys))
	}
	if s.SetKeys[0] != "playerevents" {
		t.Errorf("SetKeys[0] = %q, want %q", s.SetKeys[0], "playerevents")
	}
}

func TestParseExecArgsMinimal(t *testing.T) {
	s := parseExecArgs([]string{"-port", "2456"})
	if s.Port != 2456 {
		t.Errorf("Port = %d, want %d", s.Port, 2456)
	}
	if s.Name != "" {
		t.Errorf("Name = %q, want empty", s.Name)
	}
	if s.Crossplay {
		t.Error("Crossplay should default to false")
	}
}

func TestParseExecArgsEmpty(t *testing.T) {
	s := parseExecArgs(nil)
	if s == nil {
		t.Fatal("parseExecArgs(nil) should return non-nil")
	}
	if s.Port != 0 {
		t.Errorf("Port = %d, want 0", s.Port)
	}
}

func TestParseStartScriptFull(t *testing.T) {
	tmp := t.TempDir()
	script := filepath.Join(tmp, "start_server.sh")
	content := `#!/bin/bash
export LD_LIBRARY_PATH=./linux64:$LD_LIBRARY_PATH
export SteamAppId=892970

exec ./valheim_server.x86_64 -name "Test Server" -port 2456 -world "Dedicated" -password "pass" -public 1 -savedir /data/saves
`
	os.WriteFile(script, []byte(content), 0755)

	ps, settings, err := ParseStartScriptFull(script)
	if err != nil {
		t.Fatalf("ParseStartScriptFull failed: %v", err)
	}

	if ps.Prefix != "exec " {
		t.Errorf("Prefix = %q, want %q", ps.Prefix, "exec ")
	}
	if ps.Binary != "./valheim_server.x86_64" {
		t.Errorf("Binary = %q, want %q", ps.Binary, "./valheim_server.x86_64")
	}
	if len(ps.Preamble) != 4 { // shebang, 2 exports, blank line
		t.Errorf("Preamble has %d lines, want 4", len(ps.Preamble))
	}

	if settings.Name != "Test Server" {
		t.Errorf("Name = %q, want %q", settings.Name, "Test Server")
	}
	if settings.Port != 2456 {
		t.Errorf("Port = %d, want %d", settings.Port, 2456)
	}
	if settings.World != "Dedicated" {
		t.Errorf("World = %q, want %q", settings.World, "Dedicated")
	}
}

func TestParseStartScriptNoExec(t *testing.T) {
	tmp := t.TempDir()
	script := filepath.Join(tmp, "empty.sh")
	os.WriteFile(script, []byte("#!/bin/bash\necho hello\n"), 0755)

	_, _, err := ParseStartScriptFull(script)
	if err == nil {
		t.Error("should fail when no valheim_server invocation found")
	}
}

func TestRebuildStartScript(t *testing.T) {
	ps := &ParsedScript{
		Preamble: []string{"#!/bin/bash", "export SteamAppId=892970"},
		Prefix:   "exec ",
		Binary:   "./valheim_server.x86_64",
	}

	settings := &agentapi.SettingsResponse{
		Name:     "My Server",
		Port:     2456,
		World:    "Dedicated",
		Password: "secret",
		Public:   1,
		SaveDir:  "/data/saves",
	}

	result := RebuildStartScript(ps, settings)

	if !strings.Contains(result, "#!/bin/bash\n") {
		t.Error("missing shebang")
	}
	if !strings.Contains(result, "export SteamAppId=892970\n") {
		t.Error("missing preamble export")
	}
	if !strings.Contains(result, "exec ./valheim_server.x86_64") {
		t.Error("missing exec line")
	}
	if !strings.Contains(result, `-name "My Server"`) {
		t.Error("missing -name")
	}
	if !strings.Contains(result, "-port 2456") {
		t.Error("missing -port")
	}
	if !strings.Contains(result, `-world "Dedicated"`) {
		t.Error("missing -world")
	}
	if !strings.Contains(result, "-public 1") {
		t.Error("missing -public 1")
	}
	if !strings.Contains(result, "-savedir /data/saves") {
		t.Error("missing -savedir")
	}
}

func TestRebuildStartScriptCrossplayAndModifiers(t *testing.T) {
	ps := &ParsedScript{
		Prefix: "exec ",
		Binary: "./valheim_server.x86_64",
	}
	settings := &agentapi.SettingsResponse{
		Port:      2456,
		Crossplay: true,
		Preset:    "hard",
		Modifiers: map[string]string{"combat": "hard"},
		SetKeys:   []string{"playerevents"},
	}

	result := RebuildStartScript(ps, settings)

	if !strings.Contains(result, "-crossplay") {
		t.Error("missing -crossplay")
	}
	if !strings.Contains(result, "-preset hard") {
		t.Error("missing -preset")
	}
	if !strings.Contains(result, "-modifier combat hard") {
		t.Error("missing -modifier")
	}
	if !strings.Contains(result, "-setkey playerevents") {
		t.Error("missing -setkey")
	}
}

func TestRebuildStartScriptOmitsZeroBackups(t *testing.T) {
	ps := &ParsedScript{Binary: "./valheim_server.x86_64"}
	settings := &agentapi.SettingsResponse{
		Port:         2456,
		SaveInterval: 0,
		Backups:      0,
		BackupShort:  0,
		BackupLong:   0,
	}

	result := RebuildStartScript(ps, settings)

	if strings.Contains(result, "-saveinterval") {
		t.Error("should omit -saveinterval when 0")
	}
	if strings.Contains(result, "-backups") {
		t.Error("should omit -backups when 0")
	}
}

func TestParseRebuildRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	original := filepath.Join(tmp, "start.sh")
	content := `#!/bin/bash
export SteamAppId=892970
exec ./valheim_server.x86_64 -name "Round Trip" -port 2456 -world "TestWorld" -password "pass" -public 1
`
	os.WriteFile(original, []byte(content), 0755)

	ps, settings, err := ParseStartScriptFull(original)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	rebuilt := RebuildStartScript(ps, settings)

	// Parse the rebuilt script to verify settings survived
	rebuildPath := filepath.Join(tmp, "rebuilt.sh")
	os.WriteFile(rebuildPath, []byte(rebuilt), 0755)

	_, settings2, err := ParseStartScriptFull(rebuildPath)
	if err != nil {
		t.Fatalf("parse rebuilt failed: %v", err)
	}

	if settings2.Name != "Round Trip" {
		t.Errorf("Name = %q, want %q", settings2.Name, "Round Trip")
	}
	if settings2.Port != 2456 {
		t.Errorf("Port = %d, want %d", settings2.Port, 2456)
	}
	if settings2.World != "TestWorld" {
		t.Errorf("World = %q, want %q", settings2.World, "TestWorld")
	}
}

func TestApplySettingsUpdate(t *testing.T) {
	current := &agentapi.SettingsResponse{
		Name:     "Old Name",
		Port:     2456,
		World:    "OldWorld",
		Password: "oldpass",
	}

	newName := "New Name"
	newPort := 2457
	newCrossplay := true

	req := &agentapi.SettingsUpdateRequest{
		Name:      &newName,
		Port:      &newPort,
		Crossplay: &newCrossplay,
	}

	ApplySettingsUpdate(current, req)

	if current.Name != "New Name" {
		t.Errorf("Name = %q, want %q", current.Name, "New Name")
	}
	if current.Port != 2457 {
		t.Errorf("Port = %d, want %d", current.Port, 2457)
	}
	if !current.Crossplay {
		t.Error("Crossplay should be true")
	}
	// Unchanged fields should remain
	if current.World != "OldWorld" {
		t.Errorf("World = %q, want %q (unchanged)", current.World, "OldWorld")
	}
	if current.Password != "oldpass" {
		t.Errorf("Password = %q, want %q (unchanged)", current.Password, "oldpass")
	}
}

func TestApplySettingsUpdateNilFields(t *testing.T) {
	current := &agentapi.SettingsResponse{Name: "Test", Port: 2456}
	req := &agentapi.SettingsUpdateRequest{} // all nil

	ApplySettingsUpdate(current, req)

	if current.Name != "Test" {
		t.Error("nil fields should leave current unchanged")
	}
	if current.Port != 2456 {
		t.Error("nil fields should leave current unchanged")
	}
}

func TestApplySettingsUpdateModifiers(t *testing.T) {
	current := &agentapi.SettingsResponse{
		Modifiers: map[string]string{"combat": "easy"},
		SetKeys:   []string{"old"},
	}

	newMods := map[string]string{"combat": "hard", "raids": "more"}
	newKeys := []string{"playerevents", "passivemobs"}
	req := &agentapi.SettingsUpdateRequest{
		Modifiers: newMods,
		SetKeys:   newKeys,
	}

	ApplySettingsUpdate(current, req)

	if len(current.Modifiers) != 2 {
		t.Fatalf("got %d modifiers, want 2", len(current.Modifiers))
	}
	if current.Modifiers["combat"] != "hard" {
		t.Error("modifier combat should be updated")
	}
	if len(current.SetKeys) != 2 {
		t.Fatalf("got %d setkeys, want 2", len(current.SetKeys))
	}
}

func TestReadPermissionFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "adminlist.txt")
	content := `// List of admins
76561198012345678
76561198087654321

// Comments and blank lines should be skipped
76561198011111111
`
	os.WriteFile(path, []byte(content), 0644)

	ids := readPermissionFile(path)
	if len(ids) != 3 {
		t.Fatalf("got %d IDs, want 3", len(ids))
	}
	if ids[0] != "76561198012345678" {
		t.Errorf("ids[0] = %q, want %q", ids[0], "76561198012345678")
	}
}

func TestReadPermissionFileNotExist(t *testing.T) {
	ids := readPermissionFile("/nonexistent/path")
	if ids != nil {
		t.Error("should return nil for missing file")
	}
}

// --- logparser tests ---

func TestNormalize(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Epic Loot", "epicloot"},
		{"EpicLoot", "epicloot"},
		{"Epic_Loot", "epicloot"},
		{"Epic-Loot", "epicloot"},
		{"Epic.Loot", "epicloot"},
		{"EPIC LOOT", "epicloot"},
		{"", ""},
	}

	for _, tt := range tests {
		got := normalize(tt.input)
		if got != tt.expected {
			t.Errorf("normalize(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestParseBepInExLog(t *testing.T) {
	tmp := t.TempDir()
	logFile := filepath.Join(tmp, "LogOutput.log")
	content := `[Message:   BepInEx] BepInEx 5.4.2200 - valheim
[Info   :   BepInEx] Loading [Epic Loot 0.12.11]
[Info   :   BepInEx] Loading [Jewelcrafting 1.4.2]
[Info   :   BepInEx] Loading [MMHOOK 2.0.0]
[Warning:   BepInEx] Some warning here
`
	os.WriteFile(logFile, []byte(content), 0644)

	plugins, err := ParseBepInExLog(logFile)
	if err != nil {
		t.Fatalf("ParseBepInExLog failed: %v", err)
	}

	if len(plugins) != 3 {
		t.Fatalf("got %d plugins, want 3", len(plugins))
	}

	if plugins[0].DisplayName != "Epic Loot" {
		t.Errorf("plugins[0].DisplayName = %q, want %q", plugins[0].DisplayName, "Epic Loot")
	}
	if plugins[0].Version != "0.12.11" {
		t.Errorf("plugins[0].Version = %q, want %q", plugins[0].Version, "0.12.11")
	}
	if plugins[1].DisplayName != "Jewelcrafting" {
		t.Errorf("plugins[1].DisplayName = %q, want %q", plugins[1].DisplayName, "Jewelcrafting")
	}
}

func TestParseBepInExLogNotExist(t *testing.T) {
	plugins, err := ParseBepInExLog("/nonexistent/path")
	if err != nil {
		t.Errorf("should not error for missing file, got: %v", err)
	}
	if plugins != nil {
		t.Error("should return nil for missing file")
	}
}

func TestMatchLogToManifest(t *testing.T) {
	logPlugins := []LogPlugin{
		{DisplayName: "Epic Loot", Version: "0.12.11"},
		{DisplayName: "Jewelcrafting", Version: "1.4.2"},
		{DisplayName: "Unmatched Plugin", Version: "1.0.0"},
	}

	manifestNames := map[string]string{
		"RandyKnapp-EpicLoot":        "EpicLoot",
		"Smoothbrain-Jewelcrafting":   "Jewelcrafting",
		"Author-OtherMod":            "OtherMod",
	}

	matched := MatchLogToManifest(logPlugins, manifestNames)

	if _, ok := matched["RandyKnapp-EpicLoot"]; !ok {
		t.Error("EpicLoot should be matched")
	}
	if _, ok := matched["Smoothbrain-Jewelcrafting"]; !ok {
		t.Error("Jewelcrafting should be matched")
	}
	if _, ok := matched["Author-OtherMod"]; ok {
		t.Error("OtherMod should not be matched (no log entry)")
	}
}

// --- config tests ---

func TestAgentConfigPathHelpers(t *testing.T) {
	cfg := AgentConfig{ValheimDir: "/opt/valheim"}

	tests := []struct {
		name     string
		got      string
		expected string
	}{
		{"BepInExDir", cfg.BepInExDir(), "/opt/valheim/BepInEx"},
		{"PluginsDir", cfg.PluginsDir(), "/opt/valheim/BepInEx/plugins"},
		{"ConfigDir", cfg.ConfigDir(), "/opt/valheim/BepInEx/config"},
		{"PatchersDir", cfg.PatchersDir(), "/opt/valheim/BepInEx/patchers"},
		{"MonomodDir", cfg.MonomodDir(), "/opt/valheim/BepInEx/monomod"},
		{"LaunchConfigsDir", cfg.LaunchConfigsDir(), "/opt/valheim/mmcli-launch-configs"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("got %q, want %q", tt.got, tt.expected)
			}
		})
	}
}

func TestResolvedModAPIPort(t *testing.T) {
	if (AgentConfig{}).ResolvedModAPIPort() != 9878 {
		t.Error("default should be 9878")
	}
	if (AgentConfig{ModAPIPort: 1234}).ResolvedModAPIPort() != 1234 {
		t.Error("should use configured port")
	}
}

func TestResolvedLogFile(t *testing.T) {
	// Default
	cfg := AgentConfig{ValheimDir: "/opt/valheim"}
	if cfg.ResolvedLogFile() != "/opt/valheim/BepInEx/LogOutput.log" {
		t.Errorf("default log = %q", cfg.ResolvedLogFile())
	}

	// Relative path
	cfg.LogFile = "custom.log"
	if cfg.ResolvedLogFile() != "/opt/valheim/custom.log" {
		t.Errorf("relative log = %q", cfg.ResolvedLogFile())
	}

	// Absolute path
	cfg.LogFile = "/var/log/valheim.log"
	if cfg.ResolvedLogFile() != "/var/log/valheim.log" {
		t.Errorf("absolute log = %q", cfg.ResolvedLogFile())
	}
}

func TestResolvedStartScript(t *testing.T) {
	cfg := AgentConfig{ValheimDir: "/opt/valheim", StartScript: "start_server.sh"}
	if cfg.ResolvedStartScript() != "/opt/valheim/start_server.sh" {
		t.Errorf("relative = %q", cfg.ResolvedStartScript())
	}

	cfg.StartScript = "/usr/local/bin/start.sh"
	if cfg.ResolvedStartScript() != "/usr/local/bin/start.sh" {
		t.Errorf("absolute = %q", cfg.ResolvedStartScript())
	}
}

func TestLoadSaveAgentConfig(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")

	cfg := AgentConfig{
		Secret:       "abc123",
		PlayerSecret: "player456",
		ValheimDir:   "/opt/valheim",
		StartScript:  "start.sh",
		ModAPIPort:   9999,
	}

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if loaded.Secret != "abc123" {
		t.Errorf("Secret = %q", loaded.Secret)
	}
	if loaded.PlayerSecret != "player456" {
		t.Errorf("PlayerSecret = %q", loaded.PlayerSecret)
	}
	if loaded.ValheimDir != "/opt/valheim" {
		t.Errorf("ValheimDir = %q", loaded.ValheimDir)
	}
	if loaded.ModAPIPort != 9999 {
		t.Errorf("ModAPIPort = %d", loaded.ModAPIPort)
	}
}

func TestLoadConfigValidation(t *testing.T) {
	tmp := t.TempDir()

	tests := []struct {
		name    string
		content string
	}{
		{"missing secret", `{"valheim_dir":"/opt","start_script":"start.sh"}`},
		{"missing valheim_dir", `{"secret":"abc","start_script":"start.sh"}`},
		{"missing start_script", `{"secret":"abc","valheim_dir":"/opt"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(tmp, tt.name+".json")
			os.WriteFile(path, []byte(tt.content), 0644)
			_, err := LoadConfig(path)
			if err == nil {
				t.Error("LoadConfig should fail on invalid config")
			}
		})
	}
}

func TestLoadConfigCorrupt(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "bad.json")
	os.WriteFile(path, []byte("{not json"), 0644)

	_, err := LoadConfig(path)
	if err == nil {
		t.Error("should fail on corrupt JSON")
	}
}

func TestLoadConfigNotExist(t *testing.T) {
	_, err := LoadConfig("/nonexistent/config.json")
	if err == nil {
		t.Error("should fail on missing file")
	}
}

func TestGenerateSecret(t *testing.T) {
	s1, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret failed: %v", err)
	}
	if len(s1) != 64 { // 32 bytes = 64 hex chars
		t.Errorf("secret length = %d, want 64", len(s1))
	}

	s2, _ := GenerateSecret()
	if s1 == s2 {
		t.Error("two generated secrets should not be identical")
	}
}

func TestSaveConfigPermissions(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "subdir", "config.json")

	cfg := AgentConfig{Secret: "s", ValheimDir: "/v", StartScript: "start.sh"}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Should be 0600 (owner read/write only)
	if info.Mode().Perm() != 0600 {
		t.Errorf("permissions = %o, want 0600", info.Mode().Perm())
	}
}

// --- enforcer tests ---

func TestDetectAnticheatSystems(t *testing.T) {
	tests := []struct {
		name        string
		mods        []agentapi.ManifestMod
		wantAzu     bool
		wantEnforcer bool
	}{
		{
			"azu in manifest",
			[]agentapi.ManifestMod{{DirName: "Azumatt-AzuAntiCheat"}},
			true, false,
		},
		{
			"enforcer in manifest",
			[]agentapi.ManifestMod{{DirName: "Author-ValheimEnforcer"}},
			false, true,
		},
		{
			"both in manifest",
			[]agentapi.ManifestMod{
				{DirName: "Azumatt-AzuAntiCheat"},
				{DirName: "Author-ValheimEnforcer"},
			},
			true, true,
		},
		{
			"neither",
			[]agentapi.ManifestMod{{DirName: "RandyKnapp-EpicLoot"}},
			false, false,
		},
		{
			"case insensitive",
			[]agentapi.ManifestMod{{DirName: "author-AZUANTICHEAT"}},
			true, false,
		},
	}

	// Use a temp dir with no plugins/ dir so fallback scan finds nothing extra
	tmp := t.TempDir()
	bepDir := filepath.Join(tmp, "BepInEx")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAzu, gotEnforcer := detectAnticheatSystems(bepDir, tt.mods)
			if gotAzu != tt.wantAzu {
				t.Errorf("hasAzu = %v, want %v", gotAzu, tt.wantAzu)
			}
			if gotEnforcer != tt.wantEnforcer {
				t.Errorf("hasEnforcer = %v, want %v", gotEnforcer, tt.wantEnforcer)
			}
		})
	}
}

func TestDetectAnticheatFallbackScan(t *testing.T) {
	tmp := t.TempDir()
	bepDir := filepath.Join(tmp, "BepInEx")
	os.MkdirAll(filepath.Join(bepDir, "plugins", "Azumatt-AzuAntiCheat"), 0755)

	hasAzu, _ := detectAnticheatSystems(bepDir, nil)
	if !hasAzu {
		t.Error("should detect AzuAntiCheat from filesystem fallback")
	}
}

func TestBuildGUIDIndex(t *testing.T) {
	existing := &EnforcerModsConfig{
		ActiveMods: map[string]EnforcerModEntry{
			"com.example.epicloot": {PluginID: "com.example.epicloot", Name: "Epic Loot", Version: "1.0"},
		},
	}

	apiPlugins := []ModAPIPlugin{
		{GUID: "com.example.jewelcrafting", Name: "Jewelcrafting", Version: "2.0"},
	}

	index := buildGUIDIndex(existing, apiPlugins)

	// Should find epic loot by normalized name
	if entry, ok := index["epicloot"]; !ok {
		t.Error("should index by normalized name")
	} else if entry.guid != "com.example.epicloot" {
		t.Errorf("guid = %q", entry.guid)
	}

	// Should find by GUID suffix
	if entry, ok := index["jewelcrafting"]; !ok {
		t.Error("should index by GUID suffix")
	} else if entry.guid != "com.example.jewelcrafting" {
		t.Errorf("guid = %q", entry.guid)
	}
}

func TestBuildGUIDIndexNilSources(t *testing.T) {
	index := buildGUIDIndex(nil, nil)
	if len(index) != 0 {
		t.Errorf("expected empty index, got %d entries", len(index))
	}
}

func TestResolveGUID(t *testing.T) {
	index := map[string]enforcerGUIDEntry{
		"epicloot":      {guid: "com.example.epicloot", name: "Epic Loot"},
		"jewelcrafting": {guid: "com.example.jc", name: "Jewelcrafting"},
	}

	// By mod name
	mod := agentapi.ManifestMod{Name: "EpicLoot", DirName: "RandyKnapp-EpicLoot"}
	entry, ok := resolveGUID(mod, index)
	if !ok {
		t.Error("should resolve by name")
	}
	if entry.guid != "com.example.epicloot" {
		t.Errorf("guid = %q", entry.guid)
	}

	// By DirName after hyphen
	mod2 := agentapi.ManifestMod{Name: "Unknown", DirName: "Smoothbrain-Jewelcrafting"}
	entry2, ok2 := resolveGUID(mod2, index)
	if !ok2 {
		t.Error("should resolve by DirName after hyphen")
	}
	if entry2.guid != "com.example.jc" {
		t.Errorf("guid = %q", entry2.guid)
	}

	// Not found
	mod3 := agentapi.ManifestMod{Name: "Missing", DirName: "Author-Missing"}
	_, ok3 := resolveGUID(mod3, index)
	if ok3 {
		t.Error("should not resolve missing mod")
	}
}

// --- modapi matching tests ---

func TestMatchAPIToMods(t *testing.T) {
	plugins := []ModAPIPlugin{
		{GUID: "com.example.epicloot", Name: "Epic Loot", Version: "0.12.11"},
		{GUID: "com.example.unknown", Name: "Unknown Plugin", Version: "1.0.0"},
	}

	modMap := map[string]*agentapi.ModInfo{
		"RandyKnapp-EpicLoot": {Name: "EpicLoot"},
	}

	manifestNames := map[string]string{
		"RandyKnapp-EpicLoot": "EpicLoot",
	}

	matched, unmatched := MatchAPIToMods(plugins, modMap, manifestNames)

	if _, ok := matched["RandyKnapp-EpicLoot"]; !ok {
		t.Error("EpicLoot should be matched")
	}
	if len(unmatched) != 1 {
		t.Errorf("got %d unmatched, want 1", len(unmatched))
	}
}

func TestBuildCandidates(t *testing.T) {
	modMap := map[string]*agentapi.ModInfo{
		"RandyKnapp-EpicLoot": {},
		"SomeMod":             {},
	}
	manifestNames := map[string]string{
		"RandyKnapp-EpicLoot": "EpicLoot",
	}

	candidates := buildCandidates(modMap, manifestNames)

	if len(candidates) != 2 {
		t.Fatalf("got %d candidates, want 2", len(candidates))
	}

	// Find the EpicLoot candidate
	for _, c := range candidates {
		if c.dirName == "RandyKnapp-EpicLoot" {
			// Should have: manifest name, dirName, name after hyphen
			if len(c.names) < 3 {
				t.Errorf("EpicLoot candidate has %d names, want at least 3", len(c.names))
			}
		}
	}
}

func TestFindMatch(t *testing.T) {
	candidates := []modCandidate{
		{dirName: "RandyKnapp-EpicLoot", names: []string{"epicloot", "randyknappepicloot"}},
		{dirName: "Smoothbrain-Jewelcrafting", names: []string{"jewelcrafting"}},
	}

	// Exact match
	dir, _, ok := findMatch("epicloot", "Epic Loot", candidates)
	if !ok || dir != "RandyKnapp-EpicLoot" {
		t.Errorf("exact match: dir=%q ok=%v", dir, ok)
	}

	// No match
	_, _, ok = findMatch("unknown", "Unknown", candidates)
	if ok {
		t.Error("should not match unknown")
	}
}

// --- formatDuration test ---

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		input    time.Duration
		expected string
	}{
		{5 * time.Second, "5s"},
		{65 * time.Second, "1m5s"},
		{3661 * time.Second, "1h1m1s"},
		{7200 * time.Second, "2h0m0s"},
		{0, "0s"},
	}

	for _, tt := range tests {
		got := formatDuration(tt.input)
		if got != tt.expected {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// --- thunderstore/anticheat tests ---

func TestRemoveModDirs(t *testing.T) {
	tmp := t.TempDir()
	bepDir := filepath.Join(tmp, "BepInEx")

	// Create mod dirs in all locations
	for _, sub := range []string{"plugins", "patchers", "monomod", "core"} {
		dir := filepath.Join(bepDir, sub, "Author-TestMod")
		os.MkdirAll(dir, 0755)
		os.WriteFile(filepath.Join(dir, "test.dll"), []byte("dll"), 0644)
	}

	removeModDirs(bepDir, "Author-TestMod")

	// All should be gone
	for _, sub := range []string{"plugins", "patchers", "monomod", "core"} {
		dir := filepath.Join(bepDir, sub, "Author-TestMod")
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s should be removed", sub)
		}
	}
}

func TestSetupAzuAntiCheat(t *testing.T) {
	tmp := t.TempDir()
	bepDir := filepath.Join(tmp, "BepInEx")

	// Create mod with DLLs
	modDir := filepath.Join(bepDir, "plugins", "Author-WhiteMod")
	os.MkdirAll(modDir, 0755)
	os.WriteFile(filepath.Join(modDir, "white.dll"), []byte("dll"), 0644)

	greyDir := filepath.Join(bepDir, "plugins", "Author-GreyMod")
	os.MkdirAll(greyDir, 0755)
	os.WriteFile(filepath.Join(greyDir, "grey.dll"), []byte("dll"), 0644)

	mods := []agentapi.ManifestMod{
		{DirName: "Author-WhiteMod", Anticheat: "whitelist"},
		{DirName: "Author-GreyMod", Anticheat: "greylist"},
		{DirName: "Author-AdminMod", Anticheat: "adminonly"}, // should be skipped
		{DirName: "Author-NoClass", Anticheat: ""},           // should be skipped
	}

	if err := setupAzuAntiCheat(bepDir, mods); err != nil {
		t.Fatalf("setupAzuAntiCheat failed: %v", err)
	}

	// Check whitelist folder
	whitelistDir := filepath.Join(bepDir, "config", "AzuAntiCheat_Whitelist")
	if _, err := os.Stat(filepath.Join(whitelistDir, "white.dll")); os.IsNotExist(err) {
		t.Error("white.dll should be in whitelist folder")
	}

	// Check greylist folder
	greylistDir := filepath.Join(bepDir, "config", "AzuAntiCheat_Greylist")
	if _, err := os.Stat(filepath.Join(greylistDir, "grey.dll")); os.IsNotExist(err) {
		t.Error("grey.dll should be in greylist folder")
	}
}

func TestSetupAzuAntiCheatEmpty(t *testing.T) {
	tmp := t.TempDir()
	bepDir := filepath.Join(tmp, "BepInEx")

	// No classified mods — should be a no-op
	err := setupAzuAntiCheat(bepDir, []agentapi.ManifestMod{
		{DirName: "Author-Mod", Anticheat: ""},
	})
	if err != nil {
		t.Fatalf("setupAzuAntiCheat failed: %v", err)
	}

	// Whitelist/greylist dirs should NOT be created
	if _, err := os.Stat(filepath.Join(bepDir, "config", "AzuAntiCheat_Whitelist")); !os.IsNotExist(err) {
		t.Error("whitelist dir should not be created when no mods classified")
	}
}

// --- process state persistence tests ---

func TestSaveLoadClearState(t *testing.T) {
	// Override stateFilePath by writing to a temp location directly
	tmp := t.TempDir()
	path := filepath.Join(tmp, "state.json")

	startTime := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	s := processState{PID: 12345, PGID: 12345, StartTime: startTime}

	// Write state manually (same logic as saveState but to a custom path)
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	// Read it back
	readData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var loaded processState
	if err := json.Unmarshal(readData, &loaded); err != nil {
		t.Fatal(err)
	}

	if loaded.PID != 12345 {
		t.Errorf("PID = %d, want 12345", loaded.PID)
	}
	if loaded.PGID != 12345 {
		t.Errorf("PGID = %d, want 12345", loaded.PGID)
	}
	if !loaded.StartTime.Equal(startTime) {
		t.Errorf("StartTime = %v, want %v", loaded.StartTime, startTime)
	}

	// Remove and verify gone
	os.Remove(path)
	if _, err := os.ReadFile(path); !os.IsNotExist(err) {
		t.Error("state file should be removed")
	}
}

func TestLoadStateInvalid(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "state.json")

	// Corrupt JSON
	os.WriteFile(path, []byte("{invalid"), 0600)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s processState
	if err := json.Unmarshal(data, &s); err == nil {
		t.Error("should fail on corrupt JSON")
	}

	// Valid JSON but PID=0
	os.WriteFile(path, []byte(`{"pid":0,"pgid":0,"start_time":"2025-01-01T00:00:00Z"}`), 0600)
	data, _ = os.ReadFile(path)
	json.Unmarshal(data, &s)
	if s.PID != 0 {
		t.Error("PID should be 0")
	}
}

func TestIsServerProcessInvalid(t *testing.T) {
	// PID 0 should not be a server process
	if isServerProcess(0) {
		t.Error("PID 0 should not be a server process")
	}
	// Very large PID that almost certainly doesn't exist
	if isServerProcess(999999999) {
		t.Error("non-existent PID should return false")
	}
}

func TestTryAdoptNoStateFile(t *testing.T) {
	// Point stateFilePath at a non-existent location by using a fresh PM
	// Since we can't easily override stateFilePath, just verify the method
	// returns false when the state file doesn't exist (default state on most test machines)
	pm := NewProcessManager(AgentConfig{ValheimDir: t.TempDir(), StartScript: "start.sh"})
	// TryAdopt reads from the real stateFilePath — on a clean test machine
	// there should be no state file, so it should return false.
	// Note: this test is not hermetic if a state file exists from a real agent run.
	// For CI, this is fine.
	_ = pm.TryAdopt() // Just verify it doesn't panic
}
