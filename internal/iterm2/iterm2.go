// Package iterm2 is a thin client that drives iTerm2 over AppleScript.
//
// It exposes the tab-client surface the cmd layer routes through
// (NewTab/CloseSession/Focus/SetAppearance/Execute/List).
//
// Every operation shells out to `osascript`, feeding the script on stdin to
// dodge `-e` quoting. Each script is prefixed with an `application "iTerm2" is
// running` guard: that's a Launch Services query (no launch, no Apple event, no
// TCC prompt), so we never accidentally start iTerm2 just to ask about it. A
// session is addressed by its AppleScript `id` — a UUID stable for the life of
// the session — which we locate by walking windows → tabs → sessions, since the
// dictionary has no top-level `session id "X"` accessor.
package iterm2

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// DefaultTimeout caps every osascript invocation. AppleScript round-trips to a
// running iTerm2 are typically tens of ms; the headroom is for a close that
// stalls on a confirmation dialog (see CloseSession).
const DefaultTimeout = 5 * time.Second

// ErrUnavailable means iTerm2 isn't running (or has no window to act on).
// Callers treat this as "no tab UI available" and continue silently.
var ErrUnavailable = errors.New("iterm2 unavailable")

// ErrPermission means macOS denied the Apple event (TCC Automation not granted,
// AppleScript error -1743). Distinct from ErrUnavailable so callers can log it
// once and explain the fix rather than silently degrading forever.
var ErrPermission = errors.New("iterm2 automation not permitted")

// errNotFound is returned by id-addressed ops when no session matches.
var errNotFound = errors.New("iterm2: session not found")

type Client struct {
	Timeout time.Duration
}

// TabInfo is the list-entry shape. CWD is always "" — iTerm2's AppleScript
// dictionary has no cwd property, and no caller needs it.
type TabInfo struct {
	Index     int
	SessionID string
	Title     string
	CWD       string
	Active    bool
}

func New(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{Timeout: timeout}
}

// run executes an AppleScript body wrapped in the is-running guard and returns
// trimmed stdout. A guard miss yields ErrUnavailable; an Apple-event denial
// yields ErrPermission.
func (c *Client) run(body string) (string, error) {
	script := "if application \"iTerm2\" is not running then return \"NOTRUNNING\"\n" + body
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "osascript", "-")
	cmd.Stdin = strings.NewReader(script)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb

	if err := cmd.Run(); err != nil {
		stderr := errb.String()
		if isPermissionErr(stderr) {
			return "", ErrPermission
		}
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("iterm2: osascript timed out: %s", strings.TrimSpace(stderr))
		}
		return "", fmt.Errorf("iterm2: osascript: %v: %s", err, strings.TrimSpace(stderr))
	}
	result := strings.TrimRight(out.String(), "\n")
	if result == "NOTRUNNING" {
		return "", ErrUnavailable
	}
	return result, nil
}

func isPermissionErr(stderr string) bool {
	return strings.Contains(stderr, "-1743") ||
		strings.Contains(stderr, "Not authorized to send Apple events") ||
		strings.Contains(stderr, "not allowed to send Apple events")
}

// NewTab opens a new tab in the current window (creating a window if none) and
// returns its session id. dir is best-effort: the subsequent Execute exec's
// tmux and replaces the shell, so the cd only matters if attach never happens.
func (c *Client) NewTab(dir string) (string, error) {
	cd := ""
	if dir != "" {
		cd = fmt.Sprintf("\n  tell s to write text \"cd \" & quoted form of \"%s\"", asEscape(dir))
	}
	body := fmt.Sprintf(`tell application "iTerm2"
  if (count of windows) = 0 then
    set w to (create window with default profile)
  else
    set w to current window
  end if
  tell w to set t to (create tab with default profile)
  set s to current session of t%s
  return id of s
end tell`, cd)
	sid, err := c.run(body)
	if err != nil {
		return "", err
	}
	if sid == "" {
		return "", fmt.Errorf("iterm2: new-tab returned no session id")
	}
	return sid, nil
}

// SetAppearance sets the session's name. tmux's set-titles will also drive the
// title (to the same displayName), so this mainly covers the brief window before
// tmux attaches.
func (c *Client) SetAppearance(sessionID, title string) error {
	return c.matchAction(sessionID, fmt.Sprintf(`set name of s to "%s"`, asEscape(title)))
}

// Execute types command into the session and submits it (write text appends a
// newline).
func (c *Client) Execute(sessionID, command string) error {
	return c.matchAction(sessionID, fmt.Sprintf(`tell s to write text "%s"`, asEscape(command)))
}

// Focus selects the tab + pane, raises the window, and brings iTerm2 forward.
func (c *Client) Focus(sessionID string) error {
	// `select w` raises the containing window; `select t`/`select s` pick the
	// tab and split; `activate` brings iTerm2 itself to the front. We previously
	// used `set frontmost of w to true`, but that throws -10000 (AppleEvent
	// handler failed) against a window reference from the enclosing repeat loop.
	return c.matchAction(sessionID, "select w\n          select t\n          select s\n          activate")
}

// CloseSession closes the matched session. Note: if the session still has a
// running job, iTerm2 may show a "Prompt before closing" dialog that blocks
// until our timeout — set the profile's prompt to "No". In normal operation the
// tmux session is killed first, so the exec'd tmux client has already exited and
// iTerm2 reaps the session without a prompt; this is the fallback path.
func (c *Client) CloseSession(sessionID string) error {
	return c.matchAction(sessionID, "close s")
}

// matchAction runs `action` against the session whose id matches sessionID.
// Returns errNotFound if no session matches.
func (c *Client) matchAction(sessionID, action string) error {
	body := fmt.Sprintf(`tell application "iTerm2"
  set theID to "%s"
  repeat with w in windows
    repeat with t in tabs of w
      repeat with s in sessions of t
        if (id of s) is theID then
          %s
          return "OK"
        end if
      end repeat
    end repeat
  end repeat
end tell
return "NOTFOUND"`, asEscape(sessionID), action)
	out, err := c.run(body)
	if err != nil {
		return err
	}
	if out == "NOTFOUND" {
		return errNotFound
	}
	return nil
}

// List returns all current iTerm2 sessions. Fields are emitted as
// `id \t active \t name` per line and split with SplitN(.,3) so a tab inside a
// title can't shift columns.
func (c *Client) List() ([]TabInfo, error) {
	body := `tell application "iTerm2"
  set out to ""
  set cur to ""
  try
    set cur to id of current session of current tab of current window
  end try
  repeat with w in windows
    repeat with t in tabs of w
      repeat with s in sessions of t
        set sid to (id of s)
        set act to "0"
        if sid is cur then set act to "1"
        set out to out & sid & tab & act & tab & (name of s) & linefeed
      end repeat
    end repeat
  end repeat
  return out
end tell`
	out, err := c.run(body)
	if err != nil {
		return nil, err
	}
	var tabs []TabInfo
	for i, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		tabs = append(tabs, TabInfo{
			Index:     i,
			SessionID: parts[0],
			Active:    parts[1] == "1",
			Title:     parts[2],
		})
	}
	return tabs, nil
}

// IsNotFound reports whether err is the "no session matched" result — callers
// treat this as success when closing or focusing a tab that's already gone.
func IsNotFound(err error) bool {
	return errors.Is(err, errNotFound)
}

// asEscape escapes a Go string for embedding in an AppleScript double-quoted
// literal. Backslashes and quotes are escaped; raw control chars (newline, CR,
// tab) are stripped so a hostile title/dir can't break out of the literal.
// Our own command strings legitimately contain backslash sequences (printf
// '\033...'), which are escaped here, never stripped.
func asEscape(s string) string {
	s = strings.NewReplacer("\n", "", "\r", "", "\t", "").Replace(s)
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
