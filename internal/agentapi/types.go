package agentapi

const (
	DefaultPort  = 9877
	HeaderAPIKey = "X-API-Key"

	PathStatus  = "/api/v1/status"
	PathStart   = "/api/v1/start"
	PathStop    = "/api/v1/stop"
	PathRestart = "/api/v1/restart"
	PathMods     = "/api/v1/mods"
	PathLogs     = "/api/v1/logs"
	PathSettings = "/api/v1/settings"
)

type StatusResponse struct {
	Running    bool     `json:"running"`
	Uptime     string   `json:"uptime,omitempty"`
	UptimeSecs int64    `json:"uptime_secs,omitempty"`
	ModCount   int      `json:"mod_count"`
	Mods       []string `json:"mods,omitempty"`
	BepInEx    bool     `json:"bepinex"`
	Version    string   `json:"version"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

type ActionResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type ModListResponse struct {
	Mods []ModInfo `json:"mods"`
}

type ModInfo struct {
	Name     string `json:"name"`
	Disabled bool   `json:"disabled"`
}

// Config management paths
const (
	PathConfigs = "/api/v1/configs"
)

type ConfigListResponse struct {
	Files []string `json:"files"`
}

type ConfigFileResponse struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

type ConfigPushRequest struct {
	Patches []ConfigPatch `json:"patches,omitempty"` // entry-level patches for .cfg files
	Files   []ConfigFile  `json:"files,omitempty"`   // whole-file push for YAML/JSON
}

type ConfigPatch struct {
	Filename string `json:"filename"`
	Section  string `json:"section"`
	Key      string `json:"key"`
	Value    string `json:"value"`
}

type ConfigFile struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

// SettingsResponse contains all Valheim server settings parsed from the start script.
type SettingsResponse struct {
	// Core settings
	Name       string `json:"name"`
	Port       int    `json:"port"`
	World      string `json:"world"`
	Password   string `json:"password"`
	SaveDir    string `json:"savedir"`
	Public     int    `json:"public"`
	LogFile    string `json:"logfile,omitempty"`
	InstanceID string `json:"instanceid,omitempty"`

	// Backup settings
	SaveInterval int `json:"saveinterval"`
	Backups      int `json:"backups"`
	BackupShort  int `json:"backupshort"`
	BackupLong   int `json:"backuplong"`

	// World modifiers
	Crossplay bool              `json:"crossplay"`
	Preset    string            `json:"preset,omitempty"`
	Modifiers map[string]string `json:"modifiers,omitempty"`
	SetKeys   []string          `json:"setkeys,omitempty"`

	// Permission lists
	Admins    []string `json:"admins,omitempty"`
	Banned    []string `json:"banned,omitempty"`
	Permitted []string `json:"permitted,omitempty"`
}

type ConfigPushResponse struct {
	OK      bool   `json:"ok"`
	Applied int    `json:"applied"` // .cfg patches applied
	Written int    `json:"written"` // whole files written
	Message string `json:"message"`
}
