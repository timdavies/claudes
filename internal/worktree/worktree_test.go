package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitInit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")
	return dir
}

func TestCreateTeardown(t *testing.T) {
	repo := gitInit(t)
	repo, err := RepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	branch := "claudes-sched-1-x"
	path := SiblingPath(repo, "my task", "20260617-120000")

	if err := Create(repo, branch, path); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("worktree dir missing: %v", err)
	}
	// Dirty the worktree — teardown must force through it.
	if err := os.WriteFile(filepath.Join(path, "dirty"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Teardown(repo, branch, path); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("worktree dir should be gone, got %v", err)
	}
	// Idempotent: a second teardown is a no-op.
	if err := Teardown(repo, branch, path); err != nil {
		t.Fatalf("second teardown should be a no-op: %v", err)
	}
	// Branch should be gone.
	if out, _ := exec.Command("git", "-C", repo, "branch", "--list", branch).Output(); len(out) != 0 {
		t.Fatalf("branch should be deleted, got %q", out)
	}
}

func TestRepoRootNonGit(t *testing.T) {
	if _, err := RepoRoot(t.TempDir()); err == nil {
		t.Fatal("expected error for non-git dir")
	}
}

func TestSiblingPath(t *testing.T) {
	got := SiblingPath("/home/u/grow", "fix flakes", "20260617-120000")
	want := "/home/u/grow-worktrees/fix-flakes-20260617-120000"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
