package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/timdavies/claudes/internal/schedule"
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

// TestDaemonAliveMissingPidfileIsDead guards the fix where a clean daemon exit
// (which removes the pidfile) must read as dead even though its last heartbeat
// is still fresh — otherwise `start` no-ops for ~5 minutes after a restart.
func TestDaemonAliveMissingPidfileIsDead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(heartbeatPath(dir), []byte(time.Now().UTC().Format(time.RFC3339)), 0o644); err != nil {
		t.Fatal(err)
	}
	if alive, _ := daemonAlive(dir); alive {
		t.Fatal("missing pidfile must read as dead even with a fresh heartbeat")
	}
}

func TestFireHealthThreshold(t *testing.T) {
	dir := t.TempDir()
	h := &fireHealth{dir: dir}
	permErr := errors.New("git worktree add: exit status 128: Operation not permitted")

	// Below threshold → no warning surfaced.
	h.record(permErr)
	h.record(permErr)
	if _, ok := ReadHealth(dir); ok {
		t.Fatal("2 failures should not warn yet (threshold 3)")
	}

	// Third identical failure → warning written, EPERM detected.
	h.record(permErr)
	got, ok := ReadHealth(dir)
	if !ok || got.Failures != 3 {
		t.Fatalf("expected a warning at 3 failures; ok=%v failures=%d", ok, got.Failures)
	}
	if !IsPermErr(got.Error) {
		t.Fatalf("expected EPERM to be detected in %q", got.Error)
	}

	// A different failure mode resets below threshold and clears the warning.
	h.record(errors.New("some unrelated git error"))
	if _, ok := ReadHealth(dir); ok {
		t.Fatal("a new failure mode should clear the stale warning")
	}

	// A success clears everything.
	h.record(permErr)
	h.record(permErr)
	h.record(permErr)
	h.reset()
	if _, ok := ReadHealth(dir); ok {
		t.Fatal("a successful fire should clear the warning")
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

func TestContainsAuthFailure(t *testing.T) {
	fail := []string{
		"Not logged in · Please run /login",
		"not logged in",
		"some preamble\nPlease run /login to continue\n",
	}
	for _, out := range fail {
		if !containsAuthFailure(out) {
			t.Errorf("expected auth failure for %q", out)
		}
	}
	ok := []string{
		"",
		"Updated the brag doc with 3 entries.",
		"logged in as tim",
	}
	for _, out := range ok {
		if containsAuthFailure(out) {
			t.Errorf("did not expect auth failure for %q", out)
		}
	}
}

func TestRunAuthFailedReadsLog(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "schedules", "9"), 0o755); err != nil {
		t.Fatal(err)
	}
	logRel := filepath.Join("schedules", "9", "9-x.log")
	if err := os.WriteFile(filepath.Join(dir, logRel), []byte("Not logged in · Please run /login\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !runAuthFailed(dir, schedule.Run{LogFile: logRel}) {
		t.Fatal("expected runAuthFailed=true for not-logged-in log")
	}
	if runAuthFailed(dir, schedule.Run{LogFile: ""}) {
		t.Fatal("empty logfile should not report auth failure")
	}
}
