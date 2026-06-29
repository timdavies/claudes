package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Hooks struct {
	PostNew  string `toml:"post_new"`
	PostStop string `toml:"post_stop"`
}

type Project struct {
	Dir         string   `toml:"dir"`
	Model       string   `toml:"model"`
	DefaultArgs []string `toml:"default_args"`
	Hooks       Hooks    `toml:"hooks"`
}

// Tabs selects the terminal-tab integration backend: "iterm2" or "" (off).
type Tabs struct {
	Backend string `toml:"backend"`
}

// Daemon holds daemon-global toggles. Cost is a *bool so an explicit
// `cost = false` overrides the default-on (a plain bool's zero value couldn't).
type Daemon struct {
	Cost *bool `toml:"cost"`
	// NotifySession, if set, is the agent the daemon writes scheduled-run
	// alerts to (currently headless auth failures), via `claudes write`-style
	// SendKeys. Empty disables notifications.
	NotifySession string `toml:"notify_session"`
}

type Config struct {
	DefaultArgs    []string           `toml:"default_args"`
	Model          string             `toml:"model"`
	StopTimeout    int                `toml:"stop_timeout"`
	Prefix         string             `toml:"prefix"`
	TmuxSocket     string             `toml:"tmux_socket"`
	TmuxConfig     string             `toml:"tmux_config"`
	DefaultProject string             `toml:"default_project"`
	Models         map[string]string  `toml:"models"`
	Projects       map[string]Project `toml:"projects"`
	Hooks          Hooks              `toml:"hooks"`
	Tabs           Tabs               `toml:"tabs"`
	Daemon         Daemon             `toml:"daemon"`

	Path string `toml:"-"`
}

// TabBackend returns the active tab-integration backend: "iterm2" or "" (off).
func (c *Config) TabBackend() string {
	return c.Tabs.Backend
}

// CostEnabled reports whether the daemon should stamp per-session ccusage cost
// (shown in `claudes ls`). Default-on; `[daemon] cost = false` disables it.
// This is independent of CLAUDES_DAEMON_AMBIENT (pane summaries + tab reconcile).
func (c *Config) CostEnabled() bool {
	return c.Daemon.Cost == nil || *c.Daemon.Cost
}

// Resolved is the merged settings for a single command invocation.
type Resolved struct {
	Project     string // empty if none
	Dir         string
	Model       string
	Group       string // agent group; "" means the default group
	DefaultArgs []string
	Hooks       Hooks
	// from global
	StopTimeout int
	Prefix      string
	TmuxSocket  string
	TmuxConfig  string
	Models      map[string]string
}

func defaultConfig() Config {
	return Config{
		StopTimeout: 10,
		Prefix:      "claudes-",
		TmuxSocket:  "claudes",
		// Default to opus rather than empty so that claudes always passes
		// --model to the spawned claude. Without this, a Haiku-running
		// Claude Code spawning `claudes new` produces a Haiku session by
		// inheritance — surprising and easy to miss.
		Model: "opus",
		Models: map[string]string{
			"haiku":  "haiku",
			"sonnet": "sonnet",
			"opus":   "opus",
		},
		Projects: map[string]Project{},
	}
}

func configPath(override string) (string, error) {
	if override != "" {
		return expand(override), nil
	}
	// CLAUDES_CONFIG lets the CLI propagate its --config flag to spawned
	// subprocesses (e.g. the daemon) without rewiring every callsite.
	if env := os.Getenv("CLAUDES_CONFIG"); env != "" {
		return expand(env), nil
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "claudes", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "claudes", "config.toml"), nil
}

// Load reads config; missing file is fine (returns defaults).
func Load(override string) (Config, error) {
	cfg := defaultConfig()
	path, err := configPath(override)
	if err != nil {
		return cfg, err
	}
	cfg.Path = path
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			applyTmuxConfFallback(&cfg)
			return cfg, nil
		}
		return cfg, err
	}
	// Merge over defaults
	var loaded Config
	if _, err := toml.Decode(string(data), &loaded); err != nil {
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	mergeOver(&cfg, &loaded)
	// Expand ~ in project dirs and tmux_config
	cfg.TmuxConfig = expand(cfg.TmuxConfig)
	for name, p := range cfg.Projects {
		p.Dir = expand(p.Dir)
		cfg.Projects[name] = p
	}
	applyTmuxConfFallback(&cfg)
	return cfg, nil
}

// applyTmuxConfFallback points TmuxConfig at the bundled default at
// ~/.config/claudes/tmux.conf (installed via `make install`) when the user
// hasn't set tmux_config explicitly and the file exists.
func applyTmuxConfFallback(cfg *Config) {
	if cfg.TmuxConfig == "" {
		if def := defaultTmuxConfPath(); def != "" {
			if _, err := os.Stat(def); err == nil {
				cfg.TmuxConfig = def
			}
		}
	}
}

func defaultTmuxConfPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "claudes", "tmux.conf")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "claudes", "tmux.conf")
}

func mergeOver(base, over *Config) {
	if len(over.DefaultArgs) > 0 {
		base.DefaultArgs = over.DefaultArgs
	}
	if over.Model != "" {
		base.Model = over.Model
	}
	if over.StopTimeout != 0 {
		base.StopTimeout = over.StopTimeout
	}
	if over.Prefix != "" {
		base.Prefix = over.Prefix
	}
	if over.TmuxSocket != "" {
		base.TmuxSocket = over.TmuxSocket
	}
	if over.TmuxConfig != "" {
		base.TmuxConfig = over.TmuxConfig
	}
	if over.DefaultProject != "" {
		base.DefaultProject = over.DefaultProject
	}
	if len(over.Models) > 0 {
		for k, v := range over.Models {
			base.Models[k] = v
		}
	}
	if len(over.Projects) > 0 {
		for k, v := range over.Projects {
			base.Projects[k] = v
		}
	}
	if over.Hooks.PostNew != "" {
		base.Hooks.PostNew = over.Hooks.PostNew
	}
	if over.Hooks.PostStop != "" {
		base.Hooks.PostStop = over.Hooks.PostStop
	}
	if over.Tabs.Backend != "" {
		base.Tabs.Backend = over.Tabs.Backend
	}
	if over.Daemon.Cost != nil {
		base.Daemon.Cost = over.Daemon.Cost
	}
	if over.Daemon.NotifySession != "" {
		base.Daemon.NotifySession = over.Daemon.NotifySession
	}
}

// Resolve picks dir/project/model/args according to README precedence.
//
//	explicitDir: from -d flag (may be "")
//	projectFlag: from --project flag (may be "")
//	cwd: working directory of the caller
func (c *Config) Resolve(explicitDir, projectFlag, cwd string) (Resolved, error) {
	r := Resolved{
		Model:       c.Model,
		DefaultArgs: append([]string(nil), c.DefaultArgs...),
		Hooks:       c.Hooks,
		StopTimeout: c.StopTimeout,
		Prefix:      c.Prefix,
		TmuxSocket:  c.TmuxSocket,
		TmuxConfig:  c.TmuxConfig,
		Models:      c.Models,
	}

	// Pick project name
	projName := ""
	switch {
	case projectFlag != "":
		if _, ok := c.Projects[projectFlag]; !ok {
			return r, fmt.Errorf("unknown project %q", projectFlag)
		}
		projName = projectFlag
	case explicitDir == "":
		// Try cwd auto-detect
		if name := c.matchProjectByDir(cwd); name != "" {
			projName = name
		} else if c.DefaultProject != "" {
			if _, ok := c.Projects[c.DefaultProject]; !ok {
				return r, fmt.Errorf("default_project %q not defined", c.DefaultProject)
			}
			projName = c.DefaultProject
		}
	}

	if projName != "" {
		p := c.Projects[projName]
		r.Project = projName
		if p.Dir != "" {
			r.Dir = p.Dir
		}
		if p.Model != "" {
			r.Model = p.Model
		}
		if len(p.DefaultArgs) > 0 {
			r.DefaultArgs = append([]string(nil), p.DefaultArgs...) // replace, not append
		}
		// Project hooks REPLACE global hooks (not merge).
		if p.Hooks.PostNew != "" || p.Hooks.PostStop != "" {
			r.Hooks = p.Hooks
		}
	}

	// Explicit -d wins for dir
	if explicitDir != "" {
		r.Dir = expand(explicitDir)
	}
	if r.Dir == "" {
		r.Dir = cwd
	}
	return r, nil
}

func (c *Config) matchProjectByDir(cwd string) string {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	for name, p := range c.Projects {
		if p.Dir == "" {
			continue
		}
		pdir, err := filepath.Abs(p.Dir)
		if err != nil {
			continue
		}
		if abs == pdir || strings.HasPrefix(abs, pdir+string(filepath.Separator)) {
			return name
		}
	}
	return ""
}

func expand(p string) string {
	if p == "" {
		return p
	}
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			if p == "~" {
				return home
			}
			if strings.HasPrefix(p, "~/") {
				return filepath.Join(home, p[2:])
			}
		}
	}
	return p
}
