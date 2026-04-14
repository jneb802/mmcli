package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"mmcli/internal/platform"
)

type ServerEntry struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Secret string `json:"secret"`
	Role   string `json:"role,omitempty"`
}

type Config struct {
	ActiveProfile      string                 `json:"active_profile"`
	ValheimPath        string                 `json:"valheim_path"`
	Initialized        bool                   `json:"initialized"`
	ActiveServer       string                 `json:"active_server,omitempty"`
	Servers            map[string]ServerEntry `json:"servers,omitempty"`
	AnticheatSystem    string                 `json:"anticheat_system,omitempty"` // "auto", "azu", "enforcer", "" (= auto)
	ServerManagement   *bool                  `json:"server_management,omitempty"`  // nil/true = enabled, false = disabled
	ModpackPath        string                 `json:"modpack_path,omitempty"`
	ModpackManagement  *bool                  `json:"modpack_management,omitempty"` // nil/true = enabled, false = disabled
	ThunderstoreToken  string                 `json:"thunderstore_token,omitempty"`
	ThunderstoreAuthor string                 `json:"thunderstore_author,omitempty"` // team/namespace on Thunderstore
}

type Paths struct {
	ConfigDir    string
	ConfigFile   string
	RegistryFile string
	CacheDir     string
	ProfilesDir  string
	ValheimDir   string
}

func DefaultPaths() (Paths, error) {
	configDir, err := platform.ConfigDir()
	if err != nil {
		return Paths{}, err
	}
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
	return filepath.Join(p.profileBaseDir(name), "plugins")
}

func (p Paths) ProfileConfigDir(name string) string {
	return filepath.Join(p.profileBaseDir(name), "config")
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
	return filepath.Join(p.profileBaseDir(name), "patchers")
}

func (p Paths) ProfileMonomodDir(name string) string {
	return filepath.Join(p.profileBaseDir(name), "monomod")
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

func (p Paths) ProfileCoreDir(name string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(p.ProfileDir(name), "BepInEx", "core")
	}
	return p.BepInExCoreDir()
}

func (p Paths) ProfileLogFile(name string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(p.ProfileDir(name), "BepInEx", "LogOutput.log")
	}
	return p.BepInExLogFile()
}

func (p Paths) RunBepInExScript() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	return filepath.Join(p.ValheimDir, "run_bepinex.sh")
}

func DetectValheimPath() (string, error) {
	return platform.DetectValheimPath()
}

func (p Paths) profileBaseDir(name string) string {
	base := p.ProfileDir(name)
	if runtime.GOOS == "windows" {
		return filepath.Join(base, "BepInEx")
	}
	return base
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

// MigrateProfileSettings moves legacy global config fields to per-profile
// registry settings for all existing profiles. Returns whether config and
// registry were modified.
func MigrateProfileSettings(cfg *Config, reg *Registry, activeProfile string) (cfgDirty, regDirty bool) {
	hasOldFields := cfg.ActiveServer != "" || cfg.ServerManagement != nil ||
		cfg.ModpackPath != "" || cfg.ModpackManagement != nil ||
		cfg.AnticheatSystem != ""
	if !hasOldFields {
		return false, false
	}

	for name := range reg.Profiles {
		ps := reg.GetSettings(name)

		if ps.Server == "" && cfg.ActiveServer != "" {
			ps.Server = cfg.ActiveServer
		}
		if ps.ServerManagement == nil && cfg.ServerManagement != nil {
			ps.ServerManagement = cfg.ServerManagement
		}
		if ps.ModpackPath == "" && cfg.ModpackPath != "" {
			ps.ModpackPath = cfg.ModpackPath
		}
		if ps.ModpackManagement == nil && cfg.ModpackManagement != nil {
			ps.ModpackManagement = cfg.ModpackManagement
		}
		if ps.AnticheatSystem == "" && cfg.AnticheatSystem != "" {
			ps.AnticheatSystem = cfg.AnticheatSystem
		}
		reg.SetSettings(name, ps)
	}

	cfg.ActiveServer = ""
	cfg.ServerManagement = nil
	cfg.ModpackPath = ""
	cfg.ModpackManagement = nil
	cfg.AnticheatSystem = ""

	return true, true
}
