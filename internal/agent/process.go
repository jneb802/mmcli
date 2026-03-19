package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type ProcessManager struct {
	cfg       AgentConfig
	cmd       *exec.Cmd
	pid       int
	pgid      int
	startTime time.Time
	running   bool
	mu        sync.Mutex
	logFile   *os.File
}

// processState is the on-disk representation of server process state.
type processState struct {
	PID       int       `json:"pid"`
	PGID      int       `json:"pgid"`
	StartTime time.Time `json:"start_time"`
}

func NewProcessManager(cfg AgentConfig) *ProcessManager {
	return &ProcessManager{cfg: cfg}
}

func (pm *ProcessManager) Start() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.running {
		return fmt.Errorf("server is already running")
	}

	scriptPath := pm.cfg.ResolvedStartScript()
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return fmt.Errorf("start script not found: %s", scriptPath)
	}

	// Open log file for stdout/stderr capture
	logPath := pm.cfg.ValheimDir + "/mmcli-agent-server.log"
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	// Remove stale BepInEx log
	os.Remove(pm.cfg.ResolvedLogFile())

	cmd := exec.Command("/bin/bash", scriptPath)
	cmd.Dir = pm.cfg.ValheimDir
	cmd.Stdout = lf
	cmd.Stderr = lf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		lf.Close()
		return fmt.Errorf("failed to start server: %w", err)
	}

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		pgid = cmd.Process.Pid
	}

	pm.cmd = cmd
	pm.pid = cmd.Process.Pid
	pm.pgid = pgid
	pm.startTime = time.Now()
	pm.running = true
	pm.logFile = lf

	if err := saveState(processState{PID: pm.pid, PGID: pgid, StartTime: pm.startTime}); err != nil {
		log.Printf("Warning: failed to save process state: %v", err)
	}

	// Wait for process in background
	go func() {
		cmd.Wait()
		pm.mu.Lock()
		pm.running = false
		pm.cmd = nil
		if pm.logFile != nil {
			pm.logFile.Close()
			pm.logFile = nil
		}
		pm.mu.Unlock()
		clearState()
	}()

	return nil
}

func (pm *ProcessManager) Stop() error {
	pm.mu.Lock()
	if !pm.running {
		pm.mu.Unlock()
		return fmt.Errorf("server is not running")
	}
	pgid := pm.pgid
	pm.mu.Unlock()

	// Graceful shutdown
	syscall.Kill(-pgid, syscall.SIGTERM)

	// Wait up to 10 seconds
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			syscall.Kill(-pgid, syscall.SIGKILL)
			time.Sleep(500 * time.Millisecond)
			clearState()
			return nil
		case <-ticker.C:
			pm.mu.Lock()
			still := pm.running
			// For adopted processes (no cmd), check liveness directly
			if still && pm.cmd == nil {
				if err := syscall.Kill(pm.pid, 0); err != nil {
					pm.running = false
					still = false
				}
			}
			pm.mu.Unlock()
			if !still {
				clearState()
				return nil
			}
		}
	}
}

// TryAdopt checks for a persisted process state file and re-adopts the
// server process if it is still running. Returns true if adoption succeeded.
func (pm *ProcessManager) TryAdopt() bool {
	state, err := loadState()
	if err != nil {
		return false
	}

	if !isServerProcess(state.PID) {
		log.Printf("Stale process state (PID %d no longer a server process), cleaning up", state.PID)
		clearState()
		return false
	}

	pm.mu.Lock()
	pm.pid = state.PID
	pm.pgid = state.PGID
	pm.startTime = state.StartTime
	pm.running = true
	pm.mu.Unlock()

	// Poll for process exit since we have no *exec.Cmd to Wait() on
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if !isServerProcess(state.PID) {
				pm.mu.Lock()
				pm.running = false
				pm.mu.Unlock()
				clearState()
				log.Printf("Adopted server process (PID %d) has exited", state.PID)
				return
			}
		}
	}()

	age := time.Since(state.StartTime).Truncate(time.Second)
	log.Printf("Re-adopted running server (PID %d, PGID %d, up %s)", state.PID, state.PGID, age)
	return true
}

func (pm *ProcessManager) IsRunning() bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.running
}

func (pm *ProcessManager) Uptime() time.Duration {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if !pm.running {
		return 0
	}
	return time.Since(pm.startTime)
}

// --- State file helpers ---

func stateFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/etc/mmcli-agent/state.json"
	}
	return filepath.Join(home, ".config", "mmcli-agent", "state.json")
}

func saveState(s processState) error {
	path := stateFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "state-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	tmp.Close()
	return os.Rename(tmpPath, path)
}

func loadState() (processState, error) {
	data, err := os.ReadFile(stateFilePath())
	if err != nil {
		return processState{}, err
	}
	var s processState
	if err := json.Unmarshal(data, &s); err != nil {
		return processState{}, err
	}
	if s.PID == 0 {
		return processState{}, fmt.Errorf("invalid state: PID is 0")
	}
	return s, nil
}

func clearState() {
	if err := os.Remove(stateFilePath()); err != nil && !os.IsNotExist(err) {
		log.Printf("Warning: failed to remove state file: %v", err)
	}
}

// isServerProcess checks whether the given PID is alive and running a
// Valheim server binary. Guards against PID recycling.
func isServerProcess(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "valheim_server")
}
