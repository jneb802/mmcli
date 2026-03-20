package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"mmcli/internal/agent"
	"mmcli/internal/agentapi"
)

// Version is set at build time via -ldflags "-X main.Version=vX.Y.Z"
var Version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "init" {
		runInit()
		return
	}

	configPath := flag.String("config", agent.DefaultConfigPath(), "path to agent config")
	listenAddr := flag.String("listen", fmt.Sprintf(":%d", agentapi.DefaultPort), "listen address")
	flag.Parse()

	cfg, err := agent.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Error: %v", err)
	}

	if cfg.PlayerSecret != "" && cfg.PlayerSecret == cfg.Secret {
		log.Fatalf("Error: admin and player secrets must be different")
	}

	if err := agent.Run(cfg, *configPath, *listenAddr, Version); err != nil {
		log.Fatalf("Error: %v", err)
	}
}

func runInit() {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	configPath := fs.String("config", agent.DefaultConfigPath(), "path to write config")
	fs.Parse(os.Args[2:])

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Valheim server directory (e.g., /home/steam/valheim): ")
	valheimDir, _ := reader.ReadString('\n')
	valheimDir = strings.TrimSpace(valheimDir)

	if _, err := os.Stat(valheimDir); os.IsNotExist(err) {
		log.Fatalf("Directory does not exist: %s", valheimDir)
	}

	fmt.Print("Start script filename (default: start_server_bepinex.sh): ")
	startScript, _ := reader.ReadString('\n')
	startScript = strings.TrimSpace(startScript)
	if startScript == "" {
		startScript = "start_server_bepinex.sh"
	}

	adminSecret, err := agent.GenerateSecret()
	if err != nil {
		log.Fatalf("Failed to generate admin secret: %v", err)
	}

	playerSecret, err := agent.GenerateSecret()
	if err != nil {
		log.Fatalf("Failed to generate player secret: %v", err)
	}

	cfg := agent.AgentConfig{
		Secret:       adminSecret,
		PlayerSecret: playerSecret,
		ValheimDir:   valheimDir,
		StartScript:  startScript,
	}

	if err := agent.SaveConfig(*configPath, cfg); err != nil {
		log.Fatalf("Failed to save config: %v", err)
	}

	fmt.Printf("\nConfig written to: %s\n", *configPath)
	fmt.Printf("\nYour admin secret (full control — keep private):\n")
	fmt.Printf("\n  %s\n\n", adminSecret)
	fmt.Printf("Your player secret (read-only — share with players):\n")
	fmt.Printf("\n  %s\n\n", playerSecret)
	fmt.Printf("Start the agent with:\n")
	fmt.Printf("  mmcli-agent\n\n")
}
