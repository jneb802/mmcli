package cmd

import (
	"github.com/spf13/cobra"

	"mmcli/internal/config"
	"mmcli/internal/installer"
)

var removeCmd = &cobra.Command{
	Use:   "remove <mod>",
	Short: "Remove a mod and its orphaned dependencies from the active profile",
	Long: `Remove a mod and delete its files from the active profile. Any dependencies
that are no longer required by other mods are also removed. Config files
are preserved. The mod argument is matched by Owner-Name or just the mod Name.`,
	Args: cobra.ExactArgs(1),
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
