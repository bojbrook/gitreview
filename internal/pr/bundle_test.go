package pr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubGhFailing creates a stub `gh` script that exits 1, places it in a
// fresh tempdir, and prepends that dir to PATH. The rest of PATH (where the
// real `git` lives) is preserved so other commands still work. Used to
// force ResolveToken to fall through to $GITHUB_TOKEN.
func stubGhFailing(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "gh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestLoad_RepoMismatch(t *testing.T) {
	_, work := setupFakeRemote(t)
	stubGhFailing(t)
	t.Setenv("GITHUB_TOKEN", "x") // satisfy ResolveToken

	_, err := Load(context.Background(), "foo/bar#1",
		func() (string, error) { return work, nil }, false)
	if err == nil {
		t.Fatal("want error from repo-mismatch path")
	}
	if !strings.Contains(err.Error(), "doesn't match any remote") {
		t.Errorf("error: got %v", err)
	}
}

func TestLoad_BareNumberWithoutRemote(t *testing.T) {
	work := t.TempDir()
	gitInit(t, work)
	stubGhFailing(t)
	t.Setenv("GITHUB_TOKEN", "x")

	_, err := Load(context.Background(), "1",
		func() (string, error) { return work, nil }, false)
	if err == nil {
		t.Fatal("want error: no github remote to derive owner/repo")
	}
	if !strings.Contains(err.Error(), "no github.com remote") {
		t.Errorf("error: got %v", err)
	}
}
