package cmd

import (
	"fmt"

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
		clientFlag, _ := cmd.Flags().GetBool("client")
		serverFlag, _ := cmd.Flags().GetBool("server")

		if clientFlag && serverFlag {
			return fmt.Errorf("cannot specify both --client and --server")
		}

		target := "both"
		if clientFlag {
			target = "client"
		} else if serverFlag {
			target = "server"
		}

		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		reg, err := config.LoadRegistry(paths)
		if err != nil {
			return err
		}

		if err := installer.Install(paths, cfg, &reg, args[0], target); err != nil {
			return err
		}

		return config.SaveRegistry(paths, reg)
	},
}

func init() {
	rootCmd.AddCommand(installCmd)
	installCmd.Flags().Bool("client", false, "mark as client-only; mod stays local and won't be pushed to the server")
	installCmd.Flags().Bool("server", false, "mark as server-only; mod is auto-disabled locally and only pushed to the server")
}
