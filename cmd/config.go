package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"mmcli/internal/agentapi"
	"mmcli/internal/cfgfile"
	"mmcli/internal/client"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage mod config files between local and server",
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "List config files in the local profile or on the server",
	Long: `List config files in the active local profile. Use --server to list
config files on the active remote server instead.
Use --json for machine-readable output.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		server, _ := cmd.Flags().GetBool("server")

		if server {
			_, c, err := resolveActiveServer()
			if err != nil {
				return err
			}
			resp, err := c.ListConfigs()
			if err != nil {
				return err
			}
			if jsonOutput {
				type configListJSON struct {
					Source string   `json:"source"`
					Files  []string `json:"files"`
				}
				return json.NewEncoder(os.Stdout).Encode(configListJSON{Source: "server", Files: resp.Files})
			}
			fmt.Printf("Server config files (%d):\n", len(resp.Files))
			for _, f := range resp.Files {
				fmt.Printf("  %s\n", f)
			}
			return nil
		}

		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		configDir := paths.ProfileConfigDir(cfg.ActiveProfile)
		files, err := cfgfile.ListConfigFiles(configDir)
		if err != nil {
			return fmt.Errorf("failed to list config files: %w", err)
		}

		if jsonOutput {
			type configListJSON struct {
				Source  string   `json:"source"`
				Profile string  `json:"profile"`
				Files   []string `json:"files"`
			}
			return json.NewEncoder(os.Stdout).Encode(configListJSON{Source: "local", Profile: cfg.ActiveProfile, Files: files})
		}

		fmt.Printf("Local config files (%d) [profile: %s]:\n", len(files), cfg.ActiveProfile)
		for _, f := range files {
			tag := ""
			if cfgfile.IsCfg(f) {
				tag = " (cfg)"
			}
			fmt.Printf("  %s%s\n", f, tag)
		}
		return nil
	},
}

var configDiffCmd = &cobra.Command{
	Use:   "diff [filename]",
	Short: "Diff config files between local profile and server",
	Long: `Compare config files between your local profile and the active server.
For .cfg files: shows entry-level diffs with type and default info.
For .yaml/.json files: shows a text diff.
If no filename given, diffs all matching config files.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, c, err := resolveActiveServer()
		if err != nil {
			return err
		}

		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		configDir := paths.ProfileConfigDir(cfg.ActiveProfile)

		// If a specific file is given, diff just that one
		if len(args) == 1 {
			return diffFile(c, configDir, args[0])
		}

		// Otherwise diff all matching files
		return diffAll(c, configDir)
	},
}

var configPushCmd = &cobra.Command{
	Use:   "push [filename]",
	Short: "Push local config to the server",
	Long: `Push config changes from local profile to the active server.
For .cfg files: sends entry-level patches (only changed values).
For .yaml/.json files: sends the entire file (with confirmation).
If no filename given, requires --all flag.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAdmin(); err != nil {
			return err
		}
		all, _ := cmd.Flags().GetBool("all")

		if len(args) == 0 && !all {
			return fmt.Errorf("specify a filename or use --all")
		}

		_, c, err := resolveActiveServer()
		if err != nil {
			return err
		}

		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		configDir := paths.ProfileConfigDir(cfg.ActiveProfile)

		if len(args) == 1 {
			return pushFile(c, configDir, args[0])
		}

		return pushAll(c, configDir)
	},
}

var configPullCmd = &cobra.Command{
	Use:   "pull [filename]",
	Short: "Pull server config files locally",
	Long: `Fetch config files from the active server and write them to the local profile.
Always overwrites the entire file. Shows a diff first if the file exists locally.
If no filename given, requires --all flag.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		all, _ := cmd.Flags().GetBool("all")

		if len(args) == 0 && !all {
			return fmt.Errorf("specify a filename or use --all")
		}

		_, c, err := resolveActiveServer()
		if err != nil {
			return err
		}

		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		configDir := paths.ProfileConfigDir(cfg.ActiveProfile)

		if len(args) == 1 {
			return pullFile(c, configDir, args[0])
		}

		return pullAll(c, configDir)
	},
}

var configOpenCmd = &cobra.Command{
	Use:   "open <mod>",
	Short: "Open a mod's config file or print its path",
	Long: `Find the config file for a mod in the active profile and open it.
The mod argument is matched against config file names. If no matching
config file is found, opens the config directory. Use --path to print
the path instead of opening.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pathOnly, _ := cmd.Flags().GetBool("path")

		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		configDir := paths.ProfileConfigDir(cfg.ActiveProfile)
		entries, err := os.ReadDir(configDir)
		if err != nil {
			return fmt.Errorf("config directory not found: %s", configDir)
		}

		modQuery := strings.ToLower(args[0])
		// Strip owner prefix if given (e.g., "Owner-ModName" → "modname")
		if parts := strings.SplitN(modQuery, "-", 2); len(parts) == 2 {
			modQuery = parts[1]
		}
		modQuery = strings.ToLower(modQuery)

		target := configDir
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if strings.HasSuffix(e.Name(), ".cfg") && strings.Contains(strings.ToLower(e.Name()), modQuery) {
				target = filepath.Join(configDir, e.Name())
				break
			}
		}

		if pathOnly {
			fmt.Println(target)
			return nil
		}

		return exec.Command("open", target).Run()
	},
}

func init() {
	rootCmd.AddCommand(configCmd)

	configCmd.AddCommand(configListCmd)
	configCmd.AddCommand(configDiffCmd)
	configCmd.AddCommand(configPushCmd)
	configCmd.AddCommand(configPullCmd)
	configCmd.AddCommand(configOpenCmd)

	configListCmd.Flags().Bool("server", false, "list server config files instead of local")
	configPushCmd.Flags().Bool("all", false, "push all config files")
	configPullCmd.Flags().Bool("all", false, "pull all config files")
	configOpenCmd.Flags().Bool("path", false, "print the config file path instead of opening it")
}

// diffFile diffs a single config file between local and server.
func diffFile(c *client.AgentClient, configDir, filename string) error {
	localPath := filepath.Join(configDir, filename)
	localData, localErr := os.ReadFile(localPath)

	resp, remoteErr := c.GetConfig(filename)

	if localErr != nil && remoteErr != nil {
		return fmt.Errorf("file not found locally or on server: %s", filename)
	}

	if localErr != nil {
		fmt.Printf("%s: \033[33mserver only\033[0m\n", filename)
		return nil
	}

	if remoteErr != nil {
		fmt.Printf("%s: \033[33mlocal only\033[0m\n", filename)
		return nil
	}

	if cfgfile.IsCfg(filename) {
		return diffCfgFile(filename, localData, []byte(resp.Content))
	}

	return diffTextFile(filename, localData, []byte(resp.Content))
}

// diffAll diffs all matching config files between local and server.
func diffAll(c *client.AgentClient, configDir string) error {
	localFiles, err := cfgfile.ListConfigFiles(configDir)
	if err != nil {
		return fmt.Errorf("failed to list local configs: %w", err)
	}

	remoteResp, err := c.ListConfigs()
	if err != nil {
		return err
	}

	// Build sets
	localSet := make(map[string]bool)
	for _, f := range localFiles {
		localSet[f] = true
	}
	remoteSet := make(map[string]bool)
	for _, f := range remoteResp.Files {
		remoteSet[f] = true
	}

	filesCompared := 0
	cfgEntriesDiffer := 0
	textFilesDiffer := 0
	localOnly := 0
	remoteOnly := 0

	// Diff files that exist on both sides
	for _, f := range localFiles {
		if !remoteSet[f] {
			continue
		}

		localData, err := os.ReadFile(filepath.Join(configDir, f))
		if err != nil {
			continue
		}

		resp, err := c.GetConfig(f)
		if err != nil {
			continue
		}

		filesCompared++

		if cfgfile.IsCfg(f) {
			local, err := cfgfile.ParseBytes(localData)
			if err != nil {
				continue
			}
			remote, err := cfgfile.ParseBytes([]byte(resp.Content))
			if err != nil {
				continue
			}
			diffs := cfgfile.Diff(local, remote)
			if len(diffs) > 0 {
				printCfgDiff(f, diffs)
				cfgEntriesDiffer += countChanged(diffs)
			}
		} else {
			diff := cfgfile.TextDiff("local", "server", localData, []byte(resp.Content))
			if diff != "" {
				fmt.Printf("\033[1m%s:\033[0m\n", f)
				printColoredDiff(diff)
				fmt.Println()
				textFilesDiffer++
			}
		}
	}

	// Report files only on one side
	for _, f := range localFiles {
		if !remoteSet[f] {
			localOnly++
		}
	}
	for _, f := range remoteResp.Files {
		if !localSet[f] {
			remoteOnly++
		}
	}

	// Summary
	fmt.Printf("\n  %d files compared", filesCompared)
	if cfgEntriesDiffer > 0 {
		fmt.Printf(", %d .cfg entries differ", cfgEntriesDiffer)
	}
	if textFilesDiffer > 0 {
		fmt.Printf(", %d other files differ", textFilesDiffer)
	}
	if cfgEntriesDiffer == 0 && textFilesDiffer == 0 {
		fmt.Printf(", no differences")
	}
	if localOnly > 0 {
		fmt.Printf(", %d local only", localOnly)
	}
	if remoteOnly > 0 {
		fmt.Printf(", %d server only", remoteOnly)
	}
	fmt.Println()

	return nil
}

func diffCfgFile(filename string, localData, remoteData []byte) error {
	local, err := cfgfile.ParseBytes(localData)
	if err != nil {
		return fmt.Errorf("failed to parse local %s: %w", filename, err)
	}
	remote, err := cfgfile.ParseBytes(remoteData)
	if err != nil {
		return fmt.Errorf("failed to parse server %s: %w", filename, err)
	}

	diffs := cfgfile.Diff(local, remote)
	if len(diffs) == 0 {
		fmt.Printf("%s: no differences\n", filename)
		return nil
	}

	printCfgDiff(filename, diffs)
	return nil
}

func diffTextFile(filename string, localData, remoteData []byte) error {
	diff := cfgfile.TextDiff("local", "server", localData, remoteData)
	if diff == "" {
		fmt.Printf("%s: no differences\n", filename)
		return nil
	}

	fmt.Printf("\033[1m%s:\033[0m\n", filename)
	printColoredDiff(diff)
	return nil
}

// printColoredDiff prints a unified diff string with ANSI colors.
func printColoredDiff(diff string) {
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "-") {
			fmt.Printf("  \033[31m%s\033[0m\n", line)
		} else if strings.HasPrefix(line, "+") {
			fmt.Printf("  \033[32m%s\033[0m\n", line)
		} else {
			fmt.Printf("  %s\n", line)
		}
	}
}

func printCfgDiff(filename string, diffs []DiffEntry) {
	fmt.Printf("\033[1m%s:\033[0m\n", filename)

	currentSection := ""
	for _, d := range diffs {
		if d.Section != currentSection {
			fmt.Printf("  \033[36m[%s]\033[0m\n", d.Section)
			currentSection = d.Section
		}

		fmt.Printf("    %s\n", d.Key)
		switch d.Status {
		case cfgfile.Changed:
			fmt.Printf("      local:  \033[32m%s\033[0m\n", d.LocalValue)
			fmt.Printf("      server: \033[31m%s\033[0m\n", d.RemoteValue)
		case cfgfile.LocalOnly:
			fmt.Printf("      local:  \033[32m%s\033[0m\n", d.LocalValue)
			fmt.Printf("      server: \033[33m(not present)\033[0m\n")
		case cfgfile.RemoteOnly:
			fmt.Printf("      local:  \033[33m(not present)\033[0m\n")
			fmt.Printf("      server: \033[32m%s\033[0m\n", d.RemoteValue)
		}

		// Show metadata context
		var meta []string
		if d.SettingType != "" {
			meta = append(meta, d.SettingType)
		}
		if d.DefaultValue != "" {
			meta = append(meta, "default: "+d.DefaultValue)
		}
		if len(meta) > 0 {
			fmt.Printf("      (%s)\n", strings.Join(meta, ", "))
		}
	}
	fmt.Println()
}

func countChanged(diffs []DiffEntry) int {
	n := 0
	for _, d := range diffs {
		if d.Status == cfgfile.Changed {
			n++
		}
	}
	return n
}

// pushFile pushes a single config file to the server.
func pushFile(c *client.AgentClient, configDir, filename string) error {
	localPath := filepath.Join(configDir, filename)
	localData, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("local file not found: %s", filename)
	}

	if cfgfile.IsCfg(filename) {
		return pushCfgFile(c, filename, localData)
	}
	return pushTextFile(c, filename, localData)
}

func pushCfgFile(c *client.AgentClient, filename string, localData []byte) error {
	// Fetch server version for diff
	resp, err := c.GetConfig(filename)
	if err != nil {
		// File doesn't exist on server — push the whole file
		fmt.Printf("%s does not exist on server. Push entire file? [y/N] ", filename)
		if !confirmPrompt() {
			return nil
		}
		pushResp, err := c.PushConfigs(agentapi.ConfigPushRequest{
			Files: []agentapi.ConfigFile{{Filename: filename, Content: string(localData)}},
		})
		if err != nil {
			return err
		}
		fmt.Printf("\033[32m%s\033[0m\n", pushResp.Message)
		return nil
	}

	local, err := cfgfile.ParseBytes(localData)
	if err != nil {
		return err
	}
	remote, err := cfgfile.ParseBytes([]byte(resp.Content))
	if err != nil {
		return err
	}

	diffs := cfgfile.Diff(local, remote)

	// Filter to only Changed entries (we push local values to server)
	var patches []agentapi.ConfigPatch
	var changedDiffs []DiffEntry
	for _, d := range diffs {
		if d.Status == cfgfile.Changed {
			patches = append(patches, agentapi.ConfigPatch{
				Filename: filename,
				Section:  d.Section,
				Key:      d.Key,
				Value:    d.LocalValue,
			})
			changedDiffs = append(changedDiffs, d)
		}
	}

	if len(patches) == 0 {
		fmt.Printf("%s: no differences to push\n", filename)
		return nil
	}

	// Show what will change
	printCfgDiff(filename, changedDiffs)
	fmt.Printf("Push %d entries to server? [y/N] ", len(patches))
	if !confirmPrompt() {
		return nil
	}

	pushResp, err := c.PushConfigs(agentapi.ConfigPushRequest{Patches: patches})
	if err != nil {
		return err
	}
	fmt.Printf("\033[32m%s\033[0m\n", pushResp.Message)
	return nil
}

func pushTextFile(c *client.AgentClient, filename string, localData []byte) error {
	// Show diff if server has the file
	resp, err := c.GetConfig(filename)
	if err == nil {
		diff := cfgfile.TextDiff("server", "local (incoming)", []byte(resp.Content), localData)
		if diff == "" {
			fmt.Printf("%s: no differences to push\n", filename)
			return nil
		}
		fmt.Printf("\033[1m%s:\033[0m\n", filename)
		printColoredDiff(diff)
		fmt.Println()
	}

	fmt.Printf("\033[33mThis will replace the entire file on the server.\033[0m Push %s? [y/N] ", filename)
	if !confirmPrompt() {
		return nil
	}

	pushResp, err := c.PushConfigs(agentapi.ConfigPushRequest{
		Files: []agentapi.ConfigFile{{Filename: filename, Content: string(localData)}},
	})
	if err != nil {
		return err
	}
	fmt.Printf("\033[32m%s\033[0m\n", pushResp.Message)
	return nil
}

// pushAll pushes all config files to the server.
func pushAll(c *client.AgentClient, configDir string) error {
	localFiles, err := cfgfile.ListConfigFiles(configDir)
	if err != nil {
		return err
	}

	if len(localFiles) == 0 {
		fmt.Println("No config files to push.")
		return nil
	}

	// Build the push request by diffing each file
	var cfgPatches []agentapi.ConfigPatch
	var wholeFiles []agentapi.ConfigFile
	cfgDiffCount := 0
	textDiffCount := 0

	for _, f := range localFiles {
		localData, err := os.ReadFile(filepath.Join(configDir, f))
		if err != nil {
			continue
		}

		resp, remoteErr := c.GetConfig(f)

		// .cfg files with a server copy: entry-level patch for changed values
		if cfgfile.IsCfg(f) && remoteErr == nil {
			local, err := cfgfile.ParseBytes(localData)
			if err != nil {
				continue
			}
			remote, err := cfgfile.ParseBytes([]byte(resp.Content))
			if err != nil {
				continue
			}
			for _, d := range cfgfile.Diff(local, remote) {
				if d.Status == cfgfile.Changed {
					cfgPatches = append(cfgPatches, agentapi.ConfigPatch{
						Filename: f,
						Section:  d.Section,
						Key:      d.Key,
						Value:    d.LocalValue,
					})
					cfgDiffCount++
				}
			}
			continue
		}

		// Non-.cfg files that match server: only push if different
		if !cfgfile.IsCfg(f) && remoteErr == nil {
			diff := cfgfile.TextDiff("server", "local", []byte(resp.Content), localData)
			if diff == "" {
				continue
			}
		}

		// Everything else: new files on server, or files with differences — push whole file
		wholeFiles = append(wholeFiles, agentapi.ConfigFile{
			Filename: f,
			Content:  string(localData),
		})
		textDiffCount++
	}

	if cfgDiffCount == 0 && textDiffCount == 0 {
		fmt.Println("No differences to push.")
		return nil
	}

	fmt.Printf("Push %d .cfg entry patches and %d file overwrites? [y/N] ", cfgDiffCount, textDiffCount)
	if !confirmPrompt() {
		return nil
	}

	pushResp, err := c.PushConfigs(agentapi.ConfigPushRequest{
		Patches: cfgPatches,
		Files:   wholeFiles,
	})
	if err != nil {
		return err
	}
	fmt.Printf("\033[32m%s\033[0m\n", pushResp.Message)
	return nil
}

// pullFile fetches a single config file from the server.
func pullFile(c *client.AgentClient, configDir, filename string) error {
	resp, err := c.GetConfig(filename)
	if err != nil {
		return fmt.Errorf("file not found on server: %s", filename)
	}

	localPath := filepath.Join(configDir, filename)
	localData, localErr := os.ReadFile(localPath)

	// Show diff if file exists locally
	if localErr == nil {
		if cfgfile.IsCfg(filename) {
			if err := diffCfgFile(filename, localData, []byte(resp.Content)); err != nil {
				return err
			}
		} else {
			diff := cfgfile.TextDiff("local (current)", "server (incoming)", localData, []byte(resp.Content))
			if diff == "" {
				fmt.Printf("%s: no differences\n", filename)
				return nil
			}
			fmt.Printf("\033[1m%s:\033[0m\n", filename)
			printColoredDiff(diff)
		}
		fmt.Printf("\nOverwrite local file with server version? [y/N] ")
		if !confirmPrompt() {
			return nil
		}
	}

	// Write the file
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(localPath, []byte(resp.Content), 0644); err != nil {
		return err
	}
	fmt.Printf("\033[32mPulled %s\033[0m\n", filename)
	return nil
}

// pullAll fetches all server config files locally.
func pullAll(c *client.AgentClient, configDir string) error {
	resp, err := c.ListConfigs()
	if err != nil {
		return err
	}

	if len(resp.Files) == 0 {
		fmt.Println("No config files on server.")
		return nil
	}

	fmt.Printf("Pull %d config files from server? This will overwrite local files. [y/N] ", len(resp.Files))
	if !confirmPrompt() {
		return nil
	}

	pulled := 0
	for _, f := range resp.Files {
		fileResp, err := c.GetConfig(f)
		if err != nil {
			fmt.Printf("  \033[31mfailed to fetch %s: %v\033[0m\n", f, err)
			continue
		}

		localPath := filepath.Join(configDir, f)
		if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
			fmt.Printf("  \033[31mfailed to create dir for %s: %v\033[0m\n", f, err)
			continue
		}
		if err := os.WriteFile(localPath, []byte(fileResp.Content), 0644); err != nil {
			fmt.Printf("  \033[31mfailed to write %s: %v\033[0m\n", f, err)
			continue
		}
		pulled++
	}

	fmt.Printf("\033[32mPulled %d files\033[0m\n", pulled)
	return nil
}

func confirmPrompt() bool {
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes"
}

// Type alias for use in this file
type DiffEntry = cfgfile.DiffEntry
