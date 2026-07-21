package cmd

import (
	"reflect"
	"testing"
)

func TestStripKickoffPrompt(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"model then prompt", []string{"--model", "opus", "do the thing"}, []string{"--model", "opus"}},
		{"model only keeps value", []string{"--model", "opus"}, []string{"--model", "opus"}},
		{"perm flag then prompt", []string{"--permission-mode", "auto", "go"}, []string{"--permission-mode", "auto"}},
		{"boolean flag then prompt", []string{"--dangerously-skip-permissions", "start now"}, []string{"--dangerously-skip-permissions"}},
		{"trailing flag untouched", []string{"--model", "opus", "--worktree"}, []string{"--model", "opus", "--worktree"}},
		{"bare prompt only", []string{"kick it off"}, []string{}},
		{"empty", nil, nil},
		{"resume value kept", []string{"--resume", "abc-123"}, []string{"--resume", "abc-123"}},
	}
	for _, c := range cases {
		got := stripKickoffPrompt(c.in)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: stripKickoffPrompt(%v) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}
