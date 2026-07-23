package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/timdavies/claudes/internal/worktree"
)

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"}, {"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	root, err := worktree.RepoRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestResolveWorktreeDirOptOutRunsInPlace(t *testing.T) {
	dir, err := resolveWorktreeDir("/some/dir", "foo", true, nil)
	if err != nil || dir != "/some/dir" {
		t.Fatalf("opt-out should run in place with no error, got %q err=%v", dir, err)
	}
}

func TestResolveWorktreeDirNonGitRunsInPlace(t *testing.T) {
	plain := t.TempDir() // not a git repo
	dir, err := resolveWorktreeDir(plain, "foo", false, nil)
	if err != nil || dir != plain {
		t.Fatalf("non-git dir should run in place with no error, got %q err=%v", dir, err)
	}
}

// The regression: an intended-isolated spawn whose worktree add fails must
// return an error (abort the spawn), never silently fall back to the main
// checkout on the user's working branch.
func TestResolveWorktreeDirFailsLoudlyOnAddError(t *testing.T) {
	repo := initGitRepo(t)
	// Occupy the target worktree path with a regular file so `git worktree add`
	// fails deterministically.
	path := worktree.StablePath(repo, "agent-1")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveWorktreeDir(repo, "agent-1", false, nil)
	if err == nil {
		t.Fatalf("expected an error when worktree add fails, got dir=%q", got)
	}
	if got == repo {
		t.Fatal("must NOT fall back to the main checkout on failure")
	}
}
