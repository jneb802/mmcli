package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"mmcli/internal/config"
	"mmcli/internal/games"
	"mmcli/internal/profile"
)

var gameCmd = &cobra.Command{
	Use:   "game",
	Short: "Manage which game mmcli operates on",
	Long: `Switch between games that mmcli is configured for and inspect
which one is currently active.`,
}

var gameListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured games and their profile counts",
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, err := config.DefaultPaths()
		if err != nil {
			return err
		}
		cfg, _ := config.Load(paths)

		type row struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			Configured  bool   `json:"configured"`
			InstallPath string `json:"install_path,omitempty"`
			Profiles    int    `json:"profiles"`
			Active      bool   `json:"active"`
		}

		all := games.All()
		rows := make([]row, 0, len(all))
		for _, g := range all {
			installPath := cfg.GameInstalls[g.ID]
			profileCount := 0
			if paths.AllProfilesRoot != "" {
				gameProfiles := filepath.Join(paths.AllProfilesRoot, g.ID)
				if entries, err := os.ReadDir(gameProfiles); err == nil {
					for _, e := range entries {
						if e.IsDir() {
							profileCount++
						}
					}
				}
			}
			rows = append(rows, row{
				ID:          g.ID,
				DisplayName: g.DisplayName,
				Configured:  installPath != "",
				InstallPath: installPath,
				Profiles:    profileCount,
				Active:      cfg.ActiveGame == g.ID,
			})
		}

		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(rows)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "GAME\tNAME\tSTATUS\tPROFILES\tINSTALL PATH")
		for _, r := range rows {
			active := ""
			if r.Active {
				active = "\033[32m*\033[0m"
			}
			status := "not configured"
			if r.Configured {
				status = "configured"
			}
			fmt.Fprintf(w, "%s%s\t%s\t%s\t%d\t%s\n", active, r.ID, r.DisplayName, status, r.Profiles, r.InstallPath)
		}
		w.Flush()
		return nil
	},
}

var gameUseCmd = &cobra.Command{
	Use:   "use <game>",
	Short: "Switch the active game",
	Long: `Make the named game active. Subsequent commands operate on this
game's profiles and install. The game must already be configured via
'mmcli init <game>'.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		gameID := args[0]
		game, err := games.Get(gameID)
		if err != nil {
			return err
		}

		paths, err := config.DefaultPaths()
		if err != nil {
			return err
		}
		cfg, err := config.Load(paths)
		if err != nil {
			return fmt.Errorf("mmcli not initialized. Run `mmcli init` first")
		}
		if cfg.GameInstalls[gameID] == "" {
			return fmt.Errorf("%s is not configured. Run `mmcli init %s` first", game.DisplayName, gameID)
		}

		if cfg.ActiveGame == gameID {
			fmt.Printf("Already on %s.\n", game.DisplayName)
			return nil
		}

		cfg.ActiveGame = gameID
		// Pick a sensible default active profile for the game we're
		// switching to: prefer "default" if present, else the first
		// profile dir found, else leave empty.
		resolved := paths.ResolveForGame(gameID, cfg.GameInstalls[gameID])
		profileNames, _ := profile.List(resolved)
		newActive := ""
		for _, n := range profileNames {
			if n == "default" {
				newActive = "default"
				break
			}
		}
		if newActive == "" && len(profileNames) > 0 {
			newActive = profileNames[0]
		}
		if newActive != "" {
			cfg.ActiveProfile = newActive
			if err := profile.Activate(resolved, newActive); err != nil {
				return fmt.Errorf("activate %s/%s: %w", gameID, newActive, err)
			}
		}

		if err := config.Save(paths, cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("\033[32mSwitched active game to %s.\033[0m\n", game.DisplayName)
		if newActive != "" {
			fmt.Printf("Active profile: %s\n", newActive)
		}
		return nil
	},
}

var gameShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the active game and its install path",
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, err := config.DefaultPaths()
		if err != nil {
			return err
		}
		cfg, err := config.Load(paths)
		if err != nil || cfg.ActiveGame == "" {
			return fmt.Errorf("no active game. Run `mmcli init` first")
		}
		game, err := games.Get(cfg.ActiveGame)
		if err != nil {
			return err
		}
		fmt.Printf("Active game: %s (%s)\n", game.DisplayName, game.ID)
		fmt.Printf("Install path: %s\n", cfg.GamePath())
		fmt.Printf("Active profile: %s\n", cfg.ActiveProfile)
		return nil
	},
}

func init() {
	gameCmd.AddCommand(gameListCmd)
	gameCmd.AddCommand(gameUseCmd)
	gameCmd.AddCommand(gameShowCmd)
	rootCmd.AddCommand(gameCmd)
}
