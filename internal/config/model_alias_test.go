package config

import (
	"reflect"
	"testing"
)

var testModels = map[string]string{
	"opus":   "claude-opus-5[1m]",
	"sonnet": "claude-sonnet-5",
	"haiku":  "haiku",
}

func TestResolveModelAlias(t *testing.T) {
	cases := map[string]string{
		"opus":              "claude-opus-5[1m]", // the GROW-10106 case
		"sonnet":            "claude-sonnet-5",
		"haiku":             "haiku",              // maps to itself
		"claude-opus-5[1m]": "claude-opus-5[1m]", // already a full id — unchanged
		"gpt-9":             "gpt-9",              // unknown — unchanged
		"":                  "",                   // empty — unchanged
	}
	for in, want := range cases {
		if got := ResolveModelAlias(testModels, in); got != want {
			t.Errorf("ResolveModelAlias(%q) = %q, want %q", in, got, want)
		}
	}
	// Idempotent: resolving the target again is a no-op.
	once := ResolveModelAlias(testModels, "opus")
	if twice := ResolveModelAlias(testModels, once); twice != once {
		t.Errorf("not idempotent: %q -> %q", once, twice)
	}
	// Nil map: everything passes through.
	if got := ResolveModelAlias(nil, "opus"); got != "opus" {
		t.Errorf("nil map should pass through, got %q", got)
	}
}

func TestRewriteModelAliases(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"space form", []string{"--model", "opus"}, []string{"--model", "claude-opus-5[1m]"}},
		{"equals form", []string{"--model=opus"}, []string{"--model=claude-opus-5[1m]"}},
		{
			"amidst other flags",
			[]string{"--verbose", "--model", "sonnet", "do the thing"},
			[]string{"--verbose", "--model", "claude-sonnet-5", "do the thing"},
		},
		{"unknown value untouched", []string{"--model", "gpt-9"}, []string{"--model", "gpt-9"}},
		{"no model flag", []string{"--resume", "abc"}, []string{"--resume", "abc"}},
		{"trailing --model with no value", []string{"--model"}, []string{"--model"}},
		{"empty", nil, nil},
	}
	for _, c := range cases {
		got := RewriteModelAliases(testModels, c.in)
		if c.want == nil {
			if len(got) != 0 {
				t.Errorf("%s: got %v, want empty", c.name, got)
			}
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// A `--model opus` must not be mistaken for a positional prompt: rewrite leaves
// the "opus" value in place (resolved) rather than dropping it, so it composes
// with stripKickoffPrompt correctly.
func TestRewriteModelAliasesPreservesValueSlotCount(t *testing.T) {
	in := []string{"--model", "opus"}
	if got := RewriteModelAliases(testModels, in); len(got) != len(in) {
		t.Errorf("rewrite changed arg count: %v", got)
	}
}
