package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/daemon"
	"github.com/timdavies/claudes/internal/macuake"
	"github.com/timdavies/claudes/internal/tmux"
)

// All three helpers are no-ops when macuake is disabled in config, and
// log-once-and-continue when macuake is enabled but the socket isn't
// reachable. They must never fail the surrounding session operation.

var (
	unavailableOnce  sync.Once
	registryPermOnce sync.Once
)

func macuakeClient(cfg *config.Config) *macuake.Client {
	if !cfg.Macuake.Enabled {
		return nil
	}
	return macuake.New(cfg.Macuake.Socket, 0)
}

func macuakeRegistry() (*macuake.Registry, error) {
	dir, err := daemon.CacheDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return macuake.NewRegistry(filepath.Join(dir, "macuake-tabs.json")), nil
}

func logUnavailable() {
	unavailableOnce.Do(func() {
		fmt.Fprintln(os.Stderr, "claudes: macuake enabled but socket unreachable; continuing without tab")
	})
}

func logMacuakeErr(action string, err error) {
	// The registry lives in ~/.cache/claudes, which may be outside the
	// sandbox writable set when claudes is invoked from inside another
	// Claude Code session. The tab itself is already open (over the
	// macuake socket); only persistence to the registry fails. Degrade
	// to a once-only stderr line instead of one-per-spawn noise.
	if os.IsPermission(err) {
		registryPermOnce.Do(func() {
			fmt.Fprintln(os.Stderr, "claudes: macuake registry not writable (sandbox?); tab opened but won't be tracked")
		})
		return
	}
	fmt.Fprintf(os.Stderr, "claudes: macuake %s: %v\n", action, err)
}

// maybeOpenMacuakeTab opens a new macuake tab attached to the tmux session.
func maybeOpenMacuakeTab(cfg *config.Config, full, displayName, dir string, tmuxClient *tmux.Client) {
	mc := macuakeClient(cfg)
	if mc == nil {
		return
	}
	sid, err := mc.NewTab(dir)
	if err != nil {
		if errors.Is(err, macuake.ErrUnavailable) {
			logUnavailable()
			return
		}
		logMacuakeErr("new-tab", err)
		return
	}
	if err := mc.SetAppearance(sid, displayName); err != nil && !errors.Is(err, macuake.ErrUnavailable) {
		logMacuakeErr("set-appearance", err)
	}
	if err := mc.Execute(sid, attachCommand(tmuxClient, full, displayName)); err != nil && !errors.Is(err, macuake.ErrUnavailable) {
		logMacuakeErr("execute", err)
	}
	reg, err := macuakeRegistry()
	if err != nil {
		logMacuakeErr("registry", err)
		return
	}
	if err := reg.Set(displayName, sid); err != nil {
		logMacuakeErr("registry set", err)
	}
}

// maybeCloseMacuakeTab closes the tab tracked for displayName, if any.
func maybeCloseMacuakeTab(cfg *config.Config, displayName string) {
	if !cfg.Macuake.Enabled {
		return
	}
	reg, err := macuakeRegistry()
	if err != nil {
		logMacuakeErr("registry", err)
		return
	}
	tab, ok := reg.Get(displayName)
	if !ok {
		return
	}
	mc := macuakeClient(cfg)
	if err := mc.CloseSession(tab.SessionID); err != nil {
		if !errors.Is(err, macuake.ErrUnavailable) && !macuake.IsNotFound(err) {
			logMacuakeErr("close-session", err)
		}
	}
	_ = reg.Delete(displayName)
}

// maybeRenameMacuakeTab re-keys the registry entry and updates the tab title.
func maybeRenameMacuakeTab(cfg *config.Config, oldName, newName string) {
	if !cfg.Macuake.Enabled {
		return
	}
	reg, err := macuakeRegistry()
	if err != nil {
		logMacuakeErr("registry", err)
		return
	}
	tab, ok := reg.Get(oldName)
	if !ok {
		return
	}
	if err := reg.Rename(oldName, newName); err != nil {
		logMacuakeErr("registry rename", err)
	}
	mc := macuakeClient(cfg)
	if err := mc.SetAppearance(tab.SessionID, newName); err != nil {
		if !errors.Is(err, macuake.ErrUnavailable) && !macuake.IsNotFound(err) {
			logMacuakeErr("set-appearance", err)
		}
	}
}

// attachCommand reconstructs `tmux <base-args> attach-session -t <full>` as a
// single shell-safe line for macuake's `execute`. Prepended with an OSC title
// escape so the tab's title sticks (the shell's PS1 otherwise overwrites
// whatever set-appearance did) and `exec`-ed so the tab closes when tmux
// exits, instead of dropping back to a bare shell.
func attachCommand(t *tmux.Client, full, displayName string) string {
	parts := append([]string{"tmux"}, t.BaseArgs()...)
	parts = append(parts, "attach-session", "-t", full)
	quoted := make([]string, len(parts))
	for i, p := range parts {
		quoted[i] = shellQuote(p)
	}
	// OSC 0 sets icon+title; the shell will print one more PS1 before
	// disappearing into the exec, so this sticks.
	titleEsc := fmt.Sprintf("printf '\\033]0;%s\\007'; exec ", strings.ReplaceAll(displayName, "'", `'\''`))
	return titleEsc + strings.Join(quoted, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	// Single-quote unless safe set; double-up any embedded single quotes.
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '_' || r == '-' || r == '.' || r == '/' || r == '@' || r == ':' || r == '=') {
			return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
	}
	return s
}

