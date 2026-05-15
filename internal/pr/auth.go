package pr

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ResolveToken returns a GitHub token, preferring `gh auth token` and falling
// back to $GITHUB_TOKEN. The token is held only by the caller; we never
// persist it.
func ResolveToken(ctx context.Context) (string, error) {
	if tok, err := ghAuthToken(ctx); err == nil && tok != "" {
		return tok, nil
	}
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" {
		return tok, nil
	}
	return "", fmt.Errorf("no GitHub token available: tried `gh auth token` and $GITHUB_TOKEN. " +
		"Authenticate with `gh auth login`, or export GITHUB_TOKEN.")
}

func ghAuthToken(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", "auth", "token")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
