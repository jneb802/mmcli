package agent

import (
	"log"
	"time"
)

// StateTracker polls the mod API and fires Discord webhooks on state transitions.
type StateTracker struct {
	cfg    AgentConfig
	pm     *ProcessManager
	stopCh chan struct{}

	prevRunning     bool
	prevWorldLoaded bool
	prevSaveCount   int
	prevReachable   bool
	lastUptime      string
}

func NewStateTracker(cfg AgentConfig, pm *ProcessManager) *StateTracker {
	return &StateTracker{
		cfg:    cfg,
		pm:     pm,
		stopCh: make(chan struct{}),
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
	if st.prevRunning && !processRunning && !initializing {
		if wcfg.EventEnabled("server_stopped") {
			go sendDiscordWebhook(wcfg.URL, buildServerStoppedEmbed(st.lastUptime))
		}
		st.prevReachable = false
		st.prevWorldLoaded = false
		st.prevSaveCount = 0
		st.prevRunning = false
		return
	}
	st.prevRunning = processRunning

	if !processRunning {
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
		st.prevReachable = false
		return
	}

	// Server started: world became loaded (was not loaded or unreachable)
	nowReady := status.ServerRunning && status.WorldLoaded
	wasReady := st.prevReachable && st.prevWorldLoaded

	if nowReady && !wasReady && !initializing {
		if wcfg.EventEnabled("server_started") {
			go sendDiscordWebhook(wcfg.URL, buildServerStartedEmbed(status.World, status.Day))
		}
	}

	// World saved: save_count increased
	if status.SaveCount > st.prevSaveCount && st.prevSaveCount > 0 && !initializing {
		if wcfg.EventEnabled("world_saved") {
			go sendDiscordWebhook(wcfg.URL, buildWorldSavedEmbed(status.World, status.Day, status.GameTime))
		}
	}

	// Update previous state
	st.prevReachable = true
	st.prevWorldLoaded = status.WorldLoaded
	st.prevSaveCount = status.SaveCount
}
