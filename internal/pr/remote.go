package pr

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// OwnerRepo is the parsed GitHub identity of a single remote.
type OwnerRepo struct {
	Owner string
	Repo  string
}

var (
	reHTTPSRemote = regexp.MustCompile(`^https?://github\.com/([\w.-]+)/([\w.-]+?)(?:\.git)?/?$`)
	reSSHRemote   = regexp.MustCompile(`^git@github\.com:([\w.-]+)/([\w.-]+?)(?:\.git)?$`)
)

// ListRepoRemotes returns the github.com remotes of the repo at repoRoot.
// Non-github remotes are silently skipped. Empty slice if none found.
func ListRepoRemotes(repoRoot string) ([]OwnerRepo, error) {
	cmd := exec.Command("git", "-C", repoRoot, "remote", "-v")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git remote -v: %w", err)
	}
	seen := map[OwnerRepo]bool{}
	var result []OwnerRepo
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		url := fields[1]
		var owner, repo string
		if m := reHTTPSRemote.FindStringSubmatch(url); m != nil {
			owner, repo = m[1], m[2]
		} else if m := reSSHRemote.FindStringSubmatch(url); m != nil {
			owner, repo = m[1], m[2]
		} else {
			continue
		}
		or := OwnerRepo{Owner: owner, Repo: repo}
		if seen[or] {
			continue
		}
		seen[or] = true
		result = append(result, or)
	}
	return result, nil
}

// MatchesRemote returns true if any of repoRoot's github.com remotes match
// (owner, repo) — owner/repo compared case-insensitively.
func MatchesRemote(repoRoot, owner, repo string) (bool, error) {
	remotes, err := ListRepoRemotes(repoRoot)
	if err != nil {
		return false, err
	}
	for _, r := range remotes {
		if strings.EqualFold(r.Owner, owner) && strings.EqualFold(r.Repo, repo) {
			return true, nil
		}
	}
	return false, nil
}

// PrimaryRemote returns the first github.com remote (typically origin's).
// Used to resolve owner/repo for the bare-number ref form. Returns ok=false
// when the repo has no github.com remote.
func PrimaryRemote(repoRoot string) (OwnerRepo, bool, error) {
	remotes, err := ListRepoRemotes(repoRoot)
	if err != nil {
		return OwnerRepo{}, false, err
	}
	if len(remotes) == 0 {
		return OwnerRepo{}, false, nil
	}
	return remotes[0], true, nil
}
