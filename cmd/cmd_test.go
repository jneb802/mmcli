package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestAllCommandsRegistered verifies that all expected commands are registered on the root.
func TestAllCommandsRegistered(t *testing.T) {
	expectedTopLevel := []string{
		"install",
		"remove",
		"list",
		"enable",
		"disable",
		"update",
		"check-updates",
		"anticheat",
		"start",
		"logs",
		"init",
		"version",
		"tui",
		"profile",
		"config",
		"server",
		"modpack",
	}

	commands := make(map[string]bool)
	for _, cmd := range rootCmd.Commands() {
		commands[cmd.Name()] = true
	}

	for _, name := range expectedTopLevel {
		if !commands[name] {
			t.Errorf("missing top-level command: %s", name)
		}
	}
}

// TestProfileSubcommands verifies profile has all its subcommands.
func TestProfileSubcommands(t *testing.T) {
	expected := []string{"create", "list", "switch", "delete", "import", "open"}

	var profileCmd *cobra.Command
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "profile" {
			profileCmd = cmd
			break
		}
	}
	if profileCmd == nil {
		t.Fatal("profile command not found")
	}

	subcommands := make(map[string]bool)
	for _, cmd := range profileCmd.Commands() {
		subcommands[cmd.Name()] = true
	}

	for _, name := range expected {
		if !subcommands[name] {
			t.Errorf("missing profile subcommand: %s", name)
		}
	}
}

// TestConfigSubcommands verifies config has all its subcommands.
func TestConfigSubcommands(t *testing.T) {
	expected := []string{"list", "diff", "push", "pull", "open", "clean"}

	var configCmd *cobra.Command
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "config" {
			configCmd = cmd
			break
		}
	}
	if configCmd == nil {
		t.Fatal("config command not found")
	}

	subcommands := make(map[string]bool)
	for _, cmd := range configCmd.Commands() {
		subcommands[cmd.Name()] = true
	}

	for _, name := range expected {
		if !subcommands[name] {
			t.Errorf("missing config subcommand: %s", name)
		}
	}
}

// TestServerSubcommands verifies server has all its subcommands.
func TestServerSubcommands(t *testing.T) {
	expected := []string{
		"add", "list", "switch", "remove",
		"status", "start", "stop", "restart",
		"logs", "settings", "update",
	}

	var serverCmd *cobra.Command
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "server" {
			serverCmd = cmd
			break
		}
	}
	if serverCmd == nil {
		t.Fatal("server command not found")
	}

	subcommands := make(map[string]bool)
	for _, cmd := range serverCmd.Commands() {
		subcommands[cmd.Name()] = true
	}

	for _, name := range expected {
		if !subcommands[name] {
			t.Errorf("missing server subcommand: %s", name)
		}
	}
}

// TestModpackSubcommands verifies modpack has all its subcommands.
func TestModpackSubcommands(t *testing.T) {
	expected := []string{"sync", "publish"}

	var modpackCmd *cobra.Command
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "modpack" {
			modpackCmd = cmd
			break
		}
	}
	if modpackCmd == nil {
		t.Fatal("modpack command not found")
	}

	subcommands := make(map[string]bool)
	for _, cmd := range modpackCmd.Commands() {
		subcommands[cmd.Name()] = true
	}

	for _, name := range expected {
		if !subcommands[name] {
			t.Errorf("missing modpack subcommand: %s", name)
		}
	}
}

// TestServerSettingsSetSubcommand verifies the nested settings set command exists.
func TestServerSettingsSetSubcommand(t *testing.T) {
	var serverCmd *cobra.Command
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "server" {
			serverCmd = cmd
			break
		}
	}
	if serverCmd == nil {
		t.Fatal("server command not found")
	}

	var settingsCmd *cobra.Command
	for _, cmd := range serverCmd.Commands() {
		if cmd.Name() == "settings" {
			settingsCmd = cmd
			break
		}
	}
	if settingsCmd == nil {
		t.Fatal("server settings command not found")
	}

	subcommands := make(map[string]bool)
	for _, cmd := range settingsCmd.Commands() {
		subcommands[cmd.Name()] = true
	}

	if !subcommands["set"] {
		t.Error("missing server settings set subcommand")
	}
}

// TestRootFlags verifies global flags are registered.
func TestRootFlags(t *testing.T) {
	flags := rootCmd.PersistentFlags()

	if flags.Lookup("verbose") == nil {
		t.Error("missing --verbose flag")
	}
	if flags.Lookup("json") == nil {
		t.Error("missing --json flag")
	}
}

// TestInstallFlags verifies install command flags.
func TestInstallFlags(t *testing.T) {
	var installCmd *cobra.Command
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "install" {
			installCmd = cmd
			break
		}
	}
	if installCmd == nil {
		t.Fatal("install command not found")
	}

	flags := installCmd.Flags()
	if flags.Lookup("client") == nil {
		t.Error("missing --client flag")
	}
	if flags.Lookup("server") == nil {
		t.Error("missing --server flag")
	}
}

