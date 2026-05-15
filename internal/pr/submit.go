package pr

import (
	"context"
	"fmt"

	"github.com/google/go-github/v66/github"
)

// SubmitDraft is the cross-package view of a draft inline comment. The UI
// layer constructs these from ctxpane.Draft before calling Submit.
type SubmitDraft struct {
	Path string
	Line int
	Side string // "RIGHT" | "LEFT"
	Body string
}

// Submit POSTs a single review to GitHub with state=COMMENT, the optional
// overall body, and all drafts as inline comments. Returns nil on success.
// On HTTP failure, the returned error includes GitHub's response status so
// 403 (missing scope) is self-explanatory.
func Submit(ctx context.Context, c *github.Client, owner, repo string, num int, body string, drafts []SubmitDraft) error {
	if len(drafts) == 0 && body == "" {
		return fmt.Errorf("submit: nothing to send (no drafts, empty body)")
	}
	mapped := make([]*github.DraftReviewComment, 0, len(drafts))
	for _, d := range drafts {
		line := d.Line
		side := d.Side
		mapped = append(mapped, &github.DraftReviewComment{
			Path: github.String(d.Path),
			Body: github.String(d.Body),
			Line: github.Int(line),
			Side: github.String(side),
		})
	}
	req := &github.PullRequestReviewRequest{
		Event:    github.String("COMMENT"),
		Comments: mapped,
	}
	if body != "" {
		req.Body = github.String(body)
	}
	_, resp, err := c.PullRequests.CreateReview(ctx, owner, repo, num, req)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("submit review: %d: %w", resp.StatusCode, err)
		}
		return fmt.Errorf("submit review: %w", err)
	}
	return nil
}

// NewSubmitter returns a closure that POSTs a review to (owner, repo, num)
// using a fresh client built from token. Callers should resolve the token
// once and call this once per session.
func NewSubmitter(token, owner, repo string, num int) (func(ctx context.Context, body string, drafts []SubmitDraft) error, error) {
	c, err := newClient(token, "")
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, body string, drafts []SubmitDraft) error {
		return Submit(ctx, c, owner, repo, num, body, drafts)
	}, nil
}
