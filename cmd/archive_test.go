package cmd

import (
	"path/filepath"
	"testing"

	"github.com/timdavies/claudes/internal/pinned"
)

func TestResumeID(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--resume", "abc"}, "abc"},
		{[]string{"-r", "def"}, "def"},
		{[]string{"--resume=ghi"}, "ghi"},
		{[]string{"--model", "opus", "--resume", "xyz"}, "xyz"},
		{[]string{"--model", "opus"}, ""},
		{[]string{"--resume"}, ""}, // no value
	}
	for _, c := range cases {
		if got := resumeID(c.args); got != c.want {
			t.Errorf("resumeID(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestRegistryArchiveRoundTrip(t *testing.T) {
	reg := pinned.NewRegistry(filepath.Join(t.TempDir(), "pinned.json"))
	entry := pinned.Entry{Project: "grow", Dir: "/tmp/x", SessionID: "sid-1", PR: "#42"}
	if err := reg.Set("foo", entry); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetArchived("foo", true); err != nil {
		t.Fatal(err)
	}
	got, ok := reg.Get("foo")
	if !ok || !got.Archived || got.ArchivedAt == "" {
		t.Fatalf("expected archived entry with timestamp, got %+v", got)
	}
	// Static fields survive the flip.
	if got.SessionID != "sid-1" || got.PR != "#42" {
		t.Errorf("archive dropped fields: %+v", got)
	}
	if entries := archivedEntries(reg); len(entries) != 1 || entries[0].name != "foo" {
		t.Errorf("archivedEntries = %+v", entries)
	}
	if err := reg.SetArchived("foo", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := reg.Get("foo"); got.Archived || got.ArchivedAt != "" {
		t.Errorf("unarchive left flag set: %+v", got)
	}
	if entries := archivedEntries(reg); len(entries) != 0 {
		t.Errorf("expected no archived after unarchive, got %+v", entries)
	}
}
