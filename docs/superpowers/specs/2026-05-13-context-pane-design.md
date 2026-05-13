# Context pane — design

**Date:** 2026-05-13
**Status:** Approved (brainstorming)
**Scope:** v0 of `gitreview` (pre-flight review)

## Problem

GitHub-style code-review UIs strip the reviewer of the surrounding context they need to actually understand a change:

- **Code shape** — you see ten changed lines but not the function they live in, the types involved, or where the file fits.
- **Cross-file relationships** — a change in file A often implies a change in file B; the diff shows them separately and the link is left to the reviewer to reconstruct.
- **Symbol usage and callers** — a function changes; who calls it? Was every caller updated? The web UI is silent.
- **History / why-was-this** — what did this look like before, and *why* did the prior author write it that way? Blame and prior log are several clicks away.

All four matter to the user. The goal of this feature is to surface that context continuously as the reviewer reads, without forcing them out of the diff.

## Approach: continuous, cursor-driven context pane

A third column to the right of the diff viewport — the **context pane** — fills as the diff cursor moves. The pane is composed of named **sections**; which sections are populated and what they contain depends on what the cursor is on. The section *headers and order* are stable, so the pane never reshuffles under the user.

```
┌─Files──────┬─Diff──────────────────────┬─Context────────┐
│ render.go *│ ╭─ render.go ─────────────│ ▸ Where        │
│ styles.go  │ │ @@ -380,7 +395,11 @@    │   render.go    │
│ highlight  │ │       for _, l := range │   func          │
│            │ │  428 ▎+ b.WriteString... │     renderLine │
│            │ │       }                  │                │
│            │ │  430 ▎- gutter+styled    │ ▸ Symbol       │
│            │ │  431 ▎+ accent+gutter... │   renderLine   │
│            │ │       }                  │   3 refs in    │
│            │ │                          │   render.go    │
│            │ │                          │                │
│            │ │                          │ ▸ Blame        │
│            │ │                          │   dbe587b 2d   │
│            │ │                          │   "reviewed    │
│            │ │                          │    marks…"     │
└────────────┴──────────────────────────┴────────────────┘
```

### Layout and visibility

- Pane width: **32 columns** when total terminal width is ≥ 120 cols; **hidden** otherwise.
- Default visibility: **on**.
- Toggle key: `c` (mnemonic *context*). Toggle state lives in the model — not persisted across runs.
- Split-view mode (`s`) implicitly hides the pane (split + context = too cramped). The user can re-toggle with `c` if they explicitly want it.

### The section catalog (v0)

Five sections. Each rendered only when relevant. Order is fixed.

| Section | When it appears | What it shows |
|---|---|---|
| **▸ Where** | Always | File name; containing function (heuristic backward-search for `func`/`def`/`class`/`function`); hunk N of M |
| **▸ Symbol** | Cursor is on a declaration line (matched by the containing-function regex) | Symbol name; reference count in repo; top 3–5 ref locations with line numbers (jumpable) |
| **▸ Cross-file** | The symbol from ▸ Symbol also appears in another file in this diff | Other changed files mentioning it, with line numbers. The "did I miss updating a caller?" section. |
| **▸ Blame** | Cursor is on a removed/context line (i.e. a line that existed before this change) | Output of `git blame -L line,line`: commit short SHA, age, subject. One-liner. |
| **▸ History** | Cursor is on a hunk header, OR user presses `H` to expand | Recent commits to this file (`git log --oneline -n 5 -- file`); on `H` toggle, expands to commits that touched the specific line range (`git log -L`). |

Rules of thumb:

- **▸ Where is always present.** Even if everything else is empty, the user knows where they are.
- Sections never re-order; they only appear or disappear.
- An empty pane is impossible — at worst, only ▸ Where renders.
- Symbol detection in v0 is restricted to declaration lines. Arbitrary identifiers under cursor are out of scope; can be added later behind a new key.

## Architecture

### Package boundary

A new package `internal/context/` is introduced, with strict separation from `internal/ui/`. The UI pane *renders* sections; `internal/context/` *computes* them. This mirrors the existing `internal/diff/` ↔ `internal/ui/` split.

```
internal/context/
  types.go      Section, Item, Payload, Cursor structs
  resolver.go   Resolve(cursor Cursor, d *diff.Diff) (Payload, error)
  where.go      "Where" section: containing-func heuristic, file/hunk position
  symbol.go     "Symbol" section: decl detection, git-grep refs
  crossfile.go  "Cross-file" section: searches other diff files for symbol
  blame.go      "Blame" section: shells out to `git blame -L`
  history.go    "History" section: shells out to `git log` / `git log -L`
  cache.go      Concurrency-safe LRU keyed by (file, line, sectionKind)
```

### Types

- `Cursor` — what a section needs to decide what to compute: file path, the `diff.Line` under the cursor (with `LineKind`), and the current hunk index within the file. The resolver reads the file's working-tree content directly from disk when needed (e.g. for the containing-function backward-search above the visible hunk).
- `Section` — `{Title string, Items []Item, Status payloadStatus}` where `Status` is `ok | empty | loading | error`.
- `Item` — display string plus optional jump target `(file, line)`.
- `Payload` — `[]Section`, in fixed order. The UI doesn't know how items were computed, only how to render them.

### Data source policy

- **Language-agnostic, heuristics + git only.** No LSP, no gopls, no language-specific parsers.
- Reference search uses **`git grep`** (always available wherever `gitreview` runs; respects `.gitignore`; no extra binary dependency). Ripgrep is not used in v0.
- Containing-function detection is a backward-search regex over the file content. Supported delimiters in v0: `func `, `def `, `class `, `function `, `fn `. Coarse on purpose.
- Blame / history shell out to `git`.

This policy trades accuracy (especially on overloaded identifiers) for portability and simplicity. The package boundary is set up so a future "Go-aware" or "LSP-aware" implementation can be slotted in without disturbing the UI.

### Data flow on cursor move

```
key (j/k/n) → ui.Model.Update
                │
                ▼
          (debounce 150ms via tea.Tick)
                │
                ▼
       context.Resolve(cursor, diff)
                │
                ▼
   per-section goroutines (blame, refs, …)
   results merged into Payload
                │
                ▼
     tea.Cmd returns contextMsg{Payload}
                │
                ▼
    ui.Model stores payload, re-renders pane
```

- Sections run **concurrently** inside `Resolve`. Total wall-time = slowest section.
- Per-section hard timeout: **300 ms**. On timeout, that section renders as `…` and the rest of the pane still draws.
- Bubble Tea integration: **one `tea.Cmd`** that fans out internally and returns the merged `Payload` as a single message. Rationale: simpler UI state, and 300 ms total budget isn't long enough to benefit from progressive rendering.
- Debounce: 150 ms before any fetch fires, so rapid j/k spam doesn't storm git.

### Caching

- `git blame -L file:N,N` → cached by `(file, line, HEAD sha)`. Invalidated only on HEAD change (rare during a review session).
- `git grep` ref results → cached by `(symbol, repo-root)` for the session. Bounded LRU (~256 entries).
- Containing-function backward-search → cached by `(file, line)` per session.

### Error handling

- Any shell-out error sets the offending section's `Status = error`, which renders as `(error)` in muted text.
- Errors never propagate up; the rest of the pane always renders.
- The pane is a best-effort overlay and **must never crash the TUI**.

## UI integration

### Model changes (`internal/ui/model.go`)

Add fields:
- `contextPaneVisible bool`
- `contextFocus bool`
- `contextPayload context.Payload`
- `contextCursor context.Cursor`

Add message types:
- `contextRefreshMsg` (fired by `tea.Tick` after the 150 ms debounce window)
- `contextMsg{Payload}` (delivered when `Resolve` returns)

`Update` flow: any diff-cursor-moving key cancels any in-flight refresh tick and schedules a fresh one.

### Render changes (`internal/ui/render.go`)

Extract the current "files | diff" width allocation into a small layout helper. Add a third column when `contextPaneVisible && totalWidth ≥ 120`. Width contract:
- ≥ 120 cols total → pane is 32 cols.
- < 120 cols total → pane is 0 (hidden), regardless of toggle state.

### Style changes (`internal/ui/styles.go`)

Add:
- `contextPaneStyle` — border / padding for the pane column
- `contextSectionHeaderStyle` — for "▸ Where", "▸ Symbol", etc.
- `contextItemStyle` — body text
- `contextMutedStyle` — for `…`, `(error)`, age strings

Palette consistent with existing muted/accent styles.

### Keybindings

| Key | Action |
|---|---|
| `c` | Toggle context-pane visibility |
| `Tab` / `Ctrl-l` | Cycle focus diff → pane → file list → diff |
| `H` | (pane focused) Expand ▸ History to line-range mode |
| `Enter` | (pane focused, item selected) Jump diff cursor to the item's location |
| `j` / `k` | (pane focused) Move item selection up/down |
| `Esc` | Return focus to diff |

Reuses existing keys (`j/k`, `Enter`) for consistency with files/commits list behavior.

## Testing strategy

- `internal/context/` unit tests: table-driven, against a fixture repo created in `t.TempDir()` via `git init` + seed commits (same pattern `internal/diff/` already uses).
- Heuristic backward-search: pure-function tests against fixture strings; no I/O.
- `git grep` calls: assert command construction; integration test against a tiny fixture tree.
- UI pane rendering: snapshot-style tests against canned `Payload` values. No shell-out needed.

## Implementation order

Each step is a discrete commit. Steps 1–3 are pure UI scaffolding; the feature is not really alive until step 4.

1. `internal/context/` skeleton: `types.go` + `resolver.go` returning a hand-built `Payload`.
2. UI: third column rendering against hand-built payload. Width logic. Toggle key. Look right end-to-end with static data.
3. Wire cursor-change → debounced `Resolve` call. Still hand-built data; verify the message flow.
4. `where.go` — pure-function, no I/O. Quickest win.
5. `blame.go` — first shell-out; introduces caching layer.
6. `symbol.go` — declaration detection + `git grep`.
7. `crossfile.go` — reuses symbol.go's grep, scoped to other files in the diff.
8. `history.go` + `H` toggle behavior.
9. Pane focus + keyboard navigation within the pane.

## Out of scope for v0

- LSP / gopls / language-server integration.
- Symbol detection on arbitrary identifier under cursor (only on declaration lines).
- Background prefetching of context for hunks not yet visited.
- Configurable section ordering or section visibility (all five sections always on by default).
- Persistent toggle state across runs.
- GH PR comments, Claude-drafted comments, linked-ticket sections — these are the natural *next* extensions (each is one new section file), but not v0.
- Per-section streaming / progressive rendering (single fan-out is enough at 300 ms budget).

## Extension points

The section model is the explicit extension axis. New section types (GH comments, Claude drafts, linked tickets, "tests covering this code") are added as:
- A new file under `internal/context/`
- Returning a `Section` from `Resolve`'s fan-out
- Optionally a new `Title` constant and matching `contextSectionHeaderStyle` palette entry

No changes to `Payload`, UI, or layout are required. This invariant — *new context types plug in without disturbing layout* — should be preserved as new sections are added beyond v0.
