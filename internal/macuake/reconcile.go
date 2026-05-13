package macuake

import (
	"errors"
)

// Reconcile closes macuake tabs for any registered display-name not present
// in liveNames, then drops the entry. Idempotent — safe to call on every tick.
// `logf` is invoked for non-fatal errors (callers wire it to their logger).
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
				if errors.Is(err, ErrUnavailable) {
					// macuake's gone — bail this tick, keep state for later.
					return dirty, nil
				}
				if !IsNotFound(err) && logf != nil {
					logf("macuake close-session %s: %v", name, err)
				}
				// Fall through: even on error we drop the entry so we don't
				// retry forever on a permanently-bad session_id.
			}
			delete(tabs, name)
			dirty = true
		}
		return dirty, nil
	})
}
