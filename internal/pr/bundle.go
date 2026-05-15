package pr

import (
	"context"
	"fmt"
	"os"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

// PRMeta carries the human-readable PR header data the TUI displays.
type PRMeta struct {
	Number  int
	Owner   string
	Repo    string
	Title   string
	Body    string
	Author  string
	State   string // "open" | "closed" | "merged"
	HeadSHA string
	BaseSHA string
	HTMLURL string
}

// Bundle is what Load returns. The TUI consumes Diff/Commits identically to
// the local-diff path; Meta drives the PR-mode header; WorktreePath becomes
// the UI's repoRoot.
type Bundle struct {
	Diff           *diff.Diff
	Commits        []diff.Commit
	Meta           PRMeta
	WorktreePath   string
	ReviewComments []ReviewComment
	IssueComments  []IssueComment
	Reviews        []Review
}

// Load orchestrates the full PR-loading flow. repoRootFunc is a seam for
// testing: production passes diff.RepoRoot.
func Load(ctx context.Context, refStr string, repoRootFunc func() (string, error)) (*Bundle, error) {
	ref, err := ParseRef(refStr)
	if err != nil {
		return nil, err
	}
	token, err := ResolveToken(ctx)
	if err != nil {
		return nil, err
	}
	repoRoot, err := repoRootFunc()
	if err != nil {
		return nil, fmt.Errorf("repo root: %w", err)
	}
	if ref.Owner == "" {
		primary, ok, err := PrimaryRemote(repoRoot)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("PR %d given without owner/repo, and current repo has no github.com remote", ref.Number)
		}
		ref.Owner = primary.Owner
		ref.Repo = primary.Repo
	} else {
		ok, err := MatchesRemote(repoRoot, ref.Owner, ref.Repo)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("PR %s/%s#%d doesn't match any remote of this repo. cd to a clone of %s/%s first", ref.Owner, ref.Repo, ref.Number, ref.Owner, ref.Repo)
		}
	}

	state, created, err := EnsureStateDir(repoRoot)
	if err != nil {
		return nil, err
	}
	if created {
		fmt.Fprintln(os.Stderr, "(hint: add .gitreview/ to your .gitignore — gitreview state lives here)")
	}
	if err := Prune(repoRoot, state); err != nil {
		fmt.Fprintln(os.Stderr, "(warn: worktree prune:", err.Error()+")")
	}

	wtPath := WorktreePath(state, ref.Number)
	if _, err := os.Stat(wtPath); err == nil {
		if _, err := Reuse(repoRoot, wtPath, ref.Number); err != nil {
			return nil, err
		}
	} else {
		if _, err := Create(repoRoot, wtPath, ref.Number); err != nil {
			return nil, err
		}
	}

	client, err := newClient(token, "")
	if err != nil {
		return nil, err
	}
	pr, err := fetchPR(ctx, client, ref.Owner, ref.Repo, ref.Number)
	if err != nil {
		return nil, err
	}
	files, err := fetchFiles(ctx, client, ref.Owner, ref.Repo, ref.Number)
	if err != nil {
		return nil, err
	}
	commits, err := fetchCommits(ctx, client, ref.Owner, ref.Repo, ref.Number)
	if err != nil {
		return nil, err
	}

	// Non-fatal fetches: failures leave the slice empty and the affected
	// display section renders (error). The PR still opens.
	reviewComments, err := fetchReviewComments(ctx, client, ref.Owner, ref.Repo, ref.Number)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitreview: warn: list review comments:", err)
	}
	issueComments, err := fetchIssueComments(ctx, client, ref.Owner, ref.Repo, ref.Number)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitreview: warn: list issue comments:", err)
	}
	reviews, err := fetchReviews(ctx, client, ref.Owner, ref.Repo, ref.Number)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitreview: warn: list reviews:", err)
	}

	d, err := toDiff(files, ref.Owner, ref.Repo, ref.Number)
	if err != nil {
		return nil, err
	}
	return &Bundle{
		Diff:           d,
		Commits:        toCommits(commits),
		Meta:           toMeta(pr, ref.Owner, ref.Repo),
		WorktreePath:   wtPath,
		ReviewComments: reviewComments,
		IssueComments:  issueComments,
		Reviews:        reviews,
	}, nil
}
