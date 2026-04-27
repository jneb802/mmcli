//go:build darwin

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"mmcli/internal/games"
)

func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "mmcli"), nil
}

// DetectInstall locates the install directory of the given game under the
// macOS Steam library. Returns an error if the game is unsupported on
// this OS or its install dir isn't present.
func DetectInstall(game games.Game) (string, error) {
	if !game.SupportedOn("darwin") {
		return "", fmt.Errorf("%s is not supported on macOS", game.DisplayName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, "Library", "Application Support", "Steam", "steamapps", "common", game.SteamFolderName)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("%s not found at %s", game.DisplayName, path)
	}
	return path, nil
}

func OpenPath(path string) error {
	return exec.Command("open", path).Run()
}

// GameLaunchTarget returns the path mmcli executes to launch the game.
// On macOS this is run_bepinex.sh in the game install dir for any
// BepInEx-modded game; the game-specific exec name is set inside the
// patched script (see internal/loaders/bepinex/bepinex.go PatchRunScript).
func GameLaunchTarget(workDir string, game games.Game) string {
	return filepath.Join(workDir, "run_bepinex.sh")
}

// IsGameRunning reports whether the given game's process is currently
// running. Returns false if the game has no configured macOS process
// name.
func IsGameRunning(game games.Game) bool {
	procName := game.ProcessNameFor("darwin")
	if procName == "" {
		return false
	}
	return exec.Command("pgrep", "-x", procName).Run() == nil
}

// StartGameProcess launches Valheim via /bin/bash run_bepinex.sh with a
// dedicated process group so the entire tree can be killed on shutdown.
// When logPath is non-empty, stdout and stderr are redirected to that file
// so game output doesn't corrupt a TUI. The caller must close the returned
// *os.File when the process exits; it may be nil when logPath is empty.
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

// GracefulKill sends SIGTERM to the process group.
func GracefulKill(_ *exec.Cmd, pgid int) error {
	return syscall.Kill(-pgid, syscall.SIGTERM)
}

// ForceKill sends SIGKILL to the process group.
func ForceKill(_ *exec.Cmd, pgid int) error {
	return syscall.Kill(-pgid, syscall.SIGKILL)
}

// NotifySignals registers for SIGINT and SIGTERM.
func NotifySignals(c chan<- os.Signal) {
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
}
