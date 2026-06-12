package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/iterm2"
)

// The tab integration gives every session a real, focusable terminal tab. iTerm2
// (via AppleScript) is the backend, enabled with [tabs] backend = "iterm2". The
// iterm2 package is standalone with its own types; this file is the thin routing
// seam that lets the rest of the CLI stay backend-agnostic.
//
// All helpers are no-ops when no backend is selected, and log-once-and-continue
// when a backend is selected but unreachable. They must never fail the
// surrounding session operation.

// tabInfo / tabEntry are the cmd-local shapes the adapters normalize each
// backend's own types into.
type tabInfo struct {
	SessionID string
	Title     string
	Active    bool
}

type tabEntry struct {
	SessionID string
}

type tabClient interface {
	NewTab(dir string) (string, error)
	CloseSession(id string) error
	Focus(id string) error
	SetAppearance(id, title string) error
	Execute(id, command string) error
	List() ([]tabInfo, error)
	IsNotFound(err error) bool
	IsUnavailable(err error) bool
}

type tabRegistry interface {
	Get(name string) (tabEntry, bool)
	Set(name, sid string) error
	Delete(name string) error
	Rename(oldName, newName string) error
	All() map[string]tabEntry
}

// tabClientFor returns the selected backend's client, ok=false when off.
func tabClientFor(cfg *config.Config) (tabClient, bool) {
	switch cfg.TabBackend() {
	case "iterm2":
		return iterm2TabClient{iterm2.New(0)}, true
	default:
		return nil, false
	}
}

// tabRegistryFor returns the selected backend's registry, nil when off.
func tabRegistryFor(cfg *config.Config) (tabRegistry, error) {
	switch cfg.TabBackend() {
	case "iterm2":
		r, err := iterm2Registry()
		if err != nil {
			return nil, err
		}
		return iterm2TabRegistry{r}, nil
	default:
		return nil, nil
	}
}

// --- iterm2 adapters ---

type iterm2TabClient struct{ c *iterm2.Client }

func (i iterm2TabClient) NewTab(dir string) (string, error)    { return i.c.NewTab(dir) }
func (i iterm2TabClient) CloseSession(id string) error         { return i.c.CloseSession(id) }
func (i iterm2TabClient) Focus(id string) error                { return i.c.Focus(id) }
func (i iterm2TabClient) SetAppearance(id, title string) error { return i.c.SetAppearance(id, title) }
func (i iterm2TabClient) Execute(id, command string) error     { return i.c.Execute(id, command) }
func (i iterm2TabClient) IsNotFound(err error) bool            { return iterm2.IsNotFound(err) }
func (i iterm2TabClient) IsUnavailable(err error) bool {
	return errors.Is(err, iterm2.ErrUnavailable)
}
func (i iterm2TabClient) List() ([]tabInfo, error) {
	raw, err := i.c.List()
	if err != nil {
		return nil, err
	}
	out := make([]tabInfo, len(raw))
	for idx, t := range raw {
		out[idx] = tabInfo{SessionID: t.SessionID, Title: t.Title, Active: t.Active}
	}
	return out, nil
}

type iterm2TabRegistry struct{ r *iterm2.Registry }

func (i iterm2TabRegistry) Get(name string) (tabEntry, bool) {
	t, ok := i.r.Get(name)
	return tabEntry{SessionID: t.SessionID}, ok
}
func (i iterm2TabRegistry) Set(name, sid string) error   { return i.r.Set(name, sid) }
func (i iterm2TabRegistry) Delete(name string) error     { return i.r.Delete(name) }
func (i iterm2TabRegistry) Rename(old, new string) error { return i.r.Rename(old, new) }
func (i iterm2TabRegistry) All() map[string]tabEntry {
	out := map[string]tabEntry{}
	for k, v := range i.r.All() {
		out[k] = tabEntry{SessionID: v.SessionID}
	}
	return out
}

// --- one-shot loggers ---

var (
	unavailableOnce  sync.Once
	registryPermOnce sync.Once
	autoPermOnce     sync.Once
)

func logUnavailable() {
	unavailableOnce.Do(func() {
		fmt.Fprintln(os.Stderr, "claudes: tab integration enabled but terminal unreachable; continuing without tab")
	})
}

func logTabErr(action string, err error) {
	// iTerm2 Automation (TCC) not granted — explain the one-time fix.
	if errors.Is(err, iterm2.ErrPermission) {
		autoPermOnce.Do(func() {
			fmt.Fprintln(os.Stderr, "claudes: iTerm2 automation not permitted; grant access in System Settings ▸ Privacy & Security ▸ Automation — continuing without tab")
		})
		return
	}
	// The registry lives in ~/.cache/claudes, which may be outside the sandbox
	// writable set when claudes runs inside another Claude Code session. The tab
	// is already open; only persistence fails. Degrade to a once-only line.
	if os.IsPermission(err) {
		registryPermOnce.Do(func() {
			fmt.Fprintln(os.Stderr, "claudes: tab registry not writable (sandbox?); tab opened but won't be tracked")
		})
		return
	}
	fmt.Fprintf(os.Stderr, "claudes: tab %s: %v\n", action, err)
}

// --- backend-neutral lifecycle helpers ---

// maybeOpenTab opens a new tab attached to the tmux session.
func maybeOpenTab(cfg *config.Config, displayName, dir string) {
	mc, ok := tabClientFor(cfg)
	if !ok {
		return
	}
	sid, err := mc.NewTab(dir)
	if err != nil {
		if mc.IsUnavailable(err) {
			logUnavailable()
			return
		}
		logTabErr("new-tab", err)
		return
	}
	if err := mc.SetAppearance(sid, displayName); err != nil && !mc.IsUnavailable(err) {
		logTabErr("set-appearance", err)
	}
	if err := mc.Execute(sid, attachCommand(displayName)); err != nil && !mc.IsUnavailable(err) {
		logTabErr("execute", err)
	}
	reg, err := tabRegistryFor(cfg)
	if err != nil {
		logTabErr("registry", err)
		return
	}
	if reg == nil {
		return
	}
	if err := reg.Set(displayName, sid); err != nil {
		logTabErr("registry set", err)
	}
}

// maybeCloseTab closes the tab tracked for displayName, if any.
func maybeCloseTab(cfg *config.Config, displayName string) {
	mc, ok := tabClientFor(cfg)
	if !ok {
		return
	}
	reg, err := tabRegistryFor(cfg)
	if err != nil {
		logTabErr("registry", err)
		return
	}
	if reg == nil {
		return
	}
	tab, ok := reg.Get(displayName)
	if !ok {
		return
	}
	if err := mc.CloseSession(tab.SessionID); err != nil {
		if !mc.IsUnavailable(err) && !mc.IsNotFound(err) {
			logTabErr("close-session", err)
		}
	}
	_ = reg.Delete(displayName)
}

// maybeRenameTab re-keys the registry entry and updates the tab title.
func maybeRenameTab(cfg *config.Config, oldName, newName string) {
	mc, ok := tabClientFor(cfg)
	if !ok {
		return
	}
	reg, err := tabRegistryFor(cfg)
	if err != nil {
		logTabErr("registry", err)
		return
	}
	if reg == nil {
		return
	}
	tab, ok := reg.Get(oldName)
	if !ok {
		return
	}
	if err := reg.Rename(oldName, newName); err != nil {
		logTabErr("registry rename", err)
	}
	if err := mc.SetAppearance(tab.SessionID, newName); err != nil {
		if !mc.IsUnavailable(err) && !mc.IsNotFound(err) {
			logTabErr("set-appearance", err)
		}
	}
}

// attachCommand is the shell line a freshly-opened tab runs to attach to the
// agent. It delegates to `claudes open <name>` (via the absolute path to the
// running binary, so it doesn't depend on $PATH in the new tab) rather than
// reconstructing the tmux command line — `claudes open` execs tmux attach
// directly with no shell, which sidesteps shell-quoting hazards (notably zsh's
// `=name` equals-expansion that mangled the old raw `-t =name:` form). Prepended
// with an OSC title escape so the tab title sticks past the shell's PS1, and
// `exec`-ed so the tab self-reaps when the session ends.
func attachCommand(displayName string) string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = "claudes" // fall back to $PATH
	}
	titleEsc := fmt.Sprintf("printf '\\033]0;%s\\007'; exec ", strings.ReplaceAll(displayName, "'", `'\''`))
	return titleEsc + shellQuote(exe) + " open " + shellQuote(displayName)
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	for _, r := range s {
		// `=` is intentionally NOT treated as safe: a word starting with `=` is
		// equals-expanded by zsh (`=foo` → path of command foo), so anything
		// containing it must be quoted.
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '_' || r == '-' || r == '.' || r == '/' || r == '@' || r == ':') {
			return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
	}
	return s
}
