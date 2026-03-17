package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"mmcli/internal/config"
	"mmcli/internal/installer"
	"mmcli/internal/profile"
	"mmcli/internal/thunderstore"
)

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage mod profiles",
}

var profileCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}
		_ = cfg

		name := args[0]
		if err := profile.Create(paths, name); err != nil {
			return err
		}

		// Ensure profile exists in registry
		reg, err := config.LoadRegistry(paths)
		if err != nil {
			return err
		}
		reg.EnsureProfile(name)
		if err := config.SaveRegistry(paths, reg); err != nil {
			return err
		}

		fmt.Printf("\033[32mProfile '%s' created.\033[0m\n", name)
		return nil
	},
}

var profileListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all profiles",
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		names, err := profile.List(paths)
		if err != nil {
			return err
		}

		if len(names) == 0 {
			fmt.Println("No profiles found.")
			return nil
		}

		reg, err := config.LoadRegistry(paths)
		if err != nil {
			return err
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "PROFILE\tMODS\tACTIVE")
		for _, name := range names {
			active := ""
			if name == cfg.ActiveProfile {
				active = "\033[32m*\033[0m"
			}
			modCount := len(reg.ListMods(name))
			fmt.Fprintf(w, "%s\t%d\t%s\n", name, modCount, active)
		}
		w.Flush()
		return nil
	},
}

var profileSwitchCmd = &cobra.Command{
	Use:   "switch <name>",
	Short: "Switch to a different profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		name := args[0]
		if cfg.ActiveProfile == name {
			fmt.Printf("Profile '%s' is already active.\n", name)
			return nil
		}

		if err := profile.Switch(paths, &cfg, name); err != nil {
			return err
		}

		if err := config.Save(paths, cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("\033[32mSwitched to profile '%s'.\033[0m\n", name)
		return nil
	},
}

var profileDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a profile (cannot delete the active profile)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		name := args[0]
		if err := profile.Delete(paths, cfg, name); err != nil {
			return err
		}

		// Remove from registry
		reg, err := config.LoadRegistry(paths)
		if err != nil {
			return err
		}
		delete(reg.Profiles, name)
		if err := config.SaveRegistry(paths, reg); err != nil {
			return err
		}

		fmt.Printf("\033[32mProfile '%s' deleted.\033[0m\n", name)
		return nil
	},
}

var profileImportCmd = &cobra.Command{
	Use:   "import <name> <modpack>",
	Short: "Create a profile from a Thunderstore modpack",
	Long:  "Create a new profile and install all mods from a Thunderstore modpack (e.g., 'mmcli profile import mypack Author-ModpackName')",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		profileName := args[0]
		modpackQuery := args[1]

		// Resolve the modpack package
		fmt.Printf("Resolving modpack '%s'...\n", modpackQuery)
		pkg, err := thunderstore.FindPackageByQuery(modpackQuery)
		if err != nil {
			return err
		}
		if len(pkg.Versions) == 0 {
			return fmt.Errorf("modpack %s has no versions", pkg.FullName)
		}

		deps := pkg.Versions[0].Dependencies
		// Filter out BepInExPack
		var mods []string
		for _, dep := range deps {
			ref := thunderstore.ParseDep(dep)
			if ref.Name == "BepInExPack_Valheim" || ref.Name == "BepInEx_pack" {
				continue
			}
			mods = append(mods, fmt.Sprintf("%s-%s", ref.Owner, ref.Name))
		}

		fmt.Printf("Modpack \033[36m%s-%s\033[0m has %d mods:\n", pkg.Owner, pkg.Name, len(mods))
		for _, m := range mods {
			fmt.Printf("  - %s\n", m)
		}
		fmt.Println()

		// Create profile
		if err := profile.Create(paths, profileName); err != nil {
			return err
		}

		reg, err := config.LoadRegistry(paths)
		if err != nil {
			return err
		}
		reg.EnsureProfile(profileName)

		// Temporarily switch active profile so installer extracts into the new one
		origProfile := cfg.ActiveProfile
		cfg.ActiveProfile = profileName

		// Install each mod
		for _, mod := range mods {
			if err := installer.Install(paths, cfg, &reg, mod); err != nil {
				fmt.Printf("\033[31mWarning: failed to install %s: %v\033[0m\n", mod, err)
			}
		}

		// Restore original active profile
		cfg.ActiveProfile = origProfile
		if err := config.Save(paths, cfg); err != nil {
			return err
		}
		if err := config.SaveRegistry(paths, reg); err != nil {
			return err
		}

		fmt.Printf("\n\033[32mProfile '%s' created with %d mods from %s-%s.\033[0m\n", profileName, len(mods), pkg.Owner, pkg.Name)
		fmt.Printf("Run \033[36mmmcli profile switch %s\033[0m to activate it.\n", profileName)
		return nil
	},
}

var profileOpenCmd = &cobra.Command{
	Use:   "open",
	Short: "Open the active profile folder in Finder",
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		profileDir := paths.ProfileDir(cfg.ActiveProfile)
		fmt.Printf("Opening profile folder for %q: %s\n", cfg.ActiveProfile, profileDir)
		return exec.Command("open", profileDir).Run()
	},
}

func init() {
	rootCmd.AddCommand(profileCmd)
	profileCmd.AddCommand(profileCreateCmd)
	profileCmd.AddCommand(profileListCmd)
	profileCmd.AddCommand(profileSwitchCmd)
	profileCmd.AddCommand(profileDeleteCmd)
	profileCmd.AddCommand(profileImportCmd)
	profileCmd.AddCommand(profileOpenCmd)
}

// loadConfig loads paths and config, ensuring mmcli is initialized.
func loadConfig() (config.Paths, config.Config, error) {
	paths, err := config.DefaultPaths()
	if err != nil {
		return config.Paths{}, config.Config{}, err
	}

	cfg, err := config.EnsureInitialized(paths)
	if err != nil {
		return config.Paths{}, config.Config{}, err
	}

	paths.ValheimDir = cfg.ValheimPath
	return paths, cfg, nil
}
