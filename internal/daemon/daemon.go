// Package daemon implements the long-running supervisor that summarizes
// claudes-managed sessions while ≥1 session exists.
//
// Lifecycle: lazy-spawn from the CLI when a session exists, self-exit when
// the session count hits zero. No launchd. No /loop. The daemon and CLI
// communicate through three flat files in $XDG_CACHE_HOME/claudes (or
// ~/.cache/claudes):
//
//	daemon.pid    — PID + invoking binary path of the running daemon
//	daemon.log    — daemon stdout/stderr (line-buffered)
//	hashes.json   — per-session pane hash + timestamp, to skip re-summarizing
//	                a session whose pane hasn't changed since last tick.
//
// Per tick: list sessions; if zero → graceful exit. Else for each session,
// capture pane, hash, compare to hashes.json. On miss, call `claude -p
// --model haiku <prompt>` with the pane content and write the response into
// the tmux session env at @claudes-description, which `claudes ls` displays.
package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/session"
	"github.com/timdavies/claudes/internal/tmux"
)

const (
	pidFilename       = "daemon.pid"
	heartbeatFilename = "daemon.heartbeat"
	logFilename       = "daemon.log"
	hashesFilename    = "hashes.json"

	defaultTickInterval = 60 * time.Second
	captureLines        = 80
	maxDescLen          = 200 // hard cap on Haiku's output, just in case
	summaryModel        = "haiku"
	summaryTimeout      = 30 * time.Second
)

// tickInterval honors CLAUDES_DAEMON_TICK (e.g. "5s", "30s") for development;
// otherwise uses defaultTickInterval.
func tickInterval() time.Duration {
	if v := os.Getenv("CLAUDES_DAEMON_TICK"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultTickInterval
}

// metaPrompt is the Haiku instruction. Kept short and prescriptive.
const metaPrompt = `Below is the current pane content of a Claude Code agent (a TUI). In 8-12 words, describe what the agent is currently doing. Output ONLY the description sentence — no preamble, no quotes, no trailing period unless it's a complete sentence requiring one. Examples of good output:

fixing flaky auth_spec.rb in grow repo
waiting for user to approve a destructive bash command
idle at empty prompt
debugging a broken pipe to the cmux unix socket

Pane content:

`

// CacheDir returns the persistent state directory.
func CacheDir() (string, error) {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "claudes"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "claudes"), nil
}

func pidPath(dir string) string       { return filepath.Join(dir, pidFilename) }
func heartbeatPath(dir string) string { return filepath.Join(dir, heartbeatFilename) }
func logPath(dir string) string       { return filepath.Join(dir, logFilename) }
func hashesPath(dir string) string    { return filepath.Join(dir, hashesFilename) }

// pidEntry is the structured content of pidPath. Stored as one line of JSON
// so a stale pidfile from an earlier binary version still reads cleanly.
type pidEntry struct {
	PID  int    `json:"pid"`
	Path string `json:"path"`
}

// Ensure spawns the daemon if it isn't already running and at least one
// session exists. Safe to call concurrently from multiple CLI invocations —
// the loser of a spawn race will detect the winner's pidfile and exit.
//
// `spawnAlways` forces a spawn attempt regardless of session count, used by
// `claudes daemon start` to override the empty-list shortcut.
func Ensure(cfg *config.Config, sessions []session.Session, spawnAlways bool) error {
	if !spawnAlways && len(sessions) == 0 {
		return nil
	}
	dir, err := CacheDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	if alive, entry := readPid(dir); alive {
		// Self-upgrade: if the daemon's binary path differs from the CLI
		// that's invoking us, stop the old daemon and respawn so post-
		// `make install` upgrades take effect on next CLI call.
		self, err := os.Executable()
		if err == nil && entry.Path != "" && entry.Path != self {
			_ = syscall.Kill(entry.PID, syscall.SIGTERM)
			waitGone(dir, 3*time.Second)
		} else {
			return nil
		}
	}
	return spawn()
}

// readPid stats and verifies the pidfile. Returns (alive, entry).
func readPid(dir string) (bool, pidEntry) {
	b, err := os.ReadFile(pidPath(dir))
	if err != nil {
		return false, pidEntry{}
	}
	var e pidEntry
	if err := json.Unmarshal(b, &e); err != nil || e.PID <= 0 {
		// Try legacy "pid only" format, just in case.
		if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			e = pidEntry{PID: pid}
		}
	}
	if e.PID <= 0 {
		return false, e
	}
	if syscall.Kill(e.PID, 0) == nil {
		return true, e
	}
	return false, e
}

func waitGone(dir string, max time.Duration) {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if alive, _ := readPid(dir); !alive {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func spawn() error {
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "daemon", "run")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Inherit env so `claude` is on PATH and the spawned daemon can call it.
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn daemon: %w", err)
	}
	// Detach: don't Wait, let the process live independently.
	go func() { _ = cmd.Process.Release() }()
	return nil
}

// Run is the daemon main loop. It returns nil when the session count hits
// zero (graceful exit) or when SIGTERM lands. Errors mid-loop are logged but
// don't terminate the daemon.
func Run(cfg *config.Config) error {
	dir, err := CacheDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// Redirect stdout/stderr to the log file (append). Daemons started via
	// spawn() have nil stdio, so this is the first place output can go.
	if logF, err := os.OpenFile(logPath(dir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		os.Stdout = logF
		os.Stderr = logF
	}

	if err := acquirePid(dir); err != nil {
		// Lost the spawn race or stale pidfile; log and exit silently.
		fmt.Fprintf(os.Stderr, "daemon: not starting: %v\n", err)
		return nil
	}
	defer os.Remove(pidPath(dir))

	// SIGTERM handler so `claudes daemon stop` (or self-upgrade) is graceful.
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGTERM, syscall.SIGINT)

	client := tmux.New(cfg.TmuxSocket, cfg.TmuxConfig)
	cache := newHashCache(hashesPath(dir))
	cache.load()

	tick := tickInterval()
	logf("daemon starting; pid=%d tick=%s", os.Getpid(), tick)

	for {
		writeHeartbeat(dir)
		sessions, err := session.List(client, cfg)
		if err != nil {
			logf("list sessions: %v", err)
		}
		if len(sessions) == 0 {
			logf("no sessions, exiting")
			return nil
		}
		// Summarize each session that's changed since last tick.
		for _, s := range sessions {
			full := session.FullName(cfg.Prefix, s.Name)
			content, err := client.CapturePane(full, captureLines)
			if err != nil {
				logf("capture %s: %v", s.Name, err)
				continue
			}
			h := hash(content)
			if cache.get(s.Name) == h {
				continue
			}
			desc, err := summarize(content)
			if err != nil {
				logf("summarize %s: %v", s.Name, err)
				continue
			}
			if desc == "" {
				continue
			}
			if err := client.SetSessionEnv(full, "@claudes-description", desc); err != nil {
				logf("set env %s: %v", s.Name, err)
				continue
			}
			cache.set(s.Name, h)
			logf("described %s: %s", s.Name, desc)
		}
		cache.save()

		select {
		case <-stopCh:
			logf("daemon: SIGTERM received, exiting")
			return nil
		case <-time.After(tick):
		}
	}
}

func acquirePid(dir string) error {
	path := pidPath(dir)
	// O_EXCL ensures only one daemon writes the pidfile per startup.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		// If the file exists but the process is dead, take over. Otherwise lose.
		if alive, _ := readPid(dir); alive {
			return fmt.Errorf("another daemon is running")
		}
		_ = os.Remove(path)
		f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("create pidfile: %w", err)
		}
	}
	defer f.Close()
	self, _ := os.Executable()
	entry := pidEntry{PID: os.Getpid(), Path: self}
	enc := json.NewEncoder(f)
	if err := enc.Encode(entry); err != nil {
		return fmt.Errorf("write pidfile: %w", err)
	}
	return nil
}

func writeHeartbeat(dir string) {
	_ = os.WriteFile(heartbeatPath(dir), []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s "+format+"\n", append([]any{time.Now().Format("15:04:05")}, args...)...)
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// summarize calls `claude -p` non-interactively with the meta-prompt. Returns
// a single-line description. Truncates to maxDescLen.
func summarize(paneContent string) (string, error) {
	if strings.TrimSpace(paneContent) == "" {
		return "", nil
	}
	ctx := metaPrompt + paneContent
	cmd := exec.Command("claude", "-p", "--model", summaryModel)
	cmd.Stdin = strings.NewReader(ctx)
	var out strings.Builder
	var stderr strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return "", fmt.Errorf("claude -p: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
	case <-time.After(summaryTimeout):
		_ = cmd.Process.Kill()
		<-done
		return "", errors.New("claude -p timed out")
	}
	desc := strings.TrimSpace(out.String())
	// Take the first non-empty line; Haiku occasionally adds trailing context.
	if i := strings.IndexAny(desc, "\n"); i >= 0 {
		desc = strings.TrimSpace(desc[:i])
	}
	if len(desc) > maxDescLen {
		desc = desc[:maxDescLen]
	}
	return desc, nil
}

// hashCache persists per-session pane hashes to skip re-summarizing.
type hashCache struct {
	path string
	mu   sync.Mutex
	data map[string]hashEntry
}

type hashEntry struct {
	Hash      string `json:"hash"`
	UpdatedAt string `json:"updated_at"`
}

func newHashCache(path string) *hashCache {
	return &hashCache{path: path, data: map[string]hashEntry{}}
}

func (c *hashCache) load() {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := os.ReadFile(c.path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &c.data)
}

func (c *hashCache) save() {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := json.Marshal(c.data)
	if err != nil {
		return
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, c.path)
}

func (c *hashCache) get(name string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.data[name].Hash
}

func (c *hashCache) set(name, h string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[name] = hashEntry{Hash: h, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
}

// Stop sends SIGTERM to a running daemon (if any). Returns nil if none.
func Stop() error {
	dir, err := CacheDir()
	if err != nil {
		return err
	}
	alive, entry := readPid(dir)
	if !alive {
		return nil
	}
	if err := syscall.Kill(entry.PID, syscall.SIGTERM); err != nil {
		return fmt.Errorf("kill daemon: %w", err)
	}
	waitGone(dir, 3*time.Second)
	return nil
}

// Status returns a printable string about the running daemon.
func Status() (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	alive, entry := readPid(dir)
	if !alive {
		return "not running", nil
	}
	hb, _ := os.ReadFile(heartbeatPath(dir))
	return fmt.Sprintf("running pid=%d binary=%s last_tick=%s", entry.PID, entry.Path, strings.TrimSpace(string(hb))), nil
}

// LogPath returns the daemon log file path (used by `claudes daemon logs`).
func LogPath() (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	return logPath(dir), nil
}

// TailLog copies up to N bytes from the end of the log file to w.
func TailLog(w io.Writer, n int64) error {
	p, err := LogPath()
	if err != nil {
		return err
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() > n {
		_, _ = f.Seek(-n, io.SeekEnd)
	}
	_, err = io.Copy(w, f)
	return err
}
