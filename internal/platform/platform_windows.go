//go:build windows

package platform

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"

	"mmcli/internal/games"
)

func ConfigDir() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", fmt.Errorf("APPDATA is not set")
	}
	return filepath.Join(appData, "mmcli"), nil
}

// DetectInstall locates the install directory of the given game by
// walking Steam libraries on Windows.
func DetectInstall(game games.Game) (string, error) {
	if !game.SupportedOn("windows") {
		return "", fmt.Errorf("%s is not supported on Windows", game.DisplayName)
	}
	exeName := game.ExecutableFor("windows")

	steamRoot, err := steamInstallPath()
	if err != nil {
		return "", err
	}

	for _, library := range steamLibraries(steamRoot) {
		path := filepath.Join(library, "steamapps", "common", game.SteamFolderName)
		if _, err := os.Stat(filepath.Join(path, exeName)); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("%s not found in Steam libraries under %s", game.DisplayName, steamRoot)
}

func OpenPath(path string) error {
	return exec.Command("explorer", path).Run()
}

// GameLaunchTarget returns the per-game executable path. On Windows the
// BepInEx winhttp.dll proxy injects into the game on launch, so mmcli
// runs the executable directly (no shim script).
func GameLaunchTarget(workDir string, game games.Game) string {
	exeName := game.ExecutableFor("windows")
	if exeName == "" {
		exeName = game.SteamFolderName + ".exe"
	}
	return filepath.Join(workDir, exeName)
}

func IsGameRunning(game games.Game) bool {
	procName := game.ProcessNameFor("windows")
	if procName == "" {
		return false
	}
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq "+procName).CombinedOutput()
	if err != nil {
		return false
	}
	return bytes.Contains(bytes.ToLower(out), []byte(strings.ToLower(procName)))
}

// StartGameProcess launches valheim.exe directly. On Windows the doorstop DLL
// proxy (winhttp.dll) handles BepInEx injection automatically — no wrapper
// script is needed.
// When logPath is non-empty, stdout and stderr are redirected to that file
// so game output doesn't corrupt a TUI. The caller must close the returned
// *os.File when the process exits; it may be nil when logPath is empty.
func StartGameProcess(workDir, target, logPath string) (*exec.Cmd, int, *os.File, error) {
	cmd := exec.Command(target)
	cmd.Dir = workDir

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
	return cmd, cmd.Process.Pid, lf, nil
}

// GracefulKill attempts to terminate the process. Windows has no process-group
// SIGTERM equivalent, so this falls back to Kill.
func GracefulKill(cmd *exec.Cmd, _ int) error {
	return cmd.Process.Kill()
}

// ForceKill forcefully terminates the process.
func ForceKill(cmd *exec.Cmd, _ int) error {
	return cmd.Process.Kill()
}

// NotifySignals registers for os.Interrupt (Ctrl+C).
func NotifySignals(c chan<- os.Signal) {
	signal.Notify(c, os.Interrupt)
}

func steamInstallPath() (string, error) {
	keys := []string{
		`HKCU\Software\Valve\Steam`,
		`HKLM\SOFTWARE\WOW6432Node\Valve\Steam`,
		`HKLM\SOFTWARE\Valve\Steam`,
	}
	for _, key := range keys {
		out, err := exec.Command("reg", "query", key, "/v", "InstallPath").CombinedOutput()
		if err != nil {
			continue
		}
		if path := parseRegistryValue(string(out), "InstallPath"); path != "" {
			return path, nil
		}
	}
	return "", fmt.Errorf("could not find Steam InstallPath in registry")
}

func parseRegistryValue(out, valueName string) string {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != valueName {
			continue
		}
		return strings.Join(fields[2:], " ")
	}
	return ""
}

func steamLibraries(steamRoot string) []string {
	libraries := []string{steamRoot}
	vdfPath := filepath.Join(steamRoot, "steamapps", "libraryfolders.vdf")
	data, err := os.ReadFile(vdfPath)
	if err != nil {
		return libraries
	}

	re := regexp.MustCompile(`"path"\s+"([^"]+)"`)
	for _, match := range re.FindAllStringSubmatch(string(data), -1) {
		if len(match) < 2 {
			continue
		}
		path := strings.ReplaceAll(match[1], `\\`, `\`)
		if path == "" || containsPath(libraries, path) {
			continue
		}
		libraries = append(libraries, path)
	}
	return libraries
}

func containsPath(paths []string, target string) bool {
	for _, path := range paths {
		if strings.EqualFold(path, target) {
			return true
		}
	}
	return false
}
