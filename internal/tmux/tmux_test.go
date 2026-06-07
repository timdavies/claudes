package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
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

// Pane-targeted commands (capture-pane, send-keys via paste-buffer) need the
// `=name:` target form — bare `=name` is parsed as a pane spec and fails with
// "can't find pane". This is the path `claudes read`/`claudes write` ride on;
// it was broken while the session-target tests above stayed green.
func TestPaneCommandsAcceptExactTarget(t *testing.T) {
	c := newTestClient(t)
	if err := c.NewSession("foo-bar", "", nil, []string{"cat"}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if err := c.SendKeys("foo-bar", "hello-exact"); err != nil {
		t.Fatalf("SendKeys(foo-bar): %v", err)
	}
	if err := c.SendKeys("foo", "nope"); err == nil {
		t.Error("SendKeys(foo) succeeded with only foo-bar running")
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		out, err := c.CapturePane("foo-bar", 50)
		if err != nil {
			t.Fatalf("CapturePane(foo-bar): %v", err)
		}
		if strings.Contains(out, "hello-exact") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sent text never appeared in pane; capture:\n%s", out)
		}
		time.Sleep(50 * time.Millisecond)
	}

	if _, err := c.CapturePane("foo", 5); err == nil {
		t.Error("CapturePane(foo) succeeded with only foo-bar running")
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
