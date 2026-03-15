package cmd

import (
	"github.com/spf13/cobra"

	"mmcli/internal/config"
	"mmcli/internal/installer"
)

var removeCmd = &cobra.Command{
	Use:   "remove <mod>",
	Short: "Remove a mod and its orphaned dependencies from the active profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		reg, err := config.LoadRegistry(paths)
		if err != nil {
			return err
		}

		if err := installer.Remove(paths, cfg, &reg, args[0]); err != nil {
			return err
		}

		return config.SaveRegistry(paths, reg)
	},
}

func init() {
	rootCmd.AddCommand(removeCmd)
}
