//go:build integration

// Integration tests that run against a live mmcli-agent server.
// Run with: go test ./internal/client/ -tags integration -v
//
// These tests use the active server from ~/.config/mmcli/config.json.
// The agent must be running and reachable.

package client

import (
	"strings"
	"testing"
	"time"

	"mmcli/internal/agentapi"
	"mmcli/internal/config"
)

// loadTestClient reads the user's mmcli config and returns a client
// connected to the active server. Skips the test if no server is configured.
func loadTestClient(t *testing.T) *AgentClient {
	t.Helper()

	paths, err := config.DefaultPaths()
	if err != nil {
		t.Skipf("cannot determine paths: %v", err)
	}

	cfg, err := config.Load(paths)
	if err != nil {
		t.Skipf("cannot load config: %v", err)
	}

	if cfg.ActiveServer == "" {
		t.Skip("no active server configured")
	}

	srv, ok := cfg.Servers[cfg.ActiveServer]
	if !ok {
		t.Skipf("server %q not found in config", cfg.ActiveServer)
	}

	return New(srv.Host, srv.Port, srv.Secret)
}

// --- Read-only tests (safe to run anytime) ---

func TestIntegrationStatus(t *testing.T) {
	c := loadTestClient(t)

	resp, err := c.Status()
	if err != nil {
		t.Fatalf("Status() failed: %v", err)
	}

	t.Logf("Server running: %v", resp.Running)
	t.Logf("Version: %s", resp.Version)
	t.Logf("BepInEx: %v", resp.BepInEx)
	t.Logf("Mod count: %d", resp.ModCount)
	t.Logf("Role: %s", resp.Role)

	if resp.Version == "" {
		t.Error("Version should not be empty")
	}
	if resp.Role != "admin" {
		t.Errorf("Role = %q, want %q (using admin secret)", resp.Role, "admin")
	}

	if resp.Running {
		t.Logf("Uptime: %s", resp.Uptime)
		t.Logf("Players: %d", resp.PlayerCount)
		if resp.World != "" {
			t.Logf("World: %s", resp.World)
		}
		if resp.Day > 0 {
			t.Logf("Day: %d, Time: %s", resp.Day, resp.GameTime)
		}
	}
}

func TestIntegrationListMods(t *testing.T) {
	c := loadTestClient(t)

	resp, err := c.ListMods()
	if err != nil {
		t.Fatalf("ListMods() failed: %v", err)
	}

	t.Logf("Mods: %d (manifest_time: %s, log_parsed: %v, api_queried: %v)",
		len(resp.Mods), resp.ManifestTime, resp.LogParsed, resp.APIQueried)

	for _, mod := range resp.Mods {
		status := "enabled"
		if mod.Disabled {
			status = "disabled"
		}
		loaded := "unknown"
		if mod.Loaded != nil {
			if *mod.Loaded {
				loaded = "loaded"
			} else {
				loaded = "NOT loaded"
			}
		}
		t.Logf("  %s v%s [%s] [%s] target=%s anticheat=%s",
			mod.Name, mod.Version, status, loaded, mod.Target, mod.Anticheat)
	}

	if len(resp.Mods) == 0 {
		t.Log("Warning: no mods installed on server")
	}
}

func TestIntegrationListPlayers(t *testing.T) {
	c := loadTestClient(t)

	resp, err := c.ListPlayers()
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			t.Skipf("ListPlayers not available on this agent version: %v", err)
		}
		t.Fatalf("ListPlayers() failed: %v", err)
	}

	t.Logf("Players online: %d", len(resp.Players))
	for _, p := range resp.Players {
		t.Logf("  %s (Steam: %s)", p.Name, p.SteamID)
	}
}

func TestIntegrationGetSettings(t *testing.T) {
	c := loadTestClient(t)

	resp, err := c.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings() failed: %v", err)
	}

	t.Logf("Name: %s", resp.Name)
	t.Logf("Port: %d", resp.Port)
	t.Logf("World: %s", resp.World)
	t.Logf("Public: %d", resp.Public)
	t.Logf("Crossplay: %v", resp.Crossplay)
	t.Logf("Preset: %s", resp.Preset)
	t.Logf("SaveDir: %s", resp.SaveDir)

	if resp.Port == 0 {
		t.Error("Port should not be 0")
	}

	if len(resp.Modifiers) > 0 {
		t.Logf("Modifiers: %v", resp.Modifiers)
	}
	if len(resp.SetKeys) > 0 {
		t.Logf("SetKeys: %v", resp.SetKeys)
	}
	if len(resp.Admins) > 0 {
		t.Logf("Admins: %d entries", len(resp.Admins))
	}
}

func TestIntegrationListConfigs(t *testing.T) {
	c := loadTestClient(t)

	resp, err := c.ListConfigs()
	if err != nil {
		t.Fatalf("ListConfigs() failed: %v", err)
	}

	t.Logf("Config files: %d", len(resp.Files))
	for _, f := range resp.Files {
		t.Logf("  %s", f)
	}
}

func TestIntegrationGetConfig(t *testing.T) {
	c := loadTestClient(t)

	// First list configs to find one to read
	list, err := c.ListConfigs()
	if err != nil {
		t.Fatalf("ListConfigs() failed: %v", err)
	}
	if len(list.Files) == 0 {
		t.Skip("no config files on server")
	}

	// Read the first config file
	filename := list.Files[0]
	resp, err := c.GetConfig(filename)
	if err != nil {
		t.Fatalf("GetConfig(%q) failed: %v", filename, err)
	}

	if resp.Filename != filename {
		t.Errorf("Filename = %q, want %q", resp.Filename, filename)
	}
	if resp.Content == "" {
		t.Error("Content should not be empty")
	}
	t.Logf("Read %q: %d bytes", filename, len(resp.Content))
}

func TestIntegrationListWorlds(t *testing.T) {
	c := loadTestClient(t)

	resp, err := c.ListWorlds()
	if err != nil {
		t.Fatalf("ListWorlds() failed: %v", err)
	}

	t.Logf("SaveDir: %s", resp.SaveDir)
	t.Logf("Worlds: %d", len(resp.Worlds))
	for _, w := range resp.Worlds {
		t.Logf("  %s (db: %d bytes, fwl: %d bytes, modified: %s)",
			w.Name, w.SizeDB, w.SizeFWL, w.Modified)
	}
}

func TestIntegrationListLaunchConfigs(t *testing.T) {
	c := loadTestClient(t)

	resp, err := c.ListLaunchConfigs()
	if err != nil {
		t.Fatalf("ListLaunchConfigs() failed: %v", err)
	}

	t.Logf("Launch configs: %d, active: %s", len(resp.Configs), resp.Active)
	for _, lc := range resp.Configs {
		t.Logf("  %s (world: %s, preset: %s)", lc.Name, lc.World, lc.Preset)
	}
}

func TestIntegrationLogs(t *testing.T) {
	c := loadTestClient(t)

	body, err := c.Logs(10, false)
	if err != nil {
		t.Fatalf("Logs() failed: %v", err)
	}
	defer body.Close()

	buf := make([]byte, 4096)
	n, _ := body.Read(buf)
	lines := strings.Split(strings.TrimSpace(string(buf[:n])), "\n")

	t.Logf("Got %d log lines (last 10 requested)", len(lines))
	if len(lines) > 0 {
		t.Logf("Last line: %s", lines[len(lines)-1])
	}
}

// --- Mutating tests (grouped, safe sequence) ---

func TestIntegrationServerRestartCycle(t *testing.T) {
	c := loadTestClient(t)

	// Get initial status
	initial, err := c.Status()
	if err != nil {
		t.Fatalf("Status() failed: %v", err)
	}
	t.Logf("Initial state: running=%v", initial.Running)

	if !initial.Running {
		// Server is stopped — start it, verify, stop it
		t.Log("Server is stopped, testing start→verify→stop")

		startResp, err := c.Start()
		if err != nil {
			t.Fatalf("Start() failed: %v", err)
		}
		if !startResp.OK {
			t.Fatalf("Start() returned ok=false: %s", startResp.Message)
		}
		t.Logf("Start: %s", startResp.Message)

		// Wait for the server to actually be running
		time.Sleep(3 * time.Second)

		status, err := c.Status()
		if err != nil {
			t.Fatalf("Status() after start failed: %v", err)
		}
		if !status.Running {
			t.Error("Server should be running after Start()")
		}
		t.Logf("After start: running=%v uptime=%s", status.Running, status.Uptime)

		// Stop it again
		stopResp, err := c.Stop()
		if err != nil {
			t.Fatalf("Stop() failed: %v", err)
		}
		if !stopResp.OK {
			t.Fatalf("Stop() returned ok=false: %s", stopResp.Message)
		}
		t.Logf("Stop: %s", stopResp.Message)

		time.Sleep(2 * time.Second)

		status, err = c.Status()
		if err != nil {
			t.Fatalf("Status() after stop failed: %v", err)
		}
		if status.Running {
			t.Error("Server should be stopped after Stop()")
		}

	} else {
		// Server is running — test restart
		t.Log("Server is running, testing restart")

		oldUptime := initial.UptimeSecs

		restartResp, err := c.Restart()
		if err != nil {
			t.Fatalf("Restart() failed: %v", err)
		}
		if !restartResp.OK {
			t.Fatalf("Restart() returned ok=false: %s", restartResp.Message)
		}
		t.Logf("Restart: %s", restartResp.Message)

		// Wait for restart to complete
		time.Sleep(5 * time.Second)

		status, err := c.Status()
		if err != nil {
			t.Fatalf("Status() after restart failed: %v", err)
		}
		if !status.Running {
			t.Error("Server should be running after Restart()")
		}
		if status.UptimeSecs >= oldUptime {
			t.Errorf("Uptime should have reset after restart: old=%d new=%d", oldUptime, status.UptimeSecs)
		}
		t.Logf("After restart: running=%v uptime=%s (was %ds)", status.Running, status.Uptime, oldUptime)
	}
}

func TestIntegrationSettingsRoundTrip(t *testing.T) {
	c := loadTestClient(t)

	// Read current settings
	original, err := c.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings() failed: %v", err)
	}

	// Change server name to something recognizable
	testName := "mmcli-integration-test-" + time.Now().Format("150405")
	req := &agentapi.SettingsUpdateRequest{
		Name: &testName,
	}

	resp, err := c.UpdateSettings(req)
	if err != nil {
		t.Fatalf("UpdateSettings() failed: %v", err)
	}
	if !resp.OK {
		t.Fatalf("UpdateSettings() returned ok=false: %s", resp.Message)
	}
	t.Logf("Updated name to: %s", testName)

	// Verify the change persisted
	updated, err := c.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings() after update failed: %v", err)
	}
	if updated.Name != testName {
		t.Errorf("Name = %q, want %q", updated.Name, testName)
	}

	// Restore original name
	restore := &agentapi.SettingsUpdateRequest{
		Name: &original.Name,
	}
	_, err = c.UpdateSettings(restore)
	if err != nil {
		t.Fatalf("UpdateSettings() restore failed: %v", err)
	}
	t.Logf("Restored name to: %s", original.Name)

	// Verify restore
	final, err := c.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings() after restore failed: %v", err)
	}
	if final.Name != original.Name {
		t.Errorf("Name after restore = %q, want %q", final.Name, original.Name)
	}
}

func TestIntegrationLaunchConfigLifecycle(t *testing.T) {
	c := loadTestClient(t)

	configName := "mmcli-test-config"

	// Clean up from any previous failed run
	c.DeleteLaunchConfig(configName)

	// Create a new launch config
	createResp, err := c.CreateLaunchConfig(agentapi.LaunchConfigCreateRequest{
		Name:        configName,
		Description: "Integration test config",
	})
	if err != nil {
		t.Fatalf("CreateLaunchConfig() failed: %v", err)
	}
	if !createResp.OK {
		t.Fatalf("CreateLaunchConfig() returned ok=false: %s", createResp.Message)
	}
	t.Logf("Created launch config: %s", configName)

	// Get it back
	lc, err := c.GetLaunchConfig(configName)
	if err != nil {
		t.Fatalf("GetLaunchConfig() failed: %v", err)
	}
	if lc.Name != configName {
		t.Errorf("Name = %q, want %q", lc.Name, configName)
	}
	t.Logf("Retrieved: name=%s world=%s port=%d", lc.Name, lc.Settings.World, lc.Settings.Port)

	// Update its settings
	newWorld := "IntegrationTestWorld"
	updateSettings := &agentapi.SettingsResponse{
		Name:  "Test Server",
		Port:  lc.Settings.Port,
		World: newWorld,
	}
	updateResp, err := c.UpdateLaunchConfig(configName, updateSettings)
	if err != nil {
		t.Fatalf("UpdateLaunchConfig() failed: %v", err)
	}
	if !updateResp.OK {
		t.Fatalf("UpdateLaunchConfig() returned ok=false: %s", updateResp.Message)
	}

	// Verify update
	lc2, err := c.GetLaunchConfig(configName)
	if err != nil {
		t.Fatalf("GetLaunchConfig() after update failed: %v", err)
	}
	if lc2.Settings.World != newWorld {
		t.Errorf("World = %q, want %q", lc2.Settings.World, newWorld)
	}

	// Verify it shows in list
	listResp, err := c.ListLaunchConfigs()
	if err != nil {
		t.Fatalf("ListLaunchConfigs() failed: %v", err)
	}
	found := false
	for _, cfg := range listResp.Configs {
		if cfg.Name == configName {
			found = true
		}
	}
	if !found {
		t.Error("test config not found in list")
	}

	// Delete it
	delResp, err := c.DeleteLaunchConfig(configName)
	if err != nil {
		t.Fatalf("DeleteLaunchConfig() failed: %v", err)
	}
	if !delResp.OK {
		t.Fatalf("DeleteLaunchConfig() returned ok=false: %s", delResp.Message)
	}
	t.Log("Deleted test launch config")

	// Verify deletion
	_, err = c.GetLaunchConfig(configName)
	if err == nil {
		t.Error("GetLaunchConfig should fail after deletion")
	}
}

func TestIntegrationConfigPushRoundTrip(t *testing.T) {
	c := loadTestClient(t)

	// List configs to find a .cfg file to test with
	list, err := c.ListConfigs()
	if err != nil {
		t.Fatalf("ListConfigs() failed: %v", err)
	}

	var targetCfg string
	for _, f := range list.Files {
		if strings.HasSuffix(f, ".cfg") && f != "BepInEx.cfg" {
			targetCfg = f
			break
		}
	}
	if targetCfg == "" {
		t.Skip("no non-BepInEx .cfg files on server to test with")
	}

	// Read current content
	original, err := c.GetConfig(targetCfg)
	if err != nil {
		t.Fatalf("GetConfig(%q) failed: %v", targetCfg, err)
	}
	t.Logf("Testing with config: %s (%d bytes)", targetCfg, len(original.Content))

	// We just verify the push API responds correctly — don't actually change values
	// since we don't know the schema. Push an empty patch set.
	pushResp, err := c.PushConfigs(agentapi.ConfigPushRequest{
		Patches: []agentapi.ConfigPatch{},
	})
	if err != nil {
		t.Fatalf("PushConfigs() failed: %v", err)
	}
	if !pushResp.OK {
		t.Fatalf("PushConfigs() returned ok=false: %s", pushResp.Message)
	}
	t.Logf("Empty push: applied=%d written=%d", pushResp.Applied, pushResp.Written)
}

// --- Auth test ---

func TestIntegrationBadAuth(t *testing.T) {
	c := loadTestClient(t)

	// Create a client with a bad secret
	bad := New(c.BaseURL[7:strings.LastIndex(c.BaseURL, ":")], 9877, "wrong-secret-value")

	_, err := bad.Start()
	if err == nil {
		t.Fatal("Start() with bad secret should fail")
	}
	t.Logf("Bad auth correctly rejected: %v", err)
}
