# Inbound-PR view — design

**Date:** 2026-05-15
**Status:** Approved (brainstorming)
**Scope:** v1 of inbound PR review in `gitreview` — single PR, read-only, worktree-backed

## Problem

`gitreview` v0 only reviewed local diffs (pre-flight). The project roadmap (recorded in project memory at session start) calls for inbound PR review using the same TUI shell, since both flows share the same primitives (`Diff`/`File`/`Hunk`/`Commit`). This spec covers the first slice: opening one PR by reference and reviewing its diff in the existing TUI, read-only.

## Approach: worktree-backed single-PR loader

A new package `internal/pr/` adds a loader that

1. parses a PR reference (number / URL / owner-repo shorthand),
2. authenticates to GitHub,
3. fetches PR metadata, files, and commits via go-github,
4. creates a per-session git worktree at `.gitreview/worktrees/pr-<N>/` checked out to the PR's head SHA,
5. returns a `Bundle` that the TUI consumes through its existing `ui.New(...)` signature with one new optional parameter.

The existing TUI machinery is entirely unchanged because the `Bundle.Diff` and `Bundle.Commits` are the same `internal/diff` shapes the pre-flight path produces. The worktree gives every context-pane section (`Where`/`Symbol`/`Blame`/`History`) a real `repoRoot` to operate against, so no code in `internal/ctxpane/` or `internal/ui/` needs to change.

```
$ gitreview pr 1234

┌─Files──────┬─Diff──────────────────────┬─Context────────┐
│ ▾ src     │ PR #1234 "add caching"   │ ▸ Where         │
│ │ a.go * │ by alice  · 3 commits   │   src/a.go      │
│ │ b.go   │ @@ -12,4 +12,8 @@        │ ▸ Symbol        │
│ ▾ docs    │  12  func Cache() {        │   func Cache()  │
│ │ R.md   │  13 + return memo[k]      │ ▸ Blame         │
└─────────┴────────────────────────┴────────────────┘
```

## CLI surface

A new subcommand `pr` accepts three forms:

```
gitreview pr 1234                                     # current-repo, by number
gitreview pr https://github.com/foo/bar/pull/1234     # by URL
gitreview pr foo/bar#1234                             # shorthand
```

For the URL and shorthand forms, the PR's `owner/repo` must match a remote in the current working directory's repo (see invariants below). The number-only form derives `owner/repo` from the current repo's first matching remote URL.

The existing pre-flight flags (`--working`, `--staged`, `--committed`, `--base`, `--width`) remain available when the `pr` subcommand is not used.

## Authentication

In order of preference:

1. **`gh auth token`** — shell out to the gh CLI to obtain a token. Zero-config for users already authenticated via gh.
2. **`$GITHUB_TOKEN`** environment variable.
3. If neither works, exit with a hint listing both sources.

The token is held only in memory for the lifetime of the loader call; we do not persist it.

## Strict repo-matching invariant

The PR review only operates when the current working directory's repo matches the PR's `owner/repo`. The check:

- Locate the main-repo root via `git rev-parse --show-toplevel`.
- Run `git remote -v` and parse each remote URL (supports `https://github.com/foo/bar(.git)?` and `git@github.com:foo/bar(.git)?`).
- Pass if any remote matches the PR's `owner/repo` (case-insensitive on the host, exact on owner/repo).
- Fail otherwise with an actionable message:
  `gitreview pr: PR foo/bar#1234 doesn't match any remote of this repo (X). cd to a clone of foo/bar first.`

No auto-clone. Reviewing a PR from a repo you don't have locally is out of scope for v1.

## Worktree lifecycle

**Location:** `<repo-root>/.gitreview/worktrees/pr-<N>/`

**Per-session lifecycle:**

- **Startup prune:** before opening any worktree, list `.gitreview/worktrees/*` and compare against `git worktree list --porcelain`. Any directory present on disk but not registered with git (or registered as `prunable`) is an orphan from a prior crash; remove it via `git worktree remove --force` (or `rm -rf` if git doesn't know about it).
- **Create:** if `<worktree-path>` doesn't exist after pruning, run `git fetch origin pull/<N>/head` then `git worktree add --detach <path> <fetched-sha>`. If it does exist, re-fetch the PR head and `git -C <path> reset --hard <new-sha>`.
- **Use:** the loader returns the worktree path; the TUI uses it as `repoRoot` for the entire session. Every existing local-file path (context pane's `readFileLines`, `git blame`, editor invocation, `repoRoot+"/"+f.Path` joins) sees the right files on disk at the right SHAs.
- **Teardown:** on graceful exit (`q`, normal `tea.Quit`), `main.go` calls `git worktree remove <path>` via a deferred cleanup after `p.Run()` returns. If the user `ctrl-c`s, Bubble Tea typically handles it as a quit-msg too; the deferred cleanup still fires. Hard crashes leave the worktree on disk; the next launch's startup-prune handles it.

**`.gitignore` policy:** `gitreview` does NOT write to the user's `.gitignore`. On first creation of `<repo-root>/.gitreview/`, the loader prints a one-line hint to stderr:

```
(hint: add .gitreview/ to your .gitignore — gitreview state lives here)
```

After the first launch the hint is silenced (detected via the presence of `.gitreview/`).

## Data model

```go
package pr

type Bundle struct {
    Diff         *diff.Diff
    Commits      []diff.Commit
    Meta         PRMeta
    WorktreePath string
}

type PRMeta struct {
    Number   int
    Owner    string
    Repo     string
    Title    string
    Body     string
    Author   string // login
    State    string // "open" | "closed" | "merged"
    HeadSHA  string
    BaseSHA  string
    HTMLURL  string
}
```

**`Bundle.Diff` construction:** the GitHub Files endpoint returns each file's `patch` field as a standard unified-diff fragment. We synthesize the missing `diff --git` headers and feed the concatenated text to `internal/diff.Parse`. The output is a `[]diff.File` indistinguishable from what `diff.Load` produces locally — no UI changes required.

**`Bundle.Commits` construction:** the GitHub Commits endpoint returns each commit's SHA, message, author, etc. We map directly to `diff.Commit`.

## UI integration

**`ui.New` gains one optional parameter:**

```go
func New(d *diff.Diff, commits []diff.Commit, repoRoot string, prMeta *PRMeta) Model
```

The pre-flight call site passes `nil`. The PR call site passes the metadata from `Bundle.Meta`.

**Top-header strip in PR mode:** the existing header currently looks like:

```
[1 changes] [2 commits] [3 overview]    52 files +1927 -65
```

In PR mode it becomes:

```
PR #1234 · alice · open   [1 files] [2 commits] [3 overview]   52 files +1927 -65   O: open in browser
```

(The first segment is added; the tab labels stay the same; the right side gains the `O:` hint.)

**New key — `O` (capital):**

| Key | Action |
|---|---|
| `O` | (PR mode only) Open `Meta.HTMLURL` in the user's browser via `open` (macOS) / `xdg-open` (Linux). Status hint on failure. No-op in pre-flight mode. |

Capital `O` is unused today. Lowercase `o` remains a synonym for `3` (overview) in both modes — no key collision.

**Everything else unchanged.** `j/k`, `]/[`, `m/M`, `e`, `/`, `s`, `v`, `c`, `H`, `tab`, `g/G`, `1/2/3`, `q` — all operate as today. Reviewed marks are session-scoped (per-PR, since the session is per-PR).

**`e` (editor) caveat:** opens the file inside the worktree. Any edits made there are lost when the worktree is removed at exit. A doc-comment in `openInEditor` notes this. Users in practice rarely edit during review; if they do, they're aware they're in a temporary workspace.

## Failure modes

| Failure | Behavior |
|---|---|
| Auth token unavailable | Exit; print hint listing both sources. |
| Network error fetching PR | Exit; print the underlying error. Any partial worktree state is cleaned by next launch. |
| PR doesn't exist (404) | Exit; "PR foo/bar#1234 not found". |
| Repo mismatch | Exit; "you're in `<current>` but PR `<owner/repo>#<N>` is elsewhere; cd to a clone first". |
| Worktree creation fails | Exit; print git's error message. |
| Crash mid-session | Worktree stays on disk; next launch prunes it. |
| Graceful exit | `git worktree remove <path>` runs in `main.go`'s deferred cleanup. |

The pane invariant from earlier work — *the TUI must never crash from data errors* — extends here: any GitHub-API or git-shell error is converted to a top-level exit before the TUI starts. Once the TUI is running, the existing context-pane error handling covers per-section failures (e.g., a blame call against a SHA that's no longer in the worktree).

## File layout

New package `internal/pr/`:

```
internal/pr/
  parse.go      Ref parsing: number / URL / owner-repo#N
  auth.go       Token resolution (gh CLI → $GITHUB_TOKEN)
  remote.go     Strict repo-match check (parse `git remote -v`)
  worktree.go   Per-session worktree create/prune/teardown
  github.go     go-github client, PR metadata/files/commits fetchers
  bundle.go     Bundle / PRMeta types; the top-level Load(ctx, ref) entry
  parse_test.go, auth_test.go, remote_test.go, worktree_test.go, github_test.go
```

Modified files:

```
cmd/gitreview/main.go    Add `pr` subcommand dispatch; deferred worktree teardown.
internal/ui/model.go     ui.New gains an optional *pr.PRMeta param; new `O`
                         handler; PR-aware top-header strip.
internal/ui/model_test.go  Test for PR-mode header rendering.
```

**Package boundary and the `PRMeta` import direction:** `internal/pr/` imports `internal/diff/` (to reuse `Parse`) and does NOT import `internal/ui/` — the loader is testable without a TUI. `internal/ui/` imports `internal/pr/` for the `PRMeta` struct only. This is acceptable because `internal/pr/` has no UI dependencies, so the import is one-way and creates no cycle. The other option (a tiny shared `internal/prmeta/` package) is rejected as over-decomposition for one small struct.

## Testing strategy

- **`pr/parse_test.go`:** table-driven over ref parsing. Cases: `"1234"` (current repo), `"foo/bar#89"`, `"https://github.com/foo/bar/pull/89"`, invalid forms.
- **`pr/auth_test.go`:** stub `gh auth token` via `PATH` override or env mock; test fallback to `GITHUB_TOKEN`; test final-failure message.
- **`pr/remote_test.go`:** in a `t.TempDir()` git init, add fake remotes (HTTPS and SSH forms), assert match logic.
- **`pr/worktree_test.go`:** real `git init` + commit fixture; test create, re-fetch reuse, prune (orphaned directory not in `worktree list`), teardown.
- **`pr/github_test.go`:** mock the go-github HTTP transport via `httptest.NewServer`; assert that the loader correctly transforms PR file patches into `diff.File`s identical to what `internal/diff.Parse` produces on the same input.
- **`ui/model_test.go`:** add a test that `New(..., prMeta != nil)` renders the PR strip in the top header.

## Out of scope for v1

- Posting review comments back to GitHub (`gh pr review` API).
- Authoring review comments locally (REVIEW.md export).
- Multi-PR list view / inbox of PRs awaiting review.
- Existing PR comments / review threads displayed in the TUI.
- Auto-clone if not in the repo.
- GitHub Enterprise endpoints / multi-account auth.
- Prefetch / background refresh / push notifications when the PR updates.

## Extension points

- **Multi-PR list view (next slice):** add a fourth tab; selecting a row calls the same `pr.Load(ctx, ref)` already implemented here. No changes to this slice's surface required.
- **Review comment authoring/posting:** add a new `internal/review/` package that emits `gh pr review` API calls. UI gains a `Comments` pane and per-line comment authoring. No changes to `pr.Load`.
- **GitHub Enterprise / alternate hosts:** add a config layer above `pr.auth` and `pr.github`. The current loader's API surface is host-agnostic except for its hardcoded github.com base URL.
