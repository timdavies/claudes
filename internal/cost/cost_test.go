package cost

import "testing"

func TestParseCosts(t *testing.T) {
	// Shape mirrors `ccusage session --json`: a `session` array keyed by
	// `period` (the Claude Code session UUID) with a `totalCost`.
	out := []byte(`{
		"session": [
			{"period": "aaa", "totalCost": 0.291545, "totalTokens": 1},
			{"period": "bbb", "totalCost": 12.5}
		],
		"totals": {"totalCost": 12.791545}
	}`)

	costs, err := parseCosts(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := costs["aaa"]; got != 0.291545 {
		t.Errorf("aaa = %v, want 0.291545", got)
	}
	if got := costs["bbb"]; got != 12.5 {
		t.Errorf("bbb = %v, want 12.5", got)
	}
	if _, ok := costs["missing"]; ok {
		t.Error("missing session should not be present")
	}
}

func TestParseCostsGarbage(t *testing.T) {
	costs, err := parseCosts([]byte("not json"))
	if err == nil {
		t.Error("expected error on garbage input")
	}
	if costs == nil {
		t.Error("costs map should be non-nil even on error")
	}
}
