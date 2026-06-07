package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// newTestClient starts a throwaway tmux server on a scratch socket and
// registers cleanup. Skips when tmux isn't installed.
func newTestClient(t *testing.T) *Client {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	c := New(fmt.Sprintf("claudes-test-%d", os.Getpid()), "")
	t.Cleanup(func() {
		_ = c.cmd("kill-server").Run()
	})
	return c
}

// Regression test: tmux treats a bare `-t name` as a prefix (then fnmatch)
// pattern, so with only "foo-bar" running, `has-session -t foo` succeeded —
// `claudes new foo` then failed with "already exists". Session targets must
// use the exact-match form `-t =name`.
func TestHasIsExactMatch(t *testing.T) {
	c := newTestClient(t)
	if err := c.NewSession("foo-bar", "", nil, []string{"sleep", "60"}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	has, err := c.Has("foo")
	if err != nil {
		t.Fatalf("Has(foo): %v", err)
	}
	if has {
		t.Error("Has(foo) = true with only foo-bar running; prefix match leaked through")
	}

	has, err = c.Has("foo-bar")
	if err != nil {
		t.Fatalf("Has(foo-bar): %v", err)
	}
	if !has {
		t.Error("Has(foo-bar) = false, want true")
	}
}

// Kill must not prefix-match either — `kill-session -t foo` with only
// "foo-bar" running used to kill foo-bar.
func TestKillIsExactMatch(t *testing.T) {
	c := newTestClient(t)
	if err := c.NewSession("foo-bar", "", nil, []string{"sleep", "60"}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if err := c.Kill("foo"); err == nil {
		t.Error("Kill(foo) succeeded with only foo-bar running; prefix match leaked through")
	}

	has, err := c.Has("foo-bar")
	if err != nil {
		t.Fatalf("Has(foo-bar): %v", err)
	}
	if !has {
		t.Error("foo-bar was killed by Kill(foo)")
	}
}
