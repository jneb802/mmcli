package runner

import (
	"fmt"
	"os"
	"time"

	"github.com/nxadm/tail"

	"mmcli/internal/bepinex"
	"mmcli/internal/config"
	"mmcli/internal/platform"
	"mmcli/internal/profile"
)

// Start launches Valheim and streams BepInEx logs.
func Start(paths config.Paths, cfg config.Config) error {
	// Delete stale log
	logFile := paths.ProfileLogFile(cfg.ActiveProfile)
	os.Remove(logFile)

	// Re-validate active profile wiring.
	if err := profile.Activate(paths, cfg.ActiveProfile); err != nil {
		return fmt.Errorf("failed to activate profile: %w", err)
	}

	target := platform.GameLaunchTarget(paths.ValheimDir)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return fmt.Errorf("game launch target not found at %s. Run `mmcli init` first", target)
	}

	// Strip code signature before every launch — Steam updates can re-sign the
	// binary, and macOS blocks DYLD_INSERT_LIBRARIES for signed apps.
	if err := bepinex.RemoveCodeSignature(paths); err != nil {
		fmt.Printf("Warning: could not remove code signature: %v\n", err)
	}

	fmt.Printf("Launching Valheim (profile: %s)...\n", cfg.ActiveProfile)

	// Launch game
	cmd, pgid, err := platform.StartGameProcess(paths.ValheimDir, target)
	if err != nil {
		return fmt.Errorf("failed to start game: %w", err)
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
	platform.NotifySignals(sigChan)

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
		_ = platform.GracefulKill(cmd, pgid)

		// Wait up to 5 seconds for graceful shutdown
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-doneChan:
			timer.Stop()
		case <-timer.C:
			fmt.Println("Force killing...")
			_ = platform.ForceKill(cmd, pgid)
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
