package cmd

import (
	"fmt"

	"mmcli/internal/config"
	"mmcli/internal/games"
)

// requireAgentCapability returns an error if the active game does not
// support the dedicated-server agent flow. Used as a guard in front of
// `mmcli server *` so that, when more games are added in PR-2 onwards,
// invoking server commands on a game without an agent fails fast with a
// clear message instead of a confusing downstream error.
func requireAgentCapability() error {
	cfg, err := loadConfigForCapability()
	if err != nil {
		return err
	}
	g, err := games.Get(cfg.ActiveGame)
	if err != nil {
		return err
	}
	if !g.Capabilities.SupportsAgent {
		return fmt.Errorf("`mmcli server` is not supported for %s", g.DisplayName)
	}
	return nil
}

// requireAnticheatCapability mirrors requireAgentCapability for the
// anti-cheat classification commands.
func requireAnticheatCapability() error {
	cfg, err := loadConfigForCapability()
	if err != nil {
		return err
	}
	g, err := games.Get(cfg.ActiveGame)
	if err != nil {
		return err
	}
	if !g.Capabilities.SupportsAnticheat {
		return fmt.Errorf("`mmcli anticheat` is not supported for %s", g.DisplayName)
	}
	return nil
}

func loadConfigForCapability() (config.Config, error) {
	paths, err := config.DefaultPaths()
	if err != nil {
		return config.Config{}, err
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return config.Config{}, fmt.Errorf("mmcli not initialized. Run `mmcli init` first")
	}
	if cfg.ActiveGame == "" {
		return config.Config{}, fmt.Errorf("no active game. Run `mmcli init` first")
	}
	return cfg, nil
}
