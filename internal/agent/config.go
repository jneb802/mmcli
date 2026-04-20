package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"mmcli/internal/agentapi"
)

type AgentConfig struct {
	Secret             string                `json:"secret"`
	PlayerSecret       string                `json:"player_secret,omitempty"`
	ValheimDir         string                `json:"valheim_dir"`
	StartScript        string                `json:"start_script"`
	LogFile            string                `json:"log_file,omitempty"`
	ModAPIPort         int                   `json:"mod_api_port,omitempty"`
	ActiveLaunchConfig string                `json:"active_launch_config,omitempty"`
	ActiveProfile      string                `json:"active_profile,omitempty"`
	DiscordWebhook     *DiscordWebhookConfig `json:"discord_webhook,omitempty"`
}

type DiscordWebhookConfig struct {
	URL             string `json:"url"`
	ServerStarted   bool   `json:"server_started"`
	ServerStopped   bool   `json:"server_stopped"`
	WorldSaved      bool   `json:"world_saved"`
	PlayerJoined    bool   `json:"player_joined"`
	PlayerLeft      bool   `json:"player_left"`
	PlayerDied      bool   `json:"player_died"`
	PlayerFirstJoin   bool   `json:"player_first_join"`
	ServerRestarted   bool   `json:"server_restarted"`
	ServerReady       bool   `json:"server_ready"`
	PlayerShout       bool   `json:"player_shout"`
	EventStart        bool   `json:"event_start"`
	EventStop         bool   `json:"event_stop"`
	NewDay            bool   `json:"new_day"`
	CronJob           bool   `json:"cronjob"`
	StatusEmbedURL string `json:"status_embed_url,omitempty"`

	// Internal: persisted message ID for editing the status embed
	StatusEmbedMessageID string `json:"status_embed_message_id,omitempty"`
}

// EventEnabled returns whether a given event type should fire.
// If the URL is set but all bools are false, all events are enabled by default.
func (c *DiscordWebhookConfig) EventEnabled(event string) bool {
	if c == nil || c.URL == "" {
		return false
	}
	// If no events explicitly enabled, enable all
	anyEnabled := c.ServerStarted || c.ServerStopped || c.ServerRestarted || c.ServerReady || c.WorldSaved ||
		c.PlayerJoined || c.PlayerLeft || c.PlayerDied || c.PlayerFirstJoin ||
		c.PlayerShout || c.EventStart || c.EventStop || c.NewDay || c.CronJob
	if !anyEnabled {
		return true
	}
	switch event {
	case "server_started":
		return c.ServerStarted
	case "server_stopped":
		return c.ServerStopped
	case "world_saved":
		return c.WorldSaved
	case "player_joined":
		return c.PlayerJoined
	case "player_left":
		return c.PlayerLeft
	case "player_died":
		return c.PlayerDied
	case "player_first_join":
		return c.PlayerFirstJoin
	case "server_restarted":
		return c.ServerRestarted
	case "server_ready":
		return c.ServerReady
	case "player_shout":
		return c.PlayerShout
	case "event_start":
		return c.EventStart
	case "event_stop":
		return c.EventStop
	case "new_day":
		return c.NewDay
	case "cronjob":
		return c.CronJob
	}
	return false
}

func (c AgentConfig) ProfilesDir() string {
	return filepath.Join(c.ValheimDir, "mmcli-profiles")
}

func (c AgentConfig) ProfileDir(name string) string {
	return filepath.Join(c.ValheimDir, "mmcli-profiles", name)
}

func (c AgentConfig) ProfilePluginsDir(name string) string {
	return filepath.Join(c.ValheimDir, "mmcli-profiles", name, "plugins")
}

func (c AgentConfig) ProfileConfigDir(name string) string {
	return filepath.Join(c.ValheimDir, "mmcli-profiles", name, "config")
}

func (c AgentConfig) ProfilePatchersDir(name string) string {
	return filepath.Join(c.ValheimDir, "mmcli-profiles", name, "patchers")
}

func (c AgentConfig) ProfileMonomodDir(name string) string {
	return filepath.Join(c.ValheimDir, "mmcli-profiles", name, "monomod")
}

func (c AgentConfig) ProfileManifestPath(name string) string {
	return filepath.Join(c.ValheimDir, "mmcli-profiles", name, agentapi.ManifestFileName)
}

func (c AgentConfig) ActiveProfileName() string {
	if c.ActiveProfile != "" {
		return c.ActiveProfile
	}
	return "default"
}

func (c AgentConfig) ActiveManifestPath() string {
	return c.ProfileManifestPath(c.ActiveProfileName())
}

func (c AgentConfig) ProfileSubdirs(name string) []string {
	return []string{
		c.ProfilePluginsDir(name),
		c.ProfileConfigDir(name),
		c.ProfilePatchersDir(name),
		c.ProfileMonomodDir(name),
	}
}

func (c AgentConfig) LaunchConfigsDir() string {
	return filepath.Join(c.ValheimDir, "mmcli-launch-configs")
}

func (c AgentConfig) ResolvedModAPIPort() int {
	if c.ModAPIPort > 0 {
		return c.ModAPIPort
	}
	return 9878
}

func (c AgentConfig) BepInExDir() string {
	return filepath.Join(c.ValheimDir, "BepInEx")
}

func (c AgentConfig) PluginsDir() string {
	return filepath.Join(c.ValheimDir, "BepInEx", "plugins")
}

func (c AgentConfig) ConfigDir() string {
	return filepath.Join(c.ValheimDir, "BepInEx", "config")
}

func (c AgentConfig) PatchersDir() string {
	return filepath.Join(c.ValheimDir, "BepInEx", "patchers")
}

func (c AgentConfig) MonomodDir() string {
	return filepath.Join(c.ValheimDir, "BepInEx", "monomod")
}

func (c AgentConfig) ResolvedLogFile() string {
	if c.LogFile != "" {
		if filepath.IsAbs(c.LogFile) {
			return c.LogFile
		}
		return filepath.Join(c.ValheimDir, c.LogFile)
	}
	return filepath.Join(c.ValheimDir, "BepInEx", "LogOutput.log")
}

func (c AgentConfig) ResolvedStartScript() string {
	if filepath.IsAbs(c.StartScript) {
		return c.StartScript
	}
	return filepath.Join(c.ValheimDir, c.StartScript)
}

func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/etc/mmcli-agent/config.json"
	}
	return filepath.Join(home, ".config", "mmcli-agent", "config.json")
}

func LoadConfig(path string) (AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return AgentConfig{}, fmt.Errorf("failed to read agent config: %w", err)
	}
	var cfg AgentConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return AgentConfig{}, fmt.Errorf("corrupt agent config: %w", err)
	}
	if cfg.Secret == "" {
		return AgentConfig{}, fmt.Errorf("agent config missing 'secret'")
	}
	if cfg.ValheimDir == "" {
		return AgentConfig{}, fmt.Errorf("agent config missing 'valheim_dir'")
	}
	if cfg.StartScript == "" {
		return AgentConfig{}, fmt.Errorf("agent config missing 'start_script'")
	}
	return cfg, nil
}

func SaveConfig(path string, cfg AgentConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func GenerateSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
