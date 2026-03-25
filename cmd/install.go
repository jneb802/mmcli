package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"mmcli/internal/agentapi"
	"mmcli/internal/config"
	"mmcli/internal/installer"
	"mmcli/internal/thunderstore"
)

var installCmd = &cobra.Command{
	Use:   "install <mod>",
	Short: "Install a mod and its dependencies into the active profile",
	Long: `Install a mod by Owner-Name (e.g., 'RandyKnapp-EpicLoot'), Thunderstore URL, or local path.

With --server, the mod is installed directly on the active server via the agent
(the server downloads from Thunderstore itself). Nothing is installed locally.`,
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

		// Server-only: tell the agent to download from Thunderstore directly
		if target == "server" {
			_, c, err := resolveActiveServer()
			if err != nil {
				return err
			}
			pkg, err := thunderstore.FindPackageByQuery(args[0])
			if err != nil {
				return err
			}
			if len(pkg.Versions) == 0 {
				return fmt.Errorf("no versions found for %s-%s", pkg.Owner, pkg.Name)
			}
			latest := pkg.Versions[0]
			req := agentapi.ModManageRequest{
				Action: "add",
				Mod: agentapi.ManifestMod{
					DirName: fmt.Sprintf("%s-%s", pkg.Owner, pkg.Name),
					Owner:   pkg.Owner,
					Name:    pkg.Name,
					Version: latest.VersionNumber,
					Source:  "thunderstore",
				},
			}
			resp, err := c.ManageMod(req)
			if err != nil {
				return fmt.Errorf("server install failed: %w", err)
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

		// Local path — copy files directly
		if installer.IsLocalPath(args[0]) {
			if err := installer.InstallLocal(paths, cfg, &reg, args[0], target); err != nil {
				return err
			}
			return config.SaveRegistry(paths, reg)
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
	installCmd.Flags().Bool("server", false, "install directly on the active server via the agent (mod is not installed locally)")
}
