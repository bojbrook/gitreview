package pr

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v66/github"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

// newClient constructs a go-github client authenticated via WithAuthToken.
// baseURL is optional — when non-empty, used for testing against an
// httptest.NewServer. Pass "" for real github.com usage.
func newClient(token, baseURL string) (*github.Client, error) {
	c := github.NewClient(nil).WithAuthToken(token)
	if baseURL != "" {
		var err error
		c, err = c.WithEnterpriseURLs(baseURL, baseURL)
		if err != nil {
			return nil, err
		}
	}
	return c, nil
}

func fetchPR(ctx context.Context, c *github.Client, owner, repo string, num int) (*github.PullRequest, error) {
	pr, _, err := c.PullRequests.Get(ctx, owner, repo, num)
	if err != nil {
		return nil, fmt.Errorf("get PR %s/%s#%d: %w", owner, repo, num, err)
	}
	return pr, nil
}

func fetchFiles(ctx context.Context, c *github.Client, owner, repo string, num int) ([]*github.CommitFile, error) {
	var all []*github.CommitFile
	opt := &github.ListOptions{PerPage: 100}
	for {
		batch, resp, err := c.PullRequests.ListFiles(ctx, owner, repo, num, opt)
		if err != nil {
			return nil, fmt.Errorf("list PR files: %w", err)
		}
		all = append(all, batch...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return all, nil
}

func fetchCommits(ctx context.Context, c *github.Client, owner, repo string, num int) ([]*github.RepositoryCommit, error) {
	var all []*github.RepositoryCommit
	opt := &github.ListOptions{PerPage: 100}
	for {
		batch, resp, err := c.PullRequests.ListCommits(ctx, owner, repo, num, opt)
		if err != nil {
			return nil, fmt.Errorf("list PR commits: %w", err)
		}
		all = append(all, batch...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return all, nil
}

// toDiff converts the PR's CommitFile slice into a *diff.Diff. GitHub returns
// each file's unified-diff fragment in .Patch; we synthesize the `diff --git`
// headers around them and let internal/diff.Parse do the rest. Files without
// a Patch (binary or too-large) become a single-hunk placeholder.
func toDiff(files []*github.CommitFile, owner, repo string, num int) (*diff.Diff, error) {
	var b strings.Builder
	for _, f := range files {
		path := f.GetFilename()
		oldPath := path
		if f.GetPreviousFilename() != "" {
			oldPath = f.GetPreviousFilename()
		}
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n", oldPath, path)
		switch f.GetStatus() {
		case "added":
			fmt.Fprintf(&b, "new file mode 100644\n")
			fmt.Fprintf(&b, "--- /dev/null\n+++ b/%s\n", path)
		case "removed":
			fmt.Fprintf(&b, "deleted file mode 100644\n")
			fmt.Fprintf(&b, "--- a/%s\n+++ /dev/null\n", path)
		case "renamed":
			fmt.Fprintf(&b, "rename from %s\nrename to %s\n", oldPath, path)
			fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", oldPath, path)
		default:
			fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", oldPath, path)
		}
		if f.GetPatch() != "" {
			b.WriteString(f.GetPatch())
			if !strings.HasSuffix(f.GetPatch(), "\n") {
				b.WriteString("\n")
			}
		} else {
			fmt.Fprintf(&b, "@@ -0,0 +1,1 @@\n+(no patch: %s)\n", f.GetStatus())
		}
	}
	parsed, err := diff.Parse(b.String())
	if err != nil {
		return nil, fmt.Errorf("parse synthesized diff: %w", err)
	}
	return &diff.Diff{
		Mode:    diff.ModeAll,
		Label:   fmt.Sprintf("PR %s/%s#%d", owner, repo, num),
		BaseRef: "PR base",
		HeadRef: "PR head",
		Files:   parsed,
	}, nil
}

func toCommits(commits []*github.RepositoryCommit) []diff.Commit {
	out := make([]diff.Commit, 0, len(commits))
	for _, c := range commits {
		sha := c.GetSHA()
		short := sha
		if len(short) > 7 {
			short = short[:7]
		}
		message := c.GetCommit().GetMessage()
		subject, body, _ := strings.Cut(message, "\n\n")
		subject = strings.TrimSpace(strings.SplitN(subject, "\n", 2)[0])
		out = append(out, diff.Commit{
			SHA:      sha,
			ShortSHA: short,
			Subject:  subject,
			Body:     strings.TrimSpace(body),
		})
	}
	return out
}

// PRMeta carries the human-readable PR header data the TUI displays.
// Defined here temporarily; Task 6 moves it to bundle.go.
type PRMeta struct {
	Number  int
	Owner   string
	Repo    string
	Title   string
	Body    string
	Author  string
	State   string
	HeadSHA string
	BaseSHA string
	HTMLURL string
}

func toMeta(pr *github.PullRequest, owner, repo string) PRMeta {
	state := pr.GetState()
	if pr.GetMerged() {
		state = "merged"
	}
	return PRMeta{
		Number:  pr.GetNumber(),
		Owner:   owner,
		Repo:    repo,
		Title:   pr.GetTitle(),
		Body:    pr.GetBody(),
		Author:  pr.GetUser().GetLogin(),
		State:   state,
		HeadSHA: pr.GetHead().GetSHA(),
		BaseSHA: pr.GetBase().GetSHA(),
		HTMLURL: pr.GetHTMLURL(),
	}
}
