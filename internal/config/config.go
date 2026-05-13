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
	ActiveProfile string `json:"active_profile"`

	// ActiveGame is the ID of the currently selected game (e.g. "valheim").
	// Populated by `mmcli init` and switched by `mmcli game use`.
	ActiveGame string `json:"active_game,omitempty"`
	// GameInstalls maps game ID to install directory on disk.
	GameInstalls map[string]string `json:"game_installs,omitempty"`

	// ValheimPath is the legacy single-game install path. Migrated into
	// GameInstalls["valheim"] on first load. Kept on the struct only so
	// unmarshaling pre-multigame config files still works; new code never
	// writes a non-empty value here.
	ValheimPath string `json:"valheim_path,omitempty"`

	Initialized        bool                   `json:"initialized"`
	ActiveServer       string                 `json:"active_server,omitempty"`
	Servers            map[string]ServerEntry `json:"servers,omitempty"`
	AnticheatSystem    string                 `json:"anticheat_system,omitempty"`   // "auto", "azu", "enforcer", "" (= auto)
	ServerManagement   *bool                  `json:"server_management,omitempty"`  // nil/true = enabled, false = disabled
	ModpackPath        string                 `json:"modpack_path,omitempty"`
	ModpackManagement  *bool                  `json:"modpack_management,omitempty"` // nil/true = enabled, false = disabled
	ThunderstoreToken  string                 `json:"thunderstore_token,omitempty"`
	ThunderstoreAuthor string                 `json:"thunderstore_author,omitempty"` // team/namespace on Thunderstore
}

// GamePath returns the install path for the currently active game, or ""
// if no game is active. Use this everywhere the game install dir is
// needed; never read GameInstalls or ValheimPath directly.
func (c Config) GamePath() string {
	if c.ActiveGame == "" {
		return ""
	}
	return c.GameInstalls[c.ActiveGame]
}

// SetGameInstall records the install path for the given game.
func (c *Config) SetGameInstall(gameID, path string) {
	if c.GameInstalls == nil {
		c.GameInstalls = map[string]string{}
	}
	c.GameInstalls[gameID] = path
}

// migrateGameInstalls folds the legacy ValheimPath field into the new
// GameInstalls map. Returns true if the config was modified.
func migrateGameInstalls(cfg *Config) bool {
	if cfg.ValheimPath == "" {
		return false
	}
	if cfg.GameInstalls == nil {
		cfg.GameInstalls = map[string]string{}
	}
	if _, ok := cfg.GameInstalls["valheim"]; !ok {
		cfg.GameInstalls["valheim"] = cfg.ValheimPath
	}
	if cfg.ActiveGame == "" {
		cfg.ActiveGame = "valheim"
	}
	cfg.ValheimPath = ""
	return true
}

type Paths struct {
	ConfigDir    string
	ConfigFile   string
	RegistryFile string
	CacheDir     string
	// AllProfilesRoot is the top-level profiles directory; profile dirs
	// for a specific game live at <AllProfilesRoot>/<game>/<name>/. Use
	// this when enumerating across games (e.g. for `mmcli game list`).
	AllProfilesRoot string
	// ProfilesDir is the per-active-game profiles directory, equivalent
	// to <AllProfilesRoot>/<active_game>/. Empty until EnsureInitialized
	// (or a similar resolver) sets it.
	ProfilesDir string
	GameDir     string
}

func DefaultPaths() (Paths, error) {
	configDir, err := platform.ConfigDir()
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		ConfigDir:       configDir,
		ConfigFile:      filepath.Join(configDir, "config.json"),
		RegistryFile:    filepath.Join(configDir, "registry.json"),
		CacheDir:        filepath.Join(configDir, "cache"),
		AllProfilesRoot: filepath.Join(configDir, "profiles"),
	}, nil
}

// ResolveForGame returns a copy of Paths with GameDir and ProfilesDir
// populated for the given game and install path. Use this before any
// profile or BepInEx path helper is called.
func (p Paths) ResolveForGame(gameID, installPath string) Paths {
	out := p
	out.GameDir = installPath
	out.ProfilesDir = filepath.Join(p.AllProfilesRoot, gameID)
	return out
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
	return filepath.Join(p.GameDir, "BepInEx")
}

func (p Paths) BepInExPluginsDir() string {
	return filepath.Join(p.GameDir, "BepInEx", "plugins")
}

func (p Paths) BepInExConfigDir() string {
	return filepath.Join(p.GameDir, "BepInEx", "config")
}

func (p Paths) ProfilePatchersDir(name string) string {
	return filepath.Join(p.profileBaseDir(name), "patchers")
}

func (p Paths) ProfileMonomodDir(name string) string {
	return filepath.Join(p.profileBaseDir(name), "monomod")
}

func (p Paths) BepInExPatchersDir() string {
	return filepath.Join(p.GameDir, "BepInEx", "patchers")
}

func (p Paths) BepInExMonomodDir() string {
	return filepath.Join(p.GameDir, "BepInEx", "monomod")
}

func (p Paths) BepInExCoreDir() string {
	return filepath.Join(p.GameDir, "BepInEx", "core")
}

func (p Paths) BepInExLogFile() string {
	return filepath.Join(p.GameDir, "BepInEx", "LogOutput.log")
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
	return filepath.Join(p.GameDir, "run_bepinex.sh")
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
	// In-memory migration only. The unified migration runner (phase 5)
	// writes the migrated shape back to disk with backups; for now the
	// caller works against the migrated struct and the file gets rewritten
	// on the next Save().
	migrateGameInstalls(&cfg)
	return cfg, nil
}

func Save(p Paths, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.ConfigFile, data, 0644)
}

// EnsureInitialized loads config and verifies mmcli is initialized with
// an active game that has an install path. Returns the resolved Paths
// (with GameDir and ProfilesDir populated for the active game) so the
// caller doesn't have to remember to call ResolveForGame.
func EnsureInitialized(p Paths) (Paths, Config, error) {
	cfg, err := Load(p)
	if err != nil {
		return Paths{}, Config{}, fmt.Errorf("mmcli not initialized. Run `mmcli init` first")
	}
	if !cfg.Initialized {
		return Paths{}, Config{}, fmt.Errorf("mmcli not initialized. Run `mmcli init` first")
	}
	if cfg.ActiveGame == "" {
		return Paths{}, Config{}, fmt.Errorf("no active game selected. Run `mmcli init` to set up a game")
	}
	if cfg.GamePath() == "" {
		return Paths{}, Config{}, fmt.Errorf("active game %q has no install path configured", cfg.ActiveGame)
	}
	return p.ResolveForGame(cfg.ActiveGame, cfg.GamePath()), cfg, nil
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

	for _, name := range reg.ProfileNames() {
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
