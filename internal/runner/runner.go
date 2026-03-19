package runner

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/nxadm/tail"

	"mmcli/internal/bepinex"
	"mmcli/internal/config"
	"mmcli/internal/profile"
)

// Start launches Valheim via run_bepinex.sh and streams BepInEx logs.
func Start(paths config.Paths, cfg config.Config) error {
	// Delete stale log
	logFile := paths.BepInExLogFile()
	os.Remove(logFile)

	// Re-validate symlinks
	if err := profile.ActivateSymlinks(paths, cfg.ActiveProfile); err != nil {
		return fmt.Errorf("failed to validate symlinks: %w", err)
	}

	scriptPath := paths.RunBepInExScript()
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return fmt.Errorf("run_bepinex.sh not found. Run `mmcli init` first")
	}

	// Strip code signature before every launch — Steam updates can re-sign the
	// binary, and macOS blocks DYLD_INSERT_LIBRARIES for signed apps.
	if err := bepinex.RemoveCodeSignature(paths); err != nil {
		fmt.Printf("Warning: could not remove code signature: %v\n", err)
	}

	fmt.Printf("Launching Valheim (profile: %s)...\n", cfg.ActiveProfile)

	// Launch game
	cmd := exec.Command("/bin/bash", scriptPath)
	cmd.Dir = paths.ValheimDir
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start game: %w", err)
	}

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		pgid = cmd.Process.Pid
	}

	// Start log tailing
	t, err := tail.TailFile(logFile, tail.Config{
		Follow:    true,
		Poll:      true,
		MustExist: false,
		ReOpen:    true,
	})
	if err != nil {
		fmt.Printf("Warning: could not tail log file: %v\n", err)
	}

	// Handle Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	doneChan := make(chan error, 1)
	go func() {
		doneChan <- cmd.Wait()
	}()

	// Stream logs until game exits or interrupted
	go func() {
		if t != nil {
			for line := range t.Lines {
				fmt.Println(line.Text)
			}
		}
	}()

	select {
	case <-sigChan:
		fmt.Println("\nShutting down Valheim...")
		// Kill the entire process group
		syscall.Kill(-pgid, syscall.SIGTERM)

		// Wait up to 5 seconds for graceful shutdown
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-doneChan:
			timer.Stop()
		case <-timer.C:
			fmt.Println("Force killing...")
			syscall.Kill(-pgid, syscall.SIGKILL)
			<-doneChan
		}
	case err := <-doneChan:
		if err != nil {
			fmt.Printf("Game exited with error: %v\n", err)
		} else {
			fmt.Println("Game exited normally.")
		}
	}

	if t != nil {
		t.Stop()
		t.Cleanup()
	}

	return nil
}
