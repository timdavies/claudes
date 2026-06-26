package pinned

import (
	"path/filepath"
	"testing"
)

func TestSetGetDelete(t *testing.T) {
	r := NewRegistry(filepath.Join(t.TempDir(), "pinned.json"))
	if err := r.Set("a", Entry{Project: "p", Model: "opus", Dir: "/x"}); err != nil {
		t.Fatal(err)
	}
	got, ok := r.Get("a")
	if !ok || got.Project != "p" || got.PinnedAt == "" {
		t.Fatalf("get a: %+v ok=%v", got, ok)
	}
	if !r.Has("a") || r.Has("missing") {
		t.Fatal("Has")
	}
	if err := r.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if r.Has("a") {
		t.Fatal("a should be gone")
	}
}

func TestRename(t *testing.T) {
	r := NewRegistry(filepath.Join(t.TempDir(), "pinned.json"))
	_ = r.Set("old", Entry{Project: "p"})
	if err := r.Rename("old", "new"); err != nil {
		t.Fatal(err)
	}
	if r.Has("old") {
		t.Fatal("old still present")
	}
	got, ok := r.Get("new")
	if !ok || got.Project != "p" {
		t.Fatalf("renamed entry %+v ok=%v", got, ok)
	}
}

func TestReorderAndMaxOrder(t *testing.T) {
	r := NewRegistry(filepath.Join(t.TempDir(), "pinned.json"))
	for _, name := range []string{"a", "b", "c"} {
		_ = r.Set(name, Entry{Project: "p"})
	}
	if got := r.MaxOrder(); got != 0 {
		t.Fatalf("MaxOrder before reorder = %d, want 0", got)
	}
	if err := r.Reorder([]string{"c", "a", "b"}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int{"c": 1, "a": 2, "b": 3} {
		got, _ := r.Get(name)
		if got.Order != want {
			t.Fatalf("%s order = %d, want %d", name, got.Order, want)
		}
	}
	if got := r.MaxOrder(); got != 3 {
		t.Fatalf("MaxOrder after reorder = %d, want 3", got)
	}
	// Names not present are skipped, not errored.
	if err := r.Reorder([]string{"b", "missing", "a", "c"}); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Get("b")
	if got.Order != 1 {
		t.Fatalf("b order after second reorder = %d, want 1", got.Order)
	}
}

func TestRoundTripFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pinned.json")
	r := NewRegistry(path)
	_ = r.Set("x", Entry{Project: "p", Model: "opus", Dir: "/d", DefaultArgs: []string{"--a", "--b"}, PassthroughArgs: []string{"--print"}})

	r2 := NewRegistry(path)
	got, ok := r2.Get("x")
	if !ok || got.Model != "opus" || len(got.DefaultArgs) != 2 || got.DefaultArgs[1] != "--b" || got.PassthroughArgs[0] != "--print" {
		t.Fatalf("round trip: %+v ok=%v", got, ok)
	}
}
