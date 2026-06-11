package session

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/tmux"
)

type Status string

const (
	StatusRunning Status = "running"
	StatusWaiting Status = "waiting"
	StatusIdle    Status = "idle"
	StatusStopped Status = "stopped"
	StatusPaused  Status = "paused" // pinned agent whose tmux session is gone
)

type Session struct {
	Name        string // user-facing (without prefix)
	Project     string
	Model       string
	Dir         string
	Status      Status
	Description string // ambient summary written by the daemon, may be empty
	Group       string // agent group; "" means the default group
	Pinned      bool
	Raw         tmux.Info
}

// FullName returns the tmux session name (with configured prefix).
// The prefix is always prepended; we don't try to detect it in the input,
// so a user-supplied name like "claudes-1" remains distinct.
func FullName(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + name
}

// DisplayName strips the prefix.
func DisplayName(prefix, name string) string {
	return strings.TrimPrefix(name, prefix)
}

// DefaultGroup is the label shown for the implicit group that ungrouped agents
// belong to. Internally the default group is the empty string; this is only for
// display and for matching a user-typed "default".
const DefaultGroup = "default"

// NormalizeGroup canonicalizes a group name: trims whitespace and folds the
// literal "default" (any case) back to "" so the implicit group has a single
// internal representation regardless of how the user spelled it.
func NormalizeGroup(group string) string {
	group = strings.TrimSpace(group)
	if strings.EqualFold(group, DefaultGroup) {
		return ""
	}
	return group
}

// List enumerates claudes-managed sessions, enriched with project/status.
//
// For each session, we prefer values stamped into the tmux session env at
// create time (`CLAUDES_PROJECT`, `CLAUDES_MODEL`, `@claudes-description`)
// since they're durable and can't drift. Project and model fall back to
// inference from cwd/process args for sessions created before the stamp
// was added, or for sessions started via raw tmux outside of claudes new.
func List(client *tmux.Client, cfg *config.Config) ([]Session, error) {
	infos, err := client.List()
	if err != nil {
		return nil, err
	}
	var out []Session
	for _, in := range infos {
		if !strings.HasPrefix(in.Name, cfg.Prefix) {
			continue
		}
		env, _ := client.SessionEnv(in.Name) // best-effort
		s := Session{
			Name:        DisplayName(cfg.Prefix, in.Name),
			Dir:         in.Path,
			Status:      classify(in),
			Description: env["@claudes-description"],
			Raw:         in,
		}
		if v := env["CLAUDES_PROJECT"]; v != "" {
			s.Project = v
		} else {
			s.Project = inferProject(in.Path, cfg)
		}
		if v := env["CLAUDES_MODEL"]; v != "" {
			s.Model = v
		} else {
			s.Model = inferModel(in.PanePID, cfg)
		}
		if env["@claudes-pinned"] == "true" {
			s.Pinned = true
		}
		s.Group = NormalizeGroup(env["CLAUDES_GROUP"])
		out = append(out, s)
	}
	return out, nil
}

func classify(in tmux.Info) Status {
	if in.PanePID == 0 {
		return StatusStopped
	}
	if !processAlive(in.PanePID) {
		return StatusStopped
	}
	// TODO: distinguish running/waiting by parsing pane content.
	return StatusIdle
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// signal 0 probes existence without delivering a signal.
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	// EPERM means the process exists but we can't signal it.
	return err == syscall.EPERM
}

func inferProject(dir string, cfg *config.Config) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for name, p := range cfg.Projects {
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

// inferModel reads the pane's `claude` process args, looking for --model.
// Tries pane_pid itself first (claudes spawns claude directly, so the pane
// process IS claude); falls back to walking children for the unusual case
// where a shell wraps the pane.
//
// Best-effort: returns cfg.Model on any failure.
func inferModel(pid int, cfg *config.Config) string {
	fallback := cfg.Model
	if pid <= 0 {
		return fallback
	}
	if m := modelFromPID(pid); m != "" {
		return m
	}
	// Fallback: walk children. Useful if the pane is a shell that exec'd claude.
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	if err != nil {
		return fallback
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		child, err := strconv.Atoi(line)
		if err != nil {
			continue
		}
		if m := modelFromPID(child); m != "" {
			return m
		}
	}
	return fallback
}

func modelFromPID(pid int) string {
	argsOut, err := exec.Command("ps", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	args := strings.TrimSpace(string(argsOut))
	if !strings.Contains(args, "claude") {
		return ""
	}
	fields := strings.Fields(args)
	for i, f := range fields {
		if f == "--model" && i+1 < len(fields) {
			return fields[i+1]
		}
		if strings.HasPrefix(f, "--model=") {
			return strings.TrimPrefix(f, "--model=")
		}
		if f == "--haiku" {
			return "haiku"
		}
		if f == "--sonnet" {
			return "sonnet"
		}
		if f == "--opus" {
			return "opus"
		}
	}
	return ""
}
