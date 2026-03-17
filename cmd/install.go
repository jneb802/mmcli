package cmd

import (
	"github.com/spf13/cobra"

	"mmcli/internal/config"
	"mmcli/internal/installer"
)

var installCmd = &cobra.Command{
	Use:   "install <mod>",
	Short: "Install a mod and its dependencies into the active profile",
	Long:  "Install a mod by Owner-Name (e.g., 'RandyKnapp-EpicLoot') or Thunderstore URL",
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

		if err := installer.Install(paths, cfg, &reg, args[0]); err != nil {
			return err
		}

		return config.SaveRegistry(paths, reg)
	},
}

func init() {
	rootCmd.AddCommand(installCmd)
}
