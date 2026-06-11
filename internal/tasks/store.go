// Package tasks persists a cross-agent task queue. Agents (or a human at a
// shell) enqueue work, idle agents claim it, and completion reports back to
// the creating agent. The store is a single JSON file guarded by an exclusive
// flock so concurrent agents can't double-claim a task — every mutation is a
// read-modify-write inside the lock, which gives compare-and-set semantics for
// free.
package tasks

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type Status string

const (
	StatusQueued  Status = "queued"  // unclaimed, waiting for someone
	StatusClaimed Status = "claimed" // an agent is working it
	StatusDone    Status = "done"    // finished (kept for history + report)
)

// Task is one unit of queued work.
type Task struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Project     string `json:"project,omitempty"`
	Creator     string `json:"creator,omitempty"`  // session that added it ("" = human)
	Assignee    string `json:"assignee,omitempty"` // "" = open queue, anyone may claim
	Status      Status `json:"status"`
	Claimant    string `json:"claimant,omitempty"`
	Result      string `json:"result,omitempty"`
	CreatedAt   string `json:"created_at"`
	ClaimedAt   string `json:"claimed_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
}

// Open reports whether a task is available for the given actor to claim: it's
// queued and either unassigned or assigned to that actor.
func (t Task) Open(actor string) bool {
	return t.Status == StatusQueued && (t.Assignee == "" || t.Assignee == actor)
}

var (
	// ErrNotFound is returned when no task matches an id.
	ErrNotFound = errors.New("task not found")
	// ErrUnavailable is returned when a task can't be claimed (already claimed,
	// done, or assigned to someone else).
	ErrUnavailable = errors.New("task not available")
	// ErrEmptyQueue is returned by ClaimNext when nothing is claimable.
	ErrEmptyQueue = errors.New("no claimable tasks")
)

type Store struct {
	Path string
	mu   sync.Mutex
}

type storeFile struct {
	Seq   int    `json:"seq"`
	Tasks []Task `json:"tasks"`
}

func NewStore(path string) *Store { return &Store{Path: path} }

// withLock takes the file lock for the duration of fn, which may mutate the
// loaded file; it's saved when fn returns dirty=true.
func (s *Store) withLock(fn func(sf *storeFile) (dirty bool, err error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(s.Path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := flockExclusive(int(f.Fd())); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	var sf storeFile
	b, err := os.ReadFile(s.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &sf); err != nil {
			sf = storeFile{}
		}
	}

	dirty, err := fn(&sf)
	if err != nil {
		return err
	}
	if !dirty {
		return nil
	}
	return writeAtomic(s.Path, sf)
}

// Add enqueues a task and returns it with its assigned id.
func (s *Store) Add(t Task) (Task, error) {
	err := s.withLock(func(sf *storeFile) (bool, error) {
		sf.Seq++
		t.ID = strconv.Itoa(sf.Seq)
		t.Status = StatusQueued
		t.CreatedAt = now()
		sf.Tasks = append(sf.Tasks, t)
		return true, nil
	})
	return t, err
}

// Claim marks task id as claimed by actor. It fails if the task isn't open to
// that actor (already claimed, done, or assigned elsewhere).
func (s *Store) Claim(id, actor string) (Task, error) {
	var out Task
	err := s.withLock(func(sf *storeFile) (bool, error) {
		idx := indexOf(sf.Tasks, id)
		if idx < 0 {
			return false, ErrNotFound
		}
		if !sf.Tasks[idx].Open(actor) {
			return false, ErrUnavailable
		}
		sf.Tasks[idx].Status = StatusClaimed
		sf.Tasks[idx].Claimant = actor
		sf.Tasks[idx].ClaimedAt = now()
		out = sf.Tasks[idx]
		return true, nil
	})
	return out, err
}

// ClaimNext claims the oldest task open to actor. Returns ErrEmptyQueue when
// none are claimable.
func (s *Store) ClaimNext(actor string) (Task, error) {
	var out Task
	err := s.withLock(func(sf *storeFile) (bool, error) {
		for i := range sf.Tasks { // tasks are append-ordered, so oldest-first
			if sf.Tasks[i].Open(actor) {
				sf.Tasks[i].Status = StatusClaimed
				sf.Tasks[i].Claimant = actor
				sf.Tasks[i].ClaimedAt = now()
				out = sf.Tasks[i]
				return true, nil
			}
		}
		return false, ErrEmptyQueue
	})
	return out, err
}

// Complete marks task id done with an optional result note. Any non-done task
// may be completed (so an agent can close out work it claimed, or a human can
// force-close). Returns the completed task (whose Creator the caller reports
// back to).
func (s *Store) Complete(id, result string) (Task, error) {
	var out Task
	err := s.withLock(func(sf *storeFile) (bool, error) {
		idx := indexOf(sf.Tasks, id)
		if idx < 0 {
			return false, ErrNotFound
		}
		sf.Tasks[idx].Status = StatusDone
		sf.Tasks[idx].Result = result
		sf.Tasks[idx].CompletedAt = now()
		out = sf.Tasks[idx]
		return true, nil
	})
	return out, err
}

// Remove deletes task id outright (cancel / cleanup).
func (s *Store) Remove(id string) error {
	return s.withLock(func(sf *storeFile) (bool, error) {
		idx := indexOf(sf.Tasks, id)
		if idx < 0 {
			return false, ErrNotFound
		}
		sf.Tasks = append(sf.Tasks[:idx], sf.Tasks[idx+1:]...)
		return true, nil
	})
}

// Get returns a single task by id.
func (s *Store) Get(id string) (Task, error) {
	var (
		out Task
		err error
	)
	_ = s.withLock(func(sf *storeFile) (bool, error) {
		idx := indexOf(sf.Tasks, id)
		if idx < 0 {
			err = ErrNotFound
			return false, nil
		}
		out = sf.Tasks[idx]
		return false, nil
	})
	return out, err
}

// All returns every task, oldest first.
func (s *Store) All() []Task {
	var out []Task
	_ = s.withLock(func(sf *storeFile) (bool, error) {
		out = append(out, sf.Tasks...)
		return false, nil
	})
	sort.SliceStable(out, func(i, j int) bool { return numID(out[i].ID) < numID(out[j].ID) })
	return out
}

func indexOf(ts []Task, id string) int {
	for i := range ts {
		if ts[i].ID == id {
			return i
		}
	}
	return -1
}

func numID(id string) int {
	n, _ := strconv.Atoi(id)
	return n
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

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

func writeAtomic(path string, sf storeFile) error {
	b, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
