package cmd

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Open the config folder for the active profile in Finder",
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		configDir := paths.ProfileConfigDir(cfg.ActiveProfile)
		fmt.Printf("Opening config folder for profile %q: %s\n", cfg.ActiveProfile, configDir)
		return exec.Command("open", configDir).Run()
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
}
