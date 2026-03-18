package agent

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type ProcessManager struct {
	cfg       AgentConfig
	cmd       *exec.Cmd
	pgid      int
	startTime time.Time
	running   bool
	mu        sync.Mutex
	logFile   *os.File
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
	pm.pgid = pgid
	pm.startTime = time.Now()
	pm.running = true
	pm.logFile = lf

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
			// Give it a moment to die
			time.Sleep(500 * time.Millisecond)
			return nil
		case <-ticker.C:
			pm.mu.Lock()
			still := pm.running
			pm.mu.Unlock()
			if !still {
				return nil
			}
		}
	}
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
