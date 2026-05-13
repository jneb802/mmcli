package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"mmcli/internal/agentapi"
	"mmcli/internal/client"
	"mmcli/internal/config"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage remote dedicated servers (Valheim only for now)",
	Long: `Manage remote dedicated servers via the mmcli-agent.
The agent must be running on the server. Use 'server add' to register
a server, then use other subcommands to control it.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return requireAgentCapability()
	},
}

var serverAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Register a remote server and set it as active",
	Long: `Register a remote server by name and set it as the active server.
Requires --host and --secret flags. Validates connectivity before saving.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		host, _ := cmd.Flags().GetString("host")
		port, _ := cmd.Flags().GetInt("port")
		secret, _ := cmd.Flags().GetString("secret")

		if host == "" {
			return fmt.Errorf("--host is required")
		}
		if secret == "" {
			return fmt.Errorf("--secret is required")
		}

		paths, cfg, err := loadServerConfig()
		if err != nil {
			return err
		}

		if cfg.Servers == nil {
			cfg.Servers = make(map[string]config.ServerEntry)
		}

		// Validate connectivity
		c := client.New(host, port, secret)
		fmt.Printf("Connecting to %s:%d...\n", host, port)
		status, err := c.Status()
		if err != nil {
			return fmt.Errorf("could not reach agent: %w", err)
		}

		role := status.Role
		if role == "" {
			role = agentapi.RoleAdmin
		}

		cfg.Servers[name] = config.ServerEntry{
			Host:   host,
			Port:   port,
			Secret: secret,
			Role:   role,
		}

		if err := config.Save(paths, cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		// Set as active server for current profile
		reg, err := config.LoadRegistry(paths, cfg.ActiveGame)
		if err != nil {
			return fmt.Errorf("failed to load registry: %w", err)
		}
		ps := reg.GetSettings(cfg.ActiveProfile)
		ps.Server = name
		reg.SetSettings(cfg.ActiveProfile, ps)
		if err := config.SaveRegistry(paths, reg); err != nil {
			return fmt.Errorf("failed to save registry: %w", err)
		}

		fmt.Printf("\033[32mServer '%s' added as %s and set as active.\033[0m\n", name, role)
		printStatus(name, status, nil)
		return nil
	},
}

var serverListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered servers",
	Long: `List all registered servers with their host, port, and active status.
Use --json for machine-readable output.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, err := loadServerConfig()
		if err != nil {
			return err
		}

		if len(cfg.Servers) == 0 {
			if jsonOutput {
				fmt.Println("[]")
			} else {
				fmt.Println("No servers registered. Run `mmcli server add` to add one.")
			}
			return nil
		}

		reg, _ := config.LoadRegistry(paths, cfg.ActiveGame)
		ps := reg.GetSettings(cfg.ActiveProfile)

		if jsonOutput {
			type serverJSON struct {
				Name   string `json:"name"`
				Host   string `json:"host"`
				Port   int    `json:"port"`
				Role   string `json:"role"`
				Active bool   `json:"active"`
			}
			var items []serverJSON
			for name, srv := range cfg.Servers {
				role := srv.Role
				if role == "" {
					role = "admin"
				}
				items = append(items, serverJSON{
					Name:   name,
					Host:   srv.Host,
					Port:   srv.Port,
					Role:   role,
					Active: name == ps.Server,
				})
			}
			return json.NewEncoder(os.Stdout).Encode(items)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tHOST\tPORT\tROLE\tACTIVE")
		for name, srv := range cfg.Servers {
			active := ""
			if name == ps.Server {
				active = "\033[32m*\033[0m"
			}
			role := srv.Role
			if role == "" {
				role = "admin"
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", name, srv.Host, srv.Port, role, active)
		}
		w.Flush()
		return nil
	},
}

var serverSwitchCmd = &cobra.Command{
	Use:   "switch <name>",
	Short: "Switch the active server",
	Long:  `Set a different registered server as the active target for all server commands.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		paths, cfg, err := loadServerConfig()
		if err != nil {
			return err
		}

		if _, ok := cfg.Servers[name]; !ok {
			return fmt.Errorf("server '%s' not found", name)
		}

		reg, err := config.LoadRegistry(paths, cfg.ActiveGame)
		if err != nil {
			return fmt.Errorf("failed to load registry: %w", err)
		}
		ps := reg.GetSettings(cfg.ActiveProfile)
		ps.Server = name
		reg.SetSettings(cfg.ActiveProfile, ps)
		if err := config.SaveRegistry(paths, reg); err != nil {
			return fmt.Errorf("failed to save registry: %w", err)
		}

		fmt.Printf("\033[32mSwitched to server '%s'.\033[0m\n", name)
		return nil
	},
}

var serverRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Forget a registered server",
	Long:  `Remove a server from the local config. If the removed server was active, another server is selected automatically.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		paths, cfg, err := loadServerConfig()
		if err != nil {
			return err
		}

		if _, ok := cfg.Servers[name]; !ok {
			return fmt.Errorf("server '%s' not found", name)
		}

		delete(cfg.Servers, name)

		// Update profile settings if removed server was active for this profile
		reg, err := config.LoadRegistry(paths, cfg.ActiveGame)
		if err != nil {
			return fmt.Errorf("failed to load registry: %w", err)
		}
		ps := reg.GetSettings(cfg.ActiveProfile)
		if ps.Server == name {
			ps.Server = ""
			for k := range cfg.Servers {
				ps.Server = k
				break
			}
			reg.SetSettings(cfg.ActiveProfile, ps)
			config.SaveRegistry(paths, reg)
		}

		if err := config.Save(paths, cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("\033[32mServer '%s' removed.\033[0m\n", name)
		return nil
	},
}

var serverStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the active server's status",
	Long: `Query the active server's agent and display running state, uptime,
BepInEx status, and installed mods. Use --json for machine-readable output.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, c, err := resolveActiveServer()
		if err != nil {
			return err
		}

		status, err := c.Status()
		if err != nil {
			return err
		}

		modsResp, _ := c.ListMods() // best-effort enrichment

		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(status)
		}

		printStatus(name, status, modsResp)
		return nil
	},
}

var serverStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Valheim server",
	Long:  `Send a start command to the active server's agent. Returns immediately after the server begins starting.`,
	RunE:  func(cmd *cobra.Command, args []string) error {
		if err := requireAdmin(); err != nil {
			return err
		}
		name, c, err := resolveActiveServer()
		if err != nil {
			return err
		}

		fmt.Printf("Starting server '%s'...\n", name)
		resp, err := c.Start()
		if err != nil {
			return err
		}
		fmt.Printf("\033[32m%s\033[0m\n", resp.Message)
		return nil
	},
}

var serverStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the Valheim server",
	Long:  `Send a stop command to the active server's agent. Returns after the server process is terminated.`,
	RunE:  func(cmd *cobra.Command, args []string) error {
		if err := requireAdmin(); err != nil {
			return err
		}
		name, c, err := resolveActiveServer()
		if err != nil {
			return err
		}

		fmt.Printf("Stopping server '%s'...\n", name)
		resp, err := c.Stop()
		if err != nil {
			return err
		}
		fmt.Printf("\033[32m%s\033[0m\n", resp.Message)
		return nil
	},
}

var serverRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the Valheim server",
	Long:  `Stop and restart the Valheim server via the active server's agent.`,
	RunE:  func(cmd *cobra.Command, args []string) error {
		if err := requireAdmin(); err != nil {
			return err
		}
		name, c, err := resolveActiveServer()
		if err != nil {
			return err
		}

		fmt.Printf("Restarting server '%s'...\n", name)
		resp, err := c.Restart()
		if err != nil {
			return err
		}
		fmt.Printf("\033[32m%s\033[0m\n", resp.Message)
		return nil
	},
}

var serverLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "View server logs",
	Long: `Fetch and display server logs from the active server's agent.
Shows the last N lines (default 100). Use -f to stream new lines continuously.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		lines, _ := cmd.Flags().GetInt("lines")
		follow, _ := cmd.Flags().GetBool("follow")

		_, c, err := resolveActiveServer()
		if err != nil {
			return err
		}

		body, err := c.Logs(lines, follow)
		if err != nil {
			return err
		}
		defer body.Close()

		if follow {
			// Stream until Ctrl+C
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigCh
				body.Close()
			}()
		}

		io.Copy(os.Stdout, body)
		return nil
	},
}

var serverUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update the server-side agent to the latest release",
	Long: `Download and install the latest mmcli-agent binary from GitHub Releases
on the remote server. The agent restarts automatically after updating.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAdmin(); err != nil {
			return err
		}
		_, c, err := resolveActiveServer()
		if err != nil {
			return err
		}

		status, err := c.Status()
		if err != nil {
			return err
		}
		fmt.Printf("Agent version: %s, checking for updates...\n", status.Version)

		resp, err := c.Update()
		if err != nil {
			// Connection reset is expected — agent re-execs after update
			fmt.Println("Update in progress (agent is restarting)...")
			time.Sleep(2 * time.Second)

			newStatus, err := c.Status()
			if err != nil {
				return fmt.Errorf("agent did not come back after update: %w", err)
			}
			fmt.Printf("\033[32mAgent updated: %s → %s\033[0m\n", status.Version, newStatus.Version)
			return nil
		}

		if resp.Message == "already up to date" {
			fmt.Printf("Agent is already up to date (%s).\n", resp.NewVersion)
			return nil
		}

		fmt.Printf("\033[32mAgent updated: %s → %s\033[0m\n", resp.OldVersion, resp.NewVersion)

		// Wait for restart and verify
		time.Sleep(2 * time.Second)
		newStatus, err := c.Status()
		if err != nil {
			fmt.Println("Warning: could not verify agent restarted.")
		} else {
			fmt.Printf("Agent confirmed running: %s\n", newStatus.Version)
		}
		return nil
	},
}

var serverSettingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Show the server's world settings",
	Long: `Fetch and display the active server's world configuration including
core settings, backup schedule, world modifiers, and permissions.
Use --json for machine-readable output.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, c, err := resolveActiveServer()
		if err != nil {
			return err
		}

		settings, err := c.GetSettings()
		if err != nil {
			return err
		}

		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(settings)
		}

		printSettings(name, settings)
		return nil
	},
}

var serverSettingsSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Update server world settings",
	Long: `Update one or more server settings. Only explicitly provided flags are changed.
Changes are written to the start script and take effect on next server restart.

Examples:
  mmcli server settings set --world "newWorld"
  mmcli server settings set --name "My Server" --public 1
  mmcli server settings set --preset hard --modifier "raids=none"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAdmin(); err != nil {
			return err
		}
		_, c, err := resolveActiveServer()
		if err != nil {
			return err
		}

		req := &agentapi.SettingsUpdateRequest{}
		anyChanged := false

		// String flags
		for _, sf := range []struct {
			name string
			dest **string
		}{
			{"name", &req.Name},
			{"world", &req.World},
			{"password", &req.Password},
			{"savedir", &req.SaveDir},
			{"logfile", &req.LogFile},
			{"instanceid", &req.InstanceID},
			{"preset", &req.Preset},
		} {
			if cmd.Flags().Changed(sf.name) {
				v, _ := cmd.Flags().GetString(sf.name)
				*sf.dest = &v
				anyChanged = true
			}
		}

		// Int flags
		for _, nf := range []struct {
			name string
			dest **int
		}{
			{"port", &req.Port},
			{"public", &req.Public},
			{"saveinterval", &req.SaveInterval},
			{"backups", &req.Backups},
			{"backupshort", &req.BackupShort},
			{"backuplong", &req.BackupLong},
		} {
			if cmd.Flags().Changed(nf.name) {
				v, _ := cmd.Flags().GetInt(nf.name)
				*nf.dest = &v
				anyChanged = true
			}
		}

		// Bool flag
		if cmd.Flags().Changed("crossplay") {
			v, _ := cmd.Flags().GetBool("crossplay")
			req.Crossplay = &v
			anyChanged = true
		}

		// StringSlice: modifiers (key=value format)
		if cmd.Flags().Changed("modifier") {
			mods, _ := cmd.Flags().GetStringSlice("modifier")
			req.Modifiers = make(map[string]string)
			for _, m := range mods {
				parts := strings.SplitN(m, "=", 2)
				if len(parts) == 2 {
					req.Modifiers[strings.ToLower(parts[0])] = strings.ToLower(parts[1])
				} else {
					return fmt.Errorf("invalid modifier format %q, expected key=value", m)
				}
			}
			anyChanged = true
		}

		// StringSlice: setkeys
		if cmd.Flags().Changed("setkey") {
			keys, _ := cmd.Flags().GetStringSlice("setkey")
			req.SetKeys = keys
			anyChanged = true
		}

		if !anyChanged {
			return fmt.Errorf("no settings specified. Use flags like --world, --name, --preset, etc")
		}

		resp, err := c.UpdateSettings(req)
		if err != nil {
			return err
		}

		fmt.Printf("\033[32m%s\033[0m\n", resp.Message)
		fmt.Println("Restart the server for changes to take effect.")
		return nil
	},
}

var serverProfileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage server mod profiles",
	Long: `Manage mod profiles on the active server. Each profile is an isolated set of mods.
Only one profile is active at a time; the active profile is what BepInEx loads.`,
}

var serverProfileListCmd = &cobra.Command{
	Use:   "list",
	Short: "List server profiles",
	Long: `List all profiles on the active server with their mod count.
The active profile is marked with *. Use --json for machine-readable output.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, c, err := resolveActiveServer()
		if err != nil {
			return err
		}

		resp, err := c.ListProfiles()
		if err != nil {
			return err
		}

		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(resp)
		}

		if len(resp.Profiles) == 0 {
			fmt.Println("No profiles found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "PROFILE\tMODS\tACTIVE")
		for _, p := range resp.Profiles {
			active := ""
			if p.Name == resp.Active {
				active = "\033[32m*\033[0m"
			}
			fmt.Fprintf(w, "%s\t%d\t%s\n", p.Name, p.ModCount, active)
		}
		w.Flush()
		return nil
	},
}

var serverProfileCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new server profile",
	Long: `Create a new empty mod profile on the server. Use --copy-from to clone
an existing profile's mods into the new one.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAdmin(); err != nil {
			return err
		}
		_, c, err := resolveActiveServer()
		if err != nil {
			return err
		}

		copyFrom, _ := cmd.Flags().GetString("copy-from")
		req := agentapi.ProfileCreateRequest{
			Name:     args[0],
			CopyFrom: copyFrom,
		}

		resp, err := c.CreateProfile(req)
		if err != nil {
			return err
		}
		fmt.Printf("\033[32m%s\033[0m\n", resp.Message)
		return nil
	},
}

var serverProfileSwitchCmd = &cobra.Command{
	Use:   "switch <name>",
	Short: "Switch the active server profile",
	Long: `Switch which mod profile is active on the server. The server should be stopped
before switching profiles. Use --force to switch while the server is running.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAdmin(); err != nil {
			return err
		}
		_, c, err := resolveActiveServer()
		if err != nil {
			return err
		}

		force, _ := cmd.Flags().GetBool("force")
		resp, err := c.ActivateProfile(args[0], force)
		if err != nil {
			return err
		}
		fmt.Printf("\033[32m%s\033[0m\n", resp.Message)
		return nil
	},
}

var serverProfileDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a server profile",
	Long:  `Delete a mod profile from the server. The active profile cannot be deleted.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAdmin(); err != nil {
			return err
		}
		_, c, err := resolveActiveServer()
		if err != nil {
			return err
		}

		resp, err := c.DeleteProfile(args[0])
		if err != nil {
			return err
		}
		fmt.Printf("\033[32m%s\033[0m\n", resp.Message)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(serverCmd)

	serverCmd.AddCommand(serverAddCmd)
	serverCmd.AddCommand(serverListCmd)
	serverCmd.AddCommand(serverSwitchCmd)
	serverCmd.AddCommand(serverRemoveCmd)
	serverCmd.AddCommand(serverStatusCmd)
	serverCmd.AddCommand(serverStartCmd)
	serverCmd.AddCommand(serverStopCmd)
	serverCmd.AddCommand(serverRestartCmd)
	serverCmd.AddCommand(serverLogsCmd)
	serverCmd.AddCommand(serverSettingsCmd)
	serverCmd.AddCommand(serverUpdateCmd)
	serverCmd.AddCommand(serverProfileCmd)
	serverSettingsCmd.AddCommand(serverSettingsSetCmd)
	serverProfileCmd.AddCommand(serverProfileListCmd)
	serverProfileCmd.AddCommand(serverProfileCreateCmd)
	serverProfileCmd.AddCommand(serverProfileSwitchCmd)
	serverProfileCmd.AddCommand(serverProfileDeleteCmd)

	// settings set flags
	serverSettingsSetCmd.Flags().String("name", "", "server name")
	serverSettingsSetCmd.Flags().Int("port", 0, "server port")
	serverSettingsSetCmd.Flags().String("world", "", "world name (new name creates a new world)")
	serverSettingsSetCmd.Flags().String("password", "", "server password")
	serverSettingsSetCmd.Flags().String("savedir", "", "save directory path")
	serverSettingsSetCmd.Flags().Int("public", 0, "server visibility (0 or 1)")
	serverSettingsSetCmd.Flags().String("logfile", "", "log file path")
	serverSettingsSetCmd.Flags().String("instanceid", "", "instance ID for multiple servers")
	serverSettingsSetCmd.Flags().Int("saveinterval", 0, "save interval in seconds")
	serverSettingsSetCmd.Flags().Int("backups", 0, "number of automatic backups")
	serverSettingsSetCmd.Flags().Int("backupshort", 0, "short backup interval in seconds")
	serverSettingsSetCmd.Flags().Int("backuplong", 0, "long backup interval in seconds")
	serverSettingsSetCmd.Flags().Bool("crossplay", false, "enable crossplay")
	serverSettingsSetCmd.Flags().String("preset", "", "world modifier preset (Normal, Casual, Easy, Hard, Hardcore, Immersive, Hammer)")
	serverSettingsSetCmd.Flags().StringSlice("modifier", nil, "world modifier as key=value (e.g. raids=none)")
	serverSettingsSetCmd.Flags().StringSlice("setkey", nil, "world modifier key (e.g. nomap)")

	serverAddCmd.Flags().String("host", "", "server hostname or IP (required)")
	serverAddCmd.Flags().Int("port", agentapi.DefaultPort, "agent port")
	serverAddCmd.Flags().String("secret", "", "agent API secret (required)")
	serverAddCmd.MarkFlagRequired("host")
	serverAddCmd.MarkFlagRequired("secret")

	serverLogsCmd.Flags().Int("lines", 100, "number of log lines to show")
	serverLogsCmd.Flags().BoolP("follow", "f", false, "stream new log lines")

	serverProfileCreateCmd.Flags().String("copy-from", "", "copy mods from an existing profile")
	serverProfileSwitchCmd.Flags().Bool("force", false, "switch even if server is running")
}

// requireAdmin returns an error if the active server's cached role is not admin.
func requireAdmin() error {
	paths, cfg, err := loadServerConfig()
	if err != nil {
		return err
	}
	reg, _ := config.LoadRegistry(paths, cfg.ActiveGame)
	ps := reg.GetSettings(cfg.ActiveProfile)
	if ps.Server == "" {
		return nil // resolveActiveServer will handle this
	}
	if srv, ok := cfg.Servers[ps.Server]; ok && srv.Role == agentapi.RolePlayer {
		return fmt.Errorf("this action requires admin access (your key has player role)")
	}
	return nil
}

// resolveActiveServer loads config and returns a client for the active server.
func resolveActiveServer() (string, *client.AgentClient, error) {
	paths, cfg, err := loadServerConfig()
	if err != nil {
		return "", nil, err
	}

	reg, err := config.LoadRegistry(paths, cfg.ActiveGame)
	if err != nil {
		return "", nil, err
	}
	ps := reg.GetSettings(cfg.ActiveProfile)

	if ps.Server == "" {
		return "", nil, fmt.Errorf("no active server for profile '%s'. Run `mmcli server add` to register one", cfg.ActiveProfile)
	}

	srv, ok := cfg.Servers[ps.Server]
	if !ok {
		return "", nil, fmt.Errorf("active server '%s' not found in config", ps.Server)
	}

	return ps.Server, client.New(srv.Host, srv.Port, srv.Secret), nil
}

// loadServerConfig loads paths and config without requiring mmcli init.
// Server commands only need the config file to exist, not full initialization.
func loadServerConfig() (config.Paths, config.Config, error) {
	paths, err := config.DefaultPaths()
	if err != nil {
		return config.Paths{}, config.Config{}, err
	}

	cfg, err := config.Load(paths)
	if err != nil {
		// If config doesn't exist yet, return empty config
		if os.IsNotExist(err) {
			os.MkdirAll(paths.ConfigDir, 0755)
			return paths, config.Config{}, nil
		}
		return config.Paths{}, config.Config{}, err
	}

	return paths, cfg, nil
}

func printSettings(name string, s *agentapi.SettingsResponse) {
	fmt.Printf("\n  \033[1mServer Settings: %s\033[0m\n\n", name)

	// Core
	fmt.Printf("  \033[36mCore\033[0m\n")
	fmt.Printf("    Name:       %s\n", s.Name)
	fmt.Printf("    Port:       %d\n", s.Port)
	fmt.Printf("    World:      %s\n", s.World)
	fmt.Printf("    Password:   ***\n")
	if s.Public == 1 {
		fmt.Printf("    Public:     \033[32myes\033[0m\n")
	} else {
		fmt.Printf("    Public:     \033[2mno\033[0m\n")
	}
	fmt.Printf("    Save Dir:   %s\n", s.SaveDir)
	if s.LogFile != "" {
		fmt.Printf("    Log File:   %s\n", s.LogFile)
	}
	if s.InstanceID != "" {
		fmt.Printf("    Instance:   %s\n", s.InstanceID)
	}
	fmt.Println()

	// Backup
	fmt.Printf("  \033[36mBackup\033[0m\n")
	fmt.Printf("    Save Interval:   %s\n", formatSettingSeconds(s.SaveInterval, 1800))
	fmt.Printf("    Backups:         %s\n", formatSettingDefault(s.Backups, 4))
	fmt.Printf("    Short Interval:  %s\n", formatSettingSeconds(s.BackupShort, 7200))
	fmt.Printf("    Long Interval:   %s\n", formatSettingSeconds(s.BackupLong, 43200))
	fmt.Println()

	// World
	fmt.Printf("  \033[36mWorld\033[0m\n")
	if s.Crossplay {
		fmt.Printf("    Crossplay:  \033[32myes\033[0m\n")
	} else {
		fmt.Printf("    Crossplay:  \033[2mno\033[0m\n")
	}
	if s.Preset != "" {
		fmt.Printf("    Preset:     %s\n", s.Preset)
	} else {
		fmt.Printf("    Preset:     \033[2mnone\033[0m\n")
	}
	if len(s.Modifiers) > 0 {
		fmt.Printf("    Modifiers:\n")
		for k, v := range s.Modifiers {
			fmt.Printf("      %-14s %s\n", k, v)
		}
	}
	if len(s.SetKeys) > 0 {
		fmt.Printf("    Keys:       %s\n", strings.Join(s.SetKeys, ", "))
	}
	fmt.Println()

	// Permissions
	if len(s.Admins) > 0 || len(s.Banned) > 0 || len(s.Permitted) > 0 {
		fmt.Printf("  \033[36mPermissions\033[0m\n")
		if len(s.Admins) > 0 {
			fmt.Printf("    Admins:     %d entries\n", len(s.Admins))
		}
		if len(s.Banned) > 0 {
			fmt.Printf("    Banned:     %d entries\n", len(s.Banned))
		}
		if len(s.Permitted) > 0 {
			fmt.Printf("    Permitted:  %d entries\n", len(s.Permitted))
		}
		fmt.Println()
	}
}

func formatSettingSeconds(val, defaultVal int) string {
	if val == 0 {
		return fmt.Sprintf("\033[2mnot set (default: %s)\033[0m", humanDuration(defaultVal))
	}
	s := humanDuration(val)
	if val == defaultVal {
		return fmt.Sprintf("%s \033[2m(default)\033[0m", s)
	}
	return s
}

func formatSettingDefault(val, defaultVal int) string {
	if val == 0 {
		return fmt.Sprintf("\033[2mnot set (default: %d)\033[0m", defaultVal)
	}
	if val == defaultVal {
		return fmt.Sprintf("%d \033[2m(default)\033[0m", val)
	}
	return fmt.Sprintf("%d", val)
}

func humanDuration(seconds int) string {
	if seconds >= 3600 && seconds%3600 == 0 {
		return fmt.Sprintf("%dh", seconds/3600)
	}
	if seconds >= 60 && seconds%60 == 0 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	return fmt.Sprintf("%ds", seconds)
}

func printStatus(name string, s *agentapi.StatusResponse, modsResp *agentapi.ModListResponse) {
	status := "\033[31mstopped\033[0m"
	if s.Running {
		status = fmt.Sprintf("\033[32mrunning\033[0m (%s)", s.Uptime)
	}

	bepinex := "\033[31mnot installed\033[0m"
	if s.BepInEx {
		bepinex = "\033[32minstalled\033[0m"
	}

	role := s.Role
	if role == "" {
		role = "admin"
	}

	fmt.Printf("\n  Server:  %s\n", name)
	fmt.Printf("  Role:    %s\n", role)
	fmt.Printf("  Status:  %s\n", status)
	fmt.Printf("  BepInEx: %s\n", bepinex)
	if s.ActiveProfile != "" {
		fmt.Printf("  Profile: %s\n", s.ActiveProfile)
	}
	fmt.Printf("  Mods:    %d\n", s.ModCount)

	// Use enriched mod list if available, otherwise fall back to basic names
	if modsResp != nil && len(modsResp.Mods) > 0 {
		if modsResp.ManifestTime != "" {
			fmt.Printf("  Pushed:  %s\n", modsResp.ManifestTime)
		}
		for _, mod := range modsResp.Mods {
			version := ""
			if mod.Version != "" {
				version = " \033[2mv" + mod.Version + "\033[0m"
			}
			fmt.Printf("           - %s%s\n", mod.Name, version)
		}
	} else if len(s.Mods) > 0 {
		for _, m := range s.Mods {
			fmt.Printf("           - %s\n", m)
		}
	}
	fmt.Println()
}

