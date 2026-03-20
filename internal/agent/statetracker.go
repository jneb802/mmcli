package agent

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	"mmcli/internal/agentapi"
)

// StateTracker polls the mod API and fires Discord webhooks on state transitions.
type StateTracker struct {
	cfg     AgentConfig
	pm      *ProcessManager
	cfgPath string
	stopCh  chan struct{}

	prevRunning     bool
	prevWorldLoaded bool
	prevSaveCount   int
	prevReachable   bool
	lastUptime      string

	lastEventSeq int
	seenPlayers  map[int64]bool
}

func NewStateTracker(cfg AgentConfig, pm *ProcessManager, cfgPath string) *StateTracker {
	return &StateTracker{
		cfg:     cfg,
		pm:      pm,
		cfgPath: cfgPath,
		stopCh:  make(chan struct{}),
	}
}

func (st *StateTracker) Start() {
	go st.run()
}

func (st *StateTracker) Stop() {
	close(st.stopCh)
}

func (st *StateTracker) run() {
	wcfg := st.cfg.DiscordWebhook
	if wcfg == nil || wcfg.URL == "" {
		log.Println("Discord webhooks not configured, state tracker disabled")
		return
	}
	log.Println("State tracker started (polling every 15s)")
	st.loadSeenPlayers()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	// Initial poll to set baseline — no webhooks fired
	st.poll(true)

	for {
		select {
		case <-st.stopCh:
			log.Println("State tracker stopped")
			return
		case <-ticker.C:
			st.poll(false)
		}
	}
}

func (st *StateTracker) poll(initializing bool) {
	wcfg := st.cfg.DiscordWebhook
	processRunning := st.pm.IsRunning()

	// Detect process stop
	if st.prevRunning && !processRunning {
		if wcfg.EventEnabled("server_stopped") {
			go sendDiscordWebhook(wcfg.URL, buildServerStoppedMessage(st.lastUptime))
		}
		st.prevReachable = false
		st.prevWorldLoaded = false
		st.prevSaveCount = 0
		st.lastEventSeq = 0
		st.prevRunning = false
		st.updateEmbed(false, nil)
		return
	}
	st.prevRunning = processRunning

	if !processRunning {
		st.updateEmbed(false, nil)
		return
	}

	// Track uptime for stop message
	uptime := st.pm.Uptime()
	if uptime > 0 {
		st.lastUptime = formatDuration(uptime)
	}

	// Query mod API
	status, _ := QueryModStatus(st.cfg.ResolvedModAPIPort())
	if status == nil {
		if st.prevReachable {
			st.lastEventSeq = 0
		}
		st.prevReachable = false
		st.updateEmbed(true, nil)
		return
	}

	// Server started: world became loaded (was not loaded or unreachable)
	nowReady := status.ServerRunning && status.WorldLoaded
	wasReady := st.prevReachable && st.prevWorldLoaded

	if nowReady && !wasReady {
		// On first poll (initializing), only fire if the server just started recently
		// (uptime < 2 min). This prevents false notifications when the agent restarts
		// while the server has been running for a long time.
		recentStart := uptime < 2*time.Minute
		if !initializing || recentStart {
			if wcfg.EventEnabled("server_started") {
				go sendDiscordWebhook(wcfg.URL, buildServerStartedMessage(status.World, status.Day))
			}
		}
	}

	// World saved: save_count increased (skip on first poll to set baseline)
	if status.SaveCount > st.prevSaveCount && st.prevSaveCount > 0 {
		if wcfg.EventEnabled("world_saved") {
			go sendDiscordWebhook(wcfg.URL, buildWorldSavedMessage(status.World, status.Day, status.GameTime))
		}
	}

	// Process game events from mod event log
	if nowReady {
		events, _ := QueryModEvents(st.cfg.ResolvedModAPIPort(), st.lastEventSeq)
		baseline := st.lastEventSeq == 0 && len(events) > 0
		for _, ev := range events {
			st.lastEventSeq = ev.Seq
			if baseline {
				// First poll after start/restart — set baseline, don't fire webhooks
				if ev.Type == "player_joined" && ev.UID != 0 {
					st.markSeen(ev.UID)
				}
				continue
			}
			st.processEvent(wcfg, ev)
		}
		if baseline {
			st.persistSeenPlayers()
		}
	}

	// Update previous state
	st.prevReachable = true
	st.prevWorldLoaded = status.WorldLoaded
	st.prevSaveCount = status.SaveCount

	st.updateEmbed(true, status)
}

func (st *StateTracker) updateEmbed(processRunning bool, status *ModAPIStatus) {
	wcfg := st.cfg.DiscordWebhook
	if wcfg.StatusEmbedURL == "" {
		return
	}

	var uptime time.Duration
	if processRunning {
		uptime = st.pm.Uptime()
	}

	var players []ModAPIPlayer
	if processRunning && status != nil {
		players, _ = QueryModPlayers(st.cfg.ResolvedModAPIPort())
	}

	// Read push manifest for mod lists
	var manifestMods []agentapi.ManifestMod
	manifestPath := filepath.Join(st.cfg.BepInExDir(), agentapi.ManifestFileName)
	if data, err := os.ReadFile(manifestPath); err == nil {
		var manifest agentapi.PushManifest
		if json.Unmarshal(data, &manifest) == nil {
			manifestMods = manifest.Mods
		}
	}

	embed := buildStatusEmbed(processRunning, status, players, uptime, manifestMods)

	// Try to edit existing message
	if wcfg.StatusEmbedMessageID != "" {
		if err := editDiscordEmbed(wcfg.StatusEmbedURL, wcfg.StatusEmbedMessageID, embed); err == nil {
			return
		}
		log.Printf("Discord embed: edit failed, creating new message")
		wcfg.StatusEmbedMessageID = ""
	}

	// Create new message
	msgID, err := createDiscordEmbed(wcfg.StatusEmbedURL, embed)
	if err != nil {
		log.Printf("Discord embed: create failed: %v", err)
		return
	}
	wcfg.StatusEmbedMessageID = msgID
	if err := SaveConfig(st.cfgPath, st.cfg); err != nil {
		log.Printf("Discord embed: failed to persist message ID: %v", err)
	}
}

func (st *StateTracker) processEvent(wcfg *DiscordWebhookConfig, ev ModAPIEvent) {
	switch ev.Type {
	case "player_joined":
		if st.isFirstJoin(ev.UID) && wcfg.EventEnabled("player_first_join") {
			go sendDiscordWebhook(wcfg.URL, buildPlayerFirstJoinMessage(ev.Player))
		} else if wcfg.EventEnabled("player_joined") {
			go sendDiscordWebhook(wcfg.URL, buildPlayerJoinedMessage(ev.Player))
		}
	case "player_left":
		if wcfg.EventEnabled("player_left") {
			go sendDiscordWebhook(wcfg.URL, buildPlayerLeftMessage(ev.Player))
		}
	case "player_died":
		if wcfg.EventEnabled("player_died") {
			go sendDiscordWebhook(wcfg.URL, buildPlayerDiedMessage(ev.Player))
		}
	}
}

func (st *StateTracker) seenPlayersFile() string {
	return filepath.Join(filepath.Dir(st.cfgPath), "seen_players.json")
}

func (st *StateTracker) loadSeenPlayers() {
	st.seenPlayers = make(map[int64]bool)
	data, err := os.ReadFile(st.seenPlayersFile())
	if err != nil {
		return
	}
	var uids []int64
	if json.Unmarshal(data, &uids) == nil {
		for _, uid := range uids {
			st.seenPlayers[uid] = true
		}
	}
}

func (st *StateTracker) persistSeenPlayers() {
	var uids []int64
	for uid := range st.seenPlayers {
		uids = append(uids, uid)
	}
	data, err := json.Marshal(uids)
	if err != nil {
		return
	}
	if err := os.WriteFile(st.seenPlayersFile(), data, 0600); err != nil {
		log.Printf("Failed to persist seen players: %v", err)
	}
}

func (st *StateTracker) markSeen(uid int64) {
	if uid != 0 {
		st.seenPlayers[uid] = true
	}
}

func (st *StateTracker) isFirstJoin(uid int64) bool {
	if uid == 0 {
		return false
	}
	if st.seenPlayers[uid] {
		return false
	}
	st.seenPlayers[uid] = true
	st.persistSeenPlayers()
	return true
}
