package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonardotrapani/hyprvoice/internal/notify"
	"github.com/leonardotrapani/hyprvoice/internal/transcriber"
)

// partialPipeline is a Pipeline whose transcript snapshots the test drives.
type partialPipeline struct {
	MockPipeline

	partialCh chan transcriber.TranscriptUpdate
}

func newPartialPipeline() *partialPipeline {
	return &partialPipeline{partialCh: make(chan transcriber.TranscriptUpdate, 4)}
}

func (p *partialPipeline) GetPartialCh() <-chan transcriber.TranscriptUpdate {
	return p.partialCh
}

func TestMonitorPipelinePartials_BroadcastsTranscript(t *testing.T) {
	// Arrange
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &Daemon{hub: newStreamHub(), ctx: ctx}
	sub := d.hub.add()
	p := newPartialPipeline()

	go d.monitorPipelinePartials(p)

	// Act
	p.partialCh <- transcriber.TranscriptUpdate{Final: "hello", Draft: "wor"}

	// Assert
	select {
	case ev := <-sub.ch:
		if ev.Transcript == nil {
			t.Fatalf("event %+v carries no transcript", ev)
		}
		want := transcriber.TranscriptUpdate{Final: "hello", Draft: "wor"}
		if *ev.Transcript != want {
			t.Errorf("transcript = %+v, want %+v", *ev.Transcript, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event reached the subscriber")
	}
}

func TestPublishNotice_ReachesSubscribers(t *testing.T) {
	// Arrange
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &Daemon{hub: newStreamHub(), ctx: ctx}
	sub := d.hub.add()

	// Act
	d.publishNotice(notify.Message{Body: "recording failed", IsError: true})

	// Assert
	select {
	case ev := <-sub.ch:
		if ev.Notice == nil {
			t.Fatalf("event %+v carries no notice", ev)
		}
		if ev.Notice.Body != "recording failed" || !ev.Notice.IsError {
			t.Errorf("notice = %+v, want the error body flagged as an error", *ev.Notice)
		}
	default:
		t.Fatal("no event reached the subscriber")
	}
}

func TestNewNotifier_OverlayTypeUsesOverlayNotifier(t *testing.T) {
	// Arrange: no overlay command anywhere, so nothing is spawned
	t.Setenv(overlayCommandEnv, "")
	t.Setenv("PATH", t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &Daemon{hub: newStreamHub(), overlay: newOverlaySupervisor(), ctx: ctx}
	defer d.overlay.stop()

	tests := map[string]bool{
		"overlay": true,
		"desktop": false,
		"log":     false,
		"none":    false,
	}

	for notifType, wantOverlay := range tests {
		t.Run(notifType, func(t *testing.T) {
			// Act
			notifier := d.newNotifier(notifType, map[notify.MessageType]notify.Message{})

			// Assert
			_, isOverlay := notifier.(*notify.Overlay)
			if isOverlay != wantOverlay {
				t.Errorf("newNotifier(%q) = %T, overlay = %v, want overlay = %v",
					notifType, notifier, isOverlay, wantOverlay)
			}
		})
	}
}

// A missing overlay must leave the daemon working, just without an indicator.
func TestOverlaySupervisor_StartWithoutCommandIsHarmless(t *testing.T) {
	// Arrange
	t.Setenv(overlayCommandEnv, "")
	t.Setenv("PATH", t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := newOverlaySupervisor()

	// Act
	s.start(ctx)

	// Assert: nothing to stop, and stopping is still safe
	s.stop()
	s.stop()
}

func TestOverlayCommandLine(t *testing.T) {
	t.Run("environment override wins", func(t *testing.T) {
		// Arrange
		t.Setenv(overlayCommandEnv, "python3 /opt/hyprvoice-overlay --anchor top")

		// Act
		name, args, ok := overlayCommandLine()

		// Assert
		if !ok {
			t.Fatal("overlayCommandLine() not ok, want the override to be used")
		}
		if name != "python3" {
			t.Errorf("name = %q, want %q", name, "python3")
		}
		wantArgs := []string{"/opt/hyprvoice-overlay", "--anchor", "top"}
		if len(args) != len(wantArgs) {
			t.Fatalf("args = %v, want %v", args, wantArgs)
		}
		for i := range wantArgs {
			if args[i] != wantArgs[i] {
				t.Errorf("args = %v, want %v", args, wantArgs)
				break
			}
		}
	})

	t.Run("falls back to PATH", func(t *testing.T) {
		// Arrange
		dir := t.TempDir()
		stub := filepath.Join(dir, overlayCommand)
		if err := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("writing stub: %v", err)
		}
		t.Setenv(overlayCommandEnv, "")
		t.Setenv("PATH", dir)

		// Act
		name, args, ok := overlayCommandLine()

		// Assert
		if !ok {
			t.Fatal("overlayCommandLine() not ok, want the stub on PATH to be found")
		}
		if name != stub {
			t.Errorf("name = %q, want %q", name, stub)
		}
		if len(args) != 0 {
			t.Errorf("args = %v, want none", args)
		}
	})

	// A user install puts the overlay in ~/.local/bin, which a systemd-started
	// daemon does not have on PATH.
	t.Run("falls back to ~/.local/bin", func(t *testing.T) {
		// Arrange
		home := t.TempDir()
		binDir := filepath.Join(home, ".local", "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatalf("creating bin dir: %v", err)
		}
		stub := filepath.Join(binDir, overlayCommand)
		if err := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("writing stub: %v", err)
		}
		t.Setenv(overlayCommandEnv, "")
		t.Setenv("PATH", t.TempDir())
		t.Setenv("HOME", home)

		// Act
		name, _, ok := overlayCommandLine()

		// Assert
		if !ok {
			t.Fatal("overlayCommandLine() not ok, want the overlay in ~/.local/bin to be found")
		}
		if name != stub {
			t.Errorf("name = %q, want %q", name, stub)
		}
	})

	t.Run("ignores a non-executable file in ~/.local/bin", func(t *testing.T) {
		// Arrange
		home := t.TempDir()
		binDir := filepath.Join(home, ".local", "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatalf("creating bin dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(binDir, overlayCommand), []byte("text"), 0o644); err != nil {
			t.Fatalf("writing stub: %v", err)
		}
		t.Setenv(overlayCommandEnv, "")
		t.Setenv("PATH", t.TempDir())
		t.Setenv("HOME", home)

		// Act
		_, _, ok := overlayCommandLine()

		// Assert
		if ok {
			t.Error("overlayCommandLine() ok, want a non-executable file to be ignored")
		}
	})

	t.Run("reports missing command", func(t *testing.T) {
		// Arrange
		t.Setenv(overlayCommandEnv, "")
		t.Setenv("PATH", t.TempDir())
		t.Setenv("HOME", t.TempDir())

		// Act
		_, _, ok := overlayCommandLine()

		// Assert
		if ok {
			t.Error("overlayCommandLine() ok, want not ok when the overlay is not installed")
		}
	})
}

// The overlay is meant to come back after a crash, for the life of the daemon.
func TestOverlaySupervisor_RestartsOnExit(t *testing.T) {
	// Arrange: a command that records each run and exits immediately
	dir := t.TempDir()
	marker := filepath.Join(dir, "runs")
	stub := filepath.Join(dir, "stub.sh")
	script := "#!/bin/sh\necho run >> " + marker + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("writing stub: %v", err)
	}
	t.Setenv(overlayCommandEnv, stub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := newOverlaySupervisor()

	// Act
	s.start(ctx)
	defer s.stop()

	// Assert: it runs at least once, promptly
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marker); err == nil && len(data) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("overlay command never ran")
}

func TestOverlaySupervisor_StartIsIdempotent(t *testing.T) {
	// Arrange
	t.Setenv(overlayCommandEnv, "")
	t.Setenv("PATH", t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := newOverlaySupervisor()

	// Act
	s.start(ctx)
	s.start(ctx)

	// Assert
	s.stop()
}
