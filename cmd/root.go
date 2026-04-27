package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"mmcli/internal/config"
	"mmcli/internal/profile"
)

var verbose bool
var jsonOutput bool

var rootCmd = &cobra.Command{
	Use:   "mmcli",
	Short: "BepInEx mod manager",
	Long:  "A CLI tool for managing BepInEx mods, profiles, and game launching across BepInEx-modded games.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return runStartupMigrations(cmd)
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output in JSON format for scripting and automation")
}

// runStartupMigrations is invoked once per CLI invocation, before any
// subcommand executes. It rolls a pre-multigame mmcli setup forward to
// the current shape (config field rename, registry segmentation, profile
// dir layout) and re-activates the active profile so symlinks and
// doorstop_config.ini point at post-migration paths.
func runStartupMigrations(cmd *cobra.Command) error {
	switch cmd.Name() {
	case "completion", "help", "version":
		return nil
	}

	paths, err := config.DefaultPaths()
	if err != nil {
		return err
	}

	result, err := config.RunMigrations(paths)
	if err != nil {
		backup := filepath.Join(paths.ConfigDir, ".pre-multigame-backup")
		return fmt.Errorf("multigame migration failed: %w\n\nA backup of the pre-migration config was saved to:\n  %s", err, backup)
	}

	if !result.AnyChange() {
		return nil
	}

	cfg, err := config.Load(paths)
	if err != nil || !cfg.Initialized || cfg.ActiveProfile == "" || cfg.ActiveGame == "" || cfg.GamePath() == "" {
		return nil
	}

	resolved := paths.ResolveForGame(cfg.ActiveGame, cfg.GamePath())
	if len(result.DirsMovedNames) > 0 {
		if err := profile.Activate(resolved, cfg.ActiveProfile); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: re-activate active profile after migration failed: %v\n", err)
		}
	}
	if result.AnyChange() {
		fmt.Fprintln(os.Stderr, "mmcli: multigame migration complete")
	}
	return nil
}
