package cmd

import (
	"strings"
	"testing"
)

func TestAttachCommandDelegatesToOpen(t *testing.T) {
	cmd := attachCommand("CACT-4075")
	if !strings.Contains(cmd, " open CACT-4075") {
		t.Errorf("expected delegation to `open CACT-4075`, got: %s", cmd)
	}
	// The old raw form used an unquoted `=name:` target that zsh equals-expanded.
	// Make sure we never emit a bare `=` token again.
	if strings.Contains(cmd, " =") || strings.Contains(cmd, "attach-session") {
		t.Errorf("attach command should not reconstruct tmux with a bare = target: %s", cmd)
	}
}

func TestShellQuoteQuotesEquals(t *testing.T) {
	if got := shellQuote("=foo"); got == "=foo" {
		t.Errorf("a string with `=` must be quoted to survive zsh, got: %s", got)
	}
}
