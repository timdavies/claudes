package session

import (
	"sync"
	"time"

	"github.com/timdavies/claudes/internal/tmux"
)

// envTTL bounds how stale a cached session env may be. Static fields (project,
// model, group) never change; the daemon-stamped ones (@claudes-cost/state/pr)
// update on their own cadence, so a few seconds of lag is fine — and status
// self-reports still surface within one TTL window.
const envTTL = 4 * time.Second

// EnvCache memoizes per-session tmux environments so repeated List calls (the
// TUI's refresh tick) don't spawn a `tmux show-environment` per session every
// time. An entry is reused while it's younger than envTTL and its pane_pid is
// unchanged; a restarted or replaced session (new pane_pid) forces a refetch.
type EnvCache struct {
	mu      sync.Mutex
	entries map[string]envEntry
	now     func() time.Time // injectable for tests; nil means time.Now
}

type envEntry struct {
	panePID   int
	env       map[string]string
	fetchedAt time.Time
}

func NewEnvCache() *EnvCache {
	return &EnvCache{entries: map[string]envEntry{}}
}

func (c *EnvCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *EnvCache) fetch(client *tmux.Client, name string, panePID int) map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock()
	if e, ok := c.entries[name]; ok && e.panePID == panePID && now.Sub(e.fetchedAt) < envTTL {
		return e.env
	}
	env, err := client.SessionEnv(name)
	if err != nil {
		// On a transient failure, fall back to the last known env rather than
		// blanking the row; a missing entry just yields nil (same as before).
		if e, ok := c.entries[name]; ok {
			return e.env
		}
		return env
	}
	c.entries[name] = envEntry{panePID: panePID, env: env, fetchedAt: now}
	return env
}
