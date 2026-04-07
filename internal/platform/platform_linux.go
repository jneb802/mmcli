//go:build linux

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return home + "/.config/mmcli", nil
}

func DetectValheimPath() (string, error) {
	return "", fmt.Errorf("Valheim detection not supported on Linux")
}

func OpenPath(path string) error {
	return exec.Command("xdg-open", path).Run()
}

func GameLaunchTarget(workDir string) string {
	return workDir + "/run_bepinex.sh"
}

func IsGameRunning() bool {
	return exec.Command("pgrep", "-x", "valheim.x86_64").Run() == nil
}

func StartGameProcess(workDir, target, logPath string) (*exec.Cmd, int, *os.File, error) {
	cmd := exec.Command("/bin/bash", target)
	cmd.Dir = workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var lf *os.File
	if logPath != "" {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, 0, nil, fmt.Errorf("open log file: %w", err)
		}
		cmd.Stdout = f
		cmd.Stderr = f
		lf = f
	}

	if err := cmd.Start(); err != nil {
		if lf != nil {
			lf.Close()
		}
		return nil, 0, nil, err
	}

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		pgid = cmd.Process.Pid
	}
	return cmd, pgid, lf, nil
}

func GracefulKill(_ *exec.Cmd, pgid int) error {
	return syscall.Kill(-pgid, syscall.SIGTERM)
}

func ForceKill(_ *exec.Cmd, pgid int) error {
	return syscall.Kill(-pgid, syscall.SIGKILL)
}

func NotifySignals(c chan<- os.Signal) {
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
}
