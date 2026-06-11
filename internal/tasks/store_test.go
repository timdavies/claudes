package tasks

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "tasks.json"))
}

func TestAddAssignsSequentialIDs(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.Add(Task{Title: "one"})
	b, _ := s.Add(Task{Title: "two"})
	if a.ID != "1" || b.ID != "2" {
		t.Fatalf("expected ids 1,2; got %q,%q", a.ID, b.ID)
	}
	if a.Status != StatusQueued {
		t.Fatalf("new task should be queued, got %q", a.Status)
	}
}

func TestClaimIsExclusive(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.Add(Task{Title: "work"})

	if _, err := s.Claim(task.ID, "alice"); err != nil {
		t.Fatalf("first claim should succeed: %v", err)
	}
	if _, err := s.Claim(task.ID, "bob"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("second claim should be ErrUnavailable, got %v", err)
	}
}

func TestClaimRespectsAssignee(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.Add(Task{Title: "for-bob", Assignee: "bob"})

	if _, err := s.Claim(task.ID, "alice"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("alice must not claim bob's task, got %v", err)
	}
	if _, err := s.Claim(task.ID, "bob"); err != nil {
		t.Fatalf("bob should claim his own task: %v", err)
	}
}

func TestClaimNextSkipsForeignAssignments(t *testing.T) {
	s := newTestStore(t)
	s.Add(Task{Title: "bobs", Assignee: "bob"})
	open, _ := s.Add(Task{Title: "open"})

	got, err := s.ClaimNext("alice")
	if err != nil {
		t.Fatalf("alice should claim the open task: %v", err)
	}
	if got.ID != open.ID {
		t.Fatalf("expected the open task %q, got %q", open.ID, got.ID)
	}
}

// TestConcurrentClaimNext is the crux: many goroutines racing ClaimNext must
// hand each task to exactly one claimant.
func TestConcurrentClaimNext(t *testing.T) {
	s := newTestStore(t)
	const n = 25
	for i := 0; i < n; i++ {
		s.Add(Task{Title: "t"})
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = map[string]int{}
	)
	for i := 0; i < n*2; i++ { // twice as many claimers as tasks
		wg.Add(1)
		go func() {
			defer wg.Done()
			task, err := s.ClaimNext("racer")
			if err != nil {
				return // ErrEmptyQueue once drained — fine
			}
			mu.Lock()
			seen[task.ID]++
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(seen) != n {
		t.Fatalf("expected %d distinct tasks claimed, got %d", n, len(seen))
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("task %s claimed %d times (double-claim)", id, count)
		}
	}
}

func TestCompleteAndRemove(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.Add(Task{Title: "x", Creator: "alice"})
	s.Claim(task.ID, "bob")

	done, err := s.Complete(task.ID, "shipped it")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if done.Status != StatusDone || done.Result != "shipped it" || done.Creator != "alice" {
		t.Fatalf("unexpected completed task: %+v", done)
	}

	if err := s.Remove(task.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := s.Get(task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("task should be gone, got %v", err)
	}
}
