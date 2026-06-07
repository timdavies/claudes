package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Regression test: the bundled tmux.conf fallback must apply even when
// config.toml doesn't exist. Previously Load returned early on a missing
// config file, leaving TmuxConfig empty — so tmux was started without -f
// and the claudes tmux.conf (mouse on, wheel bindings, etc.) never loaded.
func TestLoadTmuxConfFallbackWithoutConfigToml(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	confPath := filepath.Join(dir, "claudes", "tmux.conf")
	if err := os.MkdirAll(filepath.Dir(confPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(confPath, []byte("set -g mouse on\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TmuxConfig != confPath {
		t.Errorf("TmuxConfig = %q, want %q", cfg.TmuxConfig, confPath)
	}
}

// With a config.toml present (but no tmux_config set), the fallback should
// still apply — this was the only path that worked before the fix.
func TestLoadTmuxConfFallbackWithConfigToml(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	claudesDir := filepath.Join(dir, "claudes")
	if err := os.MkdirAll(claudesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	confPath := filepath.Join(claudesDir, "tmux.conf")
	if err := os.WriteFile(confPath, []byte("set -g mouse on\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudesDir, "config.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TmuxConfig != confPath {
		t.Errorf("TmuxConfig = %q, want %q", cfg.TmuxConfig, confPath)
	}
}
