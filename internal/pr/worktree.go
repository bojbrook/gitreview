package pr

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const stateDirName = ".gitreview"

// EnsureStateDir creates <repoRoot>/.gitreview/worktrees/ if missing and
// returns the path to .gitreview/. created=true the first time we touch it
// in this repo (signal to print the gitignore hint).
func EnsureStateDir(repoRoot string) (path string, created bool, err error) {
	state := filepath.Join(repoRoot, stateDirName)
	wt := filepath.Join(state, "worktrees")
	if _, statErr := os.Stat(state); os.IsNotExist(statErr) {
		created = true
	} else if statErr != nil {
		return "", false, statErr
	}
	if err := os.MkdirAll(wt, 0o755); err != nil {
		return "", false, err
	}
	return state, created, nil
}

// WorktreePath returns the conventional location for PR <num>.
func WorktreePath(stateDir string, num int) string {
	return filepath.Join(stateDir, "worktrees", fmt.Sprintf("pr-%d", num))
}

// Prune removes worktree directories under <stateDir>/worktrees/ that aren't
// registered with git (orphans from prior crashes). Best-effort.
func Prune(repoRoot, stateDir string) error {
	wtDir := filepath.Join(stateDir, "worktrees")
	entries, err := os.ReadDir(wtDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	known, err := listKnownWorktrees(repoRoot)
	if err != nil {
		return err
	}
	var firstErr error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		full := filepath.Join(wtDir, e.Name())
		if known[full] {
			continue
		}
		_ = exec.Command("git", "-C", repoRoot, "worktree", "remove", "--force", full).Run()
		if rmErr := os.RemoveAll(full); rmErr != nil && firstErr == nil {
			firstErr = rmErr
		}
	}
	_ = exec.Command("git", "-C", repoRoot, "worktree", "prune").Run()
	return firstErr
}

func listKnownWorktrees(repoRoot string) (map[string]bool, error) {
	out, err := exec.Command("git", "-C", repoRoot, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	known := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			known[strings.TrimPrefix(line, "worktree ")] = true
		}
	}
	return known, nil
}

// Create fetches the PR head ref then adds a detached worktree at the given
// path checked out to that SHA.
func Create(repoRoot, worktreePath string, prNumber int) (sha string, err error) {
	headRef := fmt.Sprintf("refs/gitreview/pr/%d", prNumber)
	if err := runGit(repoRoot, "fetch", "origin",
		fmt.Sprintf("pull/%d/head:%s", prNumber, headRef)); err != nil {
		return "", fmt.Errorf("fetch PR head: %w", err)
	}
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", headRef).Output()
	if err != nil {
		return "", fmt.Errorf("rev-parse fetched ref: %w", err)
	}
	sha = strings.TrimSpace(string(out))
	if err := runGit(repoRoot, "worktree", "add", "--detach", worktreePath, sha); err != nil {
		return "", fmt.Errorf("worktree add: %w", err)
	}
	return sha, nil
}

// Reuse re-fetches the PR head and resets the existing worktree to it.
func Reuse(repoRoot, worktreePath string, prNumber int) (sha string, err error) {
	headRef := fmt.Sprintf("refs/gitreview/pr/%d", prNumber)
	if err := runGit(repoRoot, "fetch", "origin",
		fmt.Sprintf("pull/%d/head:%s", prNumber, headRef)); err != nil {
		return "", fmt.Errorf("fetch PR head: %w", err)
	}
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", headRef).Output()
	if err != nil {
		return "", fmt.Errorf("rev-parse fetched ref: %w", err)
	}
	sha = strings.TrimSpace(string(out))
	if err := runGit(worktreePath, "reset", "--hard", sha); err != nil {
		return "", fmt.Errorf("reset worktree to head: %w", err)
	}
	return sha, nil
}

// Remove tears down a worktree. Called on graceful TUI exit.
func Remove(repoRoot, worktreePath string) error {
	if err := runGit(repoRoot, "worktree", "remove", "--force", worktreePath); err != nil {
		_ = os.RemoveAll(worktreePath)
		_ = exec.Command("git", "-C", repoRoot, "worktree", "prune").Run()
		return err
	}
	return nil
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v: %s: %w", args, strings.TrimSpace(string(out)), err)
	}
	return nil
}
