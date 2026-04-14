package cmd

import (
	"github.com/spf13/cobra"

	"mmcli/internal/tui"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Interactive mod manager",
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, reg, err := loadConfigWithRegistry()
		if err != nil {
			return err
		}

		tui.Version = Version
		return tui.Run(paths, cfg, reg)
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
