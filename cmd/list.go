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
		if len(mods) == 0 {
			fmt.Printf("No mods installed in profile '\033[36m%s\033[0m'.\n", cfg.ActiveProfile)
			return nil
		}

		// Sort: user-installed first, then deps
		sort.Slice(mods, func(i, j int) bool {
			if mods[i].IsDependency != mods[j].IsDependency {
				return !mods[i].IsDependency
			}
			return mods[i].FullName() < mods[j].FullName()
		})

		fmt.Printf("Mods in profile '\033[36m%s\033[0m':\n\n", cfg.ActiveProfile)

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "MOD\tVERSION\tTYPE")
		for _, mod := range mods {
			modType := "\033[32minstalled\033[0m"
			if mod.IsDependency {
				modType = "\033[33mdependency\033[0m"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", mod.FullName(), mod.Version, modType)
		}
		w.Flush()
		return nil
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
