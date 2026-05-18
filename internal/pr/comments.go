package pr

import (
	"context"
	"fmt"
	"time"

	"github.com/google/go-github/v66/github"
)

// ReviewComment is an inline review comment anchored to a specific line in
// the PR diff.
type ReviewComment struct {
	ID        int64
	User      string
	Path      string
	Line      int    // NEW-side line for "RIGHT"; OLD-side for "LEFT"
	Side      string // "RIGHT" | "LEFT"
	Body      string
	CreatedAt time.Time
	InReplyTo int64 // 0 for top-level
}

// IssueComment is a top-level PR comment not anchored to any line.
type IssueComment struct {
	ID        int64
	User      string
	Body      string
	CreatedAt time.Time
}

// Review is a submitted review (a bundle: optional body + state).
type Review struct {
	ID          int64
	User        string
	Body        string
	State       string // "APPROVED" | "CHANGES_REQUESTED" | "COMMENTED"
	SubmittedAt time.Time
}

func fetchReviewComments(ctx context.Context, c *github.Client, owner, repo string, num int) ([]ReviewComment, error) {
	var all []ReviewComment
	opt := &github.PullRequestListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		batch, resp, err := c.PullRequests.ListComments(ctx, owner, repo, num, opt)
		if err != nil {
			return nil, fmt.Errorf("list review comments: %w", err)
		}
		for _, b := range batch {
			all = append(all, ReviewComment{
				ID:        b.GetID(),
				User:      b.GetUser().GetLogin(),
				Path:      b.GetPath(),
				Line:      b.GetLine(),
				Side:      b.GetSide(),
				Body:      b.GetBody(),
				CreatedAt: b.GetCreatedAt().Time,
				InReplyTo: b.GetInReplyTo(),
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return all, nil
}

func fetchIssueComments(ctx context.Context, c *github.Client, owner, repo string, num int) ([]IssueComment, error) {
	var all []IssueComment
	opt := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		batch, resp, err := c.Issues.ListComments(ctx, owner, repo, num, opt)
		if err != nil {
			return nil, fmt.Errorf("list issue comments: %w", err)
		}
		for _, b := range batch {
			all = append(all, IssueComment{
				ID:        b.GetID(),
				User:      b.GetUser().GetLogin(),
				Body:      b.GetBody(),
				CreatedAt: b.GetCreatedAt().Time,
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return all, nil
}

func fetchReviews(ctx context.Context, c *github.Client, owner, repo string, num int) ([]Review, error) {
	var all []Review
	opt := &github.ListOptions{PerPage: 100}
	for {
		batch, resp, err := c.PullRequests.ListReviews(ctx, owner, repo, num, opt)
		if err != nil {
			return nil, fmt.Errorf("list reviews: %w", err)
		}
		for _, b := range batch {
			all = append(all, Review{
				ID:          b.GetID(),
				User:        b.GetUser().GetLogin(),
				Body:        b.GetBody(),
				State:       b.GetState(),
				SubmittedAt: b.GetSubmittedAt().Time,
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return all, nil
}
