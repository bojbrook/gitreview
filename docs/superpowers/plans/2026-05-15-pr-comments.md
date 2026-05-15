# PR Comments Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add three coordinated capabilities to `gitreview pr`: fetch & display existing review/issue/review-summary comments, author inline drafts locally via `$EDITOR`, and post drafts back as one review with `state=COMMENT`.

**Architecture:** A new `internal/pr/comments.go` adds three paginated fetchers and a `Submit` function; `pr.Bundle` grows three optional comment slices populated by `Load`. A new `SectionComments` kind in `internal/ctxpane/` displays comments anchored to the current line (full thread on demand via a modal). A new `[4 PR]` tab in PR mode renders title/body/issue-comments/reviews/drafts. Drafts live in-memory on the `Model` and submit as one GitHub review.

**Tech Stack:** Go 1.26, Bubble Tea, lipgloss, `google/go-github/v66` (already in deps).

**Spec:** `docs/superpowers/specs/2026-05-15-pr-comments-design.md`

---

## File Structure

**New files:**

```
internal/pr/
  comments.go         ReviewComment / IssueComment / Review types + 3 fetchers
  comments_test.go
  submit.go           Submit() — map drafts → DraftReviewComment[], POST one review
  submit_test.go

internal/ctxpane/
  comments.go         Draft type + buildCommentsSection
  comments_test.go

internal/ui/
  prtab.go            renderPRTab(): title, body, issue comments, reviews, drafts summary
  prtab_test.go
  threadmodal.go      Thread modal overlay + key handlers (t/Esc/e/x)
  threadmodal_test.go
```

**Modified files:**

```
internal/pr/bundle.go               Bundle gains ReviewComments / IssueComments / Reviews;
                                    Load populates them with non-fatal errors.
internal/ctxpane/types.go           Append SectionComments to SectionKind iota.
internal/ctxpane/resolver.go        Add buildCommentsSection to tasks slice; kindFor maps 5.
internal/ui/model.go                Fields: drafts, modal*, viewPR; new keys C/S/t/B/4.
internal/ui/render.go               Tab strip + body branch for viewPR; modal overlay.
internal/ui/styles.go               prDraftStyle, modalStyle, modalBorderStyle.
internal/ui/model_test.go           Tests for new key flows.
cmd/gitreview/main.go               Wire bundle.ReviewComments/etc. into ui.New.
```

---

## Task 1: `comments.go` — types + fetchers

**Files:**
- Create: `internal/pr/comments.go`
- Create: `internal/pr/comments_test.go`

- [ ] **Step 1.1: Write `comments.go`**

```go
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
```

- [ ] **Step 1.2: Extend the mock GitHub server in `github_test.go`**

The existing `startMockGitHub` in `internal/pr/github_test.go` already serves `/pulls/89`, `/pulls/89/files`, `/pulls/89/commits`. Append three more handlers AT THE END of the `startMockGitHub` function (just before `srv := httptest.NewServer(mux)`):

```go
	mux.HandleFunc("/api/v3/repos/foo/bar/pulls/89/comments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":   1001,
				"user": map[string]any{"login": "alice"},
				"path": "src/a.go",
				"line": 12,
				"side": "RIGHT",
				"body": "can we add a context timeout?",
			},
			{
				"id":         1002,
				"user":       map[string]any{"login": "bob"},
				"path":       "src/a.go",
				"line":       12,
				"side":       "RIGHT",
				"body":       "yeah +1",
				"in_reply_to_id": 1001,
			},
		})
	})

	mux.HandleFunc("/api/v3/repos/foo/bar/issues/89/comments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":   2001,
				"user": map[string]any{"login": "carol"},
				"body": "Looks good overall, one nit below.",
			},
		})
	})

	mux.HandleFunc("/api/v3/repos/foo/bar/pulls/89/reviews", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":    3001,
				"user":  map[string]any{"login": "carol"},
				"body":  "LGTM",
				"state": "APPROVED",
			},
		})
	})
```

- [ ] **Step 1.3: Write `comments_test.go`**

```go
package pr

import (
	"context"
	"testing"
)

func TestFetchReviewComments(t *testing.T) {
	_, base := startMockGitHub(t)
	c, err := newClient("testtoken", base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fetchReviewComments(context.Background(), c, "foo", "bar", 89)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("count: got %d want 2", len(got))
	}
	if got[0].User != "alice" || got[0].Path != "src/a.go" || got[0].Line != 12 || got[0].Side != "RIGHT" {
		t.Errorf("comment 0: got %+v", got[0])
	}
	if got[1].InReplyTo != 1001 {
		t.Errorf("comment 1 reply: got %d want 1001", got[1].InReplyTo)
	}
}

func TestFetchIssueComments(t *testing.T) {
	_, base := startMockGitHub(t)
	c, err := newClient("testtoken", base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fetchIssueComments(context.Background(), c, "foo", "bar", 89)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].User != "carol" {
		t.Errorf("got %+v", got)
	}
}

func TestFetchReviews(t *testing.T) {
	_, base := startMockGitHub(t)
	c, err := newClient("testtoken", base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fetchReviews(context.Background(), c, "foo", "bar", 89)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].State != "APPROVED" {
		t.Errorf("got %+v", got)
	}
}
```

- [ ] **Step 1.4: Run tests, build, vet, fmt**

```
go test ./internal/pr/ -v
go build ./...
go vet ./...
gofmt -l internal/pr
```
Expected: all pass; vet/fmt clean.

- [ ] **Step 1.5: Commit**

```bash
git add internal/pr/comments.go internal/pr/comments_test.go internal/pr/github_test.go
git commit -m "pr: fetch review comments, issue comments, reviews"
```

---

## Task 2: Bundle integration

**Files:**
- Modify: `internal/pr/bundle.go`

- [ ] **Step 2.1: Add fields to `Bundle`**

In `internal/pr/bundle.go`, find:

```go
type Bundle struct {
	Diff         *diff.Diff
	Commits      []diff.Commit
	Meta         PRMeta
	WorktreePath string
}
```

Replace with:

```go
type Bundle struct {
	Diff           *diff.Diff
	Commits        []diff.Commit
	Meta           PRMeta
	WorktreePath   string
	ReviewComments []ReviewComment
	IssueComments  []IssueComment
	Reviews        []Review
}
```

- [ ] **Step 2.2: Fetch the new endpoints in `Load`**

In `Load`, find the block after `commits, err := fetchCommits(...)` and BEFORE `d, err := toDiff(...)`. Add three more fetches there. Failures are non-fatal — log to stderr and continue with empty slices.

Replace:

```go
	commits, err := fetchCommits(ctx, client, ref.Owner, ref.Repo, ref.Number)
	if err != nil {
		return nil, err
	}

	d, err := toDiff(files, ref.Owner, ref.Repo, ref.Number)
```

With:

```go
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
```

Update the final return so the Bundle includes the new fields:

```go
	return &Bundle{
		Diff:           d,
		Commits:        toCommits(commits),
		Meta:           toMeta(pr, ref.Owner, ref.Repo),
		WorktreePath:   wtPath,
		ReviewComments: reviewComments,
		IssueComments:  issueComments,
		Reviews:        reviews,
	}, nil
```

- [ ] **Step 2.3: Run tests, build, vet, fmt**

```
go test ./internal/pr/ -v
go build ./...
go vet ./...
gofmt -l internal/pr
```
Expected: green. (Existing `TestLoad_*` tests still pass because they fail before reaching the new fetches.)

- [ ] **Step 2.4: Commit**

```bash
git add internal/pr/bundle.go
git commit -m "pr: include comments and reviews in Bundle"
```

---

## Task 3: `SectionComments` + `buildCommentsSection`

**Files:**
- Create: `internal/ctxpane/comments.go`
- Create: `internal/ctxpane/comments_test.go`
- Modify: `internal/ctxpane/types.go`
- Modify: `internal/ctxpane/resolver.go`

- [ ] **Step 3.1: Add `SectionComments` to the enum and title**

In `internal/ctxpane/types.go`, find:

```go
const (
	SectionWhere SectionKind = iota
	SectionSymbol
	SectionCrossFile
	SectionBlame
	SectionHistory
)
```

Append `SectionComments`:

```go
const (
	SectionWhere SectionKind = iota
	SectionSymbol
	SectionCrossFile
	SectionBlame
	SectionHistory
	SectionComments
)
```

And in the `Title()` switch, add a case before the final `return "?"`:

```go
	case SectionComments:
		return "Comments"
```

- [ ] **Step 3.2: Extend `Cursor` with comments + drafts inputs**

In `internal/ctxpane/types.go`, change `Cursor` to:

```go
type Cursor struct {
	File            diff.File
	HunkIndex       int
	Diff            *diff.Diff
	RepoRoot        string
	HistoryExpanded bool

	// Comment-related inputs. ReviewComments is the full list for the PR;
	// the Comments section filters by (Path, Line, Side). Drafts are the
	// user's pending unsubmitted comments; same filter.
	ReviewComments []CommentRef
	Drafts         []Draft
}
```

And append two new types to `types.go`:

```go
// CommentRef is the package-internal shape of a review comment. The pr
// package's ReviewComment maps directly to this — kept here so ctxpane has
// no import on pr.
type CommentRef struct {
	User string
	Path string
	Line int
	Side string // "RIGHT" | "LEFT"
	Body string
	Age  string // pre-formatted relative time ("2h ago")
}

// Draft is a locally-authored comment not yet submitted to GitHub.
type Draft struct {
	Path string
	Line int
	Side string
	Body string
}
```

- [ ] **Step 3.3: Write `comments.go`**

```go
package ctxpane

import (
	"strings"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

// buildCommentsSection returns the Comments section for the cursor's anchor
// line, mixing fetched ReviewComments and user-authored Drafts. Drafts are
// always rendered last, prefixed with [DRAFT].
//
// The section is empty when no comment or draft anchors to (Path, Line, Side).
func buildCommentsSection(cur Cursor) Section {
	s := Section{Kind: SectionComments, Status: StatusEmpty}
	if cur.File.Path == "" {
		return s
	}
	line, kind, ok := cur.AnchorLine()
	if !ok {
		return s
	}
	side := "RIGHT"
	if kind == diff.LineRemoved {
		side = "LEFT"
	}

	var items []Item
	for _, c := range cur.ReviewComments {
		if c.Path != cur.File.Path || c.Line != line || c.Side != side {
			continue
		}
		items = append(items, Item{Text: formatCommentRow(c.User, c.Age, c.Body)})
	}
	for _, d := range cur.Drafts {
		if d.Path != cur.File.Path || d.Line != line || d.Side != side {
			continue
		}
		items = append(items, Item{Text: formatDraftRow(d.Body)})
	}
	if len(items) == 0 {
		return s
	}
	s.Status = StatusOK
	s.Items = items
	return s
}

// formatCommentRow renders one fetched comment as a single short row.
// Body is collapsed to single-line and truncated to ~50 chars.
func formatCommentRow(user, age, body string) string {
	return user + " " + age + ": " + truncateBody(body, 50)
}

func formatDraftRow(body string) string {
	return "[DRAFT] you: " + truncateBody(body, 50)
}

func truncateBody(body string, max int) string {
	// Collapse runs of whitespace (including newlines) to a single space.
	flat := strings.Join(strings.Fields(body), " ")
	if len([]rune(flat)) <= max {
		return flat
	}
	r := []rune(flat)
	return string(r[:max-1]) + "…"
}
```

- [ ] **Step 3.4: Wire `buildCommentsSection` into `resolver.go`**

In `internal/ctxpane/resolver.go`, find:

```go
	tasks := []func(context.Context) Section{
		func(c context.Context) Section { return buildWhereSection(cur) },
		func(c context.Context) Section { return buildSymbolSection(c, cur) },
		func(c context.Context) Section { return buildCrossFileSection(c, cur) },
		func(c context.Context) Section { return buildBlameSection(c, cur) },
		func(c context.Context) Section { return buildHistorySection(c, cur) },
	}
```

Append the new task:

```go
	tasks := []func(context.Context) Section{
		func(c context.Context) Section { return buildWhereSection(cur) },
		func(c context.Context) Section { return buildSymbolSection(c, cur) },
		func(c context.Context) Section { return buildCrossFileSection(c, cur) },
		func(c context.Context) Section { return buildBlameSection(c, cur) },
		func(c context.Context) Section { return buildHistorySection(c, cur) },
		func(c context.Context) Section { return buildCommentsSection(cur) },
	}
```

Update `kindFor` to add the new index:

```go
func kindFor(i int) SectionKind {
	switch i {
	case 0:
		return SectionWhere
	case 1:
		return SectionSymbol
	case 2:
		return SectionCrossFile
	case 3:
		return SectionBlame
	case 4:
		return SectionHistory
	case 5:
		return SectionComments
	}
	panic(fmt.Sprintf("kindFor: no SectionKind for task index %d — update kindFor when adding tasks", i))
}
```

- [ ] **Step 3.5: Write `comments_test.go`**

```go
package ctxpane

import (
	"strings"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

func curOnLine(path string, line int, kind diff.LineKind) Cursor {
	f := diff.File{
		Path:  path,
		Hunks: []diff.Hunk{{Lines: []diff.Line{}}},
	}
	switch kind {
	case diff.LineAdded:
		f.Hunks[0].Lines = []diff.Line{{Kind: diff.LineAdded, NewNum: line}}
	case diff.LineRemoved:
		f.Hunks[0].Lines = []diff.Line{{Kind: diff.LineRemoved, OldNum: line}}
	default:
		f.Hunks[0].Lines = []diff.Line{{Kind: diff.LineContext, NewNum: line, OldNum: line}}
	}
	return Cursor{File: f, HunkIndex: 0}
}

func TestBuildCommentsSection_FiltersByAnchor(t *testing.T) {
	cur := curOnLine("src/a.go", 12, diff.LineAdded)
	cur.ReviewComments = []CommentRef{
		{User: "alice", Path: "src/a.go", Line: 12, Side: "RIGHT", Body: "looks good", Age: "2h"},
		{User: "bob", Path: "src/a.go", Line: 99, Side: "RIGHT", Body: "elsewhere", Age: "1h"}, // wrong line
		{User: "eve", Path: "other.go", Line: 12, Side: "RIGHT", Body: "wrong file", Age: "3h"},
	}
	s := buildCommentsSection(cur)
	if s.Status != StatusOK {
		t.Fatalf("status: got %v want OK", s.Status)
	}
	if len(s.Items) != 1 {
		t.Fatalf("items: got %d want 1 (%+v)", len(s.Items), s.Items)
	}
	if !strings.Contains(s.Items[0].Text, "alice") {
		t.Errorf("item text: got %q", s.Items[0].Text)
	}
}

func TestBuildCommentsSection_SideMatchesLineKind(t *testing.T) {
	cur := curOnLine("src/a.go", 5, diff.LineRemoved)
	cur.ReviewComments = []CommentRef{
		{User: "alice", Path: "src/a.go", Line: 5, Side: "LEFT", Body: "x", Age: "1h"},
		{User: "bob", Path: "src/a.go", Line: 5, Side: "RIGHT", Body: "y", Age: "1h"}, // wrong side
	}
	s := buildCommentsSection(cur)
	if len(s.Items) != 1 || !strings.Contains(s.Items[0].Text, "alice") {
		t.Errorf("items: got %+v", s.Items)
	}
}

func TestBuildCommentsSection_DraftsAfterFetched(t *testing.T) {
	cur := curOnLine("src/a.go", 12, diff.LineAdded)
	cur.ReviewComments = []CommentRef{
		{User: "alice", Path: "src/a.go", Line: 12, Side: "RIGHT", Body: "hi", Age: "2h"},
	}
	cur.Drafts = []Draft{
		{Path: "src/a.go", Line: 12, Side: "RIGHT", Body: "my reply"},
	}
	s := buildCommentsSection(cur)
	if len(s.Items) != 2 {
		t.Fatalf("items: got %d (%+v)", len(s.Items), s.Items)
	}
	if !strings.Contains(s.Items[0].Text, "alice") {
		t.Errorf("first item should be fetched comment: %q", s.Items[0].Text)
	}
	if !strings.Contains(s.Items[1].Text, "[DRAFT]") {
		t.Errorf("second item should be draft: %q", s.Items[1].Text)
	}
}

func TestTruncateBody(t *testing.T) {
	cases := map[string]string{
		"short":                          "short",
		"line one\nline two":             "line one line two",
		strings.Repeat("a", 60):          strings.Repeat("a", 49) + "…",
	}
	for in, want := range cases {
		if got := truncateBody(in, 50); got != want {
			t.Errorf("truncateBody(%q): got %q want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 3.6: Run tests, build, vet, fmt**

```
go test ./internal/ctxpane/ -v
go build ./...
go vet ./...
gofmt -l internal/ctxpane
```
Expected: green.

- [ ] **Step 3.7: Commit**

```bash
git add internal/ctxpane/comments.go internal/ctxpane/comments_test.go internal/ctxpane/types.go internal/ctxpane/resolver.go
git commit -m "ctxpane: SectionComments — anchor-filtered review comments + drafts"
```

---

## Task 4: Wire comments into the Model + `C` (compose) key

**Files:**
- Modify: `internal/ui/model.go`
- Modify: `internal/ui/model_test.go`
- Modify: `cmd/gitreview/main.go`

- [ ] **Step 4.1: Add `drafts` and a comment cache to `Model`**

In `internal/ui/model.go`'s `Model` struct, add fields near `prMeta`:

```go
	// PR comment state — non-empty only in PR mode.
	reviewComments []ctxpane.CommentRef // fetched, mapped from pr.ReviewComment
	drafts         []ctxpane.Draft       // in-memory; cleared on submit
```

Update the `New` signature to accept all four PR-related inputs in one struct (cleaner than adding three positional params). Add this type above `New`:

```go
// PRBundle is the optional PR data ui.New accepts. nil in pre-flight mode.
type PRBundle struct {
	Meta           *pr.PRMeta
	ReviewComments []ctxpane.CommentRef
}
```

And change `New`'s signature + body:

```go
func New(d *diff.Diff, commits []diff.Commit, repoRoot string, pb *PRBundle) Model {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.Placeholder = "filter files…"
	ti.CharLimit = 100
	m := Model{
		d:                  d,
		commits:            commits,
		commitDiff:         map[string]*diff.Diff{},
		commitErr:          map[string]error{},
		repoRoot:           repoRoot,
		focus:              paneLeft,
		filterInput:        ti,
		reviewedFiles:      map[string]bool{},
		contextPaneVisible: true,
		treeCollapsed:      map[string]bool{},
	}
	if pb != nil {
		m.prMeta = pb.Meta
		m.reviewComments = pb.ReviewComments
	}
	return m
}
```

- [ ] **Step 4.2: Pass comments + drafts into every `ctxpane.Cursor` construction**

There are two places in `model.go` that build a `ctxpane.Cursor`: inside `refreshDiff` (synchronous build for layout/render) and inside the `contextRefreshMsg` handler (debounced async build). Find each `ctxpane.Cursor{}` literal and add the two new fields. Example for the async one:

```go
		cur := ctxpane.Cursor{
			File:            m.currentFileForContext(),
			HunkIndex:       m.currentHunkIndex(),
			Diff:            m.d,
			RepoRoot:        m.repoRoot,
			HistoryExpanded: m.contextHistoryExpanded,
			ReviewComments:  m.reviewComments,
			Drafts:          m.drafts,
		}
```

Apply the same addition to BOTH call sites.

- [ ] **Step 4.3: Update `cmd/gitreview/main.go` to pass `PRBundle`**

The PR-mode entry point currently calls `ui.New(bundle.Diff, bundle.Commits, bundle.WorktreePath, &bundle.Meta)`. Update to map `pr.ReviewComment` → `ctxpane.CommentRef` and pass via `PRBundle`:

In `cmd/gitreview/main.go`, find `runPRMode` and replace the `m := ui.New(...)` call with:

```go
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
	m := ui.New(bundle.Diff, bundle.Commits, bundle.WorktreePath, &ui.PRBundle{
		Meta:           &bundle.Meta,
		ReviewComments: refs,
	})
```

Also add the helper at the bottom of the file:

```go
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
```

Add imports if missing:

```go
import (
	...
	"time"

	"github.com/bowenbrooks/gitreview/internal/ctxpane"
)
```

The non-PR path (`runPreflightMode`) calls `ui.New(d, commits, repoRoot, nil)` — that signature still works.

- [ ] **Step 4.4: Add the `C` key handler**

In `internal/ui/model.go`'s `Update` `tea.KeyMsg` switch, find the existing `case "O":` block and add `case "C":` right after it:

```go
		case "C":
			if m.prMeta == nil {
				return m, nil
			}
			if m.view != viewChanges || m.focus != paneLeft && m.focus != paneDiff {
				m.statusMsg = "C: switch to changes view first"
				return m, nil
			}
			fr, _, ok := m.currentFileRow()
			if !ok {
				m.statusMsg = "C: place cursor on a diff line first"
				return m, nil
			}
			cur := ctxpane.Cursor{File: fr, HunkIndex: m.currentHunkIndex()}
			line, kind, ok := cur.AnchorLine()
			if !ok || line == 0 {
				m.statusMsg = "C: no anchor line in this hunk"
				return m, nil
			}
			side := "RIGHT"
			if kind == diff.LineRemoved {
				side = "LEFT"
			}
			return m, m.composeDraft(fr.Path, line, side)
```

Add the helper near `openInEditor`:

```go
// composeDraft spawns $EDITOR on an empty temp file. On editor exit, the
// file contents (after stripping #-comment lines + trimming) become the
// draft body. Empty bodies are dropped without state change.
func (m *Model) composeDraft(path string, line int, side string) tea.Cmd {
	f, err := os.CreateTemp("", "gitreview-draft-*.md")
	if err != nil {
		m.statusMsg = "compose: " + err.Error()
		return nil
	}
	f.Close()
	cmd := editorCmd(f.Name(), 1)
	if cmd == nil {
		m.statusMsg = "compose: no editor found (set $EDITOR)"
		_ = os.Remove(f.Name())
		return nil
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(f.Name())
		if err != nil {
			return draftComposedMsg{err: err}
		}
		raw, readErr := os.ReadFile(f.Name())
		if readErr != nil {
			return draftComposedMsg{err: readErr}
		}
		body := stripDraftComments(string(raw))
		return draftComposedMsg{
			draft: ctxpane.Draft{Path: path, Line: line, Side: side, Body: body},
		}
	})
}

// stripDraftComments removes lines starting with # (and trims trailing space).
func stripDraftComments(s string) string {
	var keep []string
	for _, ln := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimLeft(ln, " \t"), "#") {
			continue
		}
		keep = append(keep, ln)
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

// draftComposedMsg is delivered after the compose editor exits.
type draftComposedMsg struct {
	draft ctxpane.Draft
	err   error
}
```

Then handle the message — add a new case to `Update` alongside `editorDoneMsg`:

```go
	case draftComposedMsg:
		if msg.err != nil {
			m.statusMsg = "compose: " + msg.err.Error()
			return m, nil
		}
		if msg.draft.Body == "" {
			m.statusMsg = "compose: cancelled (empty)"
			return m, nil
		}
		m.drafts = append(m.drafts, msg.draft)
		m.statusMsg = fmt.Sprintf("draft saved (%d total)", len(m.drafts))
		return m, m.scheduleContextRefresh()
```

- [ ] **Step 4.5: Update tests that call `New`**

The signature change `ui.New(..., *PRBundle)` requires every existing `New(fakeDiff(), nil, "", nil)` call to remain valid (the last param `nil` is now `*PRBundle` instead of `*pr.PRMeta`). Same nil. No test code changes needed there.

But `TestPRModeHeader` passes a `*pr.PRMeta` directly; update it:

```go
func TestPRModeHeader(t *testing.T) {
	meta := &pr.PRMeta{
		Number:  42,
		Owner:   "foo",
		Repo:    "bar",
		Title:   "Add caching",
		Author:  "alice",
		State:   "open",
		HTMLURL: "https://github.com/foo/bar/pull/42",
	}
	m := New(fakeDiff(), nil, "", &PRBundle{Meta: meta})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	m = updated.(Model)
	out := m.View()
	if !strings.Contains(out, "PR #42") {
		t.Errorf("View missing PR strip: %s", out)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("View missing author: %s", out)
	}
	if !strings.Contains(out, "O: open in browser") {
		t.Errorf("View missing browser hint: %s", out)
	}
}
```

- [ ] **Step 4.6: Run tests, build, vet, fmt**

```
go test ./... -v 2>&1 | tail -30
go build ./...
go vet ./...
gofmt -l internal/ui cmd
```
Expected: green. (We're not adding a test for the compose flow itself in this task — Task 4's surface is wiring + plumbing. The compose key is integration-tested as part of the smoke test in Task 7.)

- [ ] **Step 4.7: Commit**

```bash
git add internal/ui/ cmd/
git commit -m "ui: PRBundle param, C compose key, draft buffer"
```

---

## Task 5: Thread modal — `t`, `Esc`, `e`, `x`

**Files:**
- Create: `internal/ui/threadmodal.go`
- Create: `internal/ui/threadmodal_test.go`
- Modify: `internal/ui/model.go`
- Modify: `internal/ui/styles.go`

- [ ] **Step 5.1: Add modal styles**

In `internal/ui/styles.go`, append to the existing `var (...)` block:

```go
	// modalStyle is the outer box for popup overlays (thread modal etc.).
	modalStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colBorderFocus).
			Padding(1, 2)

	// prDraftStyle dims/marks user-authored drafts so they're distinguishable
	// from fetched comments.
	prDraftStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")). // amber
			Bold(true)
```

- [ ] **Step 5.2: Write `threadmodal.go`**

```go
package ui

import (
	"strings"

	"github.com/bowenbrooks/gitreview/internal/ctxpane"
	"github.com/charmbracelet/lipgloss"
)

// threadEntry is one rendered row in the thread modal: a fetched comment or
// a draft. DraftIdx is -1 for fetched; otherwise it's the index into m.drafts
// so the user can edit / delete that specific draft via e / x.
type threadEntry struct {
	Author   string
	Age      string
	Body     string
	IsDraft  bool
	DraftIdx int
}

// buildThread returns the entries for the modal at (path, line, side),
// fetched-comments first then drafts in insertion order.
func buildThread(reviewComments []ctxpane.CommentRef, drafts []ctxpane.Draft, path string, line int, side string) []threadEntry {
	var out []threadEntry
	for _, c := range reviewComments {
		if c.Path == path && c.Line == line && c.Side == side {
			out = append(out, threadEntry{
				Author: c.User,
				Age:    c.Age,
				Body:   c.Body,
			})
		}
	}
	for i, d := range drafts {
		if d.Path == path && d.Line == line && d.Side == side {
			out = append(out, threadEntry{
				Author:   "you",
				Age:      "draft",
				Body:     d.Body,
				IsDraft:  true,
				DraftIdx: i,
			})
		}
	}
	return out
}

// renderThreadModal renders the modal contents (not the overlay placement —
// caller composes that via lipgloss.Place).
func renderThreadModal(title string, entries []threadEntry, selected int, innerW int) string {
	var b strings.Builder
	b.WriteString(modalTitleLine(title))
	b.WriteString("\n\n")
	for i, e := range entries {
		header := e.Author + "  " + e.Age
		if e.IsDraft {
			header = prDraftStyle.Render("[DRAFT] you  " + e.Age)
		}
		if i == selected {
			header = cursorStyle.Render(header)
		}
		b.WriteString(header)
		b.WriteString("\n")
		b.WriteString(strings.Repeat("─", min(len(header), innerW)))
		b.WriteString("\n")
		b.WriteString(wrapText(e.Body, innerW))
		b.WriteString("\n\n")
	}
	b.WriteString(mutedStyle.Render(" Esc close · e edit draft · x delete draft "))
	return b.String()
}

// wrapText hard-wraps body at width w, preserving paragraph breaks (lines
// that are exactly empty stay as paragraph separators).
func wrapText(body string, w int) string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if line == "" {
			out = append(out, "")
			continue
		}
		words := strings.Fields(line)
		var cur string
		for _, word := range words {
			if cur == "" {
				cur = word
				continue
			}
			if len(cur)+1+len(word) > w {
				out = append(out, cur)
				cur = word
				continue
			}
			cur += " " + word
		}
		if cur != "" {
			out = append(out, cur)
		}
	}
	return strings.Join(out, "\n")
}

func modalTitleLine(s string) string {
	return lipgloss.NewStyle().Bold(true).Render(s)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

- [ ] **Step 5.3: Add modal state and keys to `model.go`**

Add fields on `Model` near `drafts`:

```go
	// Thread modal state.
	modalOpen     bool
	modalEntries  []threadEntry
	modalSelected int
	modalAnchor   modalAnchor // remembers path/line/side so refresh keeps the modal coherent
```

Add a small type near `Model`:

```go
type modalAnchor struct {
	Path string
	Line int
	Side string
}
```

Add the `t` / `Esc` / `e` / `x` handlers. Find the existing `case "O":` block. Add right after it:

```go
		case "t":
			if m.prMeta == nil || m.view != viewChanges {
				return m, nil
			}
			fr, _, ok := m.currentFileRow()
			if !ok {
				return m, nil
			}
			cur := ctxpane.Cursor{File: fr, HunkIndex: m.currentHunkIndex()}
			line, kind, ok := cur.AnchorLine()
			if !ok || line == 0 {
				return m, nil
			}
			side := "RIGHT"
			if kind == diff.LineRemoved {
				side = "LEFT"
			}
			entries := buildThread(m.reviewComments, m.drafts, fr.Path, line, side)
			if len(entries) == 0 {
				return m, nil
			}
			m.modalOpen = true
			m.modalEntries = entries
			m.modalSelected = 0
			m.modalAnchor = modalAnchor{Path: fr.Path, Line: line, Side: side}
			return m, nil
```

Add a generic `Esc`/`j`/`k`/`e`/`x` interception when the modal is open. Put this near the top of the `tea.KeyMsg` branch, BEFORE the existing key switch (because the modal traps keys):

```go
		if m.modalOpen {
			switch msg.String() {
			case "esc":
				m.modalOpen = false
				return m, nil
			case "j", "down":
				if m.modalSelected+1 < len(m.modalEntries) {
					m.modalSelected++
				}
				return m, nil
			case "k", "up":
				if m.modalSelected > 0 {
					m.modalSelected--
				}
				return m, nil
			case "x":
				if e := m.modalEntries[m.modalSelected]; e.IsDraft {
					m.drafts = append(m.drafts[:e.DraftIdx], m.drafts[e.DraftIdx+1:]...)
					// Rebuild modal entries from updated drafts.
					m.modalEntries = buildThread(m.reviewComments, m.drafts, m.modalAnchor.Path, m.modalAnchor.Line, m.modalAnchor.Side)
					if m.modalSelected >= len(m.modalEntries) {
						m.modalSelected = max(0, len(m.modalEntries)-1)
					}
					if len(m.modalEntries) == 0 {
						m.modalOpen = false
					}
					return m, m.scheduleContextRefresh()
				}
				return m, nil
			case "e":
				if e := m.modalEntries[m.modalSelected]; e.IsDraft {
					return m, m.editDraft(e.DraftIdx)
				}
				return m, nil
			}
			// Any other key while modal is open: swallow (don't fall through).
			return m, nil
		}
```

Add the `max` helper near `min` (or in model.go):

```go
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
```

Add the `editDraft` helper near `composeDraft`:

```go
// editDraft re-spawns $EDITOR with the existing draft body as the buffer.
func (m *Model) editDraft(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.drafts) {
		return nil
	}
	d := m.drafts[idx]
	f, err := os.CreateTemp("", "gitreview-draft-*.md")
	if err != nil {
		m.statusMsg = "edit: " + err.Error()
		return nil
	}
	if _, err := f.WriteString(d.Body); err != nil {
		f.Close()
		os.Remove(f.Name())
		m.statusMsg = "edit: " + err.Error()
		return nil
	}
	f.Close()
	cmd := editorCmd(f.Name(), 1)
	if cmd == nil {
		m.statusMsg = "edit: no editor found (set $EDITOR)"
		os.Remove(f.Name())
		return nil
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(f.Name())
		if err != nil {
			return draftEditedMsg{idx: idx, err: err}
		}
		raw, readErr := os.ReadFile(f.Name())
		if readErr != nil {
			return draftEditedMsg{idx: idx, err: readErr}
		}
		return draftEditedMsg{idx: idx, body: stripDraftComments(string(raw))}
	})
}

type draftEditedMsg struct {
	idx  int
	body string
	err  error
}
```

Handle `draftEditedMsg` in `Update`:

```go
	case draftEditedMsg:
		if msg.err != nil {
			m.statusMsg = "edit: " + msg.err.Error()
			return m, nil
		}
		if msg.idx < 0 || msg.idx >= len(m.drafts) {
			return m, nil
		}
		if msg.body == "" {
			// Treat empty save as delete.
			m.drafts = append(m.drafts[:msg.idx], m.drafts[msg.idx+1:]...)
		} else {
			m.drafts[msg.idx].Body = msg.body
		}
		if m.modalOpen {
			m.modalEntries = buildThread(m.reviewComments, m.drafts, m.modalAnchor.Path, m.modalAnchor.Line, m.modalAnchor.Side)
			if m.modalSelected >= len(m.modalEntries) {
				m.modalSelected = max(0, len(m.modalEntries)-1)
			}
			if len(m.modalEntries) == 0 {
				m.modalOpen = false
			}
		}
		return m, m.scheduleContextRefresh()
```

- [ ] **Step 5.4: Render the modal overlay in `View`**

In `internal/ui/model.go` `View()` (or wherever the final `JoinVertical` is), wrap the result so when `m.modalOpen` is true, the modal is overlaid via `lipgloss.Place`. Find:

```go
	return lipgloss.JoinVertical(lipgloss.Left, header, body, m.renderHelp())
}
```

Replace with:

```go
	view := lipgloss.JoinVertical(lipgloss.Left, header, body, m.renderHelp())
	if m.modalOpen {
		modalW := minInt(m.width-4, 80)
		innerW := modalW - 6 // border + padding
		title := fmt.Sprintf("Thread: %s:%d", m.modalAnchor.Path, m.modalAnchor.Line)
		content := renderThreadModal(title, m.modalEntries, m.modalSelected, innerW)
		modal := modalStyle.Width(modalW).Render(content)
		view = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal,
			lipgloss.WithWhitespaceChars(" "))
	}
	return view
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

- [ ] **Step 5.5: Write `threadmodal_test.go`**

```go
package ui

import (
	"strings"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/ctxpane"
)

func TestBuildThread(t *testing.T) {
	rcs := []ctxpane.CommentRef{
		{User: "alice", Path: "a.go", Line: 1, Side: "RIGHT", Body: "hi", Age: "2h"},
		{User: "bob", Path: "b.go", Line: 1, Side: "RIGHT", Body: "elsewhere", Age: "1h"},
	}
	drafts := []ctxpane.Draft{
		{Path: "a.go", Line: 1, Side: "RIGHT", Body: "my draft"},
	}
	got := buildThread(rcs, drafts, "a.go", 1, "RIGHT")
	if len(got) != 2 {
		t.Fatalf("entries: got %d want 2 (%+v)", len(got), got)
	}
	if got[0].Author != "alice" || got[0].IsDraft {
		t.Errorf("entry 0: %+v", got[0])
	}
	if !got[1].IsDraft || got[1].DraftIdx != 0 {
		t.Errorf("entry 1: %+v", got[1])
	}
}

func TestRenderThreadModal(t *testing.T) {
	entries := []threadEntry{
		{Author: "alice", Age: "2h", Body: "hello world"},
		{IsDraft: true, Author: "you", Age: "draft", Body: "my draft", DraftIdx: 0},
	}
	out := renderThreadModal("Thread: a.go:1", entries, 1, 40)
	if !strings.Contains(out, "Thread: a.go:1") {
		t.Errorf("missing title: %s", out)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("missing alice: %s", out)
	}
	if !strings.Contains(out, "[DRAFT]") {
		t.Errorf("missing draft marker: %s", out)
	}
	if !strings.Contains(out, "Esc close") {
		t.Errorf("missing help line: %s", out)
	}
}

func TestWrapText(t *testing.T) {
	in := "one two three four five six seven eight nine ten"
	got := wrapText(in, 12)
	for _, line := range strings.Split(got, "\n") {
		if len(line) > 12 {
			t.Errorf("line too long (%d): %q", len(line), line)
		}
	}
}
```

- [ ] **Step 5.6: Run tests, build, vet, fmt**

```
go test ./internal/ui/ -v 2>&1 | tail -20
go build ./...
go vet ./...
gofmt -l internal/ui
```
Expected: green.

- [ ] **Step 5.7: Commit**

```bash
git add internal/ui/
git commit -m "ui: thread modal — t to open, Esc/j/k/e/x within"
```

---

## Task 6: PR-info tab — `[4 PR]` + `B` review-body key

**Files:**
- Create: `internal/ui/prtab.go`
- Create: `internal/ui/prtab_test.go`
- Modify: `internal/ui/model.go`
- Modify: `internal/ui/render.go`

- [ ] **Step 6.1: Add `viewPR` mode and `reviewBody` field**

In `internal/ui/model.go`, add to the `viewMode` constants:

```go
const (
	viewChanges viewMode = iota
	viewCommits
	viewOverview
	viewPR
)
```

Add fields to `Model` near `drafts`:

```go
	// PR-mode-only fields.
	issueComments []ctxpane.IssueCommentDisplay
	reviews       []ctxpane.ReviewDisplay
	reviewBody    string // composed via B; consumed by S
	prViewport    viewport.Model
```

Note: we keep small display-only mirror types in `ctxpane` to avoid a UI→pr import. Append to `internal/ctxpane/types.go`:

```go
// IssueCommentDisplay and ReviewDisplay are display-only mirrors of pr's
// IssueComment / Review. ctxpane has no import on pr; the wiring layer
// (cmd/gitreview/main.go) maps the wire types into these.
type IssueCommentDisplay struct {
	User string
	Age  string
	Body string
}

type ReviewDisplay struct {
	User  string
	State string // "APPROVED" | "CHANGES_REQUESTED" | "COMMENTED"
	Age   string
	Body  string
}
```

Extend `PRBundle` (in `internal/ui/model.go`) and `New` to accept them:

```go
type PRBundle struct {
	Meta           *pr.PRMeta
	ReviewComments []ctxpane.CommentRef
	IssueComments  []ctxpane.IssueCommentDisplay
	Reviews        []ctxpane.ReviewDisplay
}
```

In `New`, store them on the model:

```go
	if pb != nil {
		m.prMeta = pb.Meta
		m.reviewComments = pb.ReviewComments
		m.issueComments = pb.IssueComments
		m.reviews = pb.Reviews
	}
```

In `cmd/gitreview/main.go` `runPRMode`, extend the bundle build to map the two new slices:

```go
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
	m := ui.New(bundle.Diff, bundle.Commits, bundle.WorktreePath, &ui.PRBundle{
		Meta:           &bundle.Meta,
		ReviewComments: refs,
		IssueComments:  ics,
		Reviews:        rvs,
	})
```

- [ ] **Step 6.2: Write `prtab.go`**

```go
package ui

import (
	"fmt"
	"strings"

	"github.com/bowenbrooks/gitreview/internal/ctxpane"
	"github.com/bowenbrooks/gitreview/internal/pr"
)

// renderPRTabBody returns the scrollable body of the [4 PR] tab.
// It does NOT include the tab strip — that's already rendered by
// renderTopHeader. It DOES include all sections.
func renderPRTabBody(meta *pr.PRMeta, issueComments []ctxpane.IssueCommentDisplay, reviews []ctxpane.ReviewDisplay, draftCount int, reviewBody string, width int) string {
	var b strings.Builder
	if meta == nil {
		b.WriteString(mutedStyle.Render("(no PR loaded)"))
		return b.String()
	}
	// Header
	fmt.Fprintf(&b, "PR #%d — %s — %s\n", meta.Number, meta.Author, meta.State)
	if meta.HTMLURL != "" {
		fmt.Fprintf(&b, "%s\n\n", mutedStyle.Render(meta.HTMLURL))
	}
	fmt.Fprintf(&b, "  %s\n\n", lipglossBold(meta.Title))
	if strings.TrimSpace(meta.Body) != "" {
		b.WriteString(indent(wrapText(meta.Body, width-4), "  "))
		b.WriteString("\n\n")
	}

	// Issue comments
	b.WriteString(sectionRule(fmt.Sprintf("Issue comments (%d)", len(issueComments)), width))
	b.WriteString("\n")
	if len(issueComments) == 0 {
		b.WriteString("  " + mutedStyle.Render("(none)") + "\n")
	}
	for _, c := range issueComments {
		fmt.Fprintf(&b, "  %s  %s\n", c.User, mutedStyle.Render(c.Age))
		b.WriteString(indent(wrapText(c.Body, width-4), "  "))
		b.WriteString("\n\n")
	}

	// Reviews
	b.WriteString(sectionRule(fmt.Sprintf("Reviews (%d)", len(reviews)), width))
	b.WriteString("\n")
	if len(reviews) == 0 {
		b.WriteString("  " + mutedStyle.Render("(none)") + "\n")
	}
	for _, r := range reviews {
		fmt.Fprintf(&b, "  %s  %s  %s\n", r.User, r.State, mutedStyle.Render(r.Age))
		if strings.TrimSpace(r.Body) != "" {
			b.WriteString(indent(wrapText(r.Body, width-4), "  > "))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Pending review
	b.WriteString(sectionRule("Pending review", width))
	b.WriteString("\n")
	if draftCount == 0 && reviewBody == "" {
		b.WriteString("  " + mutedStyle.Render("(no drafts)") + "\n")
	} else {
		fmt.Fprintf(&b, "  %d draft inline %s\n", draftCount, plural("comment", draftCount))
		if reviewBody != "" {
			b.WriteString("\n  review body:\n")
			b.WriteString(indent(wrapText(reviewBody, width-4), "  > "))
			b.WriteString("\n")
		}
		b.WriteString("\n  ")
		b.WriteString(mutedStyle.Render("Press S to submit · Press B to add review body"))
		b.WriteString("\n")
	}
	return b.String()
}

func sectionRule(title string, width int) string {
	const prefix = "──── "
	suffix := strings.Repeat("─", maxInt(0, width-len(title)-len(prefix)-1))
	return mutedStyle.Render(prefix + title + " " + suffix)
}

func indent(s, with string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = with + l
	}
	return strings.Join(lines, "\n")
}

func plural(noun string, n int) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func lipglossBold(s string) string {
	return titleStyle.Render(s)
}
```

- [ ] **Step 6.3: Wire `viewPR` into the View() body branch**

In `internal/ui/model.go`'s `View`, find the existing view-mode switch (the one that handles `viewOverview`). Add a branch for `viewPR`:

```go
	} else if m.view == viewOverview {
		body = m.renderOverviewBody()
	} else if m.view == viewPR {
		body = m.renderPRTabBody()
	} else {
```

Add the method on Model:

```go
// renderPRTabBody renders the [4 PR] tab using its own viewport for scrolling.
func (m *Model) renderPRTabBody() string {
	innerW := m.width - 4
	if innerW < 20 {
		innerW = 20
	}
	bodyH := m.height - headerRows - helpHeight - 2
	m.prViewport.Width = innerW
	m.prViewport.Height = bodyH
	content := renderPRTabBody(m.prMeta, m.issueComments, m.reviews, len(m.drafts), m.reviewBody, innerW)
	m.prViewport.SetContent(content)
	return paneStyle.Width(m.width-2).Height(bodyH).Render(m.prViewport.View())
}
```

In `New`, initialize `m.prViewport` to a viewport with placeholder size (real size set on first WindowSizeMsg via `renderPRTabBody`):

```go
	m.prViewport = viewport.New(0, 0)
```

(Add this line to `New` after `m := Model{...}`.)

- [ ] **Step 6.4: Add `4` key and tab strip entry for PR mode**

In `internal/ui/model.go`'s `Update`, find the `case "1":` / `case "2":` / `case "3":` block and add:

```go
		case "4":
			if m.prMeta == nil {
				m.statusMsg = "4: only in PR mode"
				return m, nil
			}
			m.setView(viewPR)
			return m, nil
```

In `renderTabsGlobal`, find the existing tabs and add a fourth conditional one:

```go
	parts := []string{
		style(m.view == viewChanges).Render("[1 changes]"),
		style(m.view == viewCommits).Render("[2 commits]"),
		style(m.view == viewOverview).Render("[3 overview]"),
	}
	if m.prMeta != nil {
		parts = append(parts, style(m.view == viewPR).Render("[4 PR]"))
	}
```

Update `setView` to accept `viewPR`:

```go
func (m *Model) setView(v viewMode) {
	if v == viewCommits && len(m.commits) == 0 {
		m.statusMsg = "no commits to browse"
		return
	}
	if v == viewOverview {
		files, _ := m.effectiveFiles()
		if len(files) == 0 {
			m.statusMsg = "no files to overview"
			return
		}
	}
	if v == viewPR && m.prMeta == nil {
		m.statusMsg = "no PR loaded"
		return
	}
	if v == m.view {
		return
	}
	m.view = v
	m.statusMsg = ""
	m.refreshDiff()
}
```

- [ ] **Step 6.5: Add `B` key (review body)**

In `Update`, near `case "C":`, add:

```go
		case "B":
			if m.prMeta == nil {
				return m, nil
			}
			return m, m.composeReviewBody()
```

Add the helper next to `composeDraft`:

```go
// composeReviewBody opens $EDITOR with the current m.reviewBody as the
// starting buffer; on save, the result replaces m.reviewBody.
func (m *Model) composeReviewBody() tea.Cmd {
	f, err := os.CreateTemp("", "gitreview-review-body-*.md")
	if err != nil {
		m.statusMsg = "B: " + err.Error()
		return nil
	}
	if _, err := f.WriteString(m.reviewBody); err != nil {
		f.Close()
		os.Remove(f.Name())
		m.statusMsg = "B: " + err.Error()
		return nil
	}
	f.Close()
	cmd := editorCmd(f.Name(), 1)
	if cmd == nil {
		m.statusMsg = "B: no editor found (set $EDITOR)"
		os.Remove(f.Name())
		return nil
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(f.Name())
		if err != nil {
			return reviewBodyComposedMsg{err: err}
		}
		raw, readErr := os.ReadFile(f.Name())
		if readErr != nil {
			return reviewBodyComposedMsg{err: readErr}
		}
		return reviewBodyComposedMsg{body: stripDraftComments(string(raw))}
	})
}

type reviewBodyComposedMsg struct {
	body string
	err  error
}
```

Handle the message in `Update`:

```go
	case reviewBodyComposedMsg:
		if msg.err != nil {
			m.statusMsg = "B: " + msg.err.Error()
			return m, nil
		}
		m.reviewBody = msg.body
		m.statusMsg = "review body saved"
		return m, nil
```

- [ ] **Step 6.6: Write `prtab_test.go`**

```go
package ui

import (
	"strings"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/ctxpane"
	"github.com/bowenbrooks/gitreview/internal/pr"
)

func TestRenderPRTabBody(t *testing.T) {
	meta := &pr.PRMeta{
		Number: 42,
		Author: "alice",
		State:  "open",
		Title:  "Add caching",
		Body:   "Speeds up lookups.",
	}
	ics := []ctxpane.IssueCommentDisplay{
		{User: "bob", Age: "2d ago", Body: "Looks good."},
	}
	rvs := []ctxpane.ReviewDisplay{
		{User: "carol", State: "APPROVED", Age: "1d ago", Body: "LGTM"},
	}
	out := renderPRTabBody(meta, ics, rvs, 2, "shipping next week", 80)

	for _, want := range []string{"PR #42", "alice", "open", "Add caching", "Speeds up lookups", "bob", "Looks good", "carol", "APPROVED", "LGTM", "2 draft inline comments", "shipping next week"} {
		if !strings.Contains(out, want) {
			t.Errorf("body missing %q. Got:\n%s", want, out)
		}
	}
}

func TestRenderPRTabBody_Empty(t *testing.T) {
	meta := &pr.PRMeta{Number: 1, Author: "x", State: "open", Title: "t"}
	out := renderPRTabBody(meta, nil, nil, 0, "", 80)
	if !strings.Contains(out, "(no drafts)") {
		t.Errorf("missing no-drafts marker: %s", out)
	}
	if !strings.Contains(out, "Issue comments (0)") {
		t.Errorf("missing issue header: %s", out)
	}
}

func TestPRTabKeyOnlyInPRMode(t *testing.T) {
	m := New(fakeDiff(), nil, "", nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	m = updated.(Model)
	if m.view == viewPR {
		t.Error("pressing 4 in non-PR mode should not switch to viewPR")
	}
}
```

(`tea` import already present from existing tests in this package; if `model_test.go` already has it, fine.)

- [ ] **Step 6.7: Run tests, build, vet, fmt**

```
go test ./... -v 2>&1 | tail -25
go build ./...
go vet ./...
gofmt -l internal/ui cmd internal/ctxpane
```
Expected: green.

- [ ] **Step 6.8: Commit**

```bash
git add internal/ui/ internal/ctxpane/ cmd/
git commit -m "ui: [4 PR] tab — PR title/body/comments/reviews/drafts + B key"
```

---

## Task 7: Submit flow — `S` key + `pr.Submit`

**Files:**
- Create: `internal/pr/submit.go`
- Create: `internal/pr/submit_test.go`
- Modify: `internal/ui/model.go`
- Modify: `internal/pr/comments.go` (re-export `Side` constants — used by Draft mapping)

- [ ] **Step 7.1: Write `submit.go`**

```go
package pr

import (
	"context"
	"fmt"

	"github.com/google/go-github/v66/github"
)

// SubmitDraft is the cross-package view of a draft inline comment. The UI
// layer constructs these from ctxpane.Draft + (path, line, side) before
// calling Submit.
type SubmitDraft struct {
	Path string
	Line int
	Side string // "RIGHT" | "LEFT"
	Body string
}

// Submit POSTs a single review to GitHub with state=COMMENT, the optional
// overall body, and all drafts as inline comments. Returns nil on success.
// On HTTP failure, the returned error includes GitHub's response body so
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
```

- [ ] **Step 7.2: Write `submit_test.go`**

The submit test uses its OWN mock server (a dedicated POST handler) rather than reusing `startMockGitHub` — keeps the mock small and the captured-body assertion local.

```go
package pr

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSubmit_HappyPath(t *testing.T) {
	var capturedBody []byte
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/foo/bar/pulls/89/reviews", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "want POST", http.StatusMethodNotAllowed)
			return
		}
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 5000})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := newClient("testtoken", srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	err = Submit(context.Background(), c, "foo", "bar", 89, "overall LGTM", []SubmitDraft{
		{Path: "src/a.go", Line: 12, Side: "RIGHT", Body: "nit"},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(capturedBody, &got); err != nil {
		t.Fatalf("body unmarshal: %v\nraw: %s", err, capturedBody)
	}
	if got["event"] != "COMMENT" {
		t.Errorf("event: got %v want COMMENT", got["event"])
	}
	if got["body"] != "overall LGTM" {
		t.Errorf("body: got %v want overall LGTM", got["body"])
	}
	comments, _ := got["comments"].([]any)
	if len(comments) != 1 {
		t.Fatalf("comments count: got %d want 1", len(comments))
	}
	c0, _ := comments[0].(map[string]any)
	if c0["path"] != "src/a.go" || c0["body"] != "nit" {
		t.Errorf("comment 0: got %+v", c0)
	}
	if c0["line"].(float64) != 12 || c0["side"] != "RIGHT" {
		t.Errorf("comment 0 anchor: got %+v", c0)
	}
}

func TestSubmit_RejectsEmptyAndNoBody(t *testing.T) {
	err := Submit(context.Background(), nil, "foo", "bar", 89, "", nil)
	if err == nil {
		t.Error("Submit with no drafts and no body should error")
	}
}

func TestSubmit_PostError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/foo/bar/pulls/89/reviews", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message": "Resource not accessible by integration"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := newClient("t", srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	err = Submit(context.Background(), c, "foo", "bar", 89, "", []SubmitDraft{{Path: "a", Line: 1, Side: "RIGHT", Body: "x"}})
	if err == nil {
		t.Fatal("want error on 403")
	}
}
```

- [ ] **Step 7.4: Add the `S` key + plumbing in `model.go`**

Add fields to `Model` if not already present (a github client + auth handles for submit; reusing what `pr.Load` constructed is non-trivial, so we re-resolve inside `S`):

Actually, simpler: store the client on the model when in PR mode. Extend `PRBundle`:

```go
type PRBundle struct {
	Meta           *pr.PRMeta
	ReviewComments []ctxpane.CommentRef
	IssueComments  []ctxpane.IssueCommentDisplay
	Reviews        []ctxpane.ReviewDisplay
	Submitter      func(ctx context.Context, body string, drafts []pr.SubmitDraft) error
}
```

Store the submitter on the Model:

```go
	submitter func(ctx context.Context, body string, drafts []pr.SubmitDraft) error
```

In `New`:

```go
	if pb != nil {
		m.prMeta = pb.Meta
		m.reviewComments = pb.ReviewComments
		m.issueComments = pb.IssueComments
		m.reviews = pb.Reviews
		m.submitter = pb.Submitter
	}
```

In `cmd/gitreview/main.go` `runPRMode`, build a submitter closure that captures the client + owner/repo/num:

```go
	// Build a submitter closure for the UI. Holds the GitHub client across the
	// session so S can post without re-auth.
	submitToken, _ := pr.ResolveToken(ctx)
	submitClient, _ := pr.NewClientForTest("") // see note below
	_ = submitToken
	_ = submitClient
```

Hmm, `newClient` is unexported. The cleanest fix: export a helper `pr.NewSubmitter` that returns a closure already bound to client/owner/repo/num. Add at the bottom of `internal/pr/submit.go`:

```go
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
```

And in `cmd/gitreview/main.go` `runPRMode`, after `bundle, err := pr.Load(...)`:

```go
	submitToken, err := pr.ResolveToken(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitreview: warn: submit disabled (auth):", err)
	}
	var submitter func(ctx context.Context, body string, drafts []pr.SubmitDraft) error
	if submitToken != "" {
		submitter, err = pr.NewSubmitter(submitToken, bundle.Meta.Owner, bundle.Meta.Repo, bundle.Meta.Number)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gitreview: warn: submit disabled (client):", err)
		}
	}
```

Pass `submitter` into `&ui.PRBundle{..., Submitter: submitter}`.

Now the `S` key handler in `model.go`'s `Update`:

```go
		case "S":
			if m.prMeta == nil {
				return m, nil
			}
			if m.submitter == nil {
				m.statusMsg = "S: submit unavailable (auth failed at startup)"
				return m, nil
			}
			if len(m.drafts) == 0 {
				m.statusMsg = "S: no drafts to submit"
				return m, nil
			}
			return m, m.composeAndSubmit()
```

Add the helper:

```go
// composeAndSubmit opens $EDITOR with the templated body, then POSTs via
// the configured submitter. On success: clears drafts + reviewBody and
// triggers a context-pane refresh. On failure: drafts kept, status shown.
func (m *Model) composeAndSubmit() tea.Cmd {
	tpl := "# Review body (optional — leave empty for no overall comment).\n# Lines starting with # are stripped.\n#\n"
	tpl += fmt.Sprintf("# %d inline drafts:\n", len(m.drafts))
	for _, d := range m.drafts {
		tpl += fmt.Sprintf("#   %s:%d  %q\n", d.Path, d.Line, truncForTemplate(d.Body, 60))
	}
	if m.reviewBody != "" {
		tpl += "\n" + m.reviewBody
	}
	f, err := os.CreateTemp("", "gitreview-submit-*.md")
	if err != nil {
		m.statusMsg = "S: " + err.Error()
		return nil
	}
	if _, err := f.WriteString(tpl); err != nil {
		f.Close()
		os.Remove(f.Name())
		m.statusMsg = "S: " + err.Error()
		return nil
	}
	f.Close()
	cmd := editorCmd(f.Name(), 1)
	if cmd == nil {
		m.statusMsg = "S: no editor found (set $EDITOR)"
		os.Remove(f.Name())
		return nil
	}
	// Capture the slice + submitter at scheduling time so the Cmd closure
	// doesn't race with later drafts edits.
	draftsSnap := make([]pr.SubmitDraft, len(m.drafts))
	for i, d := range m.drafts {
		draftsSnap[i] = pr.SubmitDraft{Path: d.Path, Line: d.Line, Side: d.Side, Body: d.Body}
	}
	submitter := m.submitter
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(f.Name())
		if err != nil {
			return submitDoneMsg{err: err}
		}
		raw, readErr := os.ReadFile(f.Name())
		if readErr != nil {
			return submitDoneMsg{err: readErr}
		}
		body := stripDraftComments(string(raw))
		if err := submitter(context.Background(), body, draftsSnap); err != nil {
			return submitDoneMsg{err: err}
		}
		return submitDoneMsg{n: len(draftsSnap)}
	})
}

func truncForTemplate(s string, n int) string {
	flat := strings.Join(strings.Fields(s), " ")
	if len([]rune(flat)) <= n {
		return flat
	}
	r := []rune(flat)
	return string(r[:n-1]) + "…"
}

type submitDoneMsg struct {
	n   int
	err error
}
```

Handle the message in `Update`:

```go
	case submitDoneMsg:
		if msg.err != nil {
			m.statusMsg = "submit failed: " + msg.err.Error()
			return m, nil
		}
		m.drafts = nil
		m.reviewBody = ""
		m.statusMsg = fmt.Sprintf("submitted %d %s", msg.n, plural("comment", msg.n))
		return m, m.scheduleContextRefresh()
```

- [ ] **Step 7.5: Update help bar to surface the new keys in PR mode**

Find `renderHelp` in `internal/ui/model.go`. Append PR-mode entries:

```go
	if m.prMeta != nil {
		parts = append(parts, "C: comment", "S: submit", "t: thread", "B: body", "O: browser")
	}
```

(Place the append BEFORE the `strings.Join` that builds the final help line.)

- [ ] **Step 7.6: Run all tests, build, vet, fmt**

```
go test ./... -v 2>&1 | tail -40
go build ./...
go vet ./...
gofmt -l internal/ cmd
```
Expected: green.

- [ ] **Step 7.7: Manual smoke (optional)**

Pick a small public PR you have access to and run:

```
cd /path/to/that/repo
/tmp/gitreview pr https://github.com/owner/repo/pull/N
```

Inside the TUI:
- Press `4` → confirm PR tab renders title, body, comments, reviews.
- Press `1` to return to changes, navigate to a hunk, press `C`, type a comment in your editor, save and quit.
- Confirm the draft appears in `▸ Comments` as `[DRAFT] you: ...`.
- Press `t` on the Comments section to open the modal.
- Press `S`, confirm body in editor, save. Verify the comment posts (check the PR on GitHub).
- After submit, drafts should be cleared and the new comment should appear as a fetched comment.

- [ ] **Step 7.8: Final commit**

```bash
git add internal/pr/ internal/ui/ cmd/
git commit -m "pr+ui: submit drafts as one review (S), B for review body"
```

---

## Self-review

**Spec coverage:**
- Fetch all three comment kinds → Task 1.
- Bundle integration with non-fatal errors → Task 2.
- `▸ Comments` section in the context pane, filtered by anchor → Task 3.
- `C` to compose drafts via `$EDITOR` → Task 4.
- Thread modal (`t`, Esc, j/k, e, x) → Task 5.
- `[4 PR]` tab with title, body, issue comments, reviews, drafts summary → Task 6.
- `B` for review body → Task 6.
- `S` submit flow with template + `state=COMMENT` + error-keeps-drafts → Task 7.
- All keys gated to PR mode (`m.prMeta != nil`) → Tasks 4-7.
- Drafts stored in-memory, cleared on success → Task 4 + Task 7.
- Threading (`InReplyTo`) preserved in the field but not rendered hierarchically → Task 1 + spec (no UI code touches it).
- Auth scope failure surfaces via the GitHub error string → Task 7 (submitter returns error verbatim).

**Placeholder scan:** No TBD/TODO. Task 5 has some `_ = time.Time{}` / `_ = fmt.Sprintf` import-keepers in the helpers — those will be needed if the implementer ends up not using `time` or `fmt`. If the final implementation does use them naturally, drop the `_ =` lines.

**Type/name consistency:**
- `ctxpane.CommentRef` / `ctxpane.Draft` / `ctxpane.IssueCommentDisplay` / `ctxpane.ReviewDisplay` — used identically across Tasks 3, 4, 6.
- `pr.ReviewComment` / `pr.IssueComment` / `pr.Review` / `pr.SubmitDraft` — consistent across Tasks 1, 2, 7.
- `ui.PRBundle{Meta, ReviewComments, IssueComments, Reviews, Submitter}` grows across Tasks 4, 6, 7 — each task only adds fields, never renames.
- `composeDraft` / `editDraft` / `composeReviewBody` / `composeAndSubmit` — consistent.

**Known scope cuts (deferred per spec):**
- Replies (`in_reply_to`).
- Approve / request-changes review states.
- Authoring top-level PR-issue comments.
- Multi-line range comments.
- Resolving threads.
- Persistent drafts.
- Markdown rendering / suggestions.

**Deviation from spec (intentional, called out for follow-up):** The spec says "on submit success, re-fetch comments/reviews to refresh the pane." This plan clears drafts on success but does NOT re-fetch from GitHub — the just-posted comment won't appear in the TUI until the user re-launches `gitreview pr`. Adding the re-fetch is ~30 LOC (a new `Refetcher` closure on `PRBundle`, a new `refetchDoneMsg`, and the handler), and can be a small follow-up commit. Status display still confirms submit success.
