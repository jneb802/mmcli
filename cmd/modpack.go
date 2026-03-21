package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"mmcli/internal/config"
	"mmcli/internal/modpack"
	"mmcli/internal/thunderstore"
)

var modpackCmd = &cobra.Command{
	Use:   "modpack",
	Short: "Manage the Thunderstore modpack",
}

var modpackInstallCmd = &cobra.Command{
	Use:   "install <mod>",
	Short: "Add a mod to the modpack manifest",
	Long:  `Look up a mod on Thunderstore and add its dependency string to manifest.json.
Accepts Owner-Name (e.g., 'RandyKnapp-EpicLoot'), Owner-Name-Version, or a Thunderstore URL.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		if err := requireModpackPath(cfg); err != nil {
			return err
		}

		pkg, err := thunderstore.FindPackageByQuery(args[0])
		if err != nil {
			return err
		}

		if len(pkg.Versions) == 0 {
			return fmt.Errorf("package %s has no versions", pkg.FullName)
		}

		depString := fmt.Sprintf("%s-%s-%s", pkg.Owner, pkg.Name, pkg.Versions[0].VersionNumber)

		if err := modpack.AddDep(cfg.ModpackPath, depString); err != nil {
			return err
		}

		fmt.Printf("Added %s to modpack manifest.\n", depString)
		return nil
	},
}

var modpackSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync modpack manifest dependencies to match the current profile",
	Long: `Update the modpack's manifest.json so its dependency list matches the
mods installed in the active profile. Shows a diff of changes before writing.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		if err := requireModpackPath(cfg); err != nil {
			return err
		}

		reg, err := config.LoadRegistry(paths)
		if err != nil {
			return err
		}

		manifest, err := modpack.LoadManifest(cfg.ModpackPath)
		if err != nil {
			return err
		}

		diff := modpack.BuildSyncDiff(&reg, cfg.ActiveProfile, manifest)
		if len(diff) == 0 {
			fmt.Println("Dependencies already match profile.")
			return nil
		}

		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(diff)
		}

		fmt.Printf("Sync %d changes:\n", len(diff))
		printSyncDiff(diff)

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			fmt.Print("\nApply? [y/N] ")
			if !confirmPrompt() {
				return nil
			}
		}

		if err := modpack.SyncManifestDeps(cfg.ModpackPath, &reg, cfg.ActiveProfile); err != nil {
			return err
		}
		fmt.Println("Dependencies synced.")
		return nil
	},
}

var modpackPublishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Publish the modpack to Thunderstore",
	Long: `Build a zip of the modpack directory and upload it to Thunderstore.
Requires thunderstore_token and thunderstore_author in config.json
(or THUNDERSTORE_TOKEN environment variable for the token).
The modpack directory must contain manifest.json, README.md, and icon.png.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		if err := requireModpackPath(cfg); err != nil {
			return err
		}

		token := os.Getenv("THUNDERSTORE_TOKEN")
		if token == "" {
			token = cfg.ThunderstoreToken
		}
		if token == "" {
			return fmt.Errorf("no Thunderstore token — set THUNDERSTORE_TOKEN env var or thunderstore_token in config.json")
		}

		if cfg.ThunderstoreAuthor == "" {
			return fmt.Errorf("no Thunderstore author — set thunderstore_author in config.json")
		}

		if _, err := os.Stat(filepath.Join(cfg.ModpackPath, "icon.png")); err != nil {
			return fmt.Errorf("icon.png is required to publish")
		}

		manifest, err := modpack.LoadManifest(cfg.ModpackPath)
		if err != nil {
			return err
		}

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			fmt.Printf("Publish %s v%s to Thunderstore? [y/N] ", manifest.Name, manifest.VersionNumber)
			if !confirmPrompt() {
				return nil
			}
		}

		if err := thunderstore.Publish(token, cfg.ThunderstoreAuthor, cfg.ModpackPath); err != nil {
			return err
		}

		fmt.Printf("Published %s v%s.\n", manifest.Name, manifest.VersionNumber)
		return nil
	},
}

func requireModpackPath(cfg config.Config) error {
	if cfg.ModpackPath == "" {
		return fmt.Errorf("no modpack path configured — set modpack_path in config.json")
	}
	return nil
}

func init() {
	rootCmd.AddCommand(modpackCmd)

	modpackCmd.AddCommand(modpackInstallCmd)
	modpackCmd.AddCommand(modpackSyncCmd)
	modpackCmd.AddCommand(modpackPublishCmd)

	modpackSyncCmd.Flags().BoolP("yes", "y", false, "skip confirmation prompt")
	modpackPublishCmd.Flags().BoolP("yes", "y", false, "skip confirmation prompt")
}

func printSyncDiff(diff []modpack.SyncDiffItem) {
	for _, d := range diff {
		switch d.Status {
		case "added":
			fmt.Printf("  \033[32m+ %s %s\033[0m\n", d.Name, d.New)
		case "removed":
			fmt.Printf("  \033[31m- %s\033[0m\n", d.Name)
		case "changed":
			fmt.Printf("  \033[33m~ %s %s → %s\033[0m\n", d.Name, d.Old, d.New)
		}
	}
}
