package cmd

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"mmcli/internal/config"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed mods in the active profile",
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

		// Detect local (untracked) mods in the plugins directory
		pluginsDir := paths.ProfilePluginsDir(cfg.ActiveProfile)
		registered := reg.Profiles[cfg.ActiveProfile]
		if registered == nil {
			registered = make(map[string]config.ModEntry)
		}
		locals := config.DetectLocalMods(pluginsDir, registered)
		mods = append(mods, locals...)

		if len(mods) == 0 {
			fmt.Printf("No mods installed in profile '\033[36m%s\033[0m'.\n", cfg.ActiveProfile)
			return nil
		}

		// Sort: local first, then user-installed, then deps
		sort.Slice(mods, func(i, j int) bool {
			rank := func(m config.ModEntry) int {
				if m.IsLocal {
					return 0
				}
				if !m.IsDependency {
					return 1
				}
				return 2
			}
			ri, rj := rank(mods[i]), rank(mods[j])
			if ri != rj {
				return ri < rj
			}
			return mods[i].FullName() < mods[j].FullName()
		})

		fmt.Printf("Mods in profile '\033[36m%s\033[0m':\n\n", cfg.ActiveProfile)

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "MOD\tVERSION\tTYPE\tTARGET\tANTICHEAT\tSTATUS")
		for _, mod := range mods {
			modType := "\033[32minstalled\033[0m"
			if mod.IsLocal {
				modType = "\033[35mlocal\033[0m"
			} else if mod.IsDependency {
				modType = "\033[33mdependency\033[0m"
			}
			status := "\033[32menabled\033[0m"
			if mod.Disabled {
				status = "\033[31mdisabled\033[0m"
			}
			version := mod.Version
			if version == "" {
				version = "-"
			}
			target := mod.ResolvedTarget()
			targetColor := target
			switch target {
			case "client":
				targetColor = "\033[36mclient\033[0m"
			case "server":
				targetColor = "\033[35mserver\033[0m"
			}
			anticheat := "-"
			switch mod.Anticheat {
			case "whitelist":
				anticheat = "\033[32mwhitelist\033[0m"
			case "greylist":
				anticheat = "\033[33mgreylist\033[0m"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", mod.FullName(), version, modType, targetColor, anticheat, status)
		}
		w.Flush()
		return nil
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
