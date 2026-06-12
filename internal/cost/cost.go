// Package cost reports per-session cost by shelling out to ccusage
// (https://github.com/ryoppippi/ccusage), which reads the same transcript JSONL
// Claude Code writes and applies maintained per-model pricing. Its per-session
// `totalCost` matches Claude Code's own billing to the cent, so claudes doesn't
// carry its own pricing table to drift out of date.
//
// ccusage keys each session by `period`, which is the Claude Code session UUID
// — the same value claudes stamps as CLAUDES_SESSION_ID at spawn — so mapping a
// cost back to a tmux session is a direct lookup.
package cost

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ccusageOutput is the slice of `ccusage session --json` we consume.
type ccusageOutput struct {
	Session []struct {
		Period    string  `json:"period"` // Claude Code session UUID
		TotalCost float64 `json:"totalCost"`
	} `json:"session"`
}

// SessionCosts returns a map of Claude Code session UUID → estimated total cost
// in USD. It returns an empty map (not an error) when ccusage isn't installed,
// so cost tracking degrades to "no cost shown" rather than breaking the daemon
// tick.
func SessionCosts() (map[string]float64, error) {
	out, err := runCCUsage()
	if err != nil {
		return map[string]float64{}, err
	}
	return parseCosts(out)
}

func parseCosts(out []byte) (map[string]float64, error) {
	var parsed ccusageOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return map[string]float64{}, err
	}
	costs := make(map[string]float64, len(parsed.Session))
	for _, s := range parsed.Session {
		costs[s.Period] = s.TotalCost
	}
	return costs, nil
}

// runCCUsage invokes ccusage, preferring one on PATH and falling back to npx so
// it works whether or not the user has installed it globally. Returns a nil
// slice + error when neither is available.
func runCCUsage() ([]byte, error) {
	if path, err := exec.LookPath("ccusage"); err == nil {
		return exec.Command(path, "session", "--json").Output()
	}
	npx, err := exec.LookPath("npx")
	if err != nil {
		return nil, err
	}
	return exec.Command(npx, "-y", "ccusage", "session", "--json").Output()
}

// EncodeDir maps a working directory to the project-dir name Claude Code uses
// under ~/.claude/projects: every rune that isn't a letter or digit becomes
// '-' (no collapsing — "/x/.claude" → "-x--claude").
func EncodeDir(dir string) string {
	var b strings.Builder
	b.Grow(len(dir))
	for _, r := range dir {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// normalizeDir matches what Claude Code encodes: symlinks resolved (macOS
// /var → /private/var) and the trailing slash stripped. Best-effort — a missing
// dir falls back to a cleaned path.
func normalizeDir(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return filepath.Clean(dir)
}

// ProjectDir returns the ~/.claude/projects subdirectory Claude Code writes a
// cwd's transcripts into, or "" when the home dir or cwd is unavailable.
func ProjectDir(cwd string) string {
	if cwd == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects", EncodeDir(normalizeDir(cwd)))
}

// ResolveSessionID best-effort recovers the Claude Code session UUID for a live
// agent that claudes didn't stamp (created before --session-id existed). It
// returns the newest transcript in cwd's project dir whose UUID isn't already
// in `claimed`, or "" if none. This is a heuristic: exact when there's one
// agent per directory, best-effort when several share one (each claims a
// distinct transcript so they don't all collapse onto the same cost).
func ResolveSessionID(cwd string, claimed map[string]bool) string {
	dir := ProjectDir(cwd)
	if dir == "" {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var bestID string
	var bestMod time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		if claimed[id] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(bestMod) {
			bestMod = info.ModTime()
			bestID = id
		}
	}
	return bestID
}
