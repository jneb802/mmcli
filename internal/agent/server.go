package agent

import (
	"context"
	"crypto/subtle"
	"log"
	"net/http"

	"mmcli/internal/agentapi"
)

type contextKey string

const roleKey contextKey = "role"

// RoleFromContext returns the authenticated role from the request context.
func RoleFromContext(r *http.Request) string {
	if v, ok := r.Context().Value(roleKey).(string); ok {
		return v
	}
	return agentapi.RoleAdmin
}

func Run(cfg AgentConfig, addr, version string) error {
	pm := NewProcessManager(cfg)
	h := NewHandlers(cfg, pm, version)

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+agentapi.PathStatus, h.HandleStatus)
	mux.HandleFunc("POST "+agentapi.PathStart, adminOnly(h.HandleStart))
	mux.HandleFunc("POST "+agentapi.PathStop, adminOnly(h.HandleStop))
	mux.HandleFunc("POST "+agentapi.PathRestart, adminOnly(h.HandleRestart))
	mux.HandleFunc("GET "+agentapi.PathMods, h.HandleModsList)
	mux.HandleFunc("POST "+agentapi.PathMods, adminOnly(h.HandleModsPush))
	mux.HandleFunc("GET "+agentapi.PathLogs, h.HandleLogs)
	mux.HandleFunc("GET "+agentapi.PathConfigs, h.HandleConfigList)
	mux.HandleFunc("GET "+agentapi.PathConfigs+"/", h.HandleConfigGet)
	mux.HandleFunc("POST "+agentapi.PathConfigs, adminOnly(h.HandleConfigPush))
	mux.HandleFunc("GET "+agentapi.PathSettings, h.HandleSettingsGet)
	mux.HandleFunc("POST "+agentapi.PathSettings, adminOnly(h.HandleSettingsUpdate))
	mux.HandleFunc("POST "+agentapi.PathUpdate, adminOnly(h.HandleUpdate))

	handler := authMiddleware(cfg, mux)

	log.Printf("mmcli-agent listening on %s", addr)
	log.Printf("Valheim dir: %s", cfg.ValheimDir)
	log.Printf("Start script: %s", cfg.ResolvedStartScript())

	return http.ListenAndServe(addr, handler)
}

func authMiddleware(cfg AgentConfig, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get(agentapi.HeaderAPIKey)

		var role string
		if subtle.ConstantTimeCompare([]byte(key), []byte(cfg.Secret)) == 1 {
			role = agentapi.RoleAdmin
		} else if cfg.PlayerSecret != "" && subtle.ConstantTimeCompare([]byte(key), []byte(cfg.PlayerSecret)) == 1 {
			role = agentapi.RolePlayer
		} else {
			writeError(w, http.StatusUnauthorized, "invalid or missing API key")
			return
		}

		ctx := context.WithValue(r.Context(), roleKey, role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if RoleFromContext(r) != agentapi.RoleAdmin {
			writeError(w, http.StatusForbidden, "admin access required")
			return
		}
		next(w, r)
	}
}
