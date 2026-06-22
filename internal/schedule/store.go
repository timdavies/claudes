// Package schedule persists recurring scheduled prompts and the history of the
// runs they spawn. A schedule pairs a prompt with a cadence (interval/daily/
// once); the daemon fires due ones, each run spawning an ephemeral session in
// its own git worktree. The store is a single JSON file guarded by an exclusive
// flock so the daemon and CLI can mutate it concurrently — every mutation is a
// read-modify-write inside the lock. Bulky per-run output isn't kept here; it's
// teed to a logfile on disk and referenced by relative path.
package schedule

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

type Kind string

const (
	KindInterval Kind = "interval" // fire every EverySec, within the active window
	KindDaily    Kind = "daily"    // fire once a day at AtClock
	KindOnce     Kind = "once"     // fire once at AtTime, then auto-disable
)

// Schedule is one saved recurring prompt.
type Schedule struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
	Kind   Kind   `json:"kind"`

	// Exactly one cluster is meaningful per Kind.
	EverySec int    `json:"every_sec,omitempty"` // KindInterval
	AtClock  string `json:"at_clock,omitempty"`  // KindDaily, "HH:MM" local
	AtTime   string `json:"at_time,omitempty"`   // KindOnce, RFC3339

	// Active window for KindInterval (local hours, [start,end)). Default 9–18.
	StartHour int `json:"start_hour"`
	EndHour   int `json:"end_hour"`

	// Spawn config.
	Dir      string `json:"dir"`
	Project  string `json:"project,omitempty"`
	Model    string `json:"model,omitempty"`
	PermMode string `json:"perm_mode,omitempty"` // "" => "auto"

	Enabled   bool   `json:"enabled"`
	LastFired string `json:"last_fired,omitempty"` // RFC3339 of last spawn
	LastRunID string `json:"last_run_id,omitempty"`
	CreatedAt string `json:"created_at"`
}

type RunStatus string

const (
	RunRunning     RunStatus = "running"
	RunDone        RunStatus = "done"        // session exited
	RunTimedOut    RunStatus = "timeout"     // exceeded max runtime, killed
	RunInterrupted RunStatus = "interrupted" // session gone but never finalized (daemon restart)
)

// Run is one firing of a schedule.
type Run struct {
	ID         string    `json:"id"`
	ScheduleID string    `json:"schedule_id"`
	Session    string    `json:"session"`   // tmux display-name (overlap probe)
	Repo       string    `json:"repo"`      // repo root the worktree hangs off
	Branch     string    `json:"branch"`    // worktree branch
	Worktree   string    `json:"worktree"`  // absolute path
	LogFile    string    `json:"log_file"`  // relative to the cache dir
	Status     RunStatus `json:"status"`
	StartedAt  string    `json:"started_at"`
	FinishedAt string    `json:"finished_at,omitempty"`
	TornDown   bool      `json:"torn_down"`
}

var (
	// ErrNotFound is returned when no schedule or run matches an id.
	ErrNotFound = errors.New("not found")
)

type Store struct {
	Path string
	mu   sync.Mutex
}

type storeFile struct {
	Seq       int        `json:"seq"`
	Schedules []Schedule `json:"schedules"`
	Runs      []Run      `json:"runs"`
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

// Add stores a new schedule (enabled), assigning its id and timestamps.
func (s *Store) Add(sc Schedule) (Schedule, error) {
	err := s.withLock(func(sf *storeFile) (bool, error) {
		sf.Seq++
		sc.ID = strconv.Itoa(sf.Seq)
		sc.Enabled = true
		sc.CreatedAt = now()
		applyDefaults(&sc)
		sf.Schedules = append(sf.Schedules, sc)
		return true, nil
	})
	return sc, err
}

// Update replaces the editable fields of an existing schedule by id.
func (s *Store) Update(sc Schedule) error {
	return s.withLock(func(sf *storeFile) (bool, error) {
		idx := indexOfSchedule(sf.Schedules, sc.ID)
		if idx < 0 {
			return false, ErrNotFound
		}
		applyDefaults(&sc)
		// Preserve firing bookkeeping the caller doesn't own.
		sc.CreatedAt = sf.Schedules[idx].CreatedAt
		sc.LastFired = sf.Schedules[idx].LastFired
		sc.LastRunID = sf.Schedules[idx].LastRunID
		sf.Schedules[idx] = sc
		return true, nil
	})
}

// Remove deletes a schedule and its run history.
func (s *Store) Remove(id string) error {
	return s.withLock(func(sf *storeFile) (bool, error) {
		idx := indexOfSchedule(sf.Schedules, id)
		if idx < 0 {
			return false, ErrNotFound
		}
		sf.Schedules = append(sf.Schedules[:idx], sf.Schedules[idx+1:]...)
		kept := sf.Runs[:0]
		for _, r := range sf.Runs {
			if r.ScheduleID != id {
				kept = append(kept, r)
			}
		}
		sf.Runs = kept
		return true, nil
	})
}

// Get returns a single schedule by id.
func (s *Store) Get(id string) (Schedule, error) {
	var (
		out Schedule
		err = ErrNotFound
	)
	_ = s.withLock(func(sf *storeFile) (bool, error) {
		if idx := indexOfSchedule(sf.Schedules, id); idx >= 0 {
			out, err = sf.Schedules[idx], nil
		}
		return false, nil
	})
	return out, err
}

// All returns every schedule, oldest id first.
func (s *Store) All() []Schedule {
	var out []Schedule
	_ = s.withLock(func(sf *storeFile) (bool, error) {
		out = append(out, sf.Schedules...)
		return false, nil
	})
	sort.SliceStable(out, func(i, j int) bool { return numID(out[i].ID) < numID(out[j].ID) })
	return out
}

// SetEnabled flips a schedule's enabled flag.
func (s *Store) SetEnabled(id string, on bool) error {
	return s.withLock(func(sf *storeFile) (bool, error) {
		idx := indexOfSchedule(sf.Schedules, id)
		if idx < 0 {
			return false, ErrNotFound
		}
		sf.Schedules[idx].Enabled = on
		return true, nil
	})
}

// EnabledCount reports how many schedules are enabled (daemon lifecycle).
func (s *Store) EnabledCount() int {
	n := 0
	_ = s.withLock(func(sf *storeFile) (bool, error) {
		for _, sc := range sf.Schedules {
			if sc.Enabled {
				n++
			}
		}
		return false, nil
	})
	return n
}

// MarkFired records a started run, stamps the schedule's LastFired/LastRunID,
// and disables a `once` schedule — all atomically so a tick can't double-fire.
func (s *Store) MarkFired(scheduleID string, r Run) (Run, error) {
	err := s.withLock(func(sf *storeFile) (bool, error) {
		idx := indexOfSchedule(sf.Schedules, scheduleID)
		if idx < 0 {
			return false, ErrNotFound
		}
		r.Status = RunRunning
		r.StartedAt = now()
		sf.Runs = append(sf.Runs, r)
		sf.Schedules[idx].LastFired = r.StartedAt
		sf.Schedules[idx].LastRunID = r.ID
		if sf.Schedules[idx].Kind == KindOnce {
			sf.Schedules[idx].Enabled = false
		}
		return true, nil
	})
	return r, err
}

// UpdateRun replaces a run's mutable fields (status, finish, teardown) by id.
func (s *Store) UpdateRun(r Run) error {
	return s.withLock(func(sf *storeFile) (bool, error) {
		idx := indexOfRun(sf.Runs, r.ID)
		if idx < 0 {
			return false, ErrNotFound
		}
		sf.Runs[idx] = r
		return true, nil
	})
}

// GetRun returns a single run by id.
func (s *Store) GetRun(id string) (Run, error) {
	var (
		out Run
		err = ErrNotFound
	)
	_ = s.withLock(func(sf *storeFile) (bool, error) {
		if idx := indexOfRun(sf.Runs, id); idx >= 0 {
			out, err = sf.Runs[idx], nil
		}
		return false, nil
	})
	return out, err
}

// RunsFor returns a schedule's runs, newest first.
func (s *Store) RunsFor(scheduleID string) []Run {
	var out []Run
	_ = s.withLock(func(sf *storeFile) (bool, error) {
		for _, r := range sf.Runs {
			if r.ScheduleID == scheduleID {
				out = append(out, r)
			}
		}
		return false, nil
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	return out
}

// RunningRuns returns every run still marked running (across all schedules).
func (s *Store) RunningRuns() []Run {
	var out []Run
	_ = s.withLock(func(sf *storeFile) (bool, error) {
		for _, r := range sf.Runs {
			if r.Status == RunRunning {
				out = append(out, r)
			}
		}
		return false, nil
	})
	return out
}

// UnfinalizedRuns returns finished runs whose worktree hasn't been torn down
// yet (so a restart sweep can retry teardown).
func (s *Store) UnfinalizedRuns() []Run {
	var out []Run
	_ = s.withLock(func(sf *storeFile) (bool, error) {
		for _, r := range sf.Runs {
			if r.Status != RunRunning && !r.TornDown {
				out = append(out, r)
			}
		}
		return false, nil
	})
	return out
}

// applyDefaults fills in the active window when unset.
func applyDefaults(sc *Schedule) {
	if sc.StartHour == 0 && sc.EndHour == 0 {
		sc.StartHour, sc.EndHour = 9, 18
	}
}

func indexOfSchedule(scs []Schedule, id string) int {
	for i := range scs {
		if scs[i].ID == id {
			return i
		}
	}
	return -1
}

func indexOfRun(runs []Run, id string) int {
	for i := range runs {
		if runs[i].ID == id {
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
