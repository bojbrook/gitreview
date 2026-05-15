# PR comments (view + author + post) — design

**Date:** 2026-05-15
**Status:** Approved (brainstorming)
**Scope:** v1 of PR review comments in `gitreview` — view existing comments, author inline drafts locally, submit one review back to GitHub.

## Problem

The current `gitreview pr` view is read-only and ignores the review conversation entirely: existing comments, threads, and reviews are invisible, and there's no way to leave a comment without leaving the TUI. The reviewer can read the diff but can't see what other reviewers said or add their own feedback. This spec unlocks the deferred "comments" surface from `2026-05-15-pr-view-design.md`.

## Approach

Three coordinated additions, all on top of the existing PR loader:

1. **Fetch** all three GitHub comment kinds — inline review comments, top-level issue comments, and reviews — and bundle them into the `pr.Bundle` already returned by `pr.Load`.
2. **Display** them in two surfaces: inline review comments slot into a new `▸ Comments` section in the context pane (one short row per comment, full thread on demand via a modal), and PR-level info (title, body, issue comments, reviews) lives in a new `[4 PR]` tab.
3. **Author and post** new inline comments. The user composes in `$EDITOR`, drafts queue in memory, and a single `S` press POSTs them as one review with `state=COMMENT` via GitHub's CreateReview API.

The existing TUI machinery is untouched in spirit; new section/tab/modal types are additive, and the worktree-backed `repoRoot` from the previous slice keeps all file-anchored operations working unchanged.

## CLI surface

No new flags or subcommands. `gitreview pr <ref>` from the existing slice gains the comment surfaces and the `C` / `S` / `t` / `B` keys whenever `prMeta != nil`.

## Data model

```go
package pr

type ReviewComment struct {
    ID        int64
    User      string
    Path      string    // file path the comment is anchored to
    Line      int       // line number (NEW-side for added/context; OLD-side for removed)
    Side      string    // "RIGHT" (NEW) or "LEFT" (OLD)
    Body      string
    CreatedAt time.Time
    InReplyTo int64     // 0 for top-level
}

type IssueComment struct {
    ID        int64
    User      string
    Body      string
    CreatedAt time.Time
}

type Review struct {
    ID          int64
    User        string
    Body        string  // may be empty
    State       string  // "APPROVED" | "CHANGES_REQUESTED" | "COMMENTED"
    SubmittedAt time.Time
}

// Bundle gains three new optional slices.
type Bundle struct {
    // ... existing fields ...
    ReviewComments []ReviewComment
    IssueComments  []IssueComment
    Reviews        []Review
}
```

Threading (`InReplyTo`) is preserved in the field for future use but **not** rendered hierarchically in v1 — comments display flat in `CreatedAt` order.

`Side` is the raw GitHub string (`"RIGHT"`/`"LEFT"`) — no enum, to keep wire-format mapping trivial.

## Fetchers

Three new functions in `internal/pr/comments.go`, paginated identically to the existing `fetchFiles` / `fetchCommits`:

```go
func fetchReviewComments(ctx, c, owner, repo string, num int) ([]ReviewComment, error)  // GET /repos/.../pulls/N/comments
func fetchIssueComments(ctx, c, owner, repo string, num int)  ([]IssueComment, error)   // GET /repos/.../issues/N/comments
func fetchReviews(ctx, c, owner, repo string, num int)        ([]Review, error)         // GET /repos/.../pulls/N/reviews
```

`Load` calls all three concurrently with the existing PR/files/commits fetches. Failures are **non-fatal**: an errored slice is left empty and the affected display section renders `(error)`; the TUI still opens.

## Display

### A. Inline review comments → `▸ Comments` section in the context pane

A fifth section kind appended to the existing iota: `SectionComments`. Display order: Where, Symbol, Cross-file, Blame, History, **Comments**.

Filter rule: section renders comments whose `(Path, Line, Side)` matches the cursor's current file + anchor line + side (anchor line via the existing `ctxpane.Cursor.AnchorLine()`).

Format per row: `<author> <relative-time>: <first ~50 chars of body>…`. Drafts (composed locally, not yet submitted) interleave with fetched comments and are marked `[DRAFT] you: …`.

```
▸ Comments (3)
  alice 2h:
  "can we add…
  bob 1h:
  "yeah +1"
  [DRAFT] you:
  "added in n…

  t: thread
```

### B. PR thread modal — opened with `t`

When the cursor is in the Comments section and the user presses `t`, a centered overlay shows the full thread for the current line, wrapped to `~70%` of the terminal width:

```
                ┌─ Thread: internal/pr/bundle.go:42 ──────────────┐
                │                                                  │
                │ alice  2h ago                                    │
                │ ─────────────────                                │
                │ Can we add a context timeout to this call?       │
                │ The default is 30s but GitHub can hang up to     │
                │ 2 minutes on rare paths.                         │
                │                                                  │
                │ bob  1h ago                                      │
                │ ─────────────────                                │
                │ Yeah +1, also handle 429 retry-after explicitly. │
                │                                                  │
                │ [DRAFT] you                                      │
                │ ─────────────────                                │
                │ Added in next push.                              │
                │                                                  │
                │  Esc close · e edit draft · x delete draft       │
                └──────────────────────────────────────────────────┘
```

- `Esc` closes.
- `j` / `k` scroll within the modal when the thread is taller than the modal.
- `e` re-edits the focused draft (re-spawns `$EDITOR` with the current body).
- `x` deletes the focused draft (drafts only — fetched comments aren't deletable).

The modal lives in `internal/ui/threadmodal.go`. Rendering uses `lipgloss.Place` for centering and the existing `truncateAnsi` for safety.

### C. PR-level info → new `[4 PR]` tab

The tab strip gains a fourth entry only when `prMeta != nil`. The tab renders PR title, body, issue comments, reviews, and a pending-drafts summary:

```
PR #1234 — alice — open
https://github.com/foo/bar/pull/1234

  Add caching to lookups

  Speeds up the repeat-lookup path in src/cache.go.
  Tested with the existing benchmark harness — 4x speedup.

──── Issue comments (2) ───────────────────────────
  bob   2d ago
  Looks good overall, one nit below.

  carol 1d ago
  Approving from my side, thanks!

──── Reviews (1) ──────────────────────────────────
  carol  APPROVED  1d ago
  > LGTM

──── Pending review ──────────────────────────────
  2 draft inline comments
  Press S to submit · Press B to add review body
```

The PR tab body uses a viewport-backed scrollable text area. `[4 PR]` activates via the `4` key or via clicking the tab.

## Authoring drafts

### `C` — compose a new inline comment

In the Changes view, when `currentFileRow()` returns `ok=true` AND `ctxpane.Cursor.AnchorLine()` resolves to a line:

1. Spawn `$EDITOR` on an empty temp file via `tea.ExecProcess`.
2. On editor exit, read the file. Strip lines starting with `#` and trim whitespace.
3. If the body is non-empty, append a `Draft` to `m.drafts`. Otherwise no-op (empty drafts aren't kept).
4. Refresh the context pane so the new draft appears immediately in `▸ Comments`.

Anchor rule: `(Path, Line, Side)` comes from the current file + `AnchorLine()`'s `(lineNum, kind)`. Side is `"LEFT"` when kind is `LineRemoved`, else `"RIGHT"`.

If `C` fires without a valid cursor location (cursor on a dir row, or no current hunk), set status: `C: place cursor on a diff line first`. No state change.

### Draft storage

```go
// In internal/ctxpane/ (lives alongside the section that renders them).
type Draft struct {
    Path string
    Line int
    Side string // "RIGHT" | "LEFT"
    Body string
}

// In internal/ui/Model:
drafts []ctxpane.Draft
```

In-memory only. Cleared on successful submit. Lost on quit/crash.

## Submit flow — `S`

Fires only in PR mode AND when `len(m.drafts) > 0`. Otherwise status hint: `S: no drafts to submit`.

1. Spawn `$EDITOR` with a templated buffer:

   ```
   # Review body (optional — leave empty for no overall comment).
   # Lines starting with # are stripped.
   #
   # 2 inline drafts:
   #   internal/pr/bundle.go:42  "Added in next push."
   #   internal/ui/model.go:118  "Should this be exported?"
   ```

2. On editor exit: strip `#` lines + trim. The result is the review body (may be empty).
3. Call a new `pr.Submit(ctx, client, owner, repo, num, body, drafts) error` that maps drafts → `[]*github.DraftReviewComment` and POSTs:
   ```go
   client.PullRequests.CreateReview(ctx, owner, repo, num, &github.PullRequestReviewRequest{
       Body:     &body,
       Event:    github.String("COMMENT"),  // v1: comment-only
       Comments: mappedDrafts,
   })
   ```
4. **Success:** clear `m.drafts`, re-fetch `ReviewComments` + `IssueComments` + `Reviews`, status: `submitted N comments`.
5. **Failure:** drafts kept intact, status: `submit failed: <error>`. User can retry with `S`.

### Auth scope on submit

Posting requires `repo` scope on the token (which most personal access tokens already have, and `gh auth login` defaults to). We don't precheck scopes — GitHub returns 403 with a body that mentions the missing scope; we surface that error string in the status bar so the user knows what's wrong.

## Keys added in PR mode

| Key | Action |
|---|---|
| `C` | Compose a new inline draft on the current diff line. |
| `S` | Submit all drafts as one review (state=COMMENT) after composing review body. |
| `t` | (in context pane, on Comments item) Open the thread modal. |
| `B` | (in `[4 PR]` tab) Compose / edit the review body separately. Same `$EDITOR` flow as `S`. |
| `4` | Switch to the PR-info tab. |

Existing keys (`j`/`k`, `]`/`[`, `m`/`M`, `e`, `/`, `s`, `v`, `c`, `H`, `O`, `tab`, `g`/`G`, `1`/`2`/`3`, `q`) are unchanged.

## File layout

**New files:**

```
internal/pr/
  comments.go              ReviewComment, IssueComment, Review types + 3 fetchers
  comments_test.go
  submit.go                Submit() — map drafts to API, POST one review
  submit_test.go

internal/ctxpane/
  comments.go              SectionComments + buildCommentsSection + Draft type
  comments_test.go

internal/ui/
  prtab.go                 renderPRTab()
  prtab_test.go
  threadmodal.go           Thread modal overlay + key handlers
  threadmodal_test.go
```

**Modified files:**

```
internal/pr/bundle.go          Add ReviewComments / IssueComments / Reviews to Bundle;
                               Load populates them concurrently with non-fatal errors.
internal/ctxpane/types.go      Append SectionComments to the SectionKind iota.
internal/ctxpane/resolver.go   Add buildCommentsSection to the tasks slice (display order
                               position 5); kindFor maps index 5 → SectionComments.
internal/ui/model.go           New fields: drafts, modalOpen, modalThread, viewPR; new
                               key handlers C / S / t / B / 4; pass drafts into context
                               pane cursor.
internal/ui/render.go          Render the [4 PR] tab + modal overlay when their flags
                               are set.
internal/ui/model_test.go      Tests for new key flows; modal open/close; submit happy
                               path (mocked) and error path.
cmd/gitreview/main.go          Wire comments from the bundle into ui.New (extend signature
                               or pass via a small input struct — implementation detail).
```

## Failure modes

| Failure | Behavior |
|---|---|
| Fetch any comment kind fails | Section renders `(error)`; rest of TUI works. |
| Compose called with no anchor line | Status hint; no state change. |
| `$EDITOR` exits non-zero on compose | Discard buffer; status: `compose cancelled`. |
| Submit called with empty drafts | Status hint; no API call. |
| Submit POST fails (network / 4xx / 5xx) | Drafts intact; status surfaces the error string. User retries. |
| 403 (missing repo scope) | Same as above; the GitHub error mentions the scope, which appears in status. |

## Out of scope for v1

- Replying to a specific existing comment (`in_reply_to` API call). All drafts are top-level.
- Approve / request-changes review states. v1 always submits `state=COMMENT`.
- Authoring top-level PR-issue comments (not the review body). Display-only.
- Multi-line range comments (`start_line` / `end_line`). Single-line only.
- Resolving / unresolving threads.
- Draft persistence across sessions (in-memory only).
- Markdown rendering / preview of comment bodies — plain-text wrapping only.
- Posting GitHub suggestions (the ```` ```suggestion ```` block syntax).

## Extension points

- **Approve / request-changes** — add a state parameter to `Submit` and a state-picker step before the review-body editor.
- **Reply to existing comment** — add optional `InReplyTo int64` on `Draft`, pass through to `github.DraftReviewComment.InReplyTo`.
- **Persistent drafts** — add a load/save layer around `m.drafts` keyed by `(owner, repo, num)`. The `Draft` type stays the same.
- **Top-level issue-comment authoring** — a new key (e.g. `I`) that opens `$EDITOR`, then POSTs directly to `/issues/N/comments`. Doesn't touch the review-comment flow.
