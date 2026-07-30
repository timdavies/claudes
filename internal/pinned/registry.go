// Package pinned persists "pinned agent" metadata so a claudes session
// survives the death of its underlying tmux session. A pinned entry stays in
// `claudes ls` as paused after claude exits, and `claudes start <name>`
// resurrects it from the saved fields.
package pinned

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"syscall"
	"time"
)

// Entry captures everything needed to recreate a session that matches the one
// that was running when it got pinned. Hooks and global config are re-resolved
// from the current config at resurrection time, on purpose — they're not
// session-specific.
type Entry struct {
	Project         string   `json:"project"`
	Model           string   `json:"model"`
	Dir             string   `json:"dir"`
	Group           string   `json:"group,omitempty"`
	DefaultArgs     []string `json:"default_args"`
	PassthroughArgs []string `json:"passthrough_args"`
	PinnedAt        string   `json:"pinned_at"`
	// Order is the manual sort position within the pinned block (1-based).
	// 0 means unset — those sort after ordered entries, by name. Reorder
	// assigns sequential values; new pins get max+1 so they land at the bottom.
	Order int `json:"order,omitempty"`

	// SessionID lets a resurrect `claude --resume` the actual conversation
	// rather than spawning a fresh one. PR/Description carry context stamped
	// from the live session's env.
	SessionID   string `json:"session_id,omitempty"`
	PR          string `json:"pr,omitempty"`
	Description string `json:"description,omitempty"`
}

type Registry struct {
	Path string
	mu   sync.Mutex
}

type registryFile struct {
	Agents map[string]Entry `json:"agents"`
}

func NewRegistry(path string) *Registry { return &Registry{Path: path} }

// WithLock takes the file lock for the duration of fn. The file is created
// (empty JSON) if missing. fn receives the loaded map and may mutate it; on
// return the map is saved if dirty=true.
func (r *Registry) WithLock(fn func(agents map[string]Entry) (dirty bool, err error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	f, err := os.OpenFile(r.Path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := flockExclusive(int(f.Fd())); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	var rf registryFile
	b, err := os.ReadFile(r.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &rf); err != nil {
			rf = registryFile{}
		}
	}
	if rf.Agents == nil {
		rf.Agents = map[string]Entry{}
	}
	// Migration: the "archived" concept was removed. Drop any entry a legacy
	// file still marks archived so it doesn't resurface as a normal pin. Older
	// files load fine either way (unknown JSON keys are ignored); this just
	// clears the stragglers and persists the cleanup on next write.
	migrated := dropLegacyArchived(b, rf.Agents)

	dirty, err := fn(rf.Agents)
	if err != nil {
		return err
	}
	if !dirty && !migrated {
		return nil
	}
	return writeAtomic(r.Path, rf)
}

func (r *Registry) Set(name string, e Entry) error {
	if e.PinnedAt == "" {
		e.PinnedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return r.WithLock(func(agents map[string]Entry) (bool, error) {
		agents[name] = e
		return true, nil
	})
}

func (r *Registry) Get(name string) (Entry, bool) {
	var (
		out Entry
		ok  bool
	)
	_ = r.WithLock(func(agents map[string]Entry) (bool, error) {
		out, ok = agents[name]
		return false, nil
	})
	return out, ok
}

func (r *Registry) Has(name string) bool {
	_, ok := r.Get(name)
	return ok
}

func (r *Registry) Delete(name string) error {
	return r.WithLock(func(agents map[string]Entry) (bool, error) {
		if _, ok := agents[name]; !ok {
			return false, nil
		}
		delete(agents, name)
		return true, nil
	})
}

func (r *Registry) Rename(oldName, newName string) error {
	return r.WithLock(func(agents map[string]Entry) (bool, error) {
		e, ok := agents[oldName]
		if !ok {
			return false, nil
		}
		delete(agents, oldName)
		agents[newName] = e
		return true, nil
	})
}

// Reorder assigns sequential 1-based Order values to the named entries in the
// given order. Names not present are skipped; entries not named are left as-is
// (their stale Order may still be > the reassigned ones, but the TUI always
// passes the full pinned set, so in practice every pinned entry is covered).
func (r *Registry) Reorder(orderedNames []string) error {
	return r.WithLock(func(agents map[string]Entry) (bool, error) {
		dirty := false
		for i, name := range orderedNames {
			e, ok := agents[name]
			if !ok {
				continue
			}
			if e.Order != i+1 {
				e.Order = i + 1
				agents[name] = e
				dirty = true
			}
		}
		return dirty, nil
	})
}

// MaxOrder returns the highest Order currently assigned (0 if none).
func (r *Registry) MaxOrder() int {
	maxOrder := 0
	_ = r.WithLock(func(agents map[string]Entry) (bool, error) {
		for _, e := range agents {
			if e.Order > maxOrder {
				maxOrder = e.Order
			}
		}
		return false, nil
	})
	return maxOrder
}

// dropLegacyArchived removes entries that a legacy registry file marked
// "archived": true (a concept since removed). Returns true if it dropped any.
func dropLegacyArchived(raw []byte, agents map[string]Entry) bool {
	if len(raw) == 0 {
		return false
	}
	var legacy struct {
		Agents map[string]struct {
			Archived bool `json:"archived"`
		} `json:"agents"`
	}
	if json.Unmarshal(raw, &legacy) != nil {
		return false
	}
	dropped := false
	for name, l := range legacy.Agents {
		if l.Archived {
			if _, ok := agents[name]; ok {
				delete(agents, name)
				dropped = true
			}
		}
	}
	return dropped
}

func (r *Registry) All() map[string]Entry {
	out := map[string]Entry{}
	_ = r.WithLock(func(agents map[string]Entry) (bool, error) {
		for k, v := range agents {
			out[k] = v
		}
		return false, nil
	})
	return out
}

func flockExclusive(fd int) error {
	for i := 0; i < 10; i++ {
		err := syscall.Flock(fd, syscall.LOCK_EX)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
	return syscall.EINTR
}

func writeAtomic(path string, rf registryFile) error {
	b, err := json.MarshalIndent(rf, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
