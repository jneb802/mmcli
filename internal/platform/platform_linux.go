//go:build linux

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"mmcli/internal/games"
)

func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return home + "/.config/mmcli", nil
}

// DetectInstall is not implemented on Linux; mmcli is run on Linux only
// by the dedicated-server agent flow which configures install paths
// explicitly.
func DetectInstall(game games.Game) (string, error) {
	return "", fmt.Errorf("install detection is not supported on Linux for %s", game.DisplayName)
}

func OpenPath(path string) error {
	return exec.Command("xdg-open", path).Run()
}

func GameLaunchTarget(workDir string, game games.Game) string {
	return workDir + "/run_bepinex.sh"
}

func IsGameRunning(game games.Game) bool {
	procName := game.ProcessNameFor("linux")
	if procName == "" {
		return false
	}
	return exec.Command("pgrep", "-x", procName).Run() == nil
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
