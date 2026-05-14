# Collapsible File Explorer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Changes view's flat file list with a two-tier dir-grouped tree the user can expand and collapse, while preserving all existing review behaviors (reviewed marks, filter, hunk jump, sparkline).

**Architecture:** A pure `BuildTree` function in `internal/ui/filetree.go` consumes `[]diff.File` + UI state (reviewed marks, collapsed dirs, filter) and returns a flat `[]treeRow` of dir and file rows. The Model migrates `fileCursor` → `rowCursor` (an index into the row list) and adds tree state. The renderer walks `treeRows` instead of files. The tree is always two-tier: each leaf-containing directory becomes a top-level row whose full path is the label; its files appear directly beneath as depth-1 children. No deeper nesting in v1.

**Tech Stack:** Go 1.26, Bubble Tea, lipgloss. No new deps.

**Spec:** `docs/superpowers/specs/2026-05-14-collapsible-files-design.md`

**Tree-shape clarification:** The spec calls this "VSCode compact folders." The actual rule (matching the preview the user selected) is simpler: every distinct directory that *directly contains a changed file* becomes one top-level row with its full path as the label. The displayed tree is always exactly two tiers (dir rows at depth 0, file rows at depth 1). No nested dir rows. This is more aggressive than strict VSCode compact-folders mode but produces the layout the user approved.

---

## File Structure

**New files:**
- `internal/ui/filetree.go` — `treeRow` type, `BuildTree(files, reviewed, collapsed, filter) []treeRow`
- `internal/ui/filetree_test.go` — pure-function tests

**Modified files:**
- `internal/ui/model.go` — `Model` fields (`rowCursor`, `treeRows`, `treeCollapsed`, `pathPreFilter`, `preFilterCollapsed`), helpers (`rowAtCursor`, `currentFileRow`), key handlers, all `fileCursor` callers
- `internal/ui/render.go` — `renderFilesList` rewritten over `treeRows`
- `internal/ui/model_test.go` — new tests for tree navigation, expand/collapse, dir-cursor behavior

---

## Task 1: `filetree.go` — types + `BuildTree`

**Files:**
- Create: `internal/ui/filetree.go`
- Create: `internal/ui/filetree_test.go`

- [ ] **Step 1.1: Write `filetree.go`**

```go
package ui

import (
	"path"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

type rowKind int

const (
	rowDir rowKind = iota
	rowFile
)

// treeRow is one visible row in the file-explorer pane. Rows are flat:
// dir rows at depth 0, their file children at depth 1. The renderer
// iterates these directly — no recursion required.
type treeRow struct {
	Kind     rowKind
	Path     string // dir: parent path (e.g. "internal/ui", "" for root files); file: full diff.File.Path
	Label    string // what to render (dir path or filename)
	Depth    int    // 0 for dirs, 1 for files
	FileIdx  int    // file rows only: index into the `files` argument to BuildTree (-1 for dirs)
	Reviewed int    // dir rows only: count of reviewed files in this dir
	Total    int    // dir rows only: total files in this dir
}

// BuildTree returns the visible rows for the given inputs.
//
//   - files: the file slice to render (already filtered for view/mode; this
//     function does NOT apply the user's text filter again — pass a
//     pre-filtered slice when needed and the filter argument for force-expand).
//   - reviewed: set of file paths the user has marked reviewed.
//   - collapsed: set of dir paths the user has collapsed (presence = collapsed).
//     Dirs default to expanded; only explicitly-collapsed dirs hide children.
//   - filter: when non-empty, every dir is force-expanded regardless of
//     `collapsed`. The caller is responsible for narrowing `files` to matches.
//
// Row order: dirs appear in the order their first file appears in `files`.
// Files within a dir appear in `files` order. Stable + predictable.
func BuildTree(files []diff.File, reviewed map[string]bool, collapsed map[string]bool, filter string) []treeRow {
	type group struct {
		dir   string
		files []int // indices into `files`
	}
	var groups []*group
	byDir := map[string]*group{}

	for i, f := range files {
		d := dirOf(f.Path)
		g, ok := byDir[d]
		if !ok {
			g = &group{dir: d}
			byDir[d] = g
			groups = append(groups, g)
		}
		g.files = append(g.files, i)
	}

	var rows []treeRow
	for _, g := range groups {
		rev := 0
		for _, i := range g.files {
			if reviewed[files[i].Path] {
				rev++
			}
		}
		rows = append(rows, treeRow{
			Kind:     rowDir,
			Path:     g.dir,
			Label:    dirLabel(g.dir),
			Depth:    0,
			FileIdx:  -1,
			Reviewed: rev,
			Total:    len(g.files),
		})
		if filter == "" && collapsed[g.dir] {
			continue
		}
		for _, i := range g.files {
			f := files[i]
			rows = append(rows, treeRow{
				Kind:    rowFile,
				Path:    f.Path,
				Label:   path.Base(f.Path),
				Depth:   1,
				FileIdx: i,
				Total:   1,
			})
		}
	}
	return rows
}

// dirOf returns the directory containing the given file path. Files at the
// repo root return "" so we can render them under a "(root)" pseudo-dir.
func dirOf(filePath string) string {
	d := path.Dir(filePath)
	if d == "." {
		return ""
	}
	return d
}

// dirLabel returns the user-visible label for a dir path. "" becomes "(root)";
// everything else is returned unchanged.
func dirLabel(dirPath string) string {
	if dirPath == "" {
		return "(root)"
	}
	return dirPath
}

// FirstFileRow returns the index of the first file row in rows, or -1 if
// there isn't one. Used by callers that want to land the cursor on a file
// after a tree rebuild.
func FirstFileRow(rows []treeRow) int {
	for i, r := range rows {
		if r.Kind == rowFile {
			return i
		}
	}
	return -1
}

// RowOfFile returns the index of the row whose Path matches the given file
// path, or -1 if no such row is visible. Used to restore cursor position
// after a filter clears.
func RowOfFile(rows []treeRow, filePath string) int {
	for i, r := range rows {
		if r.Kind == rowFile && r.Path == filePath {
			return i
		}
	}
	return -1
}

```

- [ ] **Step 1.2: Write `filetree_test.go`**

```go
package ui

import (
	"testing"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

func makeFiles(paths ...string) []diff.File {
	out := make([]diff.File, len(paths))
	for i, p := range paths {
		out[i] = diff.File{Path: p, Status: diff.StatusModified}
	}
	return out
}

func TestBuildTree_GroupsByDir(t *testing.T) {
	files := makeFiles(
		"internal/ctxpane/blame.go",
		"internal/ctxpane/cache.go",
		"internal/ui/model.go",
	)
	rows := BuildTree(files, nil, nil, "")

	// Expect: dir "internal/ctxpane" (2 files) + 2 file rows; dir "internal/ui" (1 file) + 1 file row.
	if len(rows) != 5 {
		t.Fatalf("row count: got %d want 5; rows=%+v", len(rows), rows)
	}
	if rows[0].Kind != rowDir || rows[0].Path != "internal/ctxpane" || rows[0].Total != 2 {
		t.Errorf("row 0: got %+v", rows[0])
	}
	if rows[1].Kind != rowFile || rows[1].Label != "blame.go" || rows[1].FileIdx != 0 {
		t.Errorf("row 1: got %+v", rows[1])
	}
	if rows[3].Kind != rowDir || rows[3].Path != "internal/ui" {
		t.Errorf("row 3: got %+v", rows[3])
	}
}

func TestBuildTree_RootFiles(t *testing.T) {
	files := makeFiles("README.md", "Makefile")
	rows := BuildTree(files, nil, nil, "")
	if len(rows) != 3 {
		t.Fatalf("row count: got %d want 3; rows=%+v", len(rows), rows)
	}
	if rows[0].Kind != rowDir || rows[0].Path != "" || rows[0].Label != "(root)" {
		t.Errorf("root dir row: got %+v", rows[0])
	}
}

func TestBuildTree_CollapsedDirHidesFiles(t *testing.T) {
	files := makeFiles(
		"a/x.go",
		"a/y.go",
		"b/z.go",
	)
	collapsed := map[string]bool{"a": true}
	rows := BuildTree(files, nil, collapsed, "")
	// Expect: a (no children) + b + b/z.go = 3 rows.
	if len(rows) != 3 {
		t.Fatalf("row count: got %d want 3; rows=%+v", len(rows), rows)
	}
	if rows[0].Path != "a" || rows[0].Kind != rowDir {
		t.Errorf("row 0: got %+v", rows[0])
	}
	if rows[1].Path != "b" || rows[1].Kind != rowDir {
		t.Errorf("row 1 should be dir b: got %+v", rows[1])
	}
	if rows[2].Kind != rowFile || rows[2].Path != "b/z.go" {
		t.Errorf("row 2: got %+v", rows[2])
	}
}

func TestBuildTree_FilterForceExpands(t *testing.T) {
	files := makeFiles("a/x.go", "a/y.go")
	collapsed := map[string]bool{"a": true}
	// Filter non-empty: collapsed state is overridden, files become visible.
	rows := BuildTree(files, nil, collapsed, "x")
	if len(rows) != 3 {
		t.Fatalf("row count: got %d want 3; rows=%+v", len(rows), rows)
	}
	if rows[1].Kind != rowFile || rows[2].Kind != rowFile {
		t.Errorf("expected both files visible under filter; got %+v", rows)
	}
}

func TestBuildTree_ReviewedAggregation(t *testing.T) {
	files := makeFiles("a/x.go", "a/y.go", "a/z.go")
	reviewed := map[string]bool{"a/x.go": true, "a/y.go": true}
	rows := BuildTree(files, reviewed, nil, "")
	if rows[0].Reviewed != 2 || rows[0].Total != 3 {
		t.Errorf("dir aggregation: got reviewed=%d total=%d want 2/3", rows[0].Reviewed, rows[0].Total)
	}
}

func TestBuildTree_PreservesFirstAppearanceOrder(t *testing.T) {
	files := makeFiles(
		"z/file.go",
		"a/file.go",
		"z/other.go",
	)
	rows := BuildTree(files, nil, nil, "")
	if rows[0].Path != "z" {
		t.Errorf("first dir: got %q want z", rows[0].Path)
	}
	// All z files should appear before the a dir (group order = first-appearance).
	if rows[3].Path != "a" {
		t.Errorf("second dir: got %q want a (row index 3 after z + 2 z-files)", rows[3].Path)
	}
}

func TestFirstFileRow(t *testing.T) {
	rows := []treeRow{
		{Kind: rowDir, Path: "a"},
		{Kind: rowFile, Path: "a/x.go"},
		{Kind: rowFile, Path: "a/y.go"},
	}
	if got := FirstFileRow(rows); got != 1 {
		t.Errorf("got %d want 1", got)
	}
	if got := FirstFileRow(nil); got != -1 {
		t.Errorf("nil rows: got %d want -1", got)
	}
}

func TestRowOfFile(t *testing.T) {
	rows := []treeRow{
		{Kind: rowDir, Path: "a"},
		{Kind: rowFile, Path: "a/x.go"},
		{Kind: rowFile, Path: "a/y.go"},
	}
	if got := RowOfFile(rows, "a/y.go"); got != 2 {
		t.Errorf("got %d want 2", got)
	}
	if got := RowOfFile(rows, "missing"); got != -1 {
		t.Errorf("missing: got %d want -1", got)
	}
}
```

- [ ] **Step 1.3: Run tests**

Run: `go test ./internal/ui/ -run TestBuildTree -v && go test ./internal/ui/ -run TestFirstFileRow -v && go test ./internal/ui/ -run TestRowOfFile -v`
Expected: all pass.

- [ ] **Step 1.4: Run vet and format**

```
go vet ./...
gofmt -l internal/ui
```
Expected: vet clean; gofmt prints no new files (`internal/diff/` pre-existing entries don't count).

- [ ] **Step 1.5: Commit**

```bash
git add internal/ui/filetree.go internal/ui/filetree_test.go
git commit -m "ui: filetree package — pure BuildTree over flat dir groups"
```

---

## Task 2: Wire `BuildTree` into Model and `refreshDiff`

**Files:**
- Modify: `internal/ui/model.go`

This task adds the tree state to the Model and rebuilds the row list on every `refreshDiff`. The Model continues to use `m.fileCursor` (rename happens in Task 3). The renderer still iterates the flat file list (rewrite happens in Task 3). Goal of this task: the data is present and tested without changing visible behavior.

- [ ] **Step 2.1: Add fields to `Model`**

In `internal/ui/model.go`, in the `Model` struct, add (place near the existing filter fields):

```go
	// tree state for the file explorer (Changes view).
	treeRows           []treeRow
	treeCollapsed      map[string]bool
	preFilterCollapsed map[string]bool // snapshot on filter start, restored on clear
	pathPreFilter      string          // file under cursor when filter started; restored on clear
```

Update the `New` constructor to initialize the maps:

```go
func New(d *diff.Diff, commits []diff.Commit, repoRoot string) Model {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.Placeholder = "filter files…"
	ti.CharLimit = 100
	return Model{
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
}
```

- [ ] **Step 2.2: Build the tree at the end of `refreshDiff`**

Locate `refreshDiff` and append (just before its final `m.viewport.GotoTop()` line):

```go
	// Rebuild the file-explorer tree for the Changes view.
	if m.view == viewChanges {
		files, _ := m.effectiveFiles()
		m.treeRows = BuildTree(files, m.reviewedFiles, m.treeCollapsed, m.filter)
	} else {
		m.treeRows = nil
	}
```

- [ ] **Step 2.3: Add a unit test confirming tree state is built**

In `internal/ui/model_test.go`, append:

```go
func TestRefreshDiffBuildsTree(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = updated.(Model)
	if len(m.treeRows) == 0 {
		t.Fatal("treeRows should be populated after WindowSizeMsg (which refreshes)")
	}
	// fakeDiff has files at root (main.go, added.go) → one "(root)" dir row + 2 file rows.
	if m.treeRows[0].Kind != rowDir {
		t.Errorf("row 0: got %+v want dir", m.treeRows[0])
	}
}
```

- [ ] **Step 2.4: Run tests**

```
go test ./internal/ui/ -v
go build ./...
go vet ./...
```
Expected: all pass.

- [ ] **Step 2.5: Commit**

```bash
git add internal/ui/
git commit -m "ui: build file tree in refreshDiff"
```

---

## Task 3: Rename `fileCursor` → `rowCursor`, add helpers, rewrite `renderFilesList`

**Files:**
- Modify: `internal/ui/model.go`
- Modify: `internal/ui/render.go`
- Modify: `internal/ui/model_test.go`

This is the largest task: the cursor's semantics change from "index into effectiveFiles" to "index into treeRows", and every call site must be migrated.

- [ ] **Step 3.1: Rename the field in `Model`**

In the `Model` struct, change `fileCursor int` to `rowCursor int`. Use your editor's rename to update all references in `internal/ui/`.

- [ ] **Step 3.2: Replace `cursorPreFilter int` with `pathPreFilter string`**

The field already exists as `cursorPreFilter int`; replace its declaration and behavior. Find:

```go
	cursorPreFilter int    // fileCursor before filter began, restored on clear
```

This field was already added in Step 2.1 under a different name (`pathPreFilter`). Confirm: Step 2.1 added `pathPreFilter`. Now delete the obsolete `cursorPreFilter` line entirely. The behavior change is wired in this task's later steps.

- [ ] **Step 3.3: Add helpers `rowAtCursor` and `currentFileRow`**

Add near the other cursor helpers (e.g. after `cursor()` definition):

```go
// rowAtCursor returns the row the cursor currently points at, or a zero-value
// row if the cursor is out of range (e.g. tree just rebuilt, treeRows empty).
func (m Model) rowAtCursor() treeRow {
	if m.rowCursor < 0 || m.rowCursor >= len(m.treeRows) {
		return treeRow{}
	}
	return m.treeRows[m.rowCursor]
}

// currentFileRow returns the underlying diff.File for the row under the
// cursor. ok is false when the cursor is on a dir row or out of range.
// fileIdx is the index into m.effectiveFiles() — useful for callers that
// need to index into the file slice directly.
func (m Model) currentFileRow() (f diff.File, fileIdx int, ok bool) {
	r := m.rowAtCursor()
	if r.Kind != rowFile {
		return diff.File{}, -1, false
	}
	files, _ := m.effectiveFiles()
	if r.FileIdx < 0 || r.FileIdx >= len(files) {
		return diff.File{}, -1, false
	}
	return files[r.FileIdx], r.FileIdx, true
}
```

- [ ] **Step 3.4: Update `maxCursor`**

Find:

```go
func (m *Model) maxCursor() int {
	if m.view == viewCommits {
		return len(m.commits) - 1
	}
	files, _ := m.effectiveFiles()
	return len(files) - 1
}
```

Replace with:

```go
func (m *Model) maxCursor() int {
	if m.view == viewCommits {
		return len(m.commits) - 1
	}
	if m.view == viewChanges {
		return len(m.treeRows) - 1
	}
	files, _ := m.effectiveFiles()
	return len(files) - 1
}
```

- [ ] **Step 3.5: Update `setCursor` and `cursor`**

In `cursor()`, replace `return m.fileCursor` with `return m.rowCursor` (likely already done by Step 3.1).

In `setCursor()`, the `default` branch already references `m.fileCursor` (now `m.rowCursor` after rename). Behavior is unchanged: assigning `rowCursor` and calling `refreshDiff`. Verify it reads correctly post-rename.

- [ ] **Step 3.6: Update `currentFileIndex`**

The current implementation returns `m.fileCursor`. After rename it returns `m.rowCursor` — but `rowCursor` is no longer a file index. Replace the function:

```go
// currentFileIndex returns the cursor's index into the effective file list,
// or -1 if the current view doesn't have a per-file cursor OR the cursor is
// on a dir row.
func (m Model) currentFileIndex() int {
	switch m.view {
	case viewChanges:
		_, fileIdx, ok := m.currentFileRow()
		if !ok {
			return -1
		}
		return fileIdx
	case viewOverview:
		return m.overviewCursor
	}
	return -1
}
```

- [ ] **Step 3.7: Update `selectedEditTarget` and `renderDiffPane`**

In `selectedEditTarget`:

```go
	} else {
		files, _ := m.effectiveFiles()
		if m.fileCursor >= len(files) {
			return diff.File{}, 0, false
		}
		f = files[m.fileCursor]
	}
```

Replace with:

```go
	} else {
		fr, _, ok := m.currentFileRow()
		if !ok {
			return diff.File{}, 0, false
		}
		f = fr
	}
```

In `renderDiffPane` (and the `refreshDiff` body that picks the file to render in the viewport):

Find:

```go
		files, _ := m.effectiveFiles()
		if len(files) == 0 {
			m.viewport.SetContent(mutedStyle.Render("(no matches)"))
			m.hunkOffsets = nil
			return
		}
		if m.fileCursor >= len(files) {
			m.fileCursor = 0
		}
		f := files[m.fileCursor]
```

Replace with:

```go
		files, _ := m.effectiveFiles()
		if len(files) == 0 {
			m.viewport.SetContent(mutedStyle.Render("(no matches)"))
			m.hunkOffsets = nil
			return
		}
		fr, _, ok := m.currentFileRow()
		if !ok {
			// Cursor is on a dir row (or out of range). Show a placeholder; the
			// diff pane is empty until the user moves to a file row.
			m.viewport.SetContent(mutedStyle.Render("(select a file)"))
			m.hunkOffsets = nil
			return
		}
		f := fr
```

Also in `diffTitle` and `currentDiffReadonly`:

```go
	f := d.Files[m.fileCursor]
```

This is index-into-`d.Files` (NOT effectiveFiles). Update to use the file under the cursor row when in Changes view:

```go
	if m.view == viewChanges {
		fr, _, ok := m.currentFileRow()
		if !ok {
			return "(select a file)"
		}
		if fr.Status == diff.StatusRenamed && fr.OldPath != "" && fr.OldPath != fr.Path {
			return fmt.Sprintf("%s → %s", fr.OldPath, fr.Path)
		}
		return fr.Path
	}
```

(Patch the existing `diffTitle` to use this pattern where it currently does `m.d.Files[m.fileCursor]`.)

- [ ] **Step 3.8: Update `toggleReviewed`, `jumpToNextUnreviewed`**

`toggleReviewed`:

```go
func (m *Model) toggleReviewed() {
	_, _, ok := m.currentFileRow()
	if !ok {
		m.statusMsg = "m: select a file to mark"
		return
	}
	f, _, _ := m.currentFileRow()
	if m.reviewedFiles[f.Path] {
		delete(m.reviewedFiles, f.Path)
	} else {
		m.reviewedFiles[f.Path] = true
	}
}
```

`jumpToNextUnreviewed`:

```go
func (m *Model) jumpToNextUnreviewed() {
	if m.view != viewChanges {
		// Existing overview behavior unchanged.
		// (Implement below.)
	}
	// Walk file rows starting after the current row; wrap.
	if m.view == viewChanges {
		n := len(m.treeRows)
		if n == 0 {
			return
		}
		start := m.rowCursor
		for i := 1; i <= n; i++ {
			next := (start + i) % n
			r := m.treeRows[next]
			if r.Kind != rowFile {
				continue
			}
			if !m.reviewedFiles[r.Path] {
				m.rowCursor = next
				m.refreshDiff()
				return
			}
		}
		m.statusMsg = "all files reviewed"
		return
	}
	// Existing overview path: walk by file index as before.
	files, _ := m.effectiveFiles()
	n := len(files)
	if n == 0 {
		return
	}
	start := m.overviewCursor
	for i := 1; i <= n; i++ {
		next := (start + i) % n
		if !m.reviewedFiles[files[next].Path] {
			m.overviewCursor = next
			return
		}
	}
	m.statusMsg = "all files reviewed"
}
```

- [ ] **Step 3.9: Update `contextJumpToSelected` and the Overview→Changes Enter handler**

Find in `contextJumpToSelected`:

```go
	for i, f := range files {
		if f.Path == it.Jump.File {
			m.fileCursor = i
			m.refreshDiff()
			break
		}
	}
```

Replace with:

```go
	// Check that the target file is actually in the diff before jumping.
	inDiff := false
	for _, f := range files {
		if f.Path == it.Jump.File {
			inDiff = true
			break
		}
	}
	if inDiff {
		// Rebuild the tree to ensure current row indices, move the cursor to
		// the matching row, then refresh once more so the diff pane re-renders.
		m.refreshDiff()
		if row := RowOfFile(m.treeRows, it.Jump.File); row >= 0 {
			m.rowCursor = row
		}
		m.refreshDiff()
	}
```

And in the Overview-view Enter handler:

```go
		case "enter":
			if m.view == viewOverview {
				m.fileCursor = m.overviewCursor   // OLD
				m.setView(viewChanges)
				return m, nil
			}
```

This needs to translate the overview file index to a row index. Change to:

```go
		case "enter":
			if m.view == viewOverview {
				files, _ := m.effectiveFiles()
				if m.overviewCursor >= 0 && m.overviewCursor < len(files) {
					target := files[m.overviewCursor].Path
					m.setView(viewChanges)
					if row := RowOfFile(m.treeRows, target); row >= 0 {
						m.rowCursor = row
						m.refreshDiff()
					}
				} else {
					m.setView(viewChanges)
				}
				return m, nil
			}
			if m.focus == paneContext {
				return m, m.contextJumpToSelected()
			}
			return m, nil
```

- [ ] **Step 3.10: Rewrite `renderFilesList`**

In `internal/ui/model.go` (or move to `render.go` if it lives there), replace the whole function body:

```go
func (m Model) renderFilesList(leftW int) string {
	if m.d == nil || len(m.d.Files) == 0 {
		return mutedStyle.Render("(no files)")
	}
	rowW := leftW - 4 // borders + padding
	if rowW < 8 {
		rowW = 8
	}

	var lines []string
	var sub string
	if m.filter != "" {
		files, _ := m.effectiveFiles()
		sub = fmt.Sprintf("%d/%d files · /%s", len(files), len(m.d.Files), m.filter)
	} else {
		sub = fmt.Sprintf("%d files", len(m.d.Files))
		if m.d.Label != "" {
			sub = m.d.Label + " · " + sub
		}
	}
	lines = append(lines, mutedStyle.Render(truncateRaw(sub, rowW)))

	if len(m.treeRows) == 0 {
		lines = append(lines, mutedStyle.Render("(no matches)"))
		return strings.Join(lines, "\n")
	}

	const sparkW = 6
	showSpark := rowW >= 30

	for i, r := range m.treeRows {
		var rendered string
		switch r.Kind {
		case rowDir:
			rendered = renderTreeDir(r, m.treeCollapsed[r.Path], rowW)
		case rowFile:
			fileIdx := r.FileIdx
			files, _ := m.effectiveFiles()
			if fileIdx < 0 || fileIdx >= len(files) {
				continue
			}
			f := files[fileIdx]
			reviewed := m.reviewedFiles[f.Path]
			rendered = renderTreeFile(r, f, reviewed, showSpark, sparkW, rowW)
		}
		if i == m.rowCursor {
			// Strip ANSI for the cursor row, re-render plain so cursorStyle bg applies cleanly.
			plain := stripAnsiForCursor(r, m, rowW, sparkW, showSpark)
			rendered = cursorStyle.Render(plain)
		}
		lines = append(lines, rendered)
	}
	return strings.Join(lines, "\n")
}
```

Add the helpers next to it:

```go
// renderTreeDir renders a dir row: marker + compact-path + right-aligned aggregate.
func renderTreeDir(r treeRow, collapsed bool, rowW int) string {
	marker := "▾ "
	if collapsed {
		marker = "▸ "
	}
	left := marker + r.Label
	right := ""
	if r.Total > 0 {
		if r.Reviewed == r.Total {
			right = addedLineStyle.Render("✓")
		} else if r.Reviewed > 0 {
			right = mutedStyle.Render(fmt.Sprintf("✓ %d/%d", r.Reviewed, r.Total))
		}
	}
	left = truncateRaw(left, rowW-ansi.StringWidth(right)-1)
	if right == "" {
		return left
	}
	return padBetweenAnsi(left, right, rowW)
}

// renderTreeFile renders a file row: 2-col indent + guide + status + filename, plus stats/sparkline.
func renderTreeFile(r treeRow, f diff.File, reviewed bool, showSpark bool, sparkW int, rowW int) string {
	const indent = "  │ "
	statsPlain := formatFileStats(f)
	reserve := len(indent) + 3 + len(statsPlain) // indent + marker + space + name + stats
	if showSpark {
		reserve += sparkW + 2
	}
	nameMaxW := rowW - reserve
	if nameMaxW < 4 {
		nameMaxW = 4
	}
	name := compactPath(r.Label, nameMaxW)

	var marker string
	if reviewed {
		marker = "✓"
	} else {
		marker = statusMarker(f.Status)
	}
	left := indent + marker + " " + name
	if reviewed {
		left = mutedStyle.Render(indent + "✓ " + name)
	}
	var right string
	if showSpark {
		if reviewed {
			right = mutedStyle.Render(renderSparklinePlain(f, sparkW)) + "  " + mutedStyle.Render(statsPlain)
		} else {
			right = renderSparkline(f, sparkW) + "  " + mutedStyle.Render(statsPlain)
		}
	} else {
		right = mutedStyle.Render(statsPlain)
	}
	return padBetweenAnsi(left, right, rowW)
}

// stripAnsiForCursor builds the plain (no-ANSI) version of the row at index
// m.rowCursor so cursorStyle's background can apply uniformly. We mirror the
// renderers above but without color/style markup.
func stripAnsiForCursor(r treeRow, m Model, rowW, sparkW int, showSpark bool) string {
	switch r.Kind {
	case rowDir:
		marker := "▾ "
		if m.treeCollapsed[r.Path] {
			marker = "▸ "
		}
		left := marker + r.Label
		var right string
		if r.Total > 0 {
			if r.Reviewed == r.Total {
				right = "✓"
			} else if r.Reviewed > 0 {
				right = fmt.Sprintf("✓ %d/%d", r.Reviewed, r.Total)
			}
		}
		if right == "" {
			return padBetweenPlain(left, "", rowW)
		}
		return padBetweenPlain(left, right, rowW)
	case rowFile:
		files, _ := m.effectiveFiles()
		if r.FileIdx < 0 || r.FileIdx >= len(files) {
			return ""
		}
		f := files[r.FileIdx]
		const indent = "  │ "
		reviewed := m.reviewedFiles[f.Path]
		statsPlain := formatFileStats(f)
		reserve := len(indent) + 3 + len(statsPlain)
		if showSpark {
			reserve += sparkW + 2
		}
		nameMaxW := rowW - reserve
		if nameMaxW < 4 {
			nameMaxW = 4
		}
		name := compactPath(r.Label, nameMaxW)
		var marker string
		if reviewed {
			marker = "✓"
		} else {
			marker = f.Status.String()
		}
		left := indent + marker + " " + name
		var right string
		if showSpark {
			right = renderSparklinePlain(f, sparkW) + "  " + statsPlain
		} else {
			right = statsPlain
		}
		return padBetweenPlain(left, right, rowW)
	}
	return ""
}
```

(`renderSparklinePlain` already exists and returns the unstyled bar; `formatFileStats` returns the plain `+N -M` string.)

- [ ] **Step 3.11: Update existing UI tests**

In `internal/ui/model_test.go`, `TestModelNavigation` was asserting `m.fileCursor` semantics. The test still passes after rename if the j key moves the row cursor through tree rows. But `fakeDiff` produces files at the root, so the first row (index 0) is a dir, and j moves to index 1 (first file). The existing test expects `cursor == 1` after a single j — that's still true. Just substitute `rowCursor` for `fileCursor`:

```go
func TestModelNavigation(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	// Move cursor down: starts at 0 (dir row "(root)") → 1 (first file).
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(Model)
	if m.rowCursor != 1 {
		t.Errorf("cursor after j: got %d want 1", m.rowCursor)
	}

	// Move further — fakeDiff has 2 files + 1 dir row = 3 rows. Last index = 2.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(Model)
	if m.rowCursor != 2 {
		t.Errorf("cursor at end: got %d want 2", m.rowCursor)
	}

	// Tab focus
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.focus != paneDiff {
		t.Errorf("focus after tab: got %v want paneDiff", m.focus)
	}
}
```

Add a new test for `currentFileRow`:

```go
func TestCurrentFileRow(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	// Row 0 is the "(root)" dir row.
	m.rowCursor = 0
	if _, _, ok := m.currentFileRow(); ok {
		t.Error("dir row should return ok=false")
	}

	// Row 1 is the first file.
	m.rowCursor = 1
	if f, _, ok := m.currentFileRow(); !ok || f.Path != "main.go" {
		t.Errorf("file row: ok=%v path=%q want ok=true path=main.go", ok, f.Path)
	}
}
```

- [ ] **Step 3.12: Run tests, build, vet**

```
go test ./internal/ui/ -v
go build ./...
go vet ./...
gofmt -l internal/ui
```
Expected: all green.

- [ ] **Step 3.13: Commit**

```bash
git add internal/ui/
git commit -m "ui: rename fileCursor→rowCursor, render file tree"
```

---

## Task 4: Expand/collapse keys (Enter, l, h, right, left)

**Files:**
- Modify: `internal/ui/model.go`
- Modify: `internal/ui/model_test.go`

- [ ] **Step 4.1: Add `case "enter"` branch for dir toggle**

In `Update`, the existing `case "enter":` handles overview and pane-focus paths. Extend it (just before the `if m.focus == paneContext` branch in the changes view path):

```go
		case "enter":
			if m.view == viewOverview {
				files, _ := m.effectiveFiles()
				if m.overviewCursor >= 0 && m.overviewCursor < len(files) {
					target := files[m.overviewCursor].Path
					m.setView(viewChanges)
					if row := RowOfFile(m.treeRows, target); row >= 0 {
						m.rowCursor = row
						m.refreshDiff()
					}
				} else {
					m.setView(viewChanges)
				}
				return m, nil
			}
			if m.focus == paneContext {
				return m, m.contextJumpToSelected()
			}
			if m.view == viewChanges && m.focus == paneLeft {
				r := m.rowAtCursor()
				if r.Kind == rowDir {
					m.toggleDirCollapsed(r.Path)
					return m, nil
				}
			}
			return m, nil
```

Add the helper:

```go
// toggleDirCollapsed flips a directory's expansion state and rebuilds the tree.
// If collapsing the dir would orphan the row cursor (currently on one of its
// children), the cursor jumps to the dir row itself.
func (m *Model) toggleDirCollapsed(dir string) {
	wasCollapsed := m.treeCollapsed[dir]
	if wasCollapsed {
		delete(m.treeCollapsed, dir)
	} else {
		m.treeCollapsed[dir] = true
	}
	dirRow := m.rowCursor
	for i, r := range m.treeRows {
		if r.Kind == rowDir && r.Path == dir {
			dirRow = i
			break
		}
	}
	m.refreshDiff()
	// After rebuild, ensure cursor lands on a valid row.
	if !wasCollapsed {
		// We just collapsed: snap cursor to the dir row (its children disappeared).
		if row := indexOfDirRow(m.treeRows, dir); row >= 0 {
			m.rowCursor = row
		} else {
			m.rowCursor = clamp(dirRow, 0, len(m.treeRows)-1)
		}
	}
}

// indexOfDirRow returns the row index of the dir row with the given path,
// or -1 if not found.
func indexOfDirRow(rows []treeRow, dir string) int {
	for i, r := range rows {
		if r.Kind == rowDir && r.Path == dir {
			return i
		}
	}
	return -1
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
```

- [ ] **Step 4.2: Add `l`/`right` and `h`/`left` handlers**

Replace the existing `case "h", "left":` and `case "l", "right":` blocks with:

```go
		case "l", "right":
			if m.view == viewOverview {
				m.moveOverview(+1, 0)
				return m, nil
			}
			if m.view == viewChanges && m.focus == paneLeft {
				r := m.rowAtCursor()
				if r.Kind == rowDir {
					if m.treeCollapsed[r.Path] {
						m.toggleDirCollapsed(r.Path)
					} else {
						// Already expanded → descend to first child if any.
						if m.rowCursor+1 < len(m.treeRows) && m.treeRows[m.rowCursor+1].Kind == rowFile {
							return m, m.setCursor(m.rowCursor + 1)
						}
					}
					return m, nil
				}
				// On a file row, l is a no-op.
				return m, nil
			}
			return m, nil

		case "h", "left":
			if m.view == viewOverview {
				m.moveOverview(-1, 0)
				return m, nil
			}
			if m.view == viewChanges && m.focus == paneLeft {
				r := m.rowAtCursor()
				if r.Kind == rowDir && !m.treeCollapsed[r.Path] {
					m.toggleDirCollapsed(r.Path)
					return m, nil
				}
				// On file or collapsed dir: jump to parent dir.
				parent := ""
				if r.Kind == rowFile {
					parent = path.Dir(r.Path)
					if parent == "." {
						parent = ""
					}
				}
				if r.Kind == rowDir && m.treeCollapsed[r.Path] {
					// Collapsed dir: there's no further parent in our flat 2-tier tree.
					return m, nil
				}
				if row := indexOfDirRow(m.treeRows, parent); row >= 0 {
					return m, m.setCursor(row)
				}
				return m, nil
			}
			return m, nil
```

Add `"path"` to the import block of `model.go` if not present.

- [ ] **Step 4.3: Add tests for expand/collapse**

In `internal/ui/model_test.go`, append:

```go
func TestExpandCollapseEnter(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	// Row 0 is the "(root)" dir, currently expanded. Pressing Enter collapses it.
	m.rowCursor = 0
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !m.treeCollapsed[""] {
		t.Error("after Enter on root dir: should be collapsed")
	}
	if len(m.treeRows) != 1 {
		t.Errorf("after collapse: rows=%d want 1 (dir row only)", len(m.treeRows))
	}

	// Enter again: expand.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.treeCollapsed[""] {
		t.Error("after second Enter: should be expanded")
	}
}

func TestLExpandsThenDescends(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	// Start: dir row (root, expanded). l should descend to row 1.
	m.rowCursor = 0
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	m = updated.(Model)
	if m.rowCursor != 1 {
		t.Errorf("l on expanded dir: cursor=%d want 1", m.rowCursor)
	}

	// Collapse via Enter, then l should expand (not descend).
	m.rowCursor = 0
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // collapse
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	m = updated.(Model)
	if m.treeCollapsed[""] {
		t.Error("l on collapsed dir should expand it")
	}
}

func TestHJumpsToParent(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	// Move cursor onto a file row.
	m.rowCursor = 1
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m = updated.(Model)
	if m.rowCursor != 0 {
		t.Errorf("h on file row: cursor=%d want 0 (parent dir)", m.rowCursor)
	}
}
```

- [ ] **Step 4.4: Run tests, build, vet**

```
go test ./internal/ui/ -v
go build ./...
go vet ./...
gofmt -l internal/ui
```
Expected: all green.

- [ ] **Step 4.5: Commit**

```bash
git add internal/ui/
git commit -m "ui: expand/collapse keys for file tree (Enter, l, h)"
```

---

## Task 5: File-only review keys (`m`, `M`, `e`) on dir rows

**Files:**
- Modify: `internal/ui/model.go`
- Modify: `internal/ui/model_test.go`

`toggleReviewed` was updated in Task 3 to no-op on dir rows with a status message. `jumpToNextUnreviewed` was updated to walk file rows. This task tightens `e` (editor) and adds tests.

- [ ] **Step 5.1: Update `openInEditor` to handle dir rows**

Find:

```go
func (m *Model) openInEditor() tea.Cmd {
	f, line, ok := m.selectedEditTarget()
	if !ok {
		m.statusMsg = "nothing to edit here"
		return nil
	}
```

Replace the message text:

```go
func (m *Model) openInEditor() tea.Cmd {
	f, line, ok := m.selectedEditTarget()
	if !ok {
		// Distinguish "dir row" from "no file at all" for a friendlier hint.
		if r := m.rowAtCursor(); r.Kind == rowDir {
			m.statusMsg = "e: select a file to open"
		} else {
			m.statusMsg = "nothing to edit here"
		}
		return nil
	}
```

- [ ] **Step 5.2: Add tests for file-only key behavior**

In `internal/ui/model_test.go`, append:

```go
func TestMarkReviewedNoOpOnDirRow(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	m.rowCursor = 0 // dir row
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = updated.(Model)
	if len(m.reviewedFiles) != 0 {
		t.Errorf("m on dir row should not toggle reviewed; got %d entries", len(m.reviewedFiles))
	}
	if m.statusMsg == "" {
		t.Error("m on dir row should set a status hint")
	}
}

func TestMarkReviewedTogglesOnFileRow(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	m.rowCursor = 1 // first file row
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = updated.(Model)
	if !m.reviewedFiles["main.go"] {
		t.Errorf("m on file row should mark reviewed; reviewedFiles=%v", m.reviewedFiles)
	}
}

func TestNextUnreviewedWalksFileRowsOnly(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	// Mark first file reviewed, then jump-to-next from row 0 (dir row).
	m.reviewedFiles["main.go"] = true
	m.rowCursor = 1 // first file (already reviewed)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	m = updated.(Model)
	if m.rowCursor != 2 {
		t.Errorf("M: cursor=%d want 2 (next file)", m.rowCursor)
	}
}
```

- [ ] **Step 5.3: Run tests, build, vet**

```
go test ./internal/ui/ -v
go build ./...
go vet ./...
gofmt -l internal/ui
```
Expected: green.

- [ ] **Step 5.4: Commit**

```bash
git add internal/ui/
git commit -m "ui: file-only keys (m/M/e) handle dir rows gracefully"
```

---

## Task 6: Filter integration — `pathPreFilter` and `preFilterCollapsed`

**Files:**
- Modify: `internal/ui/model.go`
- Modify: `internal/ui/model_test.go`

- [ ] **Step 6.1: Update `startFiltering`**

Find:

```go
func (m *Model) startFiltering() tea.Cmd {
	if !m.filtering {
		m.cursorPreFilter = m.fileCursor
	}
	m.filtering = true
	m.filterInput.SetValue(m.filter)
	m.filterInput.CursorEnd()
	return m.filterInput.Focus()
}
```

Replace with:

```go
func (m *Model) startFiltering() tea.Cmd {
	if !m.filtering {
		// Snapshot the file under the cursor (path is stable across rebuilds;
		// the row index is not). Snapshot expansion state too so collapsed
		// dirs come back when the filter clears.
		if f, _, ok := m.currentFileRow(); ok {
			m.pathPreFilter = f.Path
		} else {
			m.pathPreFilter = ""
		}
		m.preFilterCollapsed = copyStringBoolMap(m.treeCollapsed)
	}
	m.filtering = true
	m.filterInput.SetValue(m.filter)
	m.filterInput.CursorEnd()
	return m.filterInput.Focus()
}

func copyStringBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
```

- [ ] **Step 6.2: Update `commitFilter`**

Find:

```go
func (m *Model) commitFilter() {
	m.filter = strings.TrimSpace(m.filterInput.Value())
	m.filtering = false
	m.filterInput.Blur()
	m.fileCursor = 0
	m.refreshDiff()
}
```

Replace with:

```go
func (m *Model) commitFilter() {
	m.filter = strings.TrimSpace(m.filterInput.Value())
	m.filtering = false
	m.filterInput.Blur()
	m.refreshDiff()
	// Land on the first file row after the rebuild.
	if row := FirstFileRow(m.treeRows); row >= 0 {
		m.rowCursor = row
	} else {
		m.rowCursor = 0
	}
}
```

- [ ] **Step 6.3: Update `clearFilter`**

Find:

```go
func (m *Model) clearFilter() {
	m.filter = ""
	m.filterInput.SetValue("")
	m.fileCursor = m.cursorPreFilter
	m.refreshDiff()
}
```

Replace with:

```go
func (m *Model) clearFilter() {
	m.filter = ""
	m.filterInput.SetValue("")
	// Restore pre-filter expansion state, then rebuild.
	if m.preFilterCollapsed != nil {
		m.treeCollapsed = m.preFilterCollapsed
		m.preFilterCollapsed = nil
	}
	m.refreshDiff()
	// Try to restore the cursor to the same file path. If that file is no
	// longer visible, land on the first file row (or row 0 as a last resort).
	if m.pathPreFilter != "" {
		if row := RowOfFile(m.treeRows, m.pathPreFilter); row >= 0 {
			m.rowCursor = row
			m.pathPreFilter = ""
			return
		}
	}
	if row := FirstFileRow(m.treeRows); row >= 0 {
		m.rowCursor = row
	} else {
		m.rowCursor = 0
	}
	m.pathPreFilter = ""
}
```

- [ ] **Step 6.4: Update `handleFilterKey`**

Find:

```go
func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.cancelFilter()
		return m, nil
	case "enter":
		m.commitFilter()
		return m, nil
	}
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	// Live update: re-filter and refresh the diff for the first matching file.
	m.filter = strings.TrimSpace(m.filterInput.Value())
	m.fileCursor = 0
	m.refreshDiff()
	return m, cmd
}
```

Replace the `m.fileCursor = 0` line with:

```go
	// Live update: re-filter, rebuild, and land on the first matching file.
	m.filter = strings.TrimSpace(m.filterInput.Value())
	m.refreshDiff()
	if row := FirstFileRow(m.treeRows); row >= 0 {
		m.rowCursor = row
	} else {
		m.rowCursor = 0
	}
```

- [ ] **Step 6.5: Add filter tests**

In `internal/ui/model_test.go`, append:

```go
func TestFilterPreservesCollapsedAfterClear(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	// Collapse the (root) dir, then start filtering, then clear.
	m.rowCursor = 0
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // collapse
	m = updated.(Model)
	if !m.treeCollapsed[""] {
		t.Fatal("setup: root should be collapsed")
	}

	// Start filter; filter should NOT mutate treeCollapsed (it only force-expands during BuildTree).
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // cancel filter
	m = updated.(Model)

	if !m.treeCollapsed[""] {
		t.Error("after cancel filter: root should still be collapsed")
	}
}

func TestFilterPathPreFilterRestores(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	// Start with cursor on first file (main.go).
	m.rowCursor = 1
	if f, _, ok := m.currentFileRow(); !ok || f.Path != "main.go" {
		t.Fatalf("setup: cursor should be on main.go, got ok=%v f=%+v", ok, f)
	}

	// Filter "added" → only added.go matches.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	for _, ch := range "added" {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = updated.(Model)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // commit
	m = updated.(Model)

	// Clear filter (press `c` when no other filter behavior consumes it; in tests we call directly).
	m.clearFilter()
	if f, _, ok := m.currentFileRow(); !ok || f.Path != "main.go" {
		t.Errorf("after clear: cursor should restore to main.go, got ok=%v f=%+v", ok, f)
	}
}
```

- [ ] **Step 6.6: Run tests, build, vet, smoke**

```
go test ./internal/ui/ -v
go build -o /tmp/gitreview ./cmd/gitreview
go vet ./...
gofmt -l internal/ui
```

Smoke test (optional): cd into this repo, run `/tmp/gitreview`. Confirm:
- Files appear grouped by dir.
- j/k moves through dirs and files; Enter on a dir collapses; l expands or descends; h collapses or jumps to parent.
- `/` filter narrows; clearing restores cursor + collapsed state.
- `m` on dir shows a hint; `M` skips dirs; `e` on dir shows a hint.

- [ ] **Step 6.7: Final commit**

```bash
git add internal/ui/
git commit -m "ui: filter restores cursor by path and collapsed state on clear"
```

---

## Self-review

**Spec coverage:**
- Two-tier tree (dir at depth 0, files at depth 1) — Task 1 (BuildTree).
- Dir labels are the full leaf-dir path — Task 1.
- All dirs expanded by default — Task 1 (semantics of `collapsed` map).
- Reviewed aggregation on dir rows — Task 1.
- Filter force-expands matches — Task 1.
- `rowCursor` rename — Task 3.
- `currentFileRow` helper for file-only actions — Task 3.
- `Enter` / `l` / `h` keys — Task 4.
- `m` / `M` / `e` no-op on dir rows with status hints — Tasks 3 + 5.
- `pathPreFilter` and `preFilterCollapsed` — Tasks 2 + 6.

**Placeholder scan:** Checked — no TBD/TODO. The `_ = strings.Contains` in filetree.go Step 1.1 is a deliberate placeholder to keep the `strings` import in case helpers move here later; if it bothers the reviewer it can be removed (no test will fail).

**Type/name consistency:**
- `treeRow`, `rowKind`, `rowDir`/`rowFile`, `BuildTree`, `FirstFileRow`, `RowOfFile` — used consistently.
- `rowCursor` everywhere in model.go after Task 3.
- `treeCollapsed`, `preFilterCollapsed`, `pathPreFilter` — consistent.
- `currentFileRow()` signature `(diff.File, int, bool)` — consistent across all callers.

**Known scope cuts (out of v1 per spec):**
- No `zR` / `zM`.
- No persistent expansion state.
- No bulk-mark from dir rows.
- No custom sort orders.
- No drag-to-resize.
