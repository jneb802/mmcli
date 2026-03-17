package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"mmcli/internal/config"
	"mmcli/internal/installer"
)

var disableCmd = &cobra.Command{
	Use:   "disable <mod>",
	Short: "Disable a mod without removing it from the active profile",
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

		if err := installer.Disable(paths, cfg, &reg, args[0]); err != nil {
			return err
		}

		fmt.Printf("Disabled %s\n", args[0])
		return config.SaveRegistry(paths, reg)
	},
}

func init() {
	rootCmd.AddCommand(disableCmd)
}
