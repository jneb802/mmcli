package cmd

import (
	"github.com/spf13/cobra"

	"mmcli/internal/runner"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Launch the active game with BepInEx and stream logs",
	Long: `Launch the active game via the BepInEx run script and stream game logs to stdout.
This command blocks until the game process exits. Use Ctrl+C to stop.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		return runner.Start(paths, cfg)
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
}
