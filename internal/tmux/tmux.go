package tmux

import (
	"bytes"
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

func (c *Client) base() []string {
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
	full := append(c.base(), args...)
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

// Kill removes a session immediately.
func (c *Client) Kill(name string) error {
	out, err := c.cmd("kill-session", "-t", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux kill-session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SendKeys types text into the first pane and presses Enter.
func (c *Client) SendKeys(name, text string) error {
	out, err := c.cmd("send-keys", "-t", name, text, "Enter").CombinedOutput()
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
	args := append([]string{"tmux"}, c.base()...)
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
