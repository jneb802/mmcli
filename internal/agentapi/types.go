package agentapi

const (
	DefaultPort  = 9877
	HeaderAPIKey = "X-API-Key"

	RoleAdmin  = "admin"
	RolePlayer = "player"

	PathStatus  = "/api/v1/status"
	PathStart   = "/api/v1/start"
	PathStop    = "/api/v1/stop"
	PathRestart = "/api/v1/restart"
	PathMods     = "/api/v1/mods"
	PathModsModeration = "/api/v1/mods/moderation"
	PathModsManage     = "/api/v1/mods/manage"
	PathLogs     = "/api/v1/logs"
	PathSettings = "/api/v1/settings"
	PathUpdate   = "/api/v1/update"

	PathWorlds       = "/api/v1/worlds"
	PathWorldUpload  = "/api/v1/worlds/upload"
	PathWorldDelete  = "/api/v1/worlds/delete"

	PathPlayers = "/api/v1/players"
	PathWebhook = "/api/v1/webhook"

	PathLaunchConfigs       = "/api/v1/launch-configs"
	PathLaunchConfigsActive = "/api/v1/launch-configs/active"

	GitHubRepo      = "jneb802/mmcli"
	AgentBinaryName = "mmcli-agent-linux-amd64"
)

type StatusResponse struct {
	Running    bool     `json:"running"`
	Uptime     string   `json:"uptime,omitempty"`
	UptimeSecs int64    `json:"uptime_secs,omitempty"`
	ModCount   int      `json:"mod_count"`
	Mods       []string `json:"mods,omitempty"`
	BepInEx    bool     `json:"bepinex"`
	Version    string   `json:"version"`
	Role       string   `json:"role,omitempty"`

	// Game state from MMCLIServerMod (nil/empty if mod not available)
	PlayerCount int    `json:"player_count,omitempty"`
	Day         int    `json:"day,omitempty"`
	GameTime    string `json:"game_time,omitempty"`
	IsDay       *bool  `json:"is_day,omitempty"`
	World       string `json:"world,omitempty"`

	// Webhook config summary
	WebhookURL     string `json:"webhook_url,omitempty"`
	WebhookEnabled bool   `json:"webhook_enabled,omitempty"`
	StatusEmbedURL string `json:"status_embed_url,omitempty"`
}

type WebhookConfigResponse struct {
	URL             string `json:"url"`
	ServerStarted   bool   `json:"server_started"`
	ServerStopped   bool   `json:"server_stopped"`
	WorldSaved      bool   `json:"world_saved"`
	PlayerJoined    bool   `json:"player_joined"`
	PlayerLeft      bool   `json:"player_left"`
	PlayerDied      bool   `json:"player_died"`
	PlayerFirstJoin bool   `json:"player_first_join"`
	ServerRestarted bool   `json:"server_restarted"`
	ServerReady     bool   `json:"server_ready"`
	PlayerShout     bool   `json:"player_shout"`
	EventStart      bool   `json:"event_start"`
	EventStop       bool   `json:"event_stop"`
	NewDay          bool   `json:"new_day"`
	CronJob         bool   `json:"cronjob"`
	StatusEmbedURL  string `json:"status_embed_url"`
}

type WebhookConfigUpdate struct {
	URL             *string `json:"url,omitempty"`
	ServerStarted   *bool   `json:"server_started,omitempty"`
	ServerStopped   *bool   `json:"server_stopped,omitempty"`
	WorldSaved      *bool   `json:"world_saved,omitempty"`
	PlayerJoined    *bool   `json:"player_joined,omitempty"`
	PlayerLeft      *bool   `json:"player_left,omitempty"`
	PlayerDied      *bool   `json:"player_died,omitempty"`
	PlayerFirstJoin *bool   `json:"player_first_join,omitempty"`
	ServerRestarted *bool   `json:"server_restarted,omitempty"`
	ServerReady     *bool   `json:"server_ready,omitempty"`
	PlayerShout     *bool   `json:"player_shout,omitempty"`
	EventStart      *bool   `json:"event_start,omitempty"`
	EventStop       *bool   `json:"event_stop,omitempty"`
	NewDay          *bool   `json:"new_day,omitempty"`
	CronJob         *bool   `json:"cronjob,omitempty"`
	StatusEmbedURL  *string `json:"status_embed_url,omitempty"`
}

type PlayerInfo struct {
	Name    string `json:"name"`
	SteamID string `json:"steam_id"`
}

type PlayersResponse struct {
	Players []PlayerInfo `json:"players"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

type ActionResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// ModerationUpdateRequest sets the anticheat classification for a single mod.
type ModerationUpdateRequest struct {
	ModName   string `json:"mod_name"`              // Thunderstore name (e.g. "RandyKnapp-EpicLoot")
	Anticheat string `json:"anticheat"`             // "whitelist", "greylist", "adminonly", "serveronly", ""
	GUID      string `json:"guid,omitempty"`        // BepInEx GUID (for mods not on server)
	Version   string `json:"version,omitempty"`     // mod version (for mods not on server)
}

// ModManageRequest adds, updates, or removes a single mod on the server.
type ModManageRequest struct {
	Action string      `json:"action"` // "add", "update", "remove"
	Mod    ManifestMod `json:"mod"`
}

type ModListResponse struct {
	Mods         []ModInfo    `json:"mods"`
	Manifest     *PushManifest `json:"manifest,omitempty"`      // current server manifest for reconciliation
	ManifestTime string        `json:"manifest_time,omitempty"` // RFC3339 when last push occurred
	LogParsed    bool          `json:"log_parsed"`              // whether BepInEx log was available
	APIQueried   bool          `json:"api_queried"`             // whether MMCLIServerMod API was reachable
}

type ModInfo struct {
	Name           string `json:"name"`
	Version        string `json:"version,omitempty"`
	RuntimeVersion string `json:"runtime_version,omitempty"` // BepInEx-reported version (may differ from manifest)
	Owner          string `json:"owner,omitempty"`
	Disabled       bool   `json:"disabled"`
	Anticheat      string `json:"anticheat,omitempty"`
	Target         string `json:"target,omitempty"`
	GUID           string `json:"guid,omitempty"`
	Loaded         *bool  `json:"loaded,omitempty"`
	PluginOnly     bool   `json:"plugin_only,omitempty"`
}

// Manifest types for server-side mod metadata.

const ManifestFileName = "mmcli-manifest.json"

type ManifestMod struct {
	DirName   string `json:"dir_name"`            // "RandyKnapp-EpicLoot"
	Owner     string `json:"owner"`               // "RandyKnapp"
	Name      string `json:"name"`                // "EpicLoot"
	Version   string `json:"version"`             // "0.12.11"
	Target    string `json:"target"`              // "server" or "both"
	Anticheat string `json:"anticheat"`           // "whitelist", "greylist", "adminonly", ""
	Source    string `json:"source"`              // "thunderstore" or "upload"
	GUID      string `json:"guid,omitempty"`      // BepInEx plugin GUID (persisted after first match)
}

type PushManifest struct {
	PushedAt string        `json:"pushed_at"` // RFC3339 timestamp
	Profile  string        `json:"profile"`
	Mods     []ManifestMod `json:"mods"`
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

// SettingsUpdateRequest contains fields to update in the start script.
// Pointer fields: nil means "don't change". Non-nil means "set to this value".
type SettingsUpdateRequest struct {
	Name         *string           `json:"name,omitempty"`
	Port         *int              `json:"port,omitempty"`
	World        *string           `json:"world,omitempty"`
	Password     *string           `json:"password,omitempty"`
	SaveDir      *string           `json:"savedir,omitempty"`
	Public       *int              `json:"public,omitempty"`
	LogFile      *string           `json:"logfile,omitempty"`
	InstanceID   *string           `json:"instanceid,omitempty"`
	SaveInterval *int              `json:"saveinterval,omitempty"`
	Backups      *int              `json:"backups,omitempty"`
	BackupShort  *int              `json:"backupshort,omitempty"`
	BackupLong   *int              `json:"backuplong,omitempty"`
	Crossplay    *bool             `json:"crossplay,omitempty"`
	Preset       *string           `json:"preset,omitempty"`
	Modifiers    map[string]string `json:"modifiers,omitempty"`
	SetKeys      []string          `json:"setkeys,omitempty"`
	Admins       []string          `json:"admins,omitempty"`
}

type ConfigPushResponse struct {
	OK      bool   `json:"ok"`
	Applied int    `json:"applied"` // .cfg patches applied
	Written int    `json:"written"` // whole files written
	Message string `json:"message"`
}

// World management types.

type WorldListResponse struct {
	Worlds  []WorldInfo `json:"worlds"`
	SaveDir string      `json:"save_dir"`
}

type WorldInfo struct {
	Name     string `json:"name"`
	SizeDB   int64  `json:"size_db"`
	SizeFWL  int64  `json:"size_fwl"`
	Modified string `json:"modified"`
}

type WorldUploadResponse struct {
	OK      bool   `json:"ok"`
	Name    string `json:"name"`
	Message string `json:"message,omitempty"`
}

type WorldDeleteRequest struct {
	Name string `json:"name"`
}

type WorldDeleteResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// Launch config types.

type LaunchConfig struct {
	Name        string           `json:"name"`
	CreatedAt   string           `json:"created_at"`
	Description string           `json:"description,omitempty"`
	Settings    SettingsResponse `json:"settings"`
}

type LaunchConfigListResponse struct {
	Configs []LaunchConfigSummary `json:"configs"`
	Active  string                `json:"active"`
}

type LaunchConfigSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	World       string `json:"world"`
	Preset      string `json:"preset"`
}

type LaunchConfigCreateRequest struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	CopyFrom    string            `json:"copy_from,omitempty"`
	Settings    *SettingsResponse `json:"settings,omitempty"`
}

type LaunchConfigActivateRequest struct {
	Name string `json:"name"`
}

type UpdateResponse struct {
	OK         bool   `json:"ok"`
	OldVersion string `json:"old_version"`
	NewVersion string `json:"new_version"`
	Message    string `json:"message"`
}
