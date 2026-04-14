package cmd

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"mmcli/internal/config"
	"mmcli/internal/installer"
	"mmcli/internal/platform"
	"mmcli/internal/profile"
	"mmcli/internal/thunderstore"
)

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage mod profiles",
	Long: `Manage mod profiles. Each profile is an isolated set of mods and configs.
Only one profile is active at a time; the active profile is what BepInEx loads.`,
}

var profileCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new profile",
	Long: `Create a new empty mod profile. The profile is not activated automatically;
use 'mmcli profile switch' to make it active.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, _, err := loadConfig()
		if err != nil {
			return err
		}

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
	Long: `List all profiles with their mod count. The active profile is marked with *.
Use --json for machine-readable output.`,
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
			if jsonOutput {
				fmt.Println("[]")
			} else {
				fmt.Println("No profiles found.")
			}
			return nil
		}

		reg, err := config.LoadRegistry(paths)
		if err != nil {
			return err
		}

		if jsonOutput {
			type profileJSON struct {
				Name   string `json:"name"`
				Mods   int    `json:"mods"`
				Active bool   `json:"active"`
			}
			items := make([]profileJSON, len(names))
			for i, name := range names {
				items[i] = profileJSON{
					Name:   name,
					Mods:   len(reg.ListMods(name)),
					Active: name == cfg.ActiveProfile,
				}
			}
			return json.NewEncoder(os.Stdout).Encode(items)
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
	Long: `Switch the active profile. This updates BepInEx symlinks so the new profile's
mods and configs are loaded on next game launch.`,
	Args: cobra.ExactArgs(1),
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
	Long: `Delete a profile and all its mod files. The currently active profile cannot
be deleted; switch to a different profile first.`,
	Args: cobra.ExactArgs(1),
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
		delete(reg.Settings, name)
		if err := config.SaveRegistry(paths, reg); err != nil {
			return err
		}

		fmt.Printf("\033[32mProfile '%s' deleted.\033[0m\n", name)
		return nil
	},
}

var profileImportCmd = &cobra.Command{
	Use:   "import <name> <modpack> | import <profile-code>",
	Short: "Create a profile from a modpack or profile code",
	Long: `Create a new profile and install mods from a Thunderstore modpack or profile code.

Examples:
  mmcli profile import mypack Author-ModpackName
  mmcli profile import a1b2c3d4-e5f6-7890-abcd-ef1234567890`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		// Profile code import (single UUID argument)
		if len(args) == 1 && thunderstore.IsProfileCode(args[0]) {
			return importProfileCode(paths, cfg, args[0])
		}

		if len(args) != 2 {
			return fmt.Errorf("expected <name> <modpack> or a profile code UUID")
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
			if err := installer.Install(paths, cfg, &reg, mod, "both"); err != nil {
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

func importProfileCode(paths config.Paths, cfg config.Config, code string) error {
	fmt.Printf("Fetching profile code %s...\n", code)
	profileName, mods, zipData, err := thunderstore.FetchProfileCode(code)
	if err != nil {
		return err
	}

	// Filter out BepInExPack
	var filtered []thunderstore.ProfileMod
	for _, m := range mods {
		parts := strings.SplitN(m.Name, "-", 2)
		if len(parts) == 2 && (parts[1] == "BepInExPack_Valheim" || parts[1] == "BepInEx_pack") {
			continue
		}
		filtered = append(filtered, m)
	}

	fmt.Printf("Profile \033[36m%s\033[0m has %d mods:\n", profileName, len(filtered))
	for _, m := range filtered {
		status := ""
		if !m.Enabled {
			status = " (disabled)"
		}
		fmt.Printf("  - %s v%s%s\n", m.Name, m.Version, status)
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

	origProfile := cfg.ActiveProfile
	cfg.ActiveProfile = profileName

	// Install each mod
	for _, m := range filtered {
		if err := installer.Install(paths, cfg, &reg, m.Name, "both"); err != nil {
			fmt.Printf("\033[31mWarning: failed to install %s: %v\033[0m\n", m.Name, err)
			continue
		}
		// Toggle to disabled if the profile had it disabled
		if !m.Enabled {
			if err := installer.Toggle(paths, cfg, &reg, m.Name); err != nil {
				fmt.Printf("\033[31mWarning: failed to disable %s: %v\033[0m\n", m.Name, err)
			}
		}
	}

	// Extract config files from the zip
	extractProfileConfigs(paths, profileName, zipData)

	// Restore original active profile
	cfg.ActiveProfile = origProfile
	if err := config.Save(paths, cfg); err != nil {
		return err
	}
	if err := config.SaveRegistry(paths, reg); err != nil {
		return err
	}

	fmt.Printf("\n\033[32mProfile '%s' created with %d mods from profile code.\033[0m\n", profileName, len(filtered))
	fmt.Printf("Run \033[36mmmcli profile switch %s\033[0m to activate it.\n", profileName)
	return nil
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
		return platform.OpenPath(profileDir)
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

// extractProfileConfigs extracts config files from a profile code zip into the profile's config dir.
func extractProfileConfigs(paths config.Paths, profileName string, zipData []byte) {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return
	}

	configDir := paths.ProfileConfigDir(profileName)
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := filepath.ToSlash(f.Name)
		// Skip the manifest
		if name == "export.r2x" {
			continue
		}
		// Extract config/ prefixed files into the profile config dir
		if strings.HasPrefix(name, "config/") {
			rel := name[len("config/"):]
			dest := filepath.Join(configDir, rel)
			os.MkdirAll(filepath.Dir(dest), 0755)
			rc, err := f.Open()
			if err != nil {
				continue
			}
			out, err := os.Create(dest)
			if err != nil {
				rc.Close()
				continue
			}
			io.Copy(out, rc)
			out.Close()
			rc.Close()
		}
	}
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

// loadConfigWithRegistry loads config, registry, and runs the per-profile settings migration.
func loadConfigWithRegistry() (config.Paths, config.Config, *config.Registry, error) {
	paths, cfg, err := loadConfig()
	if err != nil {
		return config.Paths{}, config.Config{}, nil, err
	}
	reg, err := config.LoadRegistry(paths)
	if err != nil {
		return config.Paths{}, config.Config{}, nil, err
	}
	cfgDirty, regDirty := config.MigrateProfileSettings(&cfg, &reg, cfg.ActiveProfile)
	if cfgDirty {
		config.Save(paths, cfg)
	}
	if regDirty {
		config.SaveRegistry(paths, reg)
	}
	return paths, cfg, &reg, nil
}
