package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"mmcli/internal/config"
	"mmcli/internal/thunderstore"
)

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search Thunderstore for Valheim mods",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, err := config.DefaultPaths()
		if err != nil {
			return err
		}
		os.MkdirAll(paths.CacheDir, 0755)

		query := args[0]
		results, err := thunderstore.Search(query, paths.CacheDir)
		if err != nil {
			return err
		}

		if len(results) == 0 {
			fmt.Printf("No mods found matching '%s'.\n", query)
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tAUTHOR\tVERSION\tDOWNLOADS")
		for _, pkg := range results {
			version := ""
			downloads := 0
			if len(pkg.Versions) > 0 {
				version = pkg.Versions[0].VersionNumber
				downloads = pkg.Versions[0].Downloads
			}
			fmt.Fprintf(w, "\033[36m%s\033[0m\t%s\t%s\t%d\n", pkg.Name, pkg.Owner, version, downloads)
		}
		w.Flush()
		return nil
	},
}

func init() {
	rootCmd.AddCommand(searchCmd)
}
