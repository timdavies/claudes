package tmux

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// Client wraps tmux commands against a specific socket.
type Client struct {
	Socket     string
	TmuxConfig string // optional -f path
}

func New(socket, tmuxConfig string) *Client {
	return &Client{Socket: socket, TmuxConfig: tmuxConfig}
}

// BaseArgs returns the leading tmux args (-L socket, -f config) that every
// invocation prepends. Exported so callers can reconstruct an equivalent
// tmux command line — e.g. the tab integration sends
// `tmux <base-args> attach-session -t <name>` into a new tab's shell.
func (c *Client) BaseArgs() []string {
	args := []string{}
	if c.Socket != "" {
		args = append(args, "-L", c.Socket)
	}
	if c.TmuxConfig != "" {
		args = append(args, "-f", c.TmuxConfig)
	}
	return args
}

func (c *Client) cmd(args ...string) *exec.Cmd {
	full := append(c.BaseArgs(), args...)
	return exec.Command("tmux", full...)
}

// Info is the raw per-session data we can pull from tmux.
type Info struct {
	Name     string
	Path     string
	PanePID  int
	Attached bool
}

// List returns all sessions on the socket. Returns empty slice if no server.
func (c *Client) List() ([]Info, error) {
	out, err := c.cmd("list-sessions", "-F", "#{session_name}|#{session_path}|#{pane_pid}|#{?session_attached,1,0}").CombinedOutput()
	if err != nil {
		// tmux exits non-zero when there's no server; treat as empty
		s := string(out)
		if strings.Contains(s, "no server running") ||
			strings.Contains(s, "error connecting") ||
			strings.Contains(s, "No such file or directory") {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux list-sessions: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var infos []Info
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 4 {
			continue
		}
		pid, _ := strconv.Atoi(parts[2])
		infos = append(infos, Info{
			Name:     parts[0],
			Path:     parts[1],
			PanePID:  pid,
			Attached: parts[3] == "1",
		})
	}
	return infos, nil
}

// Has returns true if a session by full name exists.
func (c *Client) Has(name string) (bool, error) {
	out, err := c.cmd("has-session", "-t", name).CombinedOutput()
	if err == nil {
		return true, nil
	}
	// has-session returns non-zero for missing — distinguish from real errors
	s := string(out)
	if s == "" ||
		strings.Contains(s, "can't find session") ||
		strings.Contains(s, "no server running") ||
		strings.Contains(s, "session not found") ||
		strings.Contains(s, "error connecting") {
		return false, nil
	}
	return false, fmt.Errorf("tmux has-session: %w: %s", err, strings.TrimSpace(s))
}

// NewSession creates a detached tmux session running cmd in dir.
// extraEnv is passed to the spawned shell as KEY=VALUE entries.
func (c *Client) NewSession(name, dir string, extraEnv []string, cmdline []string) error {
	args := []string{"new-session", "-d", "-s", name}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	for _, e := range extraEnv {
		args = append(args, "-e", e)
	}
	args = append(args, cmdline...)
	command := c.cmd(args...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

// SessionEnv returns the tmux session-level environment as a KEY→VALUE map.
// Variables marked for removal (lines starting with `-`) are skipped.
// Missing/empty session returns an empty map without error.
func (c *Client) SessionEnv(name string) (map[string]string, error) {
	out, err := c.cmd("show-environment", "-t", name).CombinedOutput()
	if err != nil {
		s := string(out)
		if strings.Contains(s, "can't find session") ||
			strings.Contains(s, "session not found") ||
			strings.Contains(s, "no server running") {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("tmux show-environment: %w: %s", err, strings.TrimSpace(s))
	}
	env := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "-") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		env[k] = v
	}
	return env, nil
}

// PaneSessionName returns the session name owning paneID (typically $TMUX_PANE),
// resolved on this client's socket. Errors when the server doesn't know the pane
// — e.g. it lives on a different (nested) tmux server.
func (c *Client) PaneSessionName(paneID string) (string, error) {
	out, err := c.cmd("display-message", "-p", "-t", paneID, "#{session_name}").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux display-message: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// SetSessionEnv sets a single key=value in the session env.
func (c *Client) SetSessionEnv(name, key, value string) error {
	out, err := c.cmd("set-environment", "-t", name, key, value).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux set-environment: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// UnsetSessionEnv removes a key from the session env.
func (c *Client) UnsetSessionEnv(name, key string) error {
	out, err := c.cmd("set-environment", "-u", "-t", name, key).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux set-environment -u: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Rename renames a session.
func (c *Client) Rename(oldName, newName string) error {
	out, err := c.cmd("rename-session", "-t", oldName, newName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux rename-session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Kill removes a session immediately.
func (c *Client) Kill(name string) error {
	out, err := c.cmd("kill-session", "-t", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux kill-session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SendKeys sends text into the first pane as a single bracketed-paste block,
// then presses Enter to submit.
//
// Implementation note: we route the body through a tmux paste buffer
// (load-buffer + paste-buffer -p) rather than `send-keys -l`. Two reasons:
//
//  1. `send-keys -l <text>` passes the entire body as a single argv, which
//     hits the OS argv limit at ~17KB.
//  2. tmux 3.4+ wraps long literal text in bracketed-paste markers and may
//     split the input into multiple chunks; Claude Code's TUI then consumes
//     the trailing Enter as part of paste-edit-mode exit instead of submitting
//     the prompt. paste-buffer -p delivers the whole payload as one paste.
//
// "Literally" still applies: tmux key-name interpretation is disabled for the
// body — passing "Up" pastes the two characters U, p, not the Up arrow. Use
// SendRawKeys for key names. When text is empty we just send Enter (the
// `/exit` flow in cmd/stop.go relies on this).
func (c *Client) SendKeys(name, text string) error {
	if text != "" {
		// Unique buffer name per call so concurrent sends don't collide and a
		// leak (if paste-buffer never runs) is harmless rather than corrupting
		// the next send.
		var randBytes [8]byte
		if _, err := rand.Read(randBytes[:]); err != nil {
			return fmt.Errorf("tmux send-keys: random buffer name: %w", err)
		}
		buf := "claudes-send-" + hex.EncodeToString(randBytes[:])

		loadCmd := c.cmd("load-buffer", "-b", buf, "-")
		loadCmd.Stdin = strings.NewReader(text)
		var loadOut bytes.Buffer
		loadCmd.Stdout = &loadOut
		loadCmd.Stderr = &loadOut
		if err := loadCmd.Run(); err != nil {
			return fmt.Errorf("tmux load-buffer: %w: %s", err, strings.TrimSpace(loadOut.String()))
		}

		// -p: bracketed paste; -d: delete buffer after pasting.
		out, err := c.cmd("paste-buffer", "-p", "-d", "-b", buf, "-t", name).CombinedOutput()
		if err != nil {
			// paste-buffer didn't consume the buffer — clean it up so it
			// doesn't sit on the server forever.
			_, _ = c.cmd("delete-buffer", "-b", buf).CombinedOutput()
			return fmt.Errorf("tmux paste-buffer: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	out, err := c.cmd("send-keys", "-t", name, "Enter").CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux send-keys: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SendRawKeys passes each arg to tmux send-keys as a key name (Up, C-c, Escape,
// etc.) or literal token if no key name matches. No trailing Enter is added.
func (c *Client) SendRawKeys(name string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	args := append([]string{"send-keys", "-t", name}, keys...)
	out, err := c.cmd(args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux send-keys: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// CapturePane returns the last `lines` lines from the pane scrollback.
func (c *Client) CapturePane(name string, lines int) (string, error) {
	if lines <= 0 {
		lines = 50
	}
	args := []string{"capture-pane", "-p", "-t", name, "-S", "-" + strconv.Itoa(lines)}
	var buf bytes.Buffer
	cmd := c.cmd(args...)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w: %s", err, strings.TrimSpace(buf.String()))
	}
	return buf.String(), nil
}

// Attach replaces the current process with `tmux attach`. Does not return on success.
func (c *Client) Attach(name string) error {
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	args := append([]string{"tmux"}, c.BaseArgs()...)
	args = append(args, "attach-session", "-t", name)
	return syscall.Exec(tmuxBin, args, os.Environ())
}

// PipePaneTail attaches a pipe to the pane and tails it to stdout until ctx is done.
// Returns a stop function that disables the pipe.
func (c *Client) PipePaneStart(name, fifo string) error {
	out, err := c.cmd("pipe-pane", "-t", name, "-O", fmt.Sprintf("cat >> %q", fifo)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux pipe-pane: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (c *Client) PipePaneStop(name string) error {
	_, err := c.cmd("pipe-pane", "-t", name).CombinedOutput()
	return err
}

// IsMissingSessionErr reports whether the error is "session does not exist".
func IsMissingSessionErr(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, exec.ErrNotFound) ||
		strings.Contains(err.Error(), "can't find session") ||
		strings.Contains(err.Error(), "session not found")
}
