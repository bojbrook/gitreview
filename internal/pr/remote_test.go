package pr

import (
	"os"
	"os/exec"
	"testing"
)

// Shared test helpers for the pr package. Defined here (alphabetically first
// _test.go alongside bundle_test.go) and visible to all other tests in the
// package.

func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
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

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestListRepoRemotes(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	gitRun(t, dir, "remote", "add", "origin", "https://github.com/foo/bar.git")
	gitRun(t, dir, "remote", "add", "fork", "git@github.com:alice/bar.git")
	gitRun(t, dir, "remote", "add", "internal", "https://gitlab.com/foo/bar.git")

	got, err := ListRepoRemotes(dir)
	if err != nil {
		t.Fatalf("ListRepoRemotes: %v", err)
	}
	wantOwners := map[string]string{"foo": "bar", "alice": "bar"}
	if len(got) != 2 {
		t.Fatalf("remote count: got %d want 2 (%+v)", len(got), got)
	}
	for _, or := range got {
		if wantOwners[or.Owner] != or.Repo {
			t.Errorf("unexpected remote: %+v", or)
		}
	}
}

func TestMatchesRemote(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	gitRun(t, dir, "remote", "add", "origin", "https://github.com/foo/bar.git")

	cases := []struct {
		owner, repo string
		want        bool
	}{
		{"foo", "bar", true},
		{"FOO", "BAR", true},
		{"foo", "baz", false},
		{"someone", "bar", false},
	}
	for _, c := range cases {
		got, err := MatchesRemote(dir, c.owner, c.repo)
		if err != nil {
			t.Errorf("MatchesRemote(%s/%s): err %v", c.owner, c.repo, err)
			continue
		}
		if got != c.want {
			t.Errorf("MatchesRemote(%s/%s): got %v want %v", c.owner, c.repo, got, c.want)
		}
	}
}

func TestPrimaryRemote(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	gitRun(t, dir, "remote", "add", "origin", "https://github.com/foo/bar.git")
	gitRun(t, dir, "remote", "add", "fork", "git@github.com:alice/bar.git")

	or, ok, err := PrimaryRemote(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("want a primary remote")
	}
	if or != (OwnerRepo{Owner: "alice", Repo: "bar"}) && or != (OwnerRepo{Owner: "foo", Repo: "bar"}) {
		t.Errorf("primary remote: got %+v", or)
	}
}

func TestPrimaryRemote_NoGitHubRemote(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	gitRun(t, dir, "remote", "add", "origin", "https://gitlab.com/foo/bar.git")

	_, ok, err := PrimaryRemote(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("want ok=false when only non-github remotes exist")
	}
}
