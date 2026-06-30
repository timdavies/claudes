package daemon

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/schedule"
	"github.com/timdavies/claudes/internal/session"
	"github.com/timdavies/claudes/internal/tmux"
	"github.com/timdavies/claudes/internal/worktree"
)

const (
	scheduleStoreFilename = "schedules.json"
	healthFilename        = "daemon.health"
	runDoneSentinel       = "__CLAUDES_RUN_DONE__"
	healthFailThreshold   = 3 // consecutive identical fire failures before warning
)

func schedulesPath(dir string) string { return filepath.Join(dir, scheduleStoreFilename) }
func healthPath(dir string) string    { return filepath.Join(dir, healthFilename) }

// Health is the daemon's last-known fire health, surfaced by `daemon status`
// and `claudes ls` so repeated fire failures never go silent.
type Health struct {
	Failures int    `json:"failures"`
	Error    string `json:"error"`
	Since    string `json:"since"`
}

// fireHealth tracks consecutive identical fire failures in the running daemon
// and stamps/clears the on-disk Health so other commands can read it.
type fireHealth struct {
	consecutive int
	lastErr     string
	since       string
	dir         string
}

func (h *fireHealth) record(err error) {
	msg := err.Error()
	if msg == h.lastErr {
		h.consecutive++
	} else {
		// A new failure mode: reset the count and drop any stale warning until
		// this one recurs past the threshold.
		h.consecutive = 1
		h.lastErr = msg
		h.since = time.Now().UTC().Format(time.RFC3339)
		clearHealth(h.dir)
	}
	if h.consecutive >= healthFailThreshold {
		writeHealth(h.dir, Health{Failures: h.consecutive, Error: msg, Since: h.since})
	}
}

func (h *fireHealth) reset() {
	if h.consecutive == 0 && h.lastErr == "" {
		return
	}
	h.consecutive = 0
	h.lastErr = ""
	h.since = ""
	clearHealth(h.dir)
}

func writeHealth(dir string, h Health) {
	b, err := json.Marshal(h)
	if err != nil {
		return
	}
	tmp := healthPath(dir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, healthPath(dir))
}

func clearHealth(dir string) { _ = os.Remove(healthPath(dir)) }

// ReadHealth returns the daemon's stamped health, if any (failures past the
// warning threshold). ok=false means healthy / nothing recorded.
func ReadHealth(dir string) (Health, bool) {
	b, err := os.ReadFile(healthPath(dir))
	if err != nil {
		return Health{}, false
	}
	var h Health
	if err := json.Unmarshal(b, &h); err != nil || h.Failures == 0 {
		return Health{}, false
	}
	return h, true
}

// IsPermErr reports whether an error message looks like a sandbox/permission
// denial (the signature of a daemon stuck with an inherited sandbox profile).
func IsPermErr(msg string) bool {
	l := strings.ToLower(msg)
	return strings.Contains(l, "operation not permitted") || strings.Contains(l, "permission denied")
}

// ScheduleStore opens the schedule store rooted in the cache dir.
func ScheduleStore() (*schedule.Store, error) {
	dir, err := CacheDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return schedule.NewStore(schedulesPath(dir)), nil
}

// maxRunDuration caps a single run; a run still live past it is killed and torn
// down (so a prompt that hangs despite --permission-mode auto can't wedge a
// schedule forever). CLAUDES_SCHED_MAX_RUN overrides.
func maxRunDuration() time.Duration {
	if v := os.Getenv("CLAUDES_SCHED_MAX_RUN"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Minute
}

// fireDue fires every enabled, due schedule (called on the main tick). It feeds
// each fire's outcome to the health tracker so repeated failures surface.
func fireDue(client *tmux.Client, cfg *config.Config, store *schedule.Store, dir string, h *fireHealth) {
	now := time.Now()
	for _, sc := range store.All() {
		if !sc.Enabled || !schedule.Due(sc, now) {
			continue
		}
		if previousRunLive(client, cfg, store, dir, sc) {
			logf("skip %s (%s): previous run still live", sc.ID, sc.Name)
			continue
		}
		if err := fireOne(client, cfg, store, dir, sc); err != nil {
			logf("fire %s: %v", sc.ID, err)
			h.record(err)
		} else {
			h.reset()
		}
	}
}

// previousRunLive reports whether the schedule's last run is still running. If
// the run's session is gone but the run was never finalized (e.g. daemon died
// mid-run), it reconciles it here rather than blocking this fire.
func previousRunLive(client *tmux.Client, cfg *config.Config, store *schedule.Store, dir string, sc schedule.Schedule) bool {
	if sc.LastRunID == "" {
		return false
	}
	r, err := store.GetRun(sc.LastRunID)
	if err != nil || r.Status != schedule.RunRunning {
		return false
	}
	if live, _ := client.Has(session.FullName(cfg.Prefix, r.Session)); live {
		return true
	}
	finalizeRun(store, r, schedule.RunInterrupted)
	return false
}

// fireOne creates the worktree, spawns the ephemeral session, tees its output
// to a logfile, and records the run.
func fireOne(client *tmux.Client, cfg *config.Config, store *schedule.Store, dir string, sc schedule.Schedule) error {
	ts := time.Now().Format("20060102-150405")
	runID := sc.ID + "-" + ts
	branch := fmt.Sprintf("claudes-sched-%s-%s", sc.ID, ts)

	repo, err := worktree.RepoRoot(sc.Dir)
	if err != nil {
		return err
	}
	wt := worktree.SiblingPath(repo, sc.Name, ts)
	if err := worktree.Create(repo, branch, wt); err != nil {
		return err
	}

	logRel := filepath.Join("schedules", sc.ID, runID+".log")
	logAbs := filepath.Join(dir, logRel)
	if err := os.MkdirAll(filepath.Dir(logAbs), 0o755); err != nil {
		_ = worktree.Teardown(repo, branch, wt)
		return err
	}

	resolved, err := cfg.Resolve(wt, sc.Project, wt)
	if err != nil {
		_ = worktree.Teardown(repo, branch, wt)
		return err
	}
	model := sc.Model
	if model == "" {
		model = resolved.Model
	}
	perm := sc.PermMode
	if perm == "" {
		perm = "auto"
	}

	// Stamp a known Claude Code session id so the run's ccusage cost is
	// matchable later (same scheme as live agents' CLAUDES_SESSION_ID).
	sessionUUID := uuidV4()

	sessName := "sched-" + runID
	full := session.FullName(cfg.Prefix, sessName)
	extraEnv := []string{
		"CLAUDES_SCHEDULED=1",
		"CLAUDES_SCHEDULE_ID=" + sc.ID,
		"CLAUDES_RUN_ID=" + runID,
		"CLAUDES_NAME=" + sessName,
		"CLAUDES_PROJECT=" + resolved.Project,
		"CLAUDES_DIR=" + wt,
		"CLAUDES_PROMPT=" + sc.Prompt,
		"CLAUDES_PERM=" + perm,
	}
	if err := client.NewSession(full, wt, extraEnv, buildFireCmdline(model, sessionUUID, resolved.DefaultArgs)); err != nil {
		_ = worktree.Teardown(repo, branch, wt)
		return err
	}
	_ = client.PipePaneStart(full, logAbs)

	_, err = store.MarkFired(sc.ID, schedule.Run{
		ID: runID, ScheduleID: sc.ID, Session: sessName,
		Repo: repo, Branch: branch, Worktree: wt, LogFile: logRel,
		SessionUUID: sessionUUID,
	})
	if err != nil {
		return err
	}
	logf("fired %s -> %s (%s)", sc.ID, runID, wt)
	return nil
}

// buildFireCmdline wraps `claude -p` in a shell so the prompt comes from the
// session env (no argv quoting), and a trailing sentinel marks completion in
// the logfile. The prompt and permission mode arrive via CLAUDES_PROMPT/
// CLAUDES_PERM; model and default_args are trusted config and are shell-quoted.
func buildFireCmdline(model, sessionID string, defaultArgs []string) []string {
	script := `claude -p "$CLAUDES_PROMPT" --permission-mode "$CLAUDES_PERM"`
	if sessionID != "" {
		script += " --session-id " + shellQuote(sessionID)
	}
	if model != "" {
		script += " --model " + shellQuote(model)
	}
	for _, a := range defaultArgs {
		script += " " + shellQuote(a)
	}
	script += "; echo " + runDoneSentinel
	return []string{"sh", "-lc", script}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func uuidV4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// sweepCompletions finalizes runs whose session has exited, and kills+finalizes
// runs that outran maxRunDuration (fast tick).
func sweepCompletions(client *tmux.Client, cfg *config.Config, store *schedule.Store, dir string) {
	limit := maxRunDuration()
	for _, r := range store.RunningRuns() {
		full := session.FullName(cfg.Prefix, r.Session)
		live, err := client.Has(full)
		if err != nil {
			continue
		}
		if live {
			if started, perr := time.Parse(time.RFC3339, r.StartedAt); perr == nil && time.Since(started) > limit {
				_ = client.Kill(full)
				finalizeRun(store, r, schedule.RunTimedOut)
			}
			continue
		}
		finalizeExited(client, cfg, store, dir, r, schedule.RunDone)
	}
}

// finalizeExited finalizes a run whose session has exited. A headless run that
// couldn't authenticate exits "successfully" with only a 'Not logged in' notice
// in its output — the model never saw the prompt, so a prompt-level guard can't
// catch it. Detect that from the captured log and record it as RunAuthFailed
// (not the fallback done/interrupted), then notify.
func finalizeExited(client *tmux.Client, cfg *config.Config, store *schedule.Store, dir string, r schedule.Run, fallback schedule.RunStatus) {
	status := fallback
	if runAuthFailed(dir, r) {
		status = schedule.RunAuthFailed
		notifyAuthFailure(client, cfg, store, r)
	}
	finalizeRun(store, r, status)
}

// runAuthFailed reports whether a finished run's captured output carries the
// not-authenticated signature. Cheap substring match, best-effort.
func runAuthFailed(dir string, r schedule.Run) bool {
	if r.LogFile == "" {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, r.LogFile))
	if err != nil {
		return false
	}
	return containsAuthFailure(string(b))
}

func containsAuthFailure(out string) bool {
	l := strings.ToLower(out)
	return strings.Contains(l, "not logged in") || strings.Contains(l, "please run /login")
}

// notifyAuthFailure logs the auth failure and, if a notify session is
// configured and live, writes a one-liner to it so it lands in awareness.
func notifyAuthFailure(client *tmux.Client, cfg *config.Config, store *schedule.Store, r schedule.Run) {
	name := r.ScheduleID
	if sc, err := store.Get(r.ScheduleID); err == nil && sc.Name != "" {
		name = sc.Name
	}
	logf("run %s (%s): not logged in — /login needed", r.ID, name)

	target := cfg.Daemon.NotifySession
	if target == "" {
		return
	}
	full := session.FullName(cfg.Prefix, target)
	if live, _ := client.Has(full); !live {
		return
	}
	_ = client.SendKeys(full, fmt.Sprintf("⚠ scheduled task %q run failed: not logged in, /login needed (run %s)", name, r.ID))
}

// finalizeRun records the run's terminal status then tears down its worktree.
func finalizeRun(store *schedule.Store, r schedule.Run, status schedule.RunStatus) {
	r.Status = status
	r.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	_ = store.UpdateRun(r)
	teardownRun(store, r)
}

// teardownRun removes a finished run's worktree + branch, idempotently. On
// failure it leaves TornDown=false so the startup sweep retries.
func teardownRun(store *schedule.Store, r schedule.Run) {
	if r.TornDown || r.Repo == "" || r.Branch == "" {
		return
	}
	if err := worktree.Teardown(r.Repo, r.Branch, r.Worktree); err != nil {
		logf("teardown %s: %v", r.ID, err)
		return
	}
	r.TornDown = true
	_ = store.UpdateRun(r)
}

// reconcileOrphans runs at daemon startup: finalize runs whose session died
// while the daemon was down, and retry any teardown that didn't complete.
func reconcileOrphans(client *tmux.Client, cfg *config.Config, store *schedule.Store, dir string) {
	for _, r := range store.RunningRuns() {
		if live, _ := client.Has(session.FullName(cfg.Prefix, r.Session)); live {
			continue // still going — the sweep will adopt it
		}
		finalizeExited(client, cfg, store, dir, r, schedule.RunInterrupted)
	}
	for _, r := range store.UnfinalizedRuns() {
		teardownRun(store, r)
	}
}

// FireNow fires a schedule immediately, ignoring its window and enabled state
// (honoring overlap-skip). Used by `claudes tasks run` and the TUI. The running
// daemon owns completion + teardown; callers ensure one is up first.
func FireNow(cfg *config.Config, store *schedule.Store, id string) error {
	dir, err := CacheDir()
	if err != nil {
		return err
	}
	client := tmux.New(cfg.TmuxSocket, cfg.TmuxConfig)
	sc, err := store.Get(id)
	if err != nil {
		return err
	}
	if previousRunLive(client, cfg, store, dir, sc) {
		return fmt.Errorf("previous run still in progress")
	}
	return fireOne(client, cfg, store, dir, sc)
}
