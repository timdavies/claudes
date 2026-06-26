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
	SelfReport  string // activity the agent self-reported via `claudes status`, may be empty
	State       string // self-reported state keyword (working/waiting/blocked/done), may be empty
	Group       string // agent group; "" means the default group
	PR          string // attached pull-request URL (via `claudes pr`), may be empty
	SessionID   string // Claude Code session UUID (transcript filename stem)
	Cost        string // estimated cost in USD, stamped by the daemon (e.g. "1.23")
	Pinned      bool
	Order       int // manual sort position within the (group, pinned) block; 0 = unset
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
		// Scheduled-prompt runs are ephemeral and surfaced through the schedules
		// section, not the agent list — keep them out of ls/TUI/daemon-summaries.
		if env["CLAUDES_SCHEDULED"] == "1" {
			continue
		}
		s := Session{
			Name:        DisplayName(cfg.Prefix, in.Name),
			Dir:         in.Path,
			Status:      classify(in),
			Description: env["@claudes-description"],
			SelfReport:  env["@claudes-self-description"],
			State:       env["@claudes-state"],
			PR:          env["@claudes-pr"],
			SessionID:   env["CLAUDES_SESSION_ID"],
			Cost:        env["@claudes-cost"],
			Raw:         in,
		}
		// A self-reported state refines the rail color while the process is
		// alive — it resolves the running/waiting/idle ambiguity classify()
		// can't see. A dead process always wins (no "working" on a corpse).
		if s.Status != StatusStopped {
			if st, ok := statusForState(s.State); ok {
				s.Status = st
			}
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
		s.Order, _ = strconv.Atoi(env["@claudes-order"])
		s.Group = NormalizeGroup(env["CLAUDES_GROUP"])
		out = append(out, s)
	}
	return out, nil
}

// statusForState maps an agent's self-reported state keyword onto a lifecycle
// Status (so the rail color reflects it). Unknown words don't map — the state
// text still shows, but the color falls back to the process-derived status.
func statusForState(state string) (Status, bool) {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "working", "running":
		return StatusRunning, true
	case "waiting", "blocked":
		return StatusWaiting, true
	case "done", "idle":
		return StatusIdle, true
	}
	return "", false
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
	// claude honors the LAST --model flag when several are present (e.g. a
	// resolved --model plus a passthrough override), so keep the last match.
	fields := strings.Fields(args)
	model := ""
	for i, f := range fields {
		switch {
		case f == "--model" && i+1 < len(fields):
			model = fields[i+1]
		case strings.HasPrefix(f, "--model="):
			model = strings.TrimPrefix(f, "--model=")
		case f == "--haiku":
			model = "haiku"
		case f == "--sonnet":
			model = "sonnet"
		case f == "--opus":
			model = "opus"
		}
	}
	return model
}
