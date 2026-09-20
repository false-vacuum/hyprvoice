package daemon

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/leonardotrapani/hyprvoice/internal/notify"
	"github.com/leonardotrapani/hyprvoice/internal/pipeline"
)

const (
	// overlayCommand is the on-screen indicator the daemon starts when
	// notifications.type is "overlay". It is a separate process because
	// drawing a layer-shell surface from Go is not practical, and because the
	// daemon has to keep working when it is absent.
	overlayCommand = "hyprvoice-overlay"

	// overlayCommandEnv overrides that command, for running the overlay from a
	// checkout rather than from PATH.
	overlayCommandEnv = "HYPRVOICE_OVERLAY_CMD"

	// overlayRestartDelay throttles restarts of an overlay that keeps exiting,
	// so a broken install cannot spin.
	overlayRestartDelay = 5 * time.Second

	// overlayHandoffWindow is how quickly a clean exit counts as the overlay
	// having handed over to an instance that was already running, rather than
	// as a crash to restart.
	overlayHandoffWindow = time.Second
)

// overlaySupervisor keeps one overlay client running while the overlay
// notifier is selected.
//
// The overlay subscribes to the status stream like any other client, so the
// daemon needs nothing from it and carries on unaffected when it is missing or
// keeps crashing.
type overlaySupervisor struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func newOverlaySupervisor() *overlaySupervisor {
	return &overlaySupervisor{}
}

// start runs the overlay until stop is called or ctx ends. Calling it while
// the overlay is already running does nothing.
func (s *overlaySupervisor) start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancel != nil {
		return
	}

	name, args, ok := overlayCommandLine()
	if !ok {
		log.Printf("Overlay: %s not found in PATH or ~/.local/bin, running without an "+
			"on-screen indicator (set %s to point at it)", overlayCommand, overlayCommandEnv)
		return
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.cancel = cancel
	s.done = done

	go func() {
		defer close(done)
		superviseOverlay(runCtx, name, args)
	}()
}

// stop terminates the overlay and waits for it to exit.
func (s *overlaySupervisor) stop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.cancel, s.done = nil, nil
	s.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// superviseOverlay restarts the overlay whenever it exits, until the context
// is cancelled.
func superviseOverlay(ctx context.Context, name string, args []string) {
	for {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Stdout = os.Stderr // the daemon's log, so overlay output is not lost
		cmd.Stderr = os.Stderr

		log.Printf("Overlay: starting %s", name)
		started := time.Now()
		err := cmd.Run()

		if ctx.Err() != nil {
			log.Printf("Overlay: stopped")
			return
		}

		// An immediate clean exit is how the overlay reports that another
		// instance already has the screen, so there is nothing to supervise.
		if err == nil && time.Since(started) < overlayHandoffWindow {
			log.Printf("Overlay: an instance is already running, leaving it to that one")
			return
		}

		log.Printf("Overlay: exited (%v), restarting in %v", err, overlayRestartDelay)

		select {
		case <-ctx.Done():
			return
		case <-time.After(overlayRestartDelay):
		}
	}
}

// overlayCommandLine resolves the overlay command, preferring the environment
// override so a checkout can be run in place.
func overlayCommandLine() (string, []string, bool) {
	if override := strings.Fields(os.Getenv(overlayCommandEnv)); len(override) > 0 {
		return override[0], override[1:], true
	}

	if path, err := exec.LookPath(overlayCommand); err == nil {
		return path, nil, true
	}

	// ~/.local/bin is where a user install puts the overlay, but a daemon
	// started by systemd does not have it on PATH, so look there directly
	// rather than reporting the overlay as missing.
	if home, err := os.UserHomeDir(); err == nil {
		path := filepath.Join(home, ".local", "bin", overlayCommand)
		if isExecutable(path) {
			return path, nil, true
		}
	}

	return "", nil, false
}

// isExecutable reports whether path is a regular file the daemon can run.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

// publishNotice carries a user-facing message to the overlay on the status
// stream, in place of a desktop notification.
func (d *Daemon) publishNotice(msg notify.Message) {
	d.hub.broadcast(pipeline.StatusEvent{
		Status:    d.status(),
		Listening: d.listening(),
		Notice:    &msg,
		At:        time.Now(),
	})
}

// monitorPipelinePartials forwards transcript snapshots to subscribers, so an
// indicator can show words as they are recognised. A batch transcriber
// produces none and the loop simply idles.
func (d *Daemon) monitorPipelinePartials(p pipeline.Pipeline) {
	partialCh := p.GetPartialCh()
	for {
		select {
		case update, ok := <-partialCh:
			if !ok {
				return
			}
			d.hub.broadcast(pipeline.StatusEvent{
				Status:     p.Status(),
				Listening:  p.Listening(),
				Transcript: &update,
				At:         time.Now(),
			})
		case <-d.ctx.Done():
			return
		}
	}
}
