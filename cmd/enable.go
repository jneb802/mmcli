package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"mmcli/internal/config"
	"mmcli/internal/installer"
)

var enableCmd = &cobra.Command{
	Use:   "enable <mod>",
	Short: "Re-enable a disabled mod in the active profile",
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

		if err := installer.Enable(paths, cfg, &reg, args[0]); err != nil {
			return err
		}

		fmt.Printf("Enabled %s\n", args[0])
		return config.SaveRegistry(paths, reg)
	},
}

func init() {
	rootCmd.AddCommand(enableCmd)
}
