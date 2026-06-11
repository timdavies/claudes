package cost

import (
	"math"
	"strings"
	"testing"
)

func TestEncodeDir(t *testing.T) {
	cases := map[string]string{
		"/Users/tim/Projects/claudes":   "-Users-tim-Projects-claudes",
		"/Users/tim/.claude/skills/foo": "-Users-tim--claude-skills-foo", // dot and slash both -> '-', not collapsed
	}
	for in, want := range cases {
		if got := EncodeDir(in); got != want {
			t.Errorf("EncodeDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUsdFromReader(t *testing.T) {
	// One opus turn: 1M input + 1M output = $5 + $25 = $30. Plus a non-usage
	// line (a user message) that must be ignored.
	jsonl := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","message":{"model":"claude-opus-4-8","usage":{"input_tokens":1000000,"output_tokens":1000000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`,
	}, "\n")

	got, err := usdFromReader(strings.NewReader(jsonl))
	if err != nil {
		t.Fatal(err)
	}
	if want := 30.0; math.Abs(got-want) > 1e-9 {
		t.Errorf("usdFromReader = %v, want %v", got, want)
	}
}

// TestUsdRealOpusTurn pins the parser to a real Opus 4.8 turn that Claude Code
// billed at $0.291545 (verified against published rates): 7431 input, 25429 1h
// cache-creation, 4 output. This is the regression guard for both the pricing
// table and the 1h cache-write multiplier.
func TestUsdRealOpusTurn(t *testing.T) {
	jsonl := `{"message":{"model":"claude-opus-4-8","usage":{"input_tokens":7431,"output_tokens":4,"cache_creation_input_tokens":25429,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":25429}}}}`
	got, _ := usdFromReader(strings.NewReader(jsonl))
	if want := 0.291545; math.Abs(got-want) > 1e-6 {
		t.Errorf("real opus turn = %v, want %v", got, want)
	}
}

func TestUsdFromReaderCacheTiers(t *testing.T) {
	// 1M cache-read on haiku = 0.1 × $1 = $0.10; unknown model falls back to opus.
	jsonl := `{"message":{"model":"claude-haiku-4-5","usage":{"cache_read_input_tokens":1000000}}}`
	got, _ := usdFromReader(strings.NewReader(jsonl))
	if want := 0.10; math.Abs(got-want) > 1e-9 {
		t.Errorf("haiku cache-read = %v, want %v", got, want)
	}
}
