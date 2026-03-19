package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"mmcli/internal/config"
	"mmcli/internal/installer"
	"mmcli/internal/thunderstore"
)

var updateCmd = &cobra.Command{
	Use:   "update <mod>",
	Short: "Update a mod to its latest version",
	Long: `Remove and reinstall a mod to fetch the latest version from Thunderstore.
The mod's target and config files are preserved. The mod argument is matched
by Owner-Name or just the mod Name.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		reg, err := config.LoadRegistry(paths)
		if err != nil {
			return err
		}

		if err := installer.Update(paths, cfg, &reg, args[0]); err != nil {
			return err
		}

		if err := config.SaveRegistry(paths, reg); err != nil {
			return err
		}

		fmt.Printf("\033[32mUpdated %s\033[0m\n", args[0])
		return nil
	},
}

var checkUpdatesCmd = &cobra.Command{
	Use:   "check-updates",
	Short: "Check for available mod updates in the active profile",
	Long: `Query Thunderstore for each installed mod and report which ones have
newer versions available. Use --json for machine-readable output.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		reg, err := config.LoadRegistry(paths)
		if err != nil {
			return err
		}

		mods := reg.ListMods(cfg.ActiveProfile)

		type updateInfo struct {
			Name    string `json:"name"`
			Current string `json:"current"`
			Latest  string `json:"latest"`
		}

		var updates []updateInfo
		for _, mod := range mods {
			if mod.IsLocal || mod.Owner == "" {
				continue
			}
			pkg, err := thunderstore.GetPackage(mod.Owner, mod.Name)
			if err != nil || len(pkg.Versions) == 0 {
				continue
			}
			latest := pkg.Versions[0].VersionNumber
			if latest != mod.Version {
				updates = append(updates, updateInfo{
					Name:    mod.FullName(),
					Current: mod.Version,
					Latest:  latest,
				})
			}
		}

		if jsonOutput {
			if updates == nil {
				updates = []updateInfo{}
			}
			return json.NewEncoder(os.Stdout).Encode(updates)
		}

		if len(updates) == 0 {
			fmt.Println("All mods are up to date.")
			return nil
		}

		fmt.Printf("%d update(s) available:\n\n", len(updates))
		for _, u := range updates {
			fmt.Printf("  %s  %s → \033[32m%s\033[0m\n", u.Name, u.Current, u.Latest)
		}
		fmt.Println("\nUse 'mmcli update <mod>' to update a specific mod.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(checkUpdatesCmd)
}
