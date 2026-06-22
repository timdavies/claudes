// Package worktree is thin git-worktree plumbing for scheduled runs: each run
// gets a throwaway worktree on a fresh branch, and tears both down when done.
// It shells out to git and carries no claudes dependencies.
package worktree

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// RepoRoot resolves the top-level of the git work tree enclosing dir. It errors
// when dir isn't inside a git repository.
func RepoRoot(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("%s is not a git repo: %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// SiblingPath is where a run's worktree lives: a "<repo>-worktrees" directory
// next to the repo, holding one "<name>-<ts>" dir per run.
func SiblingPath(repoRoot, name, ts string) string {
	parent := filepath.Dir(repoRoot)
	base := filepath.Base(repoRoot)
	return filepath.Join(parent, base+"-worktrees", sanitize(name)+"-"+ts)
}

// Create adds a worktree at path on a new branch off the repo's current HEAD.
// A partial failure (git creates the branch, then fails to lay down the dir)
// is cleaned up so a retry isn't blocked by a leftover branch.
func Create(repoRoot, branch, path string) error {
	out, err := exec.Command("git", "-C", repoRoot, "worktree", "add", "-b", branch, path, "HEAD").CombinedOutput()
	if err != nil {
		_ = exec.Command("git", "-C", repoRoot, "worktree", "prune").Run()
		_ = exec.Command("git", "-C", repoRoot, "branch", "-D", branch).Run()
		return fmt.Errorf("git worktree add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Teardown force-removes the worktree and force-deletes its branch. It's
// idempotent: a missing worktree or branch is not an error, so the daemon's
// retry-on-restart sweep can call it repeatedly.
func Teardown(repoRoot, branch, path string) error {
	// remove --force discards uncommitted/untracked changes in the worktree.
	if out, err := exec.Command("git", "-C", repoRoot, "worktree", "remove", "--force", path).CombinedOutput(); err != nil {
		if !ignorableRemoveErr(string(out)) {
			return fmt.Errorf("git worktree remove: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	// Prune stale admin entries (covers a worktree dir deleted out from under git).
	_ = exec.Command("git", "-C", repoRoot, "worktree", "prune").Run()
	// -D force-deletes: the branch is never merged, so -d would refuse.
	if out, err := exec.Command("git", "-C", repoRoot, "branch", "-D", branch).CombinedOutput(); err != nil {
		if !ignorableBranchErr(string(out)) {
			return fmt.Errorf("git branch -D: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func ignorableRemoveErr(out string) bool {
	return strings.Contains(out, "is not a working tree") ||
		strings.Contains(out, "No such file or directory") ||
		strings.Contains(out, "not a working tree")
}

func ignorableBranchErr(out string) bool {
	return strings.Contains(out, "not found") || strings.Contains(out, "Cannot delete")
}

// sanitize makes a schedule name safe for a path segment.
func sanitize(name string) string {
	repl := func(r rune) rune {
		switch r {
		case '/', '\\', ' ', ':', '.':
			return '-'
		default:
			return r
		}
	}
	s := strings.Map(repl, name)
	s = strings.Trim(s, "-")
	if s == "" {
		s = "run"
	}
	return s
}
