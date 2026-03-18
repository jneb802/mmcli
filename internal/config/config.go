package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type ServerEntry struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Secret string `json:"secret"`
}

type Config struct {
	ActiveProfile string                  `json:"active_profile"`
	ValheimPath   string                  `json:"valheim_path"`
	Initialized   bool                    `json:"initialized"`
	ActiveServer  string                  `json:"active_server,omitempty"`
	Servers       map[string]ServerEntry  `json:"servers,omitempty"`
}

type Paths struct {
	ConfigDir   string
	ConfigFile  string
	RegistryFile string
	CacheDir    string
	ProfilesDir string
	ValheimDir  string
}

func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("cannot determine home directory: %w", err)
	}
	configDir := filepath.Join(home, ".config", "mmcli")
	return Paths{
		ConfigDir:    configDir,
		ConfigFile:   filepath.Join(configDir, "config.json"),
		RegistryFile: filepath.Join(configDir, "registry.json"),
		CacheDir:     filepath.Join(configDir, "cache"),
		ProfilesDir:  filepath.Join(configDir, "profiles"),
	}, nil
}

func (p Paths) ProfileDir(name string) string {
	return filepath.Join(p.ProfilesDir, name)
}

func (p Paths) ProfilePluginsDir(name string) string {
	return filepath.Join(p.ProfilesDir, name, "plugins")
}

func (p Paths) ProfileConfigDir(name string) string {
	return filepath.Join(p.ProfilesDir, name, "config")
}

func (p Paths) BepInExDir() string {
	return filepath.Join(p.ValheimDir, "BepInEx")
}

func (p Paths) BepInExPluginsDir() string {
	return filepath.Join(p.ValheimDir, "BepInEx", "plugins")
}

func (p Paths) BepInExConfigDir() string {
	return filepath.Join(p.ValheimDir, "BepInEx", "config")
}

func (p Paths) ProfilePatchersDir(name string) string {
	return filepath.Join(p.ProfilesDir, name, "patchers")
}

func (p Paths) ProfileMonomodDir(name string) string {
	return filepath.Join(p.ProfilesDir, name, "monomod")
}

func (p Paths) BepInExPatchersDir() string {
	return filepath.Join(p.ValheimDir, "BepInEx", "patchers")
}

func (p Paths) BepInExMonomodDir() string {
	return filepath.Join(p.ValheimDir, "BepInEx", "monomod")
}

func (p Paths) BepInExCoreDir() string {
	return filepath.Join(p.ValheimDir, "BepInEx", "core")
}

func (p Paths) BepInExLogFile() string {
	return filepath.Join(p.ValheimDir, "BepInEx", "LogOutput.log")
}

func (p Paths) RunBepInExScript() string {
	return filepath.Join(p.ValheimDir, "run_bepinex.sh")
}

func DetectValheimPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, "Library", "Application Support", "Steam", "steamapps", "common", "Valheim")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("Valheim not found at %s", path)
	}
	return path, nil
}

func Load(p Paths) (Config, error) {
	data, err := os.ReadFile(p.ConfigFile)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("corrupt config.json: %w", err)
	}
	return cfg, nil
}

func Save(p Paths, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.ConfigFile, data, 0644)
}

func EnsureInitialized(p Paths) (Config, error) {
	cfg, err := Load(p)
	if err != nil {
		return Config{}, fmt.Errorf("mmcli not initialized. Run `mmcli init` first")
	}
	if !cfg.Initialized {
		return Config{}, fmt.Errorf("mmcli not initialized. Run `mmcli init` first")
	}
	p.ValheimDir = cfg.ValheimPath
	return cfg, nil
}
