// Package macuake is a thin client for the macuake control socket.
//
// macuake is a Quake-style macOS terminal that exposes a Unix-socket control
// API at /tmp/macuake.sock. Requests are newline-delimited JSON of the form
// {"action": "...", ...args} and responses are {"ok": true|false, ...}. The
// socket is opt-in (the user enables it in macuake's settings); we treat
// "socket missing" or "connection refused" as a normal state — claudes
// continues to work without visible tabs.
package macuake

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// DefaultSocket is where macuake listens by default.
const DefaultSocket = "/tmp/macuake.sock"

// DefaultTimeout caps every request; macuake responses are typically <10ms.
const DefaultTimeout = 500 * time.Millisecond

// ErrUnavailable means the socket isn't reachable (macuake not running, socket
// missing, or connection refused). Callers should treat this as "no tab UI
// available" and continue silently.
var ErrUnavailable = errors.New("macuake unavailable")

type Client struct {
	Socket  string
	Timeout time.Duration

	mu sync.Mutex // serializes in-process calls; macuake's socket drops
	// concurrent connections with "socket is not connected" / broken pipe.
}

func New(socket string, timeout time.Duration) *Client {
	if socket == "" {
		socket = DefaultSocket
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{Socket: socket, Timeout: timeout}
}

// TabInfo mirrors macuake's `list` action response entries.
type TabInfo struct {
	Index     int    `json:"index"`
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	CWD       string `json:"cwd"`
	Active    bool   `json:"active"`
}

func (c *Client) call(req map[string]any) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Stat the socket first: this catches "macuake not installed / not running"
	// uniformly across platforms (different dial errors otherwise — EINVAL on
	// long paths, ENOENT on missing, ECONNREFUSED on dead listener).
	if _, err := os.Stat(c.Socket); err != nil {
		return nil, ErrUnavailable
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	body = append(body, '\n')

	// macuake's socket occasionally hangs up mid-handshake when other
	// processes (e.g. the claudes daemon) are also using it. Retry a few
	// times on transient connection errors before giving up.
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := c.callOnce(body)
		if err == nil {
			return resp, nil
		}
		if errors.Is(err, ErrUnavailable) {
			return nil, err
		}
		if !isTransient(err) {
			return nil, err
		}
		lastErr = err
		time.Sleep(time.Duration(20*(attempt+1)) * time.Millisecond)
	}
	return nil, lastErr
}

func (c *Client) callOnce(body []byte) (map[string]any, error) {
	conn, err := net.DialTimeout("unix", c.Socket, c.Timeout)
	if err != nil {
		if isUnavailable(err) {
			return nil, ErrUnavailable
		}
		return nil, fmt.Errorf("dial macuake: %w", err)
	}
	defer conn.Close()
	if _, err := conn.Write(body); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}
	// Only set the read deadline — setting a deadline BEFORE the write
	// triggers a broken-pipe response from macuake on macOS.
	_ = conn.SetReadDeadline(time.Now().Add(c.Timeout))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		msg, _ := resp["error"].(string)
		if msg == "" {
			msg = "unknown error"
		}
		return resp, fmt.Errorf("macuake: %s", msg)
	}
	return resp, nil
}

func isTransient(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "socket is not connected") ||
		strings.Contains(msg, "connection reset")
}

func isUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	return false
}

// NewTab opens a new macuake tab and returns its session_id. dir is optional.
func (c *Client) NewTab(dir string) (string, error) {
	req := map[string]any{"action": "new-tab"}
	if dir != "" {
		req["directory"] = dir
	}
	resp, err := c.call(req)
	if err != nil {
		return "", err
	}
	sid, _ := resp["session_id"].(string)
	if sid == "" {
		return "", fmt.Errorf("macuake: new-tab returned no session_id")
	}
	return sid, nil
}

// CloseSession closes the tab identified by sessionID.
func (c *Client) CloseSession(sessionID string) error {
	_, err := c.call(map[string]any{"action": "close-session", "session_id": sessionID})
	return err
}

// Focus brings the given tab to the foreground.
func (c *Client) Focus(sessionID string) error {
	_, err := c.call(map[string]any{"action": "focus", "session_id": sessionID})
	return err
}

// SetAppearance sets a tab title. Empty title resets to default.
func (c *Client) SetAppearance(sessionID, title string) error {
	_, err := c.call(map[string]any{"action": "set-appearance", "session_id": sessionID, "title": title})
	return err
}

// Execute types `command` into the tab's shell and presses Enter.
func (c *Client) Execute(sessionID, command string) error {
	_, err := c.call(map[string]any{"action": "execute", "session_id": sessionID, "command": command})
	return err
}

// List returns all current macuake tabs.
func (c *Client) List() ([]TabInfo, error) {
	resp, err := c.call(map[string]any{"action": "list"})
	if err != nil {
		return nil, err
	}
	raw, _ := resp["tabs"].([]any)
	out := make([]TabInfo, 0, len(raw))
	for _, t := range raw {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		info := TabInfo{}
		if v, ok := m["index"].(float64); ok {
			info.Index = int(v)
		}
		info.SessionID, _ = m["session_id"].(string)
		info.Title, _ = m["title"].(string)
		info.CWD, _ = m["cwd"].(string)
		info.Active, _ = m["active"].(bool)
		out = append(out, info)
	}
	return out, nil
}

// IsNotFound reports whether err is a "session not found" response from
// macuake — callers treat this as success when closing tabs.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "session not found") || strings.Contains(msg, "not found")
}
