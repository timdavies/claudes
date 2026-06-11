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
	out := renderAgents(rows, -1, 120)

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
