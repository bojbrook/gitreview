package diff

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLoadCommitsAndDiff(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	gitCfg(t, dir)

	mustWrite(t, filepath.Join(dir, "a.txt"), "first\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "first commit")

	mustWrite(t, filepath.Join(dir, "a.txt"), "first\nsecond\n")
	mustWrite(t, filepath.Join(dir, "b.txt"), "bee\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "second commit\n\nbody line")

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	commits, err := LoadCommits(10)
	if err != nil {
		t.Fatalf("LoadCommits: %v", err)
	}
	if got, want := len(commits), 2; got != want {
		t.Fatalf("commit count: got %d want %d", got, want)
	}
	if commits[0].Subject != "second commit" {
		t.Errorf("newest subject: got %q", commits[0].Subject)
	}
	if commits[0].Body != "body line" {
		t.Errorf("body: got %q want %q", commits[0].Body, "body line")
	}
	if commits[1].Subject != "first commit" || !commits[1].IsRoot() {
		t.Errorf("root commit: got %+v", commits[1])
	}
	if commits[0].ShortSHA == "" || commits[0].SHA == "" {
		t.Errorf("shas missing: %+v", commits[0])
	}

	// Per-commit diff for the newest one — should include both a.txt (M) and b.txt (A)
	d, err := LoadCommitDiff(commits[0])
	if err != nil {
		t.Fatalf("LoadCommitDiff: %v", err)
	}
	if len(d.Files) != 2 {
		t.Fatalf("commit diff files: got %d want 2", len(d.Files))
	}

	// Root commit diff
	rd, err := LoadCommitDiff(commits[1])
	if err != nil {
		t.Fatalf("LoadCommitDiff root: %v", err)
	}
	if len(rd.Files) != 1 || rd.Files[0].Path != "a.txt" {
		t.Errorf("root commit diff: got %+v", rd.Files)
	}
}

func TestLoadCommitsNoHEAD(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	commits, err := LoadCommits(10)
	if err != nil {
		t.Fatalf("LoadCommits: %v", err)
	}
	if len(commits) != 0 {
		t.Errorf("expected 0 commits, got %d", len(commits))
	}
}

func gitCfg(t *testing.T, dir string) {
	t.Helper()
	gitRun(t, dir, "config", "user.email", "t@t")
	gitRun(t, dir, "config", "user.name", "tester")
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
