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
	if pm.TryAdopt() {
		log.Printf("Re-adopted running Valheim server")
	}
	h := NewHandlers(cfg, pm, version)

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+agentapi.PathStatus, h.HandleStatus)
	mux.HandleFunc("POST "+agentapi.PathStart, adminOnly(h.HandleStart))
	mux.HandleFunc("POST "+agentapi.PathStop, adminOnly(h.HandleStop))
	mux.HandleFunc("POST "+agentapi.PathRestart, adminOnly(h.HandleRestart))
	mux.HandleFunc("GET "+agentapi.PathMods, h.HandleModsList)
	mux.HandleFunc("GET "+agentapi.PathPlayers, h.HandlePlayers)
	mux.HandleFunc("GET "+agentapi.PathWebhook, adminOnly(h.HandleWebhookGet))
	mux.HandleFunc("POST "+agentapi.PathWebhook, adminOnly(h.HandleWebhookUpdate))
	mux.HandleFunc("POST "+agentapi.PathModsSync, adminOnly(h.HandleModsSync))
	mux.HandleFunc("GET "+agentapi.PathLogs, h.HandleLogs)
	mux.HandleFunc("GET "+agentapi.PathConfigs, h.HandleConfigList)
	mux.HandleFunc("GET "+agentapi.PathConfigs+"/", h.HandleConfigGet)
	mux.HandleFunc("POST "+agentapi.PathConfigs, adminOnly(h.HandleConfigPush))
	mux.HandleFunc("GET "+agentapi.PathSettings, h.HandleSettingsGet)
	mux.HandleFunc("POST "+agentapi.PathSettings, adminOnly(h.HandleSettingsUpdate))
	mux.HandleFunc("POST "+agentapi.PathUpdate, adminOnly(h.HandleUpdate))
	mux.HandleFunc("GET "+agentapi.PathWorlds, h.HandleWorldsList)
	mux.HandleFunc("POST "+agentapi.PathWorldUpload, adminOnly(h.HandleWorldUpload))
	mux.HandleFunc("GET "+agentapi.PathLaunchConfigs, h.HandleLaunchConfigsList)
	mux.HandleFunc("POST "+agentapi.PathLaunchConfigs, adminOnly(h.HandleLaunchConfigCreate))
	mux.HandleFunc("GET "+agentapi.PathLaunchConfigs+"/", h.HandleLaunchConfigGet)
	mux.HandleFunc("PUT "+agentapi.PathLaunchConfigs+"/", adminOnly(h.HandleLaunchConfigUpdate))
	mux.HandleFunc("DELETE "+agentapi.PathLaunchConfigs+"/", adminOnly(h.HandleLaunchConfigDelete))
	mux.HandleFunc("POST "+agentapi.PathLaunchConfigsActive, adminOnly(h.HandleLaunchConfigActivate))

	handler := authMiddleware(cfg, mux)

	// Start state tracker for Discord webhooks
	st := NewStateTracker(cfg, pm)
	st.Start()
	defer st.Stop()

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
