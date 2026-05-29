package iterm2

import (
	"errors"
)

// Reconcile closes iTerm2 tabs for any registered display-name not present in
// liveNames, then drops the entry. Idempotent — safe to call on every tick.
//
// Unlike macuake, the close here is prune-first / best-effort: because the tab
// runs an exec'd `tmux attach`, when the tmux session dies iTerm2 reaps the
// session on its own (profile default "When a session ends → Close"). So the
// daemon's real job is registry hygiene; an Apple-event denial (ErrPermission)
// or a vanished tab (ErrUnavailable / not-found) is soft — we still drop the
// entry so we never loop. `logf` is invoked for genuine, unexpected errors.
func Reconcile(c *Client, reg *Registry, liveNames map[string]bool, logf func(format string, args ...any)) {
	if c == nil || reg == nil {
		return
	}
	_ = reg.WithLock(func(tabs map[string]Tab) (bool, error) {
		dirty := false
		for name, tab := range tabs {
			if liveNames[name] {
				continue
			}
			err := c.CloseSession(tab.SessionID)
			if err != nil {
				switch {
				case errors.Is(err, ErrUnavailable):
					// iTerm2's gone — bail this tick, keep state for later.
					return dirty, nil
				case errors.Is(err, ErrPermission):
					// No Apple-event grant in the daemon; the tab self-reaps
					// anyway. Drop the entry, don't log noise.
				case IsNotFound(err):
					// Tab already gone — expected (self-reaped).
				default:
					if logf != nil {
						logf("iterm2 close-session %s: %v", name, err)
					}
					// Fall through: drop the entry so a permanently-bad
					// session_id doesn't make us retry forever.
				}
			}
			delete(tabs, name)
			dirty = true
		}
		return dirty, nil
	})
}
