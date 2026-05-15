package pr

import (
	"context"
	"strings"
	"testing"
)

// We intentionally don't unit-test the gh CLI happy path here: it would
// require either a real gh binary or a complex stub. The two tests below
// cover the env-fallback branch and the no-source branch — the resolver's
// two interesting paths.

func TestResolveToken_FromEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("GITHUB_TOKEN", "envtoken")

	tok, err := ResolveToken(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "envtoken" {
		t.Errorf("token: got %q want envtoken", tok)
	}
}

func TestResolveToken_NoSource(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("GITHUB_TOKEN", "")

	_, err := ResolveToken(context.Background())
	if err == nil {
		t.Fatal("want error when no token source available")
	}
	if !strings.Contains(err.Error(), "gh auth token") || !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("error should mention both sources: %v", err)
	}
}
