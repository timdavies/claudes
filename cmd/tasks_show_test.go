package cmd

import (
	"path/filepath"
	"testing"

	"github.com/timdavies/claudes/internal/schedule"
)

func TestResolveTaskByIDAndName(t *testing.T) {
	store := schedule.NewStore(filepath.Join(t.TempDir(), "schedules.json"))
	added, err := store.Add(schedule.Schedule{Name: "Nightly-Report", Kind: schedule.KindDaily, AtClock: "09:00", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}

	// By id.
	if sc, err := resolveTask(store, added.ID); err != nil || sc.Name != "Nightly-Report" {
		t.Fatalf("by id: %+v err=%v", sc, err)
	}
	// By exact name, case-insensitive.
	if sc, err := resolveTask(store, "nightly-report"); err != nil || sc.ID != added.ID {
		t.Fatalf("by name: %+v err=%v", sc, err)
	}
	// Unknown → error.
	if _, err := resolveTask(store, "ghost"); err == nil {
		t.Fatal("expected error for unknown task")
	}
}
