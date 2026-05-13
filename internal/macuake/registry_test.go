package macuake

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestRegistrySetGetDelete(t *testing.T) {
	r := NewRegistry(filepath.Join(t.TempDir(), "tabs.json"))
	if err := r.Set("a", "sid-a"); err != nil {
		t.Fatal(err)
	}
	if err := r.Set("b", "sid-b"); err != nil {
		t.Fatal(err)
	}
	got, ok := r.Get("a")
	if !ok || got.SessionID != "sid-a" {
		t.Fatalf("get a: %+v ok=%v", got, ok)
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatal("missing should not be present")
	}
	if err := r.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get("a"); ok {
		t.Fatal("a should be gone")
	}
}

func TestRegistryRename(t *testing.T) {
	r := NewRegistry(filepath.Join(t.TempDir(), "tabs.json"))
	_ = r.Set("old", "sid-1")
	if err := r.Rename("old", "new"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get("old"); ok {
		t.Fatal("old still present")
	}
	got, ok := r.Get("new")
	if !ok || got.SessionID != "sid-1" {
		t.Fatalf("renamed entry %+v ok=%v", got, ok)
	}
}

func TestRegistryConcurrentWriters(t *testing.T) {
	r := NewRegistry(filepath.Join(t.TempDir(), "tabs.json"))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := string(rune('a' + i%5))
			_ = r.Set(name, "sid")
		}(i)
	}
	wg.Wait()
	if got := len(r.All()); got == 0 {
		t.Fatal("no entries persisted")
	}
}

func TestRegistryRoundTripFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tabs.json")
	r := NewRegistry(path)
	_ = r.Set("x", "sid-x")

	r2 := NewRegistry(path)
	got, ok := r2.Get("x")
	if !ok || got.SessionID != "sid-x" {
		t.Fatalf("round trip: %+v ok=%v", got, ok)
	}
}
