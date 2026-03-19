package cmd

import (
	"github.com/spf13/cobra"

	"mmcli/internal/config"
	"mmcli/internal/tui"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Interactive mod manager",
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		reg, err := config.LoadRegistry(paths)
		if err != nil {
			return err
		}

		tui.Version = Version
		return tui.Run(paths, cfg, &reg)
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
