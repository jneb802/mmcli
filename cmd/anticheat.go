package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"mmcli/internal/config"
)

var anticheatCmd = &cobra.Command{
	Use:   "anticheat <mod> <whitelist|greylist|adminonly|serveronly|none>",
	Short: "Set anticheat classification for a mod",
	Long: `Classify a mod for server-side anticheat enforcement.

Supports both AzuAntiCheat and ValheimEnforcer. The classification
is the same for both systems — mmcli detects which anticheat mods
are installed and configures each one automatically on push.

  whitelist  - players MUST have this mod (required)
  greylist   - players MAY have this mod (optional)
  adminonly  - only admins need this mod (ValheimEnforcer only)
  serveronly - server-side only mod (ValheimEnforcer only)
  none       - remove anticheat classification

AzuAntiCheat: classified mod DLLs are copied into the appropriate
AzuAntiCheat_Whitelist or AzuAntiCheat_Greylist config folder.
"adminonly" mods are skipped for AzuAntiCheat.

ValheimEnforcer: classified mods are written to the Mods.yaml config
with the correct category (requiredMods, optionalMods, adminOnlyMods,
serverOnlyMods). Mods with target "server" are placed in serverOnlyMods
regardless of their anticheat classification.

Dependencies are automatically classified to match.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		modName := args[0]
		classification := args[1]

		if classification != "whitelist" && classification != "greylist" && classification != "adminonly" && classification != "serveronly" && classification != "none" {
			return fmt.Errorf("classification must be 'whitelist', 'greylist', 'adminonly', 'serveronly', or 'none'")
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

		// Find the mod (fuzzy match like target.go)
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

		value := classification
		if value == "none" {
			value = ""
		}

		// Update the mod itself
		mod, _ := reg.GetMod(profile, found.FullName())
		mod.Anticheat = value
		reg.SetMod(profile, mod)

		if value != "" {
			fmt.Printf("\033[32m%s anticheat set to '%s'.\033[0m\n", found.FullName(), classification)
		} else {
			fmt.Printf("\033[32m%s anticheat classification removed.\033[0m\n", found.FullName())
		}

		if found.ResolvedTarget() == "client" && value != "" {
			fmt.Println("  Note: client-only mods are excluded from server anticheat folders.")
		}

		// Auto-propagate to dependencies
		if value != "" {
			for _, depName := range found.Dependencies {
				dep, ok := reg.GetMod(profile, depName)
				if !ok {
					continue
				}
				if dep.Anticheat != value {
					dep.Anticheat = value
					reg.SetMod(profile, dep)
					fmt.Printf("  + %s \u2192 %s (dependency)\n", depName, classification)
				}
			}
		}

		if err := config.SaveRegistry(paths, reg); err != nil {
			return err
		}

		return nil
	},
}

var anticheatAutoCmd = &cobra.Command{
	Use:   "auto",
	Short: "Auto-classify all mods based on their target",
	Long: `Automatically classify all mods for anticheat enforcement
(works with both AzuAntiCheat and ValheimEnforcer):

  server/both targets → whitelist (required)
  client targets      → greylist  (optional)`,
	RunE: func(cmd *cobra.Command, args []string) error {
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

		whitelist, greylist := 0, 0
		for _, m := range mods {
			mod, _ := reg.GetMod(profile, m.FullName())
			switch mod.ResolvedTarget() {
			case "client":
				mod.Anticheat = "greylist"
				greylist++
			default: // "server", "both"
				mod.Anticheat = "whitelist"
				whitelist++
			}
			reg.SetMod(profile, mod)
		}

		if err := config.SaveRegistry(paths, reg); err != nil {
			return err
		}

		fmt.Printf("\033[32mClassified %d mods (%d whitelist, %d greylist).\033[0m\n",
			whitelist+greylist, whitelist, greylist)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(anticheatCmd)
	anticheatCmd.AddCommand(anticheatAutoCmd)
}
