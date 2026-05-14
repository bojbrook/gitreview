# Collapsible file explorer — design

**Date:** 2026-05-14
**Status:** Approved (brainstorming)
**Scope:** v1 of the collapsible left-pane file list in `gitreview`

## Problem

The Changes view's left pane shows a flat list of files. On any non-trivial diff (10+ files touching multiple top-level packages), the user must scan a long flat list and reconstruct the directory structure mentally from path strings. Long paths are truncated by `compactPath`, which loses the very prefix the user uses to orient — "is this the `internal/ui` change or the `internal/ctxpane` change?"

## Goal

Replace the flat list with a **compact-folders** directory tree that the user can expand and collapse. Preserve all existing file-list behaviors (reviewed marks, filtering, sparkline, stats, hunk jump) and existing keyboard ergonomics; add tree-aware navigation without breaking muscle memory.

## Approach: compact-folders tree with mixed-row cursor

The flat `[]diff.File` is built into a tree of **dir** and **file** nodes. Runs of single-child directories are merged into a single row (VSCode "compact folders" mode): `internal/ui/styles.go` parents into a single `▾ internal/ui` row, not a chain of three.

The cursor moves through both dir and file rows. Enter/`l` toggle expansion or descend; `h` collapses or jumps to the parent. File-only review actions (`m`/`M`/`e`) skip over dir rows.

```
▾ internal/ctxpane            ← dir node, 9 visible children
│   blame.go                  ← file node
│   cache.go
│   …
▾ internal/ui                 ← another top-level dir
│   model.go
│   render.go
│   styles.go
▾ docs/superpowers/specs      ← single-child chain compacted to one row
│   2026-05-13-context-pane-design.md
```

### Tree shape rules

- **Compact folders.** A directory with exactly one child that is itself a directory is merged with that child into a single row whose label is their joined path (`docs/superpowers/specs`). The merge is recursive: chains of any length collapse.
- **Leaf files always shown.** A directory containing one file does *not* merge with the file; the file is always a separate row.
- **All directories expanded by default.** The user is reviewing files, so they should be visible immediately. Expansion state is session-scoped (not persisted).

### Rendering rules

- Dir rows: `▾ <compact-path>` (expanded) or `▸ <compact-path>` (collapsed).
- File rows: 2-column indent + `│ ` guide + status marker (`M`/`A`/`D`/`R` or `✓` for reviewed) + filename only.
- Sparkline + diff stats: file rows only (preserves existing right-aligned layout).
- Reviewed aggregate on dir rows: blank if no files reviewed; `✓ N/M` if partial; `✓` if all `M` files in the subtree are reviewed.
- The current-row cursor highlight uses the existing `cursorStyle` (cyan bg).

## Architecture

### File layout

```
internal/ui/
  filetree.go       NEW — tree shaping, compaction, filter-aware rebuild
  filetree_test.go  NEW — pure-function tests
  model.go          MODIFIED — rowCursor migration, key handlers, tree state
  model_test.go     MODIFIED — tree navigation, expand/collapse tests
  render.go         MODIFIED — renderFilesList rewritten over treeRows
```

The tree logic is isolated in `filetree.go` so it can be unit-tested without the TUI.

### Types

```go
// In filetree.go.
type rowKind int

const (
    rowDir rowKind = iota
    rowFile
)

type treeRow struct {
    Kind     rowKind
    Path     string   // dir: compact-path key (e.g. "internal/ui"); file: full File.Path
    Label    string   // what to render (dir compact-path or basename)
    Depth    int      // indent level (0 = top-level)
    FileIdx  int      // for rowFile: index into the effectiveFiles slice (-1 for dirs)
    Reviewed int      // for rowDir: number of reviewed files in subtree (0 for files)
    Total    int      // for rowDir: total files in subtree (1 for files)
}

// BuildTree returns the flat visible-row list for the given inputs.
// expanded[dirPath] is true when the dir is open. filter narrows visible files;
// when non-empty, every dir on the path to a match is force-expanded.
func BuildTree(
    files []diff.File,
    reviewed map[string]bool,
    expanded map[string]bool,
    filter string,
) []treeRow
```

### Model state

```go
// In model.go.
rowCursor      int               // renamed from fileCursor; index into treeRows
treeExpanded   map[string]bool   // compact-path → expanded
treeRows       []treeRow         // visible rows, rebuilt on every refreshDiff
preFilterTree  map[string]bool   // snapshot on filter start, restored on clear
```

`treeRows` is rebuilt whenever the tree's inputs change: file set, filter, reviewed set, expansion state. Rebuild is cheap (linear in file count) and happens inside `refreshDiff` and the filter handlers.

### Cursor helpers

```go
// rowAtCursor returns the row at rowCursor, or a zero-value row if out of range.
func (m Model) rowAtCursor() treeRow

// currentFileRow returns the underlying diff.File the cursor is on, or
// (zero, -1, false) when the cursor is on a dir row.
func (m Model) currentFileRow() (diff.File, int, bool)
```

All existing `m.fileCursor` callers are migrated. Sites that need a file (`renderDiffPane`, `selectedEditTarget`, `toggleReviewed`, context-pane cursor, spine column) use `currentFileRow`; sites that just need to bound-check or move the cursor (`maxCursor`, `moveCursor`) use the row count.

### Key bindings

| Key | Action |
|---|---|
| `j` / `k` / `down` / `up` | Move through visible rows (dirs + files). |
| `g` / `G` | First / last row. |
| `l` / `right` | On collapsed dir: expand. On expanded dir: move cursor to its first child. On file: no-op. |
| `h` / `left` | On expanded dir: collapse. On file or collapsed dir: jump to parent dir. If already at top level, no-op. |
| `Enter` | On dir: toggle expand/collapse. On file: no-op. |
| `]` / `[` | Hunk jump within current file. No-op on dir rows. |
| `m` | Toggle reviewed for the current file. No-op on dir rows; status hint: `m: select a file to mark`. |
| `M` | Next-unreviewed file (file rows only). |
| `e` | Open current file in editor. No-op on dir rows; status hint: `e: select a file to open`. |
| `/` | Start filter (existing behavior; tree auto-expands matches). |

### Filter behavior

When a filter is active:

- `BuildTree(filter=…)` returns only rows whose subtree contains at least one matching file.
- Every dir on the path to a matching file is force-expanded (overriding `treeExpanded`).
- `treeExpanded` itself is **not** mutated by filtering; on clear-filter the user's prior expansion state is restored from `preFilterTree`.
- Status row at the top continues to say `N/M files · /pattern`.

Side effect: when filter is committed, `rowCursor` is reset to the first file row.

The existing `cursorPreFilter int` field is replaced with `pathPreFilter string` (the file path under the cursor when filtering began). On `clearFilter`, the tree is rebuilt and `rowCursor` is moved to the row matching `pathPreFilter`; if that file is no longer visible, the cursor falls back to row 0. Storing a row index across filter/expansion transitions is unsafe because row positions change.

### Reviewed aggregation

For each dir row, `BuildTree` populates `Reviewed`/`Total` by walking the subtree. Renders as:

- `Reviewed == Total > 0` → green `✓`.
- `0 < Reviewed < Total` → muted `✓ N/M`.
- `Reviewed == 0` → blank.

Dir rows themselves are not markable: toggling reviewed only operates on file rows.

### Behavior touchpoints

| Feature | What changes |
|---|---|
| Cursor bounds | `maxCursor()` returns `len(treeRows) - 1` instead of `len(effectiveFiles) - 1`. |
| `]` / `[` hunk jump | No-op on dir rows; existing logic unchanged on file rows. |
| Context-pane cursor | `currentFileForContext()` returns the file via `currentFileRow`; zero `diff.File{}` when on a dir (already handled gracefully). |
| Sparkline + stats | Rendered only for file rows. |
| Spine column | Blank when cursor is on a dir row. |
| Overview view (3) | Unchanged — flat grid, no tree. |
| Commits view (2) | Unchanged — own list. |

## Out of scope for v1

- `zR` / `zM` global expand-all / collapse-all.
- Persistent expansion state across runs.
- Bulk "mark whole dir reviewed" from a dir row.
- Custom sort orders (alpha / by-change-size / by-status).
- Drag-to-resize the pane.

## Testing strategy

- `filetree_test.go`: pure-function tests against fixture file lists. Cases:
  - Compact-folders merging (chains of single-child dirs collapse).
  - Leaf files never merge with their parent.
  - Filter narrows + auto-expands.
  - Reviewed aggregation counts correctly under partial review.
  - Collapsed dir hides its descendants from `treeRows`.
- `model_test.go`: navigation tests.
  - j/k traverses dir and file rows in correct order.
  - Enter on dir toggles; cursor stays put.
  - `l` on collapsed dir expands; `l` on expanded dir descends.
  - `h` on file jumps to parent; on top-level file is no-op.
  - `m` on dir is a no-op; `m` on file unchanged.
  - Filter clear restores prior expansion state.

## Extension points

The tree's row model is intentionally flat (`[]treeRow`) so future features fit in without reshaping:

- A future "section row" type (e.g., `▾ Recently reviewed (3)`) can be added as a new `rowKind` without touching the renderer's main loop.
- Sort-order changes are a single argument to `BuildTree`.
- Persistent state is a serialize/deserialize wrapper around `treeExpanded`.
