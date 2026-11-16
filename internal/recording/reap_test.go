package recording

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Stop must reap pw-record. A child that is killed but never waited on stays
// a zombie in the daemon's process table, one per dictation.
func TestRecorder_StopReapsPwRecord(t *testing.T) {
	bin := t.TempDir()
	fake := filepath.Join(bin, "pw-record")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexec cat /dev/zero\n"), 0o755); err != nil {
		t.Fatalf("write fake pw-record: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	rec := NewRecorder(Config{
		SampleRate: 16000, Channels: 1, Format: "s16",
		BufferSize: 1024, ChannelBufferSize: 4, Timeout: time.Minute,
	}).(*recorder)

	frames, _, err := rec.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-frames // the child is running once a frame arrives

	rec.mu.Lock()
	pid := rec.cmd.Process.Pid
	rec.mu.Unlock()

	rec.Stop()

	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err == nil {
		t.Fatalf("pw-record (pid %d) not reaped after Stop: %s", pid, stat)
	}
}
