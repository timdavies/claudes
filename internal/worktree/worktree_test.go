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

func TestEnsureReuse(t *testing.T) {
	repo, err := RepoRoot(gitInit(t))
	if err != nil {
		t.Fatal(err)
	}
	path := StablePath(repo, "my-agent")

	created, err := Ensure(repo, "my-agent", path)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !created {
		t.Fatal("first Ensure should report created=true")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("worktree dir missing: %v", err)
	}
	// Leave a file behind, then Ensure again — it must reuse the existing
	// worktree (no error, file still there), not recreate it.
	if err := os.WriteFile(filepath.Join(path, "work"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err = Ensure(repo, "my-agent", path)
	if err != nil {
		t.Fatalf("second ensure should reuse: %v", err)
	}
	if created {
		t.Fatal("second Ensure should report created=false (reuse)")
	}
	if _, err := os.Stat(filepath.Join(path, "work")); err != nil {
		t.Fatalf("ensure recreated the worktree, lost work: %v", err)
	}
}

func TestStablePath(t *testing.T) {
	got := StablePath("/home/u/grow", "CACT-3688")
	want := "/home/u/grow-worktrees/CACT-3688"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
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

func TestCopyInto(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// A top-level file, a nested file, and a listed-but-missing path.
	if err := os.WriteFile(filepath.Join(src, "CLAUDE.local.md"), []byte("ctx"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "config", "local.env"), []byte("K=V"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CopyInto(src, dst, []string{"CLAUDE.local.md", "config/local.env", "missing.txt"}); err != nil {
		t.Fatalf("CopyInto: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "CLAUDE.local.md")); err != nil || string(b) != "ctx" {
		t.Fatalf("top-level copy: %q err=%v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "config", "local.env")); err != nil || string(b) != "K=V" {
		t.Fatalf("nested copy (parent dir made?): %q err=%v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "missing.txt")); !os.IsNotExist(err) {
		t.Fatal("missing source should be skipped, not created")
	}
}
