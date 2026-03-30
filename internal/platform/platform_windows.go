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
)

const steamAppID = "892970"

func ConfigDir() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", fmt.Errorf("APPDATA is not set")
	}
	return filepath.Join(appData, "mmcli"), nil
}

func DetectValheimPath() (string, error) {
	steamRoot, err := steamInstallPath()
	if err != nil {
		return "", err
	}

	for _, library := range steamLibraries(steamRoot) {
		path := filepath.Join(library, "steamapps", "common", "Valheim")
		if _, err := os.Stat(filepath.Join(path, "valheim.exe")); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("Valheim not found in Steam libraries under %s", steamRoot)
}

func OpenPath(path string) error {
	return exec.Command("explorer", path).Run()
}

func GameLaunchTarget(workDir string) string {
	return filepath.Join(workDir, "valheim.exe")
}

func IsGameRunning() bool {
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq valheim.exe").CombinedOutput()
	if err != nil {
		return false
	}
	return bytes.Contains(bytes.ToLower(out), []byte("valheim.exe"))
}

// StartGameProcess launches valheim.exe directly. On Windows the doorstop DLL
// proxy (winhttp.dll) handles BepInEx injection automatically — no wrapper
// script is needed.
func StartGameProcess(workDir, target string) (*exec.Cmd, int, error) {
	cmd := exec.Command(target)
	cmd.Dir = workDir
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, 0, err
	}
	return cmd, cmd.Process.Pid, nil
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
