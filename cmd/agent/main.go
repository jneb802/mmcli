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

	if err := agent.Run(cfg, *listenAddr); err != nil {
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

	secret, err := agent.GenerateSecret()
	if err != nil {
		log.Fatalf("Failed to generate secret: %v", err)
	}

	cfg := agent.AgentConfig{
		Secret:      secret,
		ValheimDir:  valheimDir,
		StartScript: startScript,
	}

	if err := agent.SaveConfig(*configPath, cfg); err != nil {
		log.Fatalf("Failed to save config: %v", err)
	}

	fmt.Printf("\nConfig written to: %s\n", *configPath)
	fmt.Printf("\nYour agent secret (save this for mmcli server add):\n")
	fmt.Printf("\n  %s\n\n", secret)
	fmt.Printf("Start the agent with:\n")
	fmt.Printf("  mmcli-agent\n\n")
}
