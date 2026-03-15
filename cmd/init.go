package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"mmcli/internal/bepinex"
	"mmcli/internal/config"
	"mmcli/internal/profile"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize mmcli: detect Valheim, install BepInEx, create default profile",
	RunE:  runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	paths, err := config.DefaultPaths()
	if err != nil {
		return err
	}

	// Check if already initialized
	if cfg, err := config.Load(paths); err == nil && cfg.Initialized {
		fmt.Print("\033[33mmmcli is already initialized. Re-initialize? [y/N]: \033[0m")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(answer)) != "y" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Detect Valheim
	fmt.Print("Detecting Valheim installation... ")
	valheimPath, err := config.DetectValheimPath()
	if err != nil {
		fmt.Println("\033[31mfailed\033[0m")
		return err
	}
	fmt.Printf("\033[32mfound\033[0m\n  %s\n", valheimPath)
	paths.ValheimDir = valheimPath

	// Create directory structure
	fmt.Print("Creating mmcli directories... ")
	for _, dir := range []string{paths.ConfigDir, paths.CacheDir, paths.ProfilesDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Println("\033[31mfailed\033[0m")
			return fmt.Errorf("failed to create %s: %w", dir, err)
		}
	}
	fmt.Println("\033[32mdone\033[0m")

	// Download and install BepInEx
	fmt.Print("Fetching latest BepInEx version... ")
	version, downloadURL, err := bepinex.LatestVersion()
	if err != nil {
		fmt.Println("\033[31mfailed\033[0m")
		return err
	}
	fmt.Printf("\033[32mv%s\033[0m\n", version)

	fmt.Print("Downloading BepInEx... ")
	zipPath, err := bepinex.Download(paths, downloadURL, version)
	if err != nil {
		fmt.Println("\033[31mfailed\033[0m")
		return err
	}
	fmt.Println("\033[32mdone\033[0m")

	fmt.Print("Installing BepInEx... ")
	if err := bepinex.Install(paths, zipPath); err != nil {
		fmt.Println("\033[31mfailed\033[0m")
		return err
	}
	fmt.Println("\033[32mdone\033[0m")

	fmt.Print("Patching run_bepinex.sh for macOS... ")
	if err := bepinex.PatchRunScript(paths); err != nil {
		fmt.Println("\033[31mfailed\033[0m")
		return err
	}
	if err := bepinex.MakeExecutable(paths); err != nil {
		fmt.Println("\033[31mfailed\033[0m")
		return err
	}
	fmt.Println("\033[32mdone\033[0m")

	// Create default profile
	fmt.Print("Creating default profile... ")
	if err := profile.Create(paths, "default"); err != nil {
		// Profile might already exist from previous init
		if !strings.Contains(err.Error(), "already exists") {
			fmt.Println("\033[31mfailed\033[0m")
			return err
		}
	}
	fmt.Println("\033[32mdone\033[0m")

	// Activate symlinks (migrates existing plugins/config into default profile)
	fmt.Print("Activating symlinks... ")
	if err := profile.ActivateSymlinks(paths, "default"); err != nil {
		fmt.Println("\033[31mfailed\033[0m")
		return err
	}
	fmt.Println("\033[32mdone\033[0m")

	// Save config
	cfg := config.Config{
		ActiveProfile: "default",
		ValheimPath:   valheimPath,
		Initialized:   true,
	}
	if err := config.Save(paths, cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	// Save empty registry
	reg := config.NewRegistry()
	reg.EnsureProfile("default")
	if err := config.SaveRegistry(paths, reg); err != nil {
		return fmt.Errorf("failed to save registry: %w", err)
	}

	fmt.Println("\n\033[32mmmcli initialized successfully!\033[0m")
	fmt.Println("Use \033[36mmmcli install <mod>\033[0m to install mods.")
	fmt.Println("Use \033[36mmmcli start\033[0m to launch Valheim with BepInEx.")
	return nil
}
