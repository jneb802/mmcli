package cmd

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"mmcli/internal/config"
	"mmcli/internal/games"
	"mmcli/internal/loaders/bepinex"
	"mmcli/internal/platform"
	"mmcli/internal/profile"
)

var initCmd = &cobra.Command{
	Use:   "init [game]",
	Short: "Initialize mmcli for a game: detect install, install BepInEx, create default profile",
	Long: `Detect the game's installation, download and install BepInEx, create the
directory structure, and set up a default mod profile.

Without an argument, defaults to "valheim". To set up an additional game alongside
an existing one, pass its game id (e.g. "mmcli init riskofrain2").

Safe to re-run; prompts for confirmation if the same game is already initialized.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	gameID := "valheim"
	if len(args) == 1 {
		gameID = args[0]
	}
	game, err := games.Get(gameID)
	if err != nil {
		return err
	}
	if !game.SupportedOn(runtime.GOOS) {
		return fmt.Errorf("%s is not supported on %s", game.DisplayName, runtime.GOOS)
	}

	paths, err := config.DefaultPaths()
	if err != nil {
		return err
	}

	cfg, _ := config.Load(paths)
	switch {
	case cfg.Initialized && cfg.GameInstalls[gameID] != "":
		// Same game is already set up — confirm re-init.
		fmt.Printf("\033[33mmmcli is already initialized for %s. Re-initialize? [y/N]: \033[0m", game.DisplayName)
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(answer)) != "y" {
			fmt.Println("Aborted.")
			return nil
		}
	case cfg.Initialized:
		// A different game is already set up; we're adding this one alongside.
		fmt.Printf("Adding %s alongside existing setup. Existing games: ", game.DisplayName)
		first := true
		for id := range cfg.GameInstalls {
			if !first {
				fmt.Print(", ")
			}
			fmt.Print(id)
			first = false
		}
		fmt.Println()
	}

	// Detect install path for the chosen game.
	fmt.Printf("Detecting %s installation... ", game.DisplayName)
	gamePath, err := platform.DetectInstall(game)
	if err != nil {
		fmt.Println("\033[33mnot found at default location\033[0m")
		fmt.Printf("Enter your %s install path: ", game.DisplayName)
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		gamePath = strings.TrimSpace(input)
		if _, err := os.Stat(gamePath); err != nil {
			return fmt.Errorf("path does not exist: %s", gamePath)
		}
	} else {
		fmt.Printf("\033[32mfound\033[0m\n  %s\n", gamePath)
	}
	paths = paths.ResolveForGame(game.ID, gamePath)

	// Create directory structure
	fmt.Print("Creating mmcli directories... ")
	for _, dir := range []string{paths.ConfigDir, paths.CacheDir, paths.AllProfilesRoot, paths.ProfilesDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Println("\033[31mfailed\033[0m")
			return fmt.Errorf("failed to create %s: %w", dir, err)
		}
	}
	fmt.Println("\033[32mdone\033[0m")

	// Download and install BepInEx for this game.
	fmt.Print("Fetching latest BepInEx version... ")
	version, downloadURL, err := bepinex.LatestVersion(game)
	if err != nil {
		fmt.Println("\033[31mfailed\033[0m")
		return err
	}
	fmt.Printf("\033[32mv%s\033[0m\n", version)

	fmt.Print("Downloading BepInEx... ")
	zipPath, err := bepinex.Download(paths, game, downloadURL, version)
	if err != nil {
		fmt.Println("\033[31mfailed\033[0m")
		return err
	}
	fmt.Println("\033[32mdone\033[0m")

	// Create default profile before install so Windows can place its BepInEx tree
	// into the profile-local directory layout.
	fmt.Print("Creating default profile... ")
	if err := profile.Create(paths, "default"); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			fmt.Println("\033[31mfailed\033[0m")
			return err
		}
	}
	fmt.Println("\033[32mdone\033[0m")

	fmt.Print("Installing BepInEx... ")
	if err := bepinex.Install(paths, zipPath, "default"); err != nil {
		fmt.Println("\033[31mfailed\033[0m")
		return err
	}
	fmt.Println("\033[32mdone\033[0m")

	if runtime.GOOS == "darwin" {
		fmt.Print("Patching run_bepinex.sh for macOS... ")
		if err := bepinex.PatchRunScript(paths, game); err != nil {
			fmt.Println("\033[31mfailed\033[0m")
			return err
		}
		if err := bepinex.MakeExecutable(paths); err != nil {
			fmt.Println("\033[31mfailed\033[0m")
			return err
		}
		fmt.Println("\033[32mdone\033[0m")

		fmt.Print("Removing macOS quarantine attributes... ")
		bepinex.RemoveQuarantine(paths)
		fmt.Println("\033[32mdone\033[0m")
	}

	fmt.Print("Activating default profile... ")
	if err := profile.Activate(paths, "default"); err != nil {
		fmt.Println("\033[31mfailed\033[0m")
		return err
	}
	fmt.Println("\033[32mdone\033[0m")

	// Save config — preserve any other games that were already set up.
	if cfg.GameInstalls == nil {
		cfg.GameInstalls = map[string]string{}
	}
	cfg.GameInstalls[game.ID] = gamePath
	cfg.ActiveGame = game.ID
	cfg.ActiveProfile = "default"
	cfg.Initialized = true
	if err := config.Save(paths, cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	// Ensure registry has a default profile entry for this game.
	reg, err := config.LoadRegistry(paths, game.ID)
	if err != nil {
		return fmt.Errorf("failed to load registry: %w", err)
	}
	reg.EnsureProfile("default")
	if err := config.SaveRegistry(paths, reg); err != nil {
		return fmt.Errorf("failed to save registry: %w", err)
	}

	fmt.Printf("\n\033[32mmmcli initialized for %s.\033[0m\n", game.DisplayName)
	fmt.Println("Use \033[36mmmcli tui\033[0m to get started.")
	fmt.Println("Use \033[36mmmcli start\033[0m to launch the game after you've installed mods.")
	return nil
}
