package pr

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/google/go-github/v66/github"

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
	// CacheDir is the on-disk cache root for this PR. Empty when caching is
	// unavailable. The refetcher writes back here so the disk stays consistent
	// with what the TUI shows.
	CacheDir string
}

// Load orchestrates the full PR-loading flow. repoRootFunc is a seam for
// testing: production passes diff.RepoRoot. When refresh is true, the
// on-disk cache for this PR is cleared before fetching.
func Load(ctx context.Context, refStr string, repoRootFunc func() (string, error), refresh bool) (*Bundle, error) {
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

	cdir, cErr := openCache(state, ref.Owner, ref.Repo, ref.Number)
	if cErr != nil {
		fmt.Fprintln(os.Stderr, "gitreview: warn: cache disabled:", cErr)
		cdir = nil
	}
	if refresh && cdir != nil {
		if err := cdir.clear(); err != nil {
			fmt.Fprintln(os.Stderr, "gitreview: warn: cache clear:", err)
		}
	}

	client, err := newClient(token, "")
	if err != nil {
		return nil, err
	}

	var (
		pr             *github.PullRequest
		files          []*github.CommitFile
		commits        []*github.RepositoryCommit
		reviewComments []ReviewComment
		issueComments  []IssueComment
		reviews        []Review

		prErr, filesErr, commitsErr error
		rcErr, icErr, rvErr         error
	)

	var wg sync.WaitGroup
	goFetch(&wg, cdir, "pr.json", &pr, &prErr, func() (*github.PullRequest, error) {
		return fetchPR(ctx, client, ref.Owner, ref.Repo, ref.Number)
	})
	goFetch(&wg, cdir, "files.json", &files, &filesErr, func() ([]*github.CommitFile, error) {
		return fetchFiles(ctx, client, ref.Owner, ref.Repo, ref.Number)
	})
	goFetch(&wg, cdir, "commits.json", &commits, &commitsErr, func() ([]*github.RepositoryCommit, error) {
		return fetchCommits(ctx, client, ref.Owner, ref.Repo, ref.Number)
	})
	goFetch(&wg, cdir, "review-comments.json", &reviewComments, &rcErr, func() ([]ReviewComment, error) {
		return fetchReviewComments(ctx, client, ref.Owner, ref.Repo, ref.Number)
	})
	goFetch(&wg, cdir, "issue-comments.json", &issueComments, &icErr, func() ([]IssueComment, error) {
		return fetchIssueComments(ctx, client, ref.Owner, ref.Repo, ref.Number)
	})
	goFetch(&wg, cdir, "reviews.json", &reviews, &rvErr, func() ([]Review, error) {
		return fetchReviews(ctx, client, ref.Owner, ref.Repo, ref.Number)
	})
	wg.Wait()

	if prErr != nil {
		return nil, prErr
	}
	if filesErr != nil {
		return nil, filesErr
	}
	if commitsErr != nil {
		return nil, commitsErr
	}
	if rcErr != nil {
		fmt.Fprintln(os.Stderr, "gitreview: warn: list review comments:", rcErr)
	}
	if icErr != nil {
		fmt.Fprintln(os.Stderr, "gitreview: warn: list issue comments:", icErr)
	}
	if rvErr != nil {
		fmt.Fprintln(os.Stderr, "gitreview: warn: list reviews:", rvErr)
	}

	d, err := toDiff(files, ref.Owner, ref.Repo, ref.Number)
	if err != nil {
		return nil, err
	}
	cacheDir := ""
	if cdir != nil {
		cacheDir = cdir.dir
	}
	return &Bundle{
		Diff:           d,
		Commits:        toCommits(commits),
		Meta:           toMeta(pr, ref.Owner, ref.Repo),
		WorktreePath:   wtPath,
		ReviewComments: reviewComments,
		IssueComments:  issueComments,
		Reviews:        reviews,
		CacheDir:       cacheDir,
	}, nil
}

// goFetch runs fetch in a goroutine, reading the cache first when fresh and
// writing on a successful miss. dst is populated on hit OR successful fetch;
// errDst is set only when the underlying fetch fails. Cache errors are
// swallowed — they never abort a load.
func goFetch[T any](wg *sync.WaitGroup, c *cache, name string, dst *T, errDst *error, fetch func() (T, error)) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		var v T
		if c.read(name, &v) {
			*dst = v
			return
		}
		v, err := fetch()
		if err != nil {
			*errDst = err
			return
		}
		*dst = v
		_ = c.write(name, v)
	}()
}
