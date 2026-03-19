package cmd

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"mmcli/internal/agentapi"
	"mmcli/internal/client"
	"mmcli/internal/config"
	"mmcli/internal/profile"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage remote Valheim dedicated servers",
}

var serverAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Register a remote server and set it as active",
	Args:  cobra.ExactArgs(1),
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

		cfg.Servers[name] = config.ServerEntry{
			Host:   host,
			Port:   port,
			Secret: secret,
		}
		cfg.ActiveServer = name

		if err := config.Save(paths, cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("\033[32mServer '%s' added and set as active.\033[0m\n", name)
		printStatus(name, status)
		return nil
	},
}

var serverListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered servers",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, err := loadServerConfig()
		if err != nil {
			return err
		}

		if len(cfg.Servers) == 0 {
			fmt.Println("No servers registered. Run `mmcli server add` to add one.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tHOST\tPORT\tACTIVE")
		for name, srv := range cfg.Servers {
			active := ""
			if name == cfg.ActiveServer {
				active = "\033[32m*\033[0m"
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", name, srv.Host, srv.Port, active)
		}
		w.Flush()
		return nil
	},
}

var serverSwitchCmd = &cobra.Command{
	Use:   "switch <name>",
	Short: "Switch the active server",
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

		cfg.ActiveServer = name
		if err := config.Save(paths, cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("\033[32mSwitched to server '%s'.\033[0m\n", name)
		return nil
	},
}

var serverRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Forget a registered server",
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
		if cfg.ActiveServer == name {
			cfg.ActiveServer = ""
			// Pick another server as active if available
			for k := range cfg.Servers {
				cfg.ActiveServer = k
				break
			}
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
	RunE: func(cmd *cobra.Command, args []string) error {
		name, c, err := resolveActiveServer()
		if err != nil {
			return err
		}

		status, err := c.Status()
		if err != nil {
			return err
		}

		printStatus(name, status)
		return nil
	},
}

var serverStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Valheim server",
	RunE: func(cmd *cobra.Command, args []string) error {
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
	RunE: func(cmd *cobra.Command, args []string) error {
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
	RunE: func(cmd *cobra.Command, args []string) error {
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

var serverPushCmd = &cobra.Command{
	Use:   "push",
	Short: "Push local profile mods to the active server",
	Long: `Push mods from a local profile to the active server. Only mods targeted
at "both" or "server" are included. Client-only mods are skipped.
Push is always additive — existing server files are not deleted.
Use --with-config to also push config files after mods.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		profileName, _ := cmd.Flags().GetString("profile")

		name, c, err := resolveActiveServer()
		if err != nil {
			return err
		}

		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		reg, err := config.LoadRegistry(paths)
		if err != nil {
			return err
		}

		if profileName == "" {
			profileName = cfg.ActiveProfile
		}

		// Verify profile exists
		profileDir := paths.ProfileDir(profileName)
		if _, err := os.Stat(profileDir); os.IsNotExist(err) {
			return fmt.Errorf("profile '%s' not found", profileName)
		}

		fmt.Printf("Pushing profile '%s' to server '%s'...\n", profileName, name)

		// Build tar.gz of profile directories (filtered by target)
		pr, pw := io.Pipe()
		errCh := make(chan error, 1)
		go func() {
			errCh <- profile.BuildProfileArchive(pw, paths, profileName, reg)
			pw.Close()
		}()

		resp, err := c.PushMods(pr, false)
		if archiveErr := <-errCh; archiveErr != nil {
			return fmt.Errorf("failed to build archive: %w", archiveErr)
		}
		if err != nil {
			return err
		}

		fmt.Printf("\033[32m%s\033[0m\n", resp.Message)

		// Push configs if requested
		withConfig, _ := cmd.Flags().GetBool("with-config")
		if withConfig {
			fmt.Println("\nPushing config files...")
			configDir := paths.ProfileConfigDir(profileName)
			if err := pushAll(c, configDir); err != nil {
				return fmt.Errorf("config push failed: %w", err)
			}
		}

		return nil
	},
}

var serverLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "View server logs",
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
	serverCmd.AddCommand(serverPushCmd)
	serverCmd.AddCommand(serverLogsCmd)

	serverAddCmd.Flags().String("host", "", "server hostname or IP")
	serverAddCmd.Flags().Int("port", agentapi.DefaultPort, "agent port")
	serverAddCmd.Flags().String("secret", "", "agent API secret")

	serverPushCmd.Flags().String("profile", "", "profile to push (default: active profile)")
	serverPushCmd.Flags().Bool("with-config", false, "also push config files after pushing mods")

	serverLogsCmd.Flags().Int("lines", 100, "number of log lines to show")
	serverLogsCmd.Flags().BoolP("follow", "f", false, "stream new log lines")
}

// resolveActiveServer loads config and returns a client for the active server.
func resolveActiveServer() (string, *client.AgentClient, error) {
	_, cfg, err := loadServerConfig()
	if err != nil {
		return "", nil, err
	}

	if cfg.ActiveServer == "" {
		return "", nil, fmt.Errorf("no active server. Run `mmcli server add` to register one")
	}

	srv, ok := cfg.Servers[cfg.ActiveServer]
	if !ok {
		return "", nil, fmt.Errorf("active server '%s' not found in config", cfg.ActiveServer)
	}

	return cfg.ActiveServer, client.New(srv.Host, srv.Port, srv.Secret), nil
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

func printStatus(name string, s *agentapi.StatusResponse) {
	status := "\033[31mstopped\033[0m"
	if s.Running {
		status = fmt.Sprintf("\033[32mrunning\033[0m (%s)", s.Uptime)
	}

	bepinex := "\033[31mnot installed\033[0m"
	if s.BepInEx {
		bepinex = "\033[32minstalled\033[0m"
	}

	fmt.Printf("\n  Server:  %s\n", name)
	fmt.Printf("  Status:  %s\n", status)
	fmt.Printf("  BepInEx: %s\n", bepinex)
	fmt.Printf("  Mods:    %d\n", s.ModCount)
	if len(s.Mods) > 0 {
		for _, m := range s.Mods {
			fmt.Printf("           - %s\n", m)
		}
	}
	fmt.Println()
}

