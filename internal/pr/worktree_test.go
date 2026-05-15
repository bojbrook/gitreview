package pr

import (
	"os"
	"path/filepath"
	"testing"
)

// setupFakeRemote initializes an origin tempdir with a single commit and a
// refs/pull/42/head ref simulating GitHub's PR head namespace. It also makes
// a separate working clone with origin set to that path. Returns (origin, work).
func setupFakeRemote(t *testing.T) (origin, work string) {
	t.Helper()
	origin = t.TempDir()
	gitInit(t, origin)
	gitRun(t, origin, "config", "user.email", "t@t")
	gitRun(t, origin, "config", "user.name", "tester")
	mustWrite(t, filepath.Join(origin, "main.go"), "package main\nfunc main(){}\n")
	gitRun(t, origin, "add", ".")
	gitRun(t, origin, "commit", "-q", "-m", "initial")
	// Simulate GitHub's per-PR ref namespace by setting refs/pull/42/head
	// directly. `git branch` would nest under refs/heads/, which isn't what
	// the production fetchspec (`pull/42/head:...`) targets.
	gitRun(t, origin, "update-ref", "refs/pull/42/head", "HEAD")

	work = t.TempDir()
	gitRun(t, work, "init", "-q", "-b", "main")
	gitRun(t, work, "config", "user.email", "t@t")
	gitRun(t, work, "config", "user.name", "tester")
	gitRun(t, work, "remote", "add", "origin", origin)
	gitRun(t, work, "fetch", "origin")
	gitRun(t, work, "checkout", "-q", "-B", "main", "origin/main")
	return
}

func TestEnsureStateDir(t *testing.T) {
	dir := t.TempDir()
	state, created, err := EnsureStateDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("first call: created should be true")
	}
	if _, err := os.Stat(filepath.Join(state, "worktrees")); err != nil {
		t.Errorf("worktrees dir: %v", err)
	}
	_, created2, err := EnsureStateDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Error("second call: created should be false")
	}
}

func TestCreateAndRemove(t *testing.T) {
	_, work := setupFakeRemote(t)
	state, _, err := EnsureStateDir(work)
	if err != nil {
		t.Fatal(err)
	}
	path := WorktreePath(state, 42)
	sha, err := Create(work, path, 42)
	if err != nil {
		t.Fatal(err)
	}
	if sha == "" {
		t.Error("Create: empty sha")
	}
	if _, err := os.Stat(filepath.Join(path, "main.go")); err != nil {
		t.Errorf("worktree should contain main.go: %v", err)
	}
	if err := Remove(work, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("worktree should be removed: stat err=%v", err)
	}
}

func TestPrune(t *testing.T) {
	_, work := setupFakeRemote(t)
	state, _, err := EnsureStateDir(work)
	if err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(state, "worktrees", "pr-99")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Prune(work, state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan should be pruned: stat err=%v", err)
	}
}
