package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"mmcli/internal/config"
	"mmcli/internal/installer"
)

var targetCmd = &cobra.Command{
	Use:   "target <mod> <client|server|both>",
	Short: "Set which environment a mod targets",
	Long: `Change a mod's target to client, server, or both.

  client  - won't be pushed to the server
  server  - auto-disabled locally, pushed to server
  both    - runs everywhere (default)`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		modName := args[0]
		newTarget := args[1]

		if newTarget != "client" && newTarget != "server" && newTarget != "both" {
			return fmt.Errorf("target must be 'client', 'server', or 'both'")
		}

		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		reg, err := config.LoadRegistry(paths)
		if err != nil {
			return err
		}

		profile := cfg.ActiveProfile
		mods := reg.ListMods(profile)

		// Find the mod (fuzzy match like enable/disable)
		var found *config.ModEntry
		for _, m := range mods {
			mod := m
			if mod.FullName() == modName || mod.Name == modName {
				found = &mod
				break
			}
		}
		if found == nil {
			return fmt.Errorf("mod '%s' not found in profile '%s'", modName, profile)
		}

		oldTarget := found.ResolvedTarget()
		if oldTarget == newTarget {
			fmt.Printf("Mod '%s' is already targeted at '%s'.\n", found.FullName(), newTarget)
			return nil
		}

		// Handle auto-disable/enable transitions
		if newTarget == "server" && !found.Disabled {
			// Moving to server → disable locally
			if err := installer.Disable(paths, cfg, &reg, found.FullName()); err != nil {
				fmt.Printf("Warning: could not auto-disable: %v\n", err)
			}
		} else if oldTarget == "server" && newTarget != "server" && found.Disabled {
			// Moving away from server → re-enable locally
			if err := installer.Enable(paths, cfg, &reg, found.FullName()); err != nil {
				fmt.Printf("Warning: could not auto-enable: %v\n", err)
			}
		}

		// Update target
		mod, _ := reg.GetMod(profile, found.FullName())
		mod.Target = newTarget
		reg.SetMod(profile, mod)

		if err := config.SaveRegistry(paths, reg); err != nil {
			return err
		}

		fmt.Printf("\033[32m%s target set to '%s'.\033[0m\n", found.FullName(), newTarget)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(targetCmd)
}
