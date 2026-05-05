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
)

type Session struct {
	Name    string // user-facing (without prefix)
	Project string
	Model   string
	Dir     string
	Status  Status
	Raw     tmux.Info
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

// List enumerates claudes-managed sessions, enriched with project/status.
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
		s := Session{
			Name:   DisplayName(cfg.Prefix, in.Name),
			Dir:    in.Path,
			Status: classify(in),
			Raw:    in,
		}
		s.Project = inferProject(in.Path, cfg)
		s.Model = inferModel(in.PanePID, cfg)
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

// inferModel reads the descendant `claude` process's args, looking for --model.
// Best-effort; returns cfg.Model or "" on failure.
func inferModel(pid int, cfg *config.Config) string {
	fallback := cfg.Model
	if pid <= 0 {
		return fallback
	}
	// `ps -o args= -p <pid>` — but pid is the shell; we want its child claude.
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	if err != nil {
		return fallback
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		child, err := strconv.Atoi(line)
		if err != nil {
			continue
		}
		argsOut, err := exec.Command("ps", "-o", "args=", "-p", strconv.Itoa(child)).Output()
		if err != nil {
			continue
		}
		args := strings.TrimSpace(string(argsOut))
		if !strings.Contains(args, "claude") {
			continue
		}
		fields := strings.Fields(args)
		for i, f := range fields {
			if f == "--model" && i+1 < len(fields) {
				return fields[i+1]
			}
			if strings.HasPrefix(f, "--model=") {
				return strings.TrimPrefix(f, "--model=")
			}
			if f == "--sonnet" {
				return "sonnet"
			}
			if f == "--opus" {
				return "opus"
			}
		}
	}
	return fallback
}
