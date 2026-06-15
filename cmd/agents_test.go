package cmd

import (
	"strings"
	"testing"

	"github.com/timdavies/claudes/internal/session"
)

func TestRenderAgentsGroups(t *testing.T) {
	rows := []agentRow{
		{Name: "alpha", Status: session.StatusIdle},               // default group
		{Name: "beta", Group: "review", Status: session.StatusIdle},
		{Name: "gamma", Group: "review", Status: session.StatusIdle},
	}
	out := renderAgents(rows, -1, 0, 120)

	// The non-default group gets a header; the default group does not.
	if !strings.Contains(out, "review") {
		t.Fatalf("expected a 'review' group header, got:\n%s", out)
	}
	if strings.Contains(out, "default") {
		t.Fatalf("default group should be headerless, got:\n%s", out)
	}

	// Default-group agent renders above the review header (default sorts first).
	if strings.Index(out, "alpha") > strings.Index(out, "review") {
		t.Fatalf("default group should render before grouped agents:\n%s", out)
	}
	// The review header sits above its members.
	if strings.Index(out, "review") > strings.Index(out, "beta") {
		t.Fatalf("group header should precede its members:\n%s", out)
	}
}

func TestPRDisplayID(t *testing.T) {
	cases := map[string]string{
		"":                                       "",
		"https://github.com/o/r/pull/123":        "#123",
		"https://github.com/o/r/pull/123/files/": "#files", // trailing-segment fallback
		"42":                                     "#42",
	}
	for in, want := range cases {
		if got := prDisplayID(in); got != want {
			t.Errorf("prDisplayID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderAgentsShowsPR(t *testing.T) {
	rows := []agentRow{{Name: "alpha", Status: session.StatusIdle, PR: "https://github.com/o/r/pull/77"}}
	if out := renderAgents(rows, -1, 0, 120); !strings.Contains(out, "#77") {
		t.Fatalf("expected the PR id #77 in the row, got:\n%s", out)
	}
}
