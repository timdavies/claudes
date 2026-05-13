package macuake

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"syscall"
	"time"
)

// Registry persists the claudes-session → macuake-tab mapping. Both CLI
// commands (new/stop/rename) and the daemon reconciler read+write it, so
// load/save is gated by an OS flock on the file.
type Registry struct {
	Path string
	mu   sync.Mutex
}

type Tab struct {
	SessionID string `json:"session_id"`
	OpenedAt  string `json:"opened_at"`
}

type registryFile struct {
	Tabs map[string]Tab `json:"tabs"`
}

func NewRegistry(path string) *Registry { return &Registry{Path: path} }

// WithLock takes the file lock for the duration of fn. The file is created
// (empty JSON) if missing. fn receives the loaded map and may mutate it; on
// return the map is saved if dirty=true.
func (r *Registry) WithLock(fn func(tabs map[string]Tab) (dirty bool, err error)) error {
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
			// Corrupt registry: start fresh rather than wedging the daemon.
			rf = registryFile{}
		}
	}
	if rf.Tabs == nil {
		rf.Tabs = map[string]Tab{}
	}

	dirty, err := fn(rf.Tabs)
	if err != nil {
		return err
	}
	if !dirty {
		return nil
	}
	return writeAtomic(r.Path, rf)
}

// Set records a tab for displayName.
func (r *Registry) Set(displayName, sessionID string) error {
	return r.WithLock(func(tabs map[string]Tab) (bool, error) {
		tabs[displayName] = Tab{SessionID: sessionID, OpenedAt: time.Now().UTC().Format(time.RFC3339)}
		return true, nil
	})
}

// Get returns the tab for displayName, ok=false if absent.
func (r *Registry) Get(displayName string) (Tab, bool) {
	var (
		out Tab
		ok  bool
	)
	_ = r.WithLock(func(tabs map[string]Tab) (bool, error) {
		out, ok = tabs[displayName]
		return false, nil
	})
	return out, ok
}

// Delete drops the entry; no-op if absent.
func (r *Registry) Delete(displayName string) error {
	return r.WithLock(func(tabs map[string]Tab) (bool, error) {
		if _, ok := tabs[displayName]; !ok {
			return false, nil
		}
		delete(tabs, displayName)
		return true, nil
	})
}

// Rename re-keys old→new, preserving the session_id. No-op if old absent.
func (r *Registry) Rename(oldName, newName string) error {
	return r.WithLock(func(tabs map[string]Tab) (bool, error) {
		t, ok := tabs[oldName]
		if !ok {
			return false, nil
		}
		delete(tabs, oldName)
		tabs[newName] = t
		return true, nil
	})
}

// All returns a snapshot copy of the map.
func (r *Registry) All() map[string]Tab {
	out := map[string]Tab{}
	_ = r.WithLock(func(tabs map[string]Tab) (bool, error) {
		for k, v := range tabs {
			out[k] = v
		}
		return false, nil
	})
	return out
}

func flockExclusive(fd int) error {
	// Retry briefly on EINTR; flock can be interrupted by signals.
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
