package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/bowenbrooks/gitreview/internal/ctxpane"
	"github.com/bowenbrooks/gitreview/internal/diff"
	"github.com/bowenbrooks/gitreview/internal/pr"
	"github.com/bowenbrooks/gitreview/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "pr" {
		runPRMode(os.Args[2:])
		return
	}
	runPreflightMode()
}

func runPreflightMode() {
	var (
		base      = flag.String("base", "", "base ref to diff against (defaults: origin/main, origin/master, main, master)")
		working   = flag.Bool("working", false, "show only uncommitted changes (staged + unstaged) vs HEAD")
		staged    = flag.Bool("staged", false, "show only staged changes vs HEAD")
		committed = flag.Bool("committed", false, "show only committed changes between merge-base and HEAD (no working tree)")
		width     = flag.Int("width", 0, "force terminal width (use when bubbletea reports the wrong size, e.g. inside tmux)")
	)
	flag.CommandLine.Parse(os.Args[1:])

	mode, err := resolveMode(*working, *staged, *committed)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitreview:", err)
		os.Exit(2)
	}
	d, err := diff.Load(diff.Options{Mode: mode, BaseRef: *base})
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitreview:", err)
		os.Exit(1)
	}
	commits, err := diff.LoadCommits(500)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitreview: warn:", err)
		commits = nil
	}
	if len(d.Files) == 0 && len(commits) == 0 {
		fmt.Println("no changes (" + d.Label + ") and no commits to browse")
		return
	}
	repoRoot, err := diff.RepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitreview: warn: could not resolve repo root:", err)
	}
	m := ui.New(d, commits, repoRoot, (*ui.PRBundle)(nil))
	if *width > 0 {
		m.ForceWidth(*width)
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "gitreview:", err)
		os.Exit(1)
	}
}

func runPRMode(args []string) {
	fs := flag.NewFlagSet("pr", flag.ExitOnError)
	width := fs.Int("width", 0, "force terminal width")
	refresh := fs.Bool("refresh", false, "bypass on-disk cache and refetch all PR data from GitHub")
	fs.Parse(args)
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "gitreview pr: missing PR ref (e.g. `gitreview pr 1234`)")
		os.Exit(2)
	}
	ctx := context.Background()

	bundle, err := pr.Load(ctx, fs.Arg(0), diff.RepoRoot, *refresh)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitreview:", err)
		os.Exit(1)
	}
	repoRoot, err := diff.RepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitreview: warn: could not resolve repo root:", err)
	}
	defer func() {
		if err := pr.Remove(repoRoot, bundle.WorktreePath); err != nil {
			fmt.Fprintln(os.Stderr, "gitreview: warn: worktree teardown:", err)
		}
	}()

	refs := make([]ctxpane.CommentRef, 0, len(bundle.ReviewComments))
	for _, c := range bundle.ReviewComments {
		refs = append(refs, ctxpane.CommentRef{
			User: c.User,
			Path: c.Path,
			Line: c.Line,
			Side: c.Side,
			Body: c.Body,
			Age:  humanRelative(c.CreatedAt),
		})
	}
	ics := make([]ctxpane.IssueCommentDisplay, 0, len(bundle.IssueComments))
	for _, c := range bundle.IssueComments {
		ics = append(ics, ctxpane.IssueCommentDisplay{
			User: c.User,
			Age:  humanRelative(c.CreatedAt),
			Body: c.Body,
		})
	}
	rvs := make([]ctxpane.ReviewDisplay, 0, len(bundle.Reviews))
	for _, r := range bundle.Reviews {
		rvs = append(rvs, ctxpane.ReviewDisplay{
			User:  r.User,
			State: r.State,
			Age:   humanRelative(r.SubmittedAt),
			Body:  r.Body,
		})
	}
	submitToken, tokenErr := pr.ResolveToken(ctx)
	if tokenErr != nil {
		fmt.Fprintln(os.Stderr, "gitreview: warn: submit disabled (auth):", tokenErr)
	}
	var submitter func(ctx context.Context, body string, drafts []pr.SubmitDraft) error
	var refetcher func(ctx context.Context) (*ui.RefetcherResult, error)
	if submitToken != "" {
		if s, sErr := pr.NewSubmitter(submitToken, bundle.Meta.Owner, bundle.Meta.Repo, bundle.Meta.Number); sErr != nil {
			fmt.Fprintln(os.Stderr, "gitreview: warn: submit disabled (client):", sErr)
		} else {
			submitter = s
		}
		if rf, rErr := pr.NewRefetcher(submitToken, bundle.Meta.Owner, bundle.Meta.Repo, bundle.Meta.Number, bundle.CacheDir); rErr != nil {
			fmt.Fprintln(os.Stderr, "gitreview: warn: refetch disabled:", rErr)
		} else {
			refetcher = func(ctx context.Context) (*ui.RefetcherResult, error) {
				rc, err := rf(ctx)
				if err != nil || rc == nil {
					return nil, err
				}
				out := &ui.RefetcherResult{
					ReviewComments: mapCommentRefs(rc.ReviewComments),
					IssueComments:  mapIssueComments(rc.IssueComments),
					Reviews:        mapReviews(rc.Reviews),
				}
				return out, nil
			}
		}
	}
	m := ui.New(bundle.Diff, bundle.Commits, bundle.WorktreePath, &ui.PRBundle{
		Meta:           &bundle.Meta,
		ReviewComments: refs,
		IssueComments:  ics,
		Reviews:        rvs,
		Submitter:      submitter,
		Refetcher:      refetcher,
	})
	if *width > 0 {
		m.ForceWidth(*width)
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "gitreview:", err)
		os.Exit(1)
	}
}

func resolveMode(working, staged, committed bool) (diff.Mode, error) {
	count := 0
	for _, b := range []bool{working, staged, committed} {
		if b {
			count++
		}
	}
	if count > 1 {
		return 0, fmt.Errorf("--working, --staged, and --committed are mutually exclusive")
	}
	switch {
	case working:
		return diff.ModeWorking, nil
	case staged:
		return diff.ModeStaged, nil
	case committed:
		return diff.ModeCommitted, nil
	default:
		return diff.ModeAll, nil
	}
}

// humanRelative formats a time as a short relative string like "2h ago".
func humanRelative(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
	return t.Format("2006-01-02")
}

// mapCommentRefs maps the wire-domain pr.ReviewComment slice into the
// UI-domain ctxpane.CommentRef slice. Pre-formats the relative time.
func mapCommentRefs(in []pr.ReviewComment) []ctxpane.CommentRef {
	out := make([]ctxpane.CommentRef, 0, len(in))
	for _, c := range in {
		out = append(out, ctxpane.CommentRef{
			User: c.User,
			Path: c.Path,
			Line: c.Line,
			Side: c.Side,
			Body: c.Body,
			Age:  humanRelative(c.CreatedAt),
		})
	}
	return out
}

func mapIssueComments(in []pr.IssueComment) []ctxpane.IssueCommentDisplay {
	out := make([]ctxpane.IssueCommentDisplay, 0, len(in))
	for _, c := range in {
		out = append(out, ctxpane.IssueCommentDisplay{
			User: c.User,
			Age:  humanRelative(c.CreatedAt),
			Body: c.Body,
		})
	}
	return out
}

func mapReviews(in []pr.Review) []ctxpane.ReviewDisplay {
	out := make([]ctxpane.ReviewDisplay, 0, len(in))
	for _, r := range in {
		out = append(out, ctxpane.ReviewDisplay{
			User:  r.User,
			State: r.State,
			Age:   humanRelative(r.SubmittedAt),
			Body:  r.Body,
		})
	}
	return out
}
