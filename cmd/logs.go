package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "View local BepInEx logs from the last game session",
	Long: `Read and display the BepInEx log file (LogOutput.log) from the Valheim
installation. Shows the last N lines (default 200). Useful for debugging
mod issues after a game session.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		lines, _ := cmd.Flags().GetInt("lines")

		paths, cfg, err := loadConfig()
		if err != nil {
			return err
		}

		logFile := paths.ProfileLogFile(cfg.ActiveProfile)
		data, err := os.ReadFile(logFile)
		if err != nil {
			return fmt.Errorf("no BepInEx log file found at %s", logFile)
		}

		logLines := strings.Split(string(data), "\n")
		if lines > 0 && len(logLines) > lines {
			logLines = logLines[len(logLines)-lines:]
		}

		fmt.Print(strings.Join(logLines, "\n"))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(logsCmd)
	logsCmd.Flags().Int("lines", 200, "number of log lines to show (0 for all)")
}
