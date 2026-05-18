package pr

import (
	"context"
	"fmt"
	"sync"

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

// Valid review events accepted by Submit. Empty / unknown coerces to
// EventComment (GitHub's "leave a comment review" mode).
const (
	EventComment        = "COMMENT"
	EventApprove        = "APPROVE"
	EventRequestChanges = "REQUEST_CHANGES"
)

// Submit POSTs a single review to GitHub with the given event (verdict),
// the optional overall body, and all drafts as inline comments. Returns nil
// on success. On HTTP failure, the returned error includes GitHub's response
// status so 403 (missing scope) is self-explanatory.
func Submit(ctx context.Context, c *github.Client, owner, repo string, num int, body string, drafts []SubmitDraft, event string) error {
	if len(drafts) == 0 && body == "" && event != EventApprove {
		return fmt.Errorf("submit: nothing to send (no drafts, empty body)")
	}
	switch event {
	case EventApprove, EventRequestChanges, EventComment:
	default:
		event = EventComment
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
		Event:    github.String(event),
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
// once and call this once per session. event is one of EventComment /
// EventApprove / EventRequestChanges; unknown values coerce to EventComment.
func NewSubmitter(token, owner, repo string, num int) (func(ctx context.Context, body string, drafts []SubmitDraft, event string) error, error) {
	c, err := newClient(token, "")
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, body string, drafts []SubmitDraft, event string) error {
		return Submit(ctx, c, owner, repo, num, body, drafts, event)
	}, nil
}

// RefetchedComments bundles the three comment streams pulled by a refetch.
type RefetchedComments struct {
	ReviewComments []ReviewComment
	IssueComments  []IssueComment
	Reviews        []Review
}

// NewRefetcher returns a closure that re-pulls the PR's three comment streams
// in parallel, bypassing the read cache. On per-stream success, the fresh
// result is written back to cacheDir (when non-empty) so subsequent loads
// see the updated data. Errors on any stream are non-fatal: the other streams
// still populate. The returned error is non-nil only when all three fail.
func NewRefetcher(token, owner, repo string, num int, cacheDir string) (func(ctx context.Context) (*RefetchedComments, error), error) {
	c, err := newClient(token, "")
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (*RefetchedComments, error) {
		var (
			rcs   []ReviewComment
			ics   []IssueComment
			rvs   []Review
			rcErr error
			icErr error
			rvErr error
		)
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			rcs, rcErr = fetchReviewComments(ctx, c, owner, repo, num)
		}()
		go func() {
			defer wg.Done()
			ics, icErr = fetchIssueComments(ctx, c, owner, repo, num)
		}()
		go func() {
			defer wg.Done()
			rvs, rvErr = fetchReviews(ctx, c, owner, repo, num)
		}()
		wg.Wait()

		if cacheDir != "" {
			cc := &cache{dir: cacheDir, ttl: defaultCacheTTL}
			if rcErr == nil {
				_ = cc.write("review-comments.json", rcs)
			}
			if icErr == nil {
				_ = cc.write("issue-comments.json", ics)
			}
			if rvErr == nil {
				_ = cc.write("reviews.json", rvs)
			}
		}

		out := &RefetchedComments{
			ReviewComments: rcs,
			IssueComments:  ics,
			Reviews:        rvs,
		}
		if rcErr != nil && icErr != nil && rvErr != nil {
			return out, fmt.Errorf("all comment fetches failed: %v / %v / %v", rcErr, icErr, rvErr)
		}
		return out, nil
	}, nil
}
