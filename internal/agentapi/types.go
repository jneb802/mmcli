package agentapi

const (
	DefaultPort  = 9877
	HeaderAPIKey = "X-API-Key"

	PathStatus  = "/api/v1/status"
	PathStart   = "/api/v1/start"
	PathStop    = "/api/v1/stop"
	PathRestart = "/api/v1/restart"
	PathMods    = "/api/v1/mods"
	PathLogs    = "/api/v1/logs"
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
