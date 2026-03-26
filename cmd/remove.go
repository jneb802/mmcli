package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"mmcli/internal/agentapi"
	"mmcli/internal/config"
	"mmcli/internal/installer"
)

var removeCmd = &cobra.Command{
	Use:   "remove <mod>",
	Short: "Remove a mod and its orphaned dependencies from the active profile",
	Long: `Remove a mod and delete its files from the active profile. Any dependencies
that are no longer required by other mods are also removed. Config files
are preserved. The mod argument is matched by Owner-Name or just the mod Name.

With --server, the mod is removed directly on the active server via the agent.
Nothing is changed locally.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		serverFlag, _ := cmd.Flags().GetBool("server")

		if serverFlag {
			_, c, err := resolveActiveServer()
			if err != nil {
				return err
			}
			req := agentapi.ModManageRequest{
				Action: "remove",
				Mod: agentapi.ManifestMod{
					DirName: args[0],
				},
			}
			resp, err := c.ManageMod(req)
			if err != nil {
				return fmt.Errorf("server remove failed: %w", err)
			}
			fmt.Printf("\033[32m%s\033[0m\n", resp.Message)
			return nil
		}

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
	removeCmd.Flags().Bool("server", false, "remove the mod directly on the active server via the agent")
}
