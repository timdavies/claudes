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
