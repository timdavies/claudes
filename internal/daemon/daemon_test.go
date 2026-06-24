package daemon

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// TestDaemonAliveFlock verifies liveness is decided by the pidfile flock, not by
// signalling — so it stays correct from a sandboxed CLI that can't Kill(0).
func TestDaemonAliveFlock(t *testing.T) {
	dir := t.TempDir()

	// Nothing on disk → not alive.
	if alive, _ := daemonAlive(dir); alive {
		t.Fatal("no pidfile/heartbeat should read as not alive")
	}

	if err := os.WriteFile(pidPath(dir), []byte(`{"pid":12345,"path":"/x"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pidfile present but unlocked → daemon is dead (its lock would be released).
	if alive, e := daemonAlive(dir); alive || e.PID != 12345 {
		t.Fatalf("unlocked pidfile: alive=%v pid=%d (want false/12345)", alive, e.PID)
	}

	// Hold the lock to simulate a live daemon → alive, with the entry parsed.
	f, err := os.OpenFile(pidPath(dir), os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("test couldn't take the lock: %v", err)
	}
	if alive, e := daemonAlive(dir); !alive || e.PID != 12345 {
		t.Fatalf("locked pidfile: alive=%v pid=%d (want true/12345)", alive, e.PID)
	}
}

func TestHeartbeatFresh(t *testing.T) {
	dir := t.TempDir()
	if heartbeatFresh(dir) {
		t.Fatal("missing heartbeat should not be fresh")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := os.WriteFile(heartbeatPath(dir), []byte(now), 0o644); err != nil {
		t.Fatal(err)
	}
	if !heartbeatFresh(dir) {
		t.Fatal("a just-written heartbeat should be fresh")
	}
	old := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	if err := os.WriteFile(heartbeatPath(dir), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if heartbeatFresh(dir) {
		t.Fatal("a 10-minute-old heartbeat should be stale")
	}
}
