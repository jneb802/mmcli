//go:build !linux

package agent

import (
	"fmt"
	"os/exec"
	"sync"
	"time"
)

type ProcessManager struct {
	cfg       AgentConfig
	cmd       *exec.Cmd
	pid       int
	pgid      int
	startTime time.Time
	running   bool
	gen       uint64
	mu        sync.Mutex
}

type processState struct {
	PID       int       `json:"pid"`
	PGID      int       `json:"pgid"`
	StartTime time.Time `json:"start_time"`
}

func NewProcessManager(cfg AgentConfig) *ProcessManager {
	return &ProcessManager{cfg: cfg}
}

func (pm *ProcessManager) Start() error {
	return fmt.Errorf("mmcli-agent process management is only supported on Linux")
}

func (pm *ProcessManager) Stop() error {
	return fmt.Errorf("mmcli-agent process management is only supported on Linux")
}

func (pm *ProcessManager) TryAdopt() bool {
	return false
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

func isServerProcess(pid int) bool {
	return false
}
