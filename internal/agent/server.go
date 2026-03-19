package agent

import (
	"crypto/subtle"
	"log"
	"net/http"

	"mmcli/internal/agentapi"
)

func Run(cfg AgentConfig, addr string) error {
	pm := NewProcessManager(cfg)
	h := NewHandlers(cfg, pm)

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+agentapi.PathStatus, h.HandleStatus)
	mux.HandleFunc("POST "+agentapi.PathStart, h.HandleStart)
	mux.HandleFunc("POST "+agentapi.PathStop, h.HandleStop)
	mux.HandleFunc("POST "+agentapi.PathRestart, h.HandleRestart)
	mux.HandleFunc("GET "+agentapi.PathMods, h.HandleModsList)
	mux.HandleFunc("POST "+agentapi.PathMods, h.HandleModsPush)
	mux.HandleFunc("GET "+agentapi.PathLogs, h.HandleLogs)
	mux.HandleFunc("GET "+agentapi.PathConfigs, h.HandleConfigList)
	mux.HandleFunc("GET "+agentapi.PathConfigs+"/", h.HandleConfigGet)
	mux.HandleFunc("POST "+agentapi.PathConfigs, h.HandleConfigPush)
	mux.HandleFunc("GET "+agentapi.PathSettings, h.HandleSettingsGet)

	handler := authMiddleware(cfg.Secret, mux)

	log.Printf("mmcli-agent listening on %s", addr)
	log.Printf("Valheim dir: %s", cfg.ValheimDir)
	log.Printf("Start script: %s", cfg.ResolvedStartScript())

	return http.ListenAndServe(addr, handler)
}

func authMiddleware(secret string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get(agentapi.HeaderAPIKey)
		if subtle.ConstantTimeCompare([]byte(key), []byte(secret)) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid or missing API key")
			return
		}
		next.ServeHTTP(w, r)
	})
}
