package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRun runs a git command in dir with a deterministic identity.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func commit(t *testing.T, dir, file, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(msg), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", msg)
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func newRepoOnBranch(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init")
	gitRun(t, dir, "checkout", "-b", branch)
	commit(t, dir, "f", "init")
	return dir
}

func TestBaseRefLocalMaster(t *testing.T) {
	repo := newRepoOnBranch(t, "master")
	if got := baseRef(repo); got != "master" {
		t.Errorf("baseRef = %q, want master", got)
	}
}

func TestBaseRefLocalMain(t *testing.T) {
	repo := newRepoOnBranch(t, "main")
	if got := baseRef(repo); got != "main" {
		t.Errorf("baseRef = %q, want main", got)
	}
}

// No remote and no main/master branch → fall back to HEAD (never worse than before).
func TestBaseRefFallbackHEAD(t *testing.T) {
	repo := newRepoOnBranch(t, "feature-x")
	if got := baseRef(repo); got != "HEAD" {
		t.Errorf("baseRef = %q, want HEAD fallback", got)
	}
}

// A remote whose default branch is main should resolve to origin/main even when
// the local checkout sits on a differently-named branch.
func TestBaseRefRemoteDefault(t *testing.T) {
	origin := newRepoOnBranch(t, "main")
	commit(t, origin, "g", "more")

	clone := t.TempDir()
	gitRun(t, clone, "clone", origin, ".")
	// Land the clone on a feature branch so HEAD != default.
	gitRun(t, clone, "checkout", "-b", "feature-y")

	if got := baseRef(clone); got != "origin/main" {
		t.Errorf("baseRef = %q, want origin/main", got)
	}
}

// Regression: a remote-tracking base (origin/main) must NOT set up upstream
// tracking, since that writes the main repo's .git/config — which the sandbox
// denies, failing the worktree add (255) and silently running in-place.
func TestEnsureFromRemoteBaseWritesNoUpstream(t *testing.T) {
	origin := newRepoOnBranch(t, "main")
	commit(t, origin, "g", "more")
	clone := t.TempDir()
	gitRun(t, clone, "clone", origin, ".")
	gitRun(t, clone, "checkout", "-b", "feature-z")

	path := StablePath(clone, "agent-r")
	created, err := Ensure(clone, "agent-r", path)
	if err != nil || !created {
		t.Fatalf("Ensure: created=%v err=%v", created, err)
	}
	// --no-track means no branch.<name>.remote/.merge is written into config.
	if out, _ := exec.Command("git", "-C", clone, "config", "branch.agent-r.remote").Output(); strings.TrimSpace(string(out)) != "" {
		t.Errorf("upstream tracking was set (config written): %q", out)
	}
	// Still forked from the remote default tip.
	want, _ := exec.Command("git", "-C", clone, "rev-parse", "origin/main").Output()
	got, _ := exec.Command("git", "-C", path, "rev-parse", "HEAD").Output()
	if strings.TrimSpace(string(got)) != strings.TrimSpace(string(want)) {
		t.Errorf("worktree HEAD %s, want origin/main %s", got, want)
	}
}

// The real regression: parked on a feature branch, a new worktree must fork
// from the default branch, not carry the feature-only commit.
func TestEnsureForksFromDefaultNotHEAD(t *testing.T) {
	repo := newRepoOnBranch(t, "main")
	mainTip, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(mainTip))

	// Add a feature-only commit and park HEAD on it.
	gitRun(t, repo, "checkout", "-b", "feature")
	featTip := commit(t, repo, "feat", "feature-only work")

	path := StablePath(repo, "agent-1")
	created, err := Ensure(repo, "agent-1", path)
	if err != nil || !created {
		t.Fatalf("Ensure: created=%v err=%v", created, err)
	}
	got, err := exec.Command("git", "-C", path, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSpace(string(got))
	if base != want {
		t.Errorf("worktree forked from %q, want main tip %q", base, want)
	}
	if base == featTip {
		t.Errorf("worktree inherited the feature commit %q — the bug", featTip)
	}
}
