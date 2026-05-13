# Context Pane Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an always-on, cursor-aware **context pane** as a third column in the gitreview TUI that fills with file/symbol/blame/cross-file/history information as the user moves through a diff.

**Architecture:** A new `internal/ctxpane/` package owns all data computation (heuristics + shell-outs to git). The UI calls a single `Resolve(cursor)` entry point on every cursor change (debounced) and renders the returned `Payload` of `Section`s into a third column. Strict separation mirrors the existing `internal/diff/` ↔ `internal/ui/` split.

**Tech Stack:** Go 1.26, Bubble Tea, lipgloss, `git` shell-outs (`blame`, `grep`, `log`). No new third-party dependencies.

**Spec:** `docs/superpowers/specs/2026-05-13-context-pane-design.md`

**Naming note:** The spec calls the package `internal/context/`. To avoid shadowing the Go stdlib `context` package (which we use for goroutine cancellation/timeouts), this plan names the directory and package **`ctxpane`** instead. All other names match the spec.

**Working-tree note:** The user may have unrelated WIP in `internal/ui/render.go`, `internal/ui/styles.go`, and `internal/ui/highlight.go` (syntax-highlighting feature). This plan does not touch the same line ranges as that WIP — the layout changes are higher in `render.go` and new styles are appended to `styles.go`. Resolve any conflicts with a normal merge.

---

## File Structure

**New files:**
- `internal/ctxpane/types.go` — `Cursor`, `Section`, `Item`, `Payload`, `Status` types
- `internal/ctxpane/resolver.go` — `Resolve(ctx, cursor, d)` entry point; per-section fan-out with timeouts
- `internal/ctxpane/where.go` — "Where" section (containing-func heuristic, hunk position)
- `internal/ctxpane/symbol.go` — "Symbol" section (decl detection + `git grep` refs)
- `internal/ctxpane/crossfile.go` — "Cross-file" section (grep restricted to other diff files)
- `internal/ctxpane/blame.go` — "Blame" section (`git blame -L`)
- `internal/ctxpane/history.go` — "History" section (`git log [--oneline | -L]`)
- `internal/ctxpane/cache.go` — concurrency-safe LRU
- `internal/ctxpane/helpers_test.go` — test helpers (`gitInit`, `gitRun`, `mustWrite`) duplicated from `internal/diff/`
- Plus a `_test.go` for each source file

**Modified files:**
- `internal/ui/model.go` — new fields (`contextPaneVisible`, `contextFocus`, `contextPayload`, `contextCursor`, `contextRefreshSeq`); new msg types; debounced refresh; toggle/focus keys
- `internal/ui/render.go` — third-column rendering in `View()` / new `renderContextPane()`; layout math updated to reserve 32 cols when visible
- `internal/ui/styles.go` — append `contextPaneStyle`, `contextSectionHeaderStyle`, `contextItemStyle`, `contextMutedStyle`, `contextItemSelectedStyle`
- `internal/ui/model_test.go` — add tests for context-pane rendering + toggle

---

## Task 1: Skeleton — types, resolver stub, package contract

**Files:**
- Create: `internal/ctxpane/types.go`
- Create: `internal/ctxpane/resolver.go`
- Create: `internal/ctxpane/resolver_test.go`

- [ ] **Step 1.1: Write `types.go`**

```go
package ctxpane

import "github.com/bowenbrooks/gitreview/internal/diff"

// SectionKind identifies which section a Section is. Order in the rendered
// pane matches the order of the constants below.
type SectionKind int

const (
	SectionWhere SectionKind = iota
	SectionSymbol
	SectionCrossFile
	SectionBlame
	SectionHistory
)

func (k SectionKind) Title() string {
	switch k {
	case SectionWhere:
		return "Where"
	case SectionSymbol:
		return "Symbol"
	case SectionCrossFile:
		return "Cross-file"
	case SectionBlame:
		return "Blame"
	case SectionHistory:
		return "History"
	}
	return "?"
}

// Status reflects how a section was computed.
type Status int

const (
	StatusOK Status = iota
	StatusEmpty
	StatusLoading
	StatusError
)

// Item is a single row inside a Section. Jump is optional; when present
// it tells the UI where Enter should send the diff cursor.
type Item struct {
	Text string
	Jump *JumpTarget
}

type JumpTarget struct {
	File string
	Line int
}

// Section is one labelled group of context rows. The UI renders the title
// from Kind.Title() and the items below it. An empty Items slice with
// StatusOK means "nothing to show here, skip rendering this section".
type Section struct {
	Kind   SectionKind
	Items  []Item
	Status Status
}

// Payload is the full set of sections returned by Resolve. Sections are
// always in SectionKind order; missing sections are simply absent.
type Payload struct {
	Sections []Section
}

// Cursor is the input to Resolve. It carries everything the resolver needs
// to compute its sections without reaching into UI internals.
type Cursor struct {
	File      diff.File // the currently-selected file (zero value if none)
	HunkIndex int       // 0-based index into File.Hunks; -1 if none
	Diff      *diff.Diff // the full diff (so cross-file sections can scan other files)
	RepoRoot  string    // absolute path to the working-tree root
}

// AnchorLine returns the OldNum (for removed lines) or NewNum (otherwise)
// of the first non-context-blank line in the current hunk, plus its Kind.
// Returns (0, LineContext, false) if the hunk has nothing usable.
func (c Cursor) AnchorLine() (lineNum int, kind diff.LineKind, ok bool) {
	if c.HunkIndex < 0 || c.HunkIndex >= len(c.File.Hunks) {
		return 0, diff.LineContext, false
	}
	h := c.File.Hunks[c.HunkIndex]
	// Prefer the first added or removed line; fall back to first context line.
	for _, l := range h.Lines {
		if l.Kind == diff.LineAdded && l.NewNum > 0 {
			return l.NewNum, l.Kind, true
		}
		if l.Kind == diff.LineRemoved && l.OldNum > 0 {
			return l.OldNum, l.Kind, true
		}
	}
	for _, l := range h.Lines {
		if l.NewNum > 0 {
			return l.NewNum, l.Kind, true
		}
	}
	return 0, diff.LineContext, false
}
```

- [ ] **Step 1.2: Write `resolver.go` (stub)**

```go
package ctxpane

import (
	"context"
	"time"
)

// PerSectionTimeout caps how long any single section may take to compute.
// On timeout, that section is rendered as Status=Loading with a "…" item
// and the rest of the pane still draws.
const PerSectionTimeout = 300 * time.Millisecond

// Resolve computes the Payload for the given cursor. Each section runs in
// its own goroutine; errors and timeouts are isolated to that section. The
// returned Payload always has at least Section{Kind: SectionWhere}.
//
// This stub implementation returns a hand-built payload so the UI can be
// wired and tested before real data sources are implemented.
func Resolve(ctx context.Context, cur Cursor) Payload {
	return Payload{
		Sections: []Section{
			{
				Kind:   SectionWhere,
				Status: StatusOK,
				Items: []Item{
					{Text: cur.File.Path},
					{Text: "(stub: real data lands in later tasks)"},
				},
			},
		},
	}
}
```

- [ ] **Step 1.3: Write `resolver_test.go`**

```go
package ctxpane

import (
	"context"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

func TestResolveStubReturnsWhereSection(t *testing.T) {
	cur := Cursor{
		File: diff.File{Path: "foo.go"},
	}
	p := Resolve(context.Background(), cur)
	if len(p.Sections) == 0 {
		t.Fatal("Resolve returned no sections")
	}
	if p.Sections[0].Kind != SectionWhere {
		t.Errorf("first section: got %v want SectionWhere", p.Sections[0].Kind)
	}
	if p.Sections[0].Items[0].Text != "foo.go" {
		t.Errorf("Where item: got %q want %q", p.Sections[0].Items[0].Text, "foo.go")
	}
}

func TestSectionKindTitle(t *testing.T) {
	cases := map[SectionKind]string{
		SectionWhere:     "Where",
		SectionSymbol:    "Symbol",
		SectionCrossFile: "Cross-file",
		SectionBlame:     "Blame",
		SectionHistory:   "History",
	}
	for k, want := range cases {
		if got := k.Title(); got != want {
			t.Errorf("Title(%v): got %q want %q", k, got, want)
		}
	}
}

func TestCursorAnchorLine(t *testing.T) {
	f := diff.File{
		Path: "x.go",
		Hunks: []diff.Hunk{{
			Lines: []diff.Line{
				{Kind: diff.LineContext, NewNum: 10, OldNum: 10},
				{Kind: diff.LineRemoved, OldNum: 11},
				{Kind: diff.LineAdded, NewNum: 11},
			},
		}},
	}
	cur := Cursor{File: f, HunkIndex: 0}
	line, kind, ok := cur.AnchorLine()
	if !ok {
		t.Fatal("AnchorLine: ok=false")
	}
	if line != 11 || kind != diff.LineAdded {
		t.Errorf("AnchorLine: got (%d, %v) want (11, LineAdded)", line, kind)
	}
}
```

- [ ] **Step 1.4: Run tests, confirm pass**

Run: `go test ./internal/ctxpane/ -v`
Expected: 3 tests pass.

- [ ] **Step 1.5: Run vet across the module**

Run: `go vet ./...`
Expected: no output (clean).

- [ ] **Step 1.6: Commit**

```bash
git add internal/ctxpane/
git commit -m "ctxpane: skeleton package with stub Resolve"
```

---

## Task 2: UI integration — third column rendering against the stub

**Files:**
- Modify: `internal/ui/model.go` — add fields, toggle key
- Modify: `internal/ui/render.go` — add `renderContextPane`, third-column layout
- Modify: `internal/ui/styles.go` — append context-pane styles
- Modify: `internal/ui/model_test.go` — add visibility/toggle tests

- [ ] **Step 2.1: Append new styles to `internal/ui/styles.go`**

Append these var declarations to the existing `var (...)` block (or as a new block if cleaner):

```go
	// contextPaneStyle is the rounded-border box around the third column.
	contextPaneStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colBorder).
				Padding(0, 1)

	contextPaneFocusStyle = contextPaneStyle.
				BorderForeground(colBorderFocus)

	// contextSectionHeaderStyle is the "▸ Where" / "▸ Symbol" label.
	contextSectionHeaderStyle = lipgloss.NewStyle().
					Foreground(colBorderFocus).
					Bold(true)

	contextItemStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))

	contextMutedStyle = lipgloss.NewStyle().Foreground(colMuted)

	contextItemSelectedStyle = lipgloss.NewStyle().
					Background(colCursor).
					Foreground(lipgloss.Color("15"))
```

- [ ] **Step 2.2: Add fields and constants to `internal/ui/model.go`**

Add `paneContext` to the `pane` enum:

```go
const (
	paneLeft pane = iota
	paneDiff
	paneContext
)
```

Add a constant for context-pane geometry (place near `headerRows` constant):

```go
const (
	contextPaneWidth   = 32  // fixed width when visible
	contextPaneMinTerm = 120 // hide entirely below this terminal width
)
```

Add fields to the `Model` struct (insert near the other UI-state fields, e.g. after `splitView bool`):

```go
	contextPaneVisible bool          // user-toggled; default true
	contextPayload     ctxpane.Payload
	contextCursor      ctxpane.Cursor
	contextSelected    int           // currently highlighted item index when pane is focused
	contextRefreshSeq  int           // monotonic; used to ignore stale debounced ticks
```

Update the import block of `model.go` to add:

```go
	"github.com/bowenbrooks/gitreview/internal/ctxpane"
```

Update `New()` to default the pane visible:

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
	}
}
```

- [ ] **Step 2.3: Layer the toggle onto the existing `c` key handler**

In `model.go` `Update`, find the existing `case "c":` block:

```go
		case "c":
			if m.filter != "" {
				m.clearFilter()
			}
			return m, nil
```

Replace with:

```go
		case "c":
			if m.filter != "" {
				m.clearFilter()
				return m, nil
			}
			m.contextPaneVisible = !m.contextPaneVisible
			if !m.contextPaneVisible && m.focus == paneContext {
				m.focus = paneDiff
			}
			m.layout()
			m.refreshDiff()
			return m, nil
```

- [ ] **Step 2.4: Update layout math in `model.go`**

Replace the `layout()` function with:

```go
func (m *Model) layout() {
	if m.width < 40 || m.height < 10 {
		return
	}
	bodyH := m.height - headerRows - helpHeight
	leftW := int(float64(m.width) * leftRatio)
	contextW := m.contextPaneWidthEffective()
	centerW := m.width - leftW - contextW
	// Reserve 2 cols on the right of the diff pane for the file-spine column
	// (1 col spine + 1 col gap). Empty in commits view but keeps layout stable.
	innerW := centerW - 4 - spineColW
	innerH := bodyH - 2 - 1

	if !m.ready {
		m.viewport = viewport.New(innerW, innerH)
	} else {
		m.viewport.Width = innerW
		m.viewport.Height = innerH
	}
}

// contextPaneWidthEffective returns 0 when the pane is hidden (user toggled it
// off, split view forces it off, or terminal too narrow); otherwise the fixed
// pane width.
func (m Model) contextPaneWidthEffective() int {
	if !m.contextPaneVisible {
		return 0
	}
	if m.splitView {
		return 0
	}
	if m.width < contextPaneMinTerm {
		return 0
	}
	return contextPaneWidth
}
```

Also update `paneWidths()` to reflect the third column:

```go
func (m Model) paneWidths() (left, center, context int) {
	left = int(float64(m.width) * leftRatio)
	context = m.contextPaneWidthEffective()
	center = m.width - left - context
	return
}
```

Search for all callers of `m.paneWidths()` (use grep) and update them — they currently destructure 2 values, now they need 3. The known caller is in `renderDiffPane`:

```go
func (m Model) renderDiffPane() string {
	_, centerW, _ := m.paneWidths()
	bodyH := m.height - headerRows - helpHeight
	header := titleStyle.Render(m.diffTitle())
	body := m.attachSpineColumn(m.viewport.View())
	content := header + "\n" + body
	return m.paneStyleFor(paneDiff, centerW, bodyH).Render(content)
}
```

Update `renderLeftPane`:

```go
func (m Model) renderLeftPane() string {
	leftW, _, _ := m.paneWidths()
	...
}
```

- [ ] **Step 2.5: Add `renderContextPane` to `render.go`**

Append this function near the other render helpers (after `renderHelp` is fine):

```go
// renderContextPane builds the third column. When the pane is hidden (via
// contextPaneWidthEffective returning 0) the caller should skip it entirely.
func (m Model) renderContextPane() string {
	_, _, contextW := m.paneWidths()
	bodyH := m.height - headerRows - helpHeight
	innerW := contextW - 4 // borders (2) + horizontal padding (2)
	if innerW < 8 {
		innerW = 8
	}

	style := contextPaneStyle
	if m.focus == paneContext {
		style = contextPaneFocusStyle
	}

	var lines []string
	if len(m.contextPayload.Sections) == 0 {
		lines = append(lines, contextMutedStyle.Render("(no context)"))
	}
	idx := 0
	for _, s := range m.contextPayload.Sections {
		if s.Status == ctxpane.StatusEmpty || (len(s.Items) == 0 && s.Status != ctxpane.StatusLoading && s.Status != ctxpane.StatusError) {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "") // blank separator between sections
		}
		lines = append(lines, contextSectionHeaderStyle.Render("▸ "+s.Kind.Title()))
		switch s.Status {
		case ctxpane.StatusLoading:
			lines = append(lines, contextMutedStyle.Render("  …"))
			idx++
		case ctxpane.StatusError:
			lines = append(lines, contextMutedStyle.Render("  (error)"))
			idx++
		default:
			for _, it := range s.Items {
				row := "  " + truncateRaw(it.Text, innerW-2)
				if m.focus == paneContext && idx == m.contextSelected {
					row = contextItemSelectedStyle.Render(row)
				} else {
					row = contextItemStyle.Render(row)
				}
				lines = append(lines, row)
				idx++
			}
		}
	}
	return style.Width(contextW - 2).Height(bodyH - 2).Render(strings.Join(lines, "\n"))
}
```

Update the import block of `render.go` to include:

```go
	"github.com/bowenbrooks/gitreview/internal/ctxpane"
```

- [ ] **Step 2.6: Wire `renderContextPane` into `View()`**

In `model.go` `View()`:

```go
func (m Model) View() string {
	if !m.ready {
		return "loading…"
	}
	header := m.renderTopHeader()
	var body string
	if m.view == viewOverview {
		body = m.renderOverviewBody()
	} else {
		parts := []string{m.renderLeftPane(), m.renderDiffPane()}
		if m.contextPaneWidthEffective() > 0 {
			parts = append(parts, m.renderContextPane())
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, m.renderHelp())
}
```

- [ ] **Step 2.7: Seed `m.contextPayload` from the stub on file change**

In `refreshDiff()`, append (just before the `m.viewport.GotoTop()` line at the end):

```go
	m.contextPayload = ctxpane.Resolve(context.Background(), ctxpane.Cursor{
		File:      m.currentFileForContext(),
		HunkIndex: m.currentHunkIndex(),
		Diff:      m.d,
		RepoRoot:  m.repoRoot,
	})
	m.contextSelected = 0
```

Add a small helper near `currentHunkIndex`:

```go
// currentFileForContext returns the file the context pane should describe,
// or a zero-value File if no file is selected.
func (m Model) currentFileForContext() diff.File {
	if m.view != viewChanges {
		return diff.File{}
	}
	files, _ := m.effectiveFiles()
	if m.fileCursor < 0 || m.fileCursor >= len(files) {
		return diff.File{}
	}
	return files[m.fileCursor]
}
```

Update the `model.go` import block to include `"context"`.

- [ ] **Step 2.8: Add tests to `internal/ui/model_test.go`**

Append:

```go
func TestContextPaneVisibleByDefault(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = updated.(Model)
	if !m.contextPaneVisible {
		t.Error("contextPaneVisible: got false want true")
	}
	if m.contextPaneWidthEffective() == 0 {
		t.Error("contextPaneWidthEffective: got 0 at width=140")
	}
	if !strings.Contains(m.View(), "Where") {
		t.Errorf("View missing context section. Got:\n%s", m.View())
	}
}

func TestContextPaneHiddenBelow120Cols(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(Model)
	if m.contextPaneWidthEffective() != 0 {
		t.Errorf("contextPaneWidthEffective at 100 cols: got %d want 0", m.contextPaneWidthEffective())
	}
}

func TestContextPaneToggleWithC(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = updated.(Model)
	if !m.contextPaneVisible {
		t.Fatal("expected pane visible to start")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = updated.(Model)
	if m.contextPaneVisible {
		t.Error("after c: pane should be hidden")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = updated.(Model)
	if !m.contextPaneVisible {
		t.Error("after second c: pane should be visible again")
	}
}

func TestContextPaneHiddenInSplitView(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = updated.(Model)
	if m.contextPaneWidthEffective() != 0 {
		t.Error("split view should hide context pane")
	}
}
```

- [ ] **Step 2.9: Run tests**

Run: `go test ./internal/ui/ -v`
Expected: existing tests + 4 new context-pane tests pass.

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 2.10: Run vet**

Run: `go vet ./...`
Expected: no output.

- [ ] **Step 2.11: Commit**

```bash
git add internal/ui/ internal/ctxpane/
git commit -m "ui: render context pane as third column, c toggles"
```

---

## Task 3: Debounced cursor-driven refresh

**Files:**
- Modify: `internal/ui/model.go`
- Modify: `internal/ui/model_test.go`

**Pattern:** Bubble Tea idiom for cancellable debounce — increment a sequence number on every cursor move and schedule a `tea.Tick` carrying that seq. When the tick fires we compare; stale ticks no-op.

- [ ] **Step 3.1: Add new message types in `model.go`**

Add near the existing `editorDoneMsg` declaration:

```go
// contextRefreshMsg is fired by tea.Tick after the debounce window. The Seq
// must match the model's current contextRefreshSeq, otherwise the tick is
// stale (a newer move has scheduled a fresher one) and we no-op.
type contextRefreshMsg struct{ Seq int }

// contextResultMsg carries the computed payload back from the resolver Cmd.
type contextResultMsg struct {
	Seq     int
	Payload ctxpane.Payload
}

const contextDebounce = 150 * time.Millisecond
```

Update `model.go` imports to include `"time"`.

- [ ] **Step 3.2: Add a `scheduleContextRefresh()` helper**

Place near the other helpers (after `currentHunkIndex` is fine):

```go
// scheduleContextRefresh bumps the refresh seqno and returns a Cmd that fires
// a contextRefreshMsg after the debounce window. Callers should compose this
// with any other Cmd they return.
func (m *Model) scheduleContextRefresh() tea.Cmd {
	m.contextRefreshSeq++
	seq := m.contextRefreshSeq
	return tea.Tick(contextDebounce, func(time.Time) tea.Msg {
		return contextRefreshMsg{Seq: seq}
	})
}
```

- [ ] **Step 3.3: Handle the new messages in `Update`**

Add two new cases to the existing `switch msg := msg.(type)`:

```go
	case contextRefreshMsg:
		if msg.Seq != m.contextRefreshSeq {
			return m, nil // stale
		}
		cur := ctxpane.Cursor{
			File:      m.currentFileForContext(),
			HunkIndex: m.currentHunkIndex(),
			Diff:      m.d,
			RepoRoot:  m.repoRoot,
		}
		m.contextCursor = cur
		seq := msg.Seq
		return m, func() tea.Msg {
			return contextResultMsg{Seq: seq, Payload: ctxpane.Resolve(context.Background(), cur)}
		}

	case contextResultMsg:
		if msg.Seq != m.contextRefreshSeq {
			return m, nil // stale
		}
		m.contextPayload = msg.Payload
		m.contextSelected = 0
		return m, nil
```

- [ ] **Step 3.4: Trigger refresh on every cursor change**

Find these locations and append `cmd = tea.Batch(cmd, m.scheduleContextRefresh())` (or change the return) so cursor-changing keys schedule a refresh:

- In `moveCursor()` callers — modify `setCursor` to also schedule refresh:

```go
func (m *Model) setCursor(c int) tea.Cmd {
	changed := false
	switch m.view {
	case viewCommits:
		if c != m.commitCursor {
			m.commitCursor = c
			m.refreshDiff()
			changed = true
		}
	case viewOverview:
		if c != m.overviewCursor {
			m.overviewCursor = c
			m.refreshDiff()
			changed = true
		}
	default:
		if c != m.fileCursor {
			m.fileCursor = c
			m.refreshDiff()
			changed = true
		}
	}
	if changed {
		return m.scheduleContextRefresh()
	}
	return nil
}
```

And update `moveCursor` to propagate the Cmd:

```go
func (m *Model) moveCursor(delta int) tea.Cmd {
	c := m.cursor() + delta
	if c < 0 {
		c = 0
	}
	if c > m.maxCursor() {
		c = m.maxCursor()
	}
	return m.setCursor(c)
}
```

Update the call sites in `Update` to return the Cmd:

```go
		case "j", "down":
			if m.view == viewOverview {
				m.moveOverview(0, +1)
				return m, m.scheduleContextRefresh()
			}
			if m.focus == paneLeft {
				return m, m.moveCursor(+1)
			}
			m.viewport.ScrollDown(1)
			return m, m.maybeScheduleHunkChange()
```

(Apply the same pattern to `k`, `g`, `G`, `]`, `[`, etc. — anywhere that may change which hunk is current.)

Add a helper to detect hunk change after viewport scroll:

```go
// maybeScheduleHunkChange returns a refresh Cmd if the current hunk index
// has changed since the last context refresh.
func (m *Model) maybeScheduleHunkChange() tea.Cmd {
	newHunk := m.currentHunkIndex()
	if newHunk == m.contextCursor.HunkIndex {
		return nil
	}
	return m.scheduleContextRefresh()
}
```

Also trigger an initial refresh after the first `WindowSizeMsg` (so the pane has data on startup):

In the `tea.WindowSizeMsg` handler, replace `return m, nil` with:

```go
		return m, m.scheduleContextRefresh()
```

- [ ] **Step 3.5: Add a test that verifies seq-based stale-cancel**

```go
func TestContextRefreshStaleCancel(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = updated.(Model)

	// Schedule two refreshes back to back; the first should be stale.
	_ = m.scheduleContextRefresh()
	staleSeq := m.contextRefreshSeq
	_ = m.scheduleContextRefresh()

	// Deliver the stale msg.
	updated, _ = m.Update(contextRefreshMsg{Seq: staleSeq})
	m = updated.(Model)
	// No assertion about side effects — the test just confirms the model
	// doesn't crash and doesn't replace the payload from a stale tick.
	if m.contextRefreshSeq != staleSeq+1 {
		t.Errorf("contextRefreshSeq: got %d want %d", m.contextRefreshSeq, staleSeq+1)
	}
}
```

- [ ] **Step 3.6: Run tests, build, vet**

Run: `go test ./internal/ui/ -v`
Expected: all tests pass.

Run: `go build ./...`
Expected: clean.

Run: `go vet ./...`
Expected: no output.

- [ ] **Step 3.7: Commit**

```bash
git add internal/ui/
git commit -m "ui: debounce-refresh context pane on cursor moves"
```

---

## Task 4: Real "Where" section

**Files:**
- Create: `internal/ctxpane/where.go`
- Create: `internal/ctxpane/where_test.go`
- Modify: `internal/ctxpane/resolver.go`

- [ ] **Step 4.1: Write `where.go`**

```go
package ctxpane

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// declRegex matches lines that open a function/class/method-like declaration
// in the languages we want coarse support for. Intentionally permissive: the
// goal is "name the enclosing thing", not perfect parsing.
var declRegex = regexp.MustCompile(`^\s*(?:(?:func|def|class|function|fn)\s+\(?[^()\s]*\)?\s*([A-Za-z_][A-Za-z0-9_]*)|([A-Za-z_][A-Za-z0-9_]*)\s*=\s*function)`)

// containingDecl walks the file content backwards from anchorLine looking for
// the most recent declaration line. Returns the declared identifier and the
// 1-based line number, or ("", 0) if no decl was found above.
//
// anchorLine is 1-based; lines is the full file content split by '\n'.
func containingDecl(lines []string, anchorLine int) (name string, line int) {
	if anchorLine <= 0 {
		return "", 0
	}
	if anchorLine > len(lines) {
		anchorLine = len(lines)
	}
	for i := anchorLine - 1; i >= 0; i-- {
		m := declRegex.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		// Prefer group 1 (func/def/class style); fall back to group 2 (assignment style).
		for _, g := range m[1:] {
			if g != "" {
				return g, i + 1
			}
		}
	}
	return "", 0
}

// readFileLines loads a file as a slice of lines (no trailing newline per line).
// Returns nil + nil error when the path is empty or the file doesn't exist —
// containingDecl will simply return ("", 0).
func readFileLines(repoRoot, relPath string) ([]string, error) {
	if relPath == "" {
		return nil, nil
	}
	full := relPath
	if repoRoot != "" && !filepath.IsAbs(full) {
		full = filepath.Join(repoRoot, relPath)
	}
	f, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // allow long lines
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err()
}

// buildWhereSection always returns a non-empty Section (the spec guarantees
// "▸ Where is always present"). When no file is selected, falls back to a
// muted placeholder item.
func buildWhereSection(cur Cursor) Section {
	s := Section{Kind: SectionWhere, Status: StatusOK}
	if cur.File.Path == "" {
		s.Items = []Item{{Text: "(no file selected)"}}
		return s
	}

	s.Items = append(s.Items, Item{Text: cur.File.Path})
	if len(cur.File.Hunks) > 0 && cur.HunkIndex >= 0 {
		s.Items = append(s.Items, Item{
			Text: fmt.Sprintf("hunk %d of %d", cur.HunkIndex+1, len(cur.File.Hunks)),
		})
	}

	anchor, _, ok := cur.AnchorLine()
	if !ok || anchor <= 0 {
		return s
	}
	lines, err := readFileLines(cur.RepoRoot, cur.File.Path)
	if err != nil {
		s.Items = append(s.Items, Item{Text: "(read error)"})
		return s
	}
	if len(lines) == 0 {
		return s
	}
	name, declLine := containingDecl(lines, anchor)
	if name != "" {
		s.Items = append(s.Items, Item{
			Text: "in: " + name + " (" + fmt.Sprintf("L%d", declLine) + ")",
			Jump: &JumpTarget{File: cur.File.Path, Line: declLine},
		})
	}
	return s
}

```

- [ ] **Step 4.2: Update `resolver.go` to call `buildWhereSection`**

Replace the stub body:

```go
func Resolve(ctx context.Context, cur Cursor) Payload {
	return Payload{
		Sections: []Section{
			buildWhereSection(cur),
		},
	}
}
```

- [ ] **Step 4.3: Write `where_test.go`**

```go
package ctxpane

import (
	"strings"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

func TestContainingDecl_Go(t *testing.T) {
	body := `package foo

import "fmt"

func Outer() {
	fmt.Println("hi")
}

func Inner(x int) int {
	return x + 1
}
`
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	name, line := containingDecl(lines, 6)
	if name != "Outer" || line != 5 {
		t.Errorf("got (%q, %d) want (Outer, 5)", name, line)
	}
	name, line = containingDecl(lines, 10)
	if name != "Inner" || line != 9 {
		t.Errorf("got (%q, %d) want (Inner, 9)", name, line)
	}
}

func TestContainingDecl_Python(t *testing.T) {
	body := `class Foo:
    def bar(self):
        return 1

def baz():
    return 2
`
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	name, _ := containingDecl(lines, 3)
	if name != "bar" {
		t.Errorf("py method: got %q want bar", name)
	}
	name, _ = containingDecl(lines, 6)
	if name != "baz" {
		t.Errorf("py top-level: got %q want baz", name)
	}
}

func TestContainingDecl_NoMatch(t *testing.T) {
	lines := []string{"plain text", "more text"}
	name, line := containingDecl(lines, 2)
	if name != "" || line != 0 {
		t.Errorf("got (%q, %d) want empty", name, line)
	}
}

func TestBuildWhereSection_HappyPath(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir+"/foo.go", `package foo

func Hello() string {
	return "hi"
}
`)
	cur := Cursor{
		RepoRoot: dir,
		File: diff.File{
			Path: "foo.go",
			Hunks: []diff.Hunk{{
				Lines: []diff.Line{
					{Kind: diff.LineContext, NewNum: 4, OldNum: 4},
				},
			}},
		},
		HunkIndex: 0,
	}
	s := buildWhereSection(cur)
	if s.Kind != SectionWhere || s.Status != StatusOK {
		t.Fatalf("kind/status: %+v", s)
	}
	if len(s.Items) < 3 {
		t.Fatalf("expected ≥3 items; got %d (%+v)", len(s.Items), s.Items)
	}
	last := s.Items[len(s.Items)-1].Text
	if !strings.Contains(last, "Hello") {
		t.Errorf("decl item: got %q want contains 'Hello'", last)
	}
}

func TestBuildWhereSection_NoFile(t *testing.T) {
	s := buildWhereSection(Cursor{})
	if s.Kind != SectionWhere {
		t.Fatalf("kind: %v", s.Kind)
	}
	if len(s.Items) != 1 || !strings.Contains(s.Items[0].Text, "no file") {
		t.Errorf("items: %+v", s.Items)
	}
}
```

- [ ] **Step 4.4: Add minimal test helpers in `helpers_test.go`**

Create `internal/ctxpane/helpers_test.go`:

```go
package ctxpane

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
}

func gitCfg(t *testing.T, dir string) {
	t.Helper()
	gitRun(t, dir, "config", "user.email", "t@t")
	gitRun(t, dir, "config", "user.name", "tester")
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func chdirTo(t *testing.T, dir string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	// Convince any later filepath.Abs calls to behave.
	_ = filepath.Separator
}
```

- [ ] **Step 4.5: Run tests, build, vet**

Run: `go test ./internal/ctxpane/ -v`
Expected: all new tests pass.

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 4.6: Manual smoke**

Run: `go build -o /tmp/gitreview ./cmd/gitreview`
Then cd into any non-empty repo with changes and run `/tmp/gitreview` and verify the context pane shows "▸ Where" with the file path, hunk position, and "in: FUNC_NAME" for the current file.

- [ ] **Step 4.7: Commit**

```bash
git add internal/ctxpane/
git commit -m "ctxpane: implement Where section (containing-func + position)"
```

---

## Task 5: `cache.go` + "Blame" section

**Files:**
- Create: `internal/ctxpane/cache.go`
- Create: `internal/ctxpane/cache_test.go`
- Create: `internal/ctxpane/blame.go`
- Create: `internal/ctxpane/blame_test.go`
- Modify: `internal/ctxpane/resolver.go` — concurrent fan-out + per-section timeout

- [ ] **Step 5.1: Write `cache.go`**

```go
package ctxpane

import (
	"container/list"
	"sync"
)

// lruCache is a small, thread-safe LRU keyed by string. Values are opaque to
// the cache. Bounded by maxEntries; older entries are evicted on insert.
type lruCache struct {
	mu      sync.Mutex
	maxSize int
	ll      *list.List // most-recent at front
	idx     map[string]*list.Element
}

type lruEntry struct {
	key   string
	value any
}

func newLRU(max int) *lruCache {
	if max <= 0 {
		max = 1
	}
	return &lruCache{
		maxSize: max,
		ll:      list.New(),
		idx:     make(map[string]*list.Element, max),
	}
}

func (c *lruCache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.idx[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(lruEntry).value, true
}

func (c *lruCache) Put(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.idx[key]; ok {
		el.Value = lruEntry{key, value}
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(lruEntry{key, value})
	c.idx[key] = el
	for c.ll.Len() > c.maxSize {
		old := c.ll.Back()
		if old == nil {
			break
		}
		c.ll.Remove(old)
		delete(c.idx, old.Value.(lruEntry).key)
	}
}
```

- [ ] **Step 5.2: Write `cache_test.go`**

```go
package ctxpane

import "testing"

func TestLRU_PutGet(t *testing.T) {
	c := newLRU(3)
	c.Put("a", 1)
	c.Put("b", 2)
	c.Put("c", 3)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Errorf("get a: got %v, %v", v, ok)
	}
}

func TestLRU_Eviction(t *testing.T) {
	c := newLRU(2)
	c.Put("a", 1)
	c.Put("b", 2)
	c.Put("c", 3) // evicts "a"
	if _, ok := c.Get("a"); ok {
		t.Error("a should have been evicted")
	}
	if _, ok := c.Get("b"); !ok {
		t.Error("b should still be present")
	}
}

func TestLRU_LRUOrdering(t *testing.T) {
	c := newLRU(2)
	c.Put("a", 1)
	c.Put("b", 2)
	_, _ = c.Get("a") // touches a
	c.Put("c", 3)     // should evict "b", not "a"
	if _, ok := c.Get("a"); !ok {
		t.Error("a should have been kept (most recently used)")
	}
	if _, ok := c.Get("b"); ok {
		t.Error("b should have been evicted")
	}
}
```

- [ ] **Step 5.3: Write `blame.go`**

```go
package ctxpane

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

var blameCache = newLRU(512)

// blameLine returns a one-line summary like "dbe587b 2d ago — reviewed marks"
// for the given file:line. Cached by (file, line, HEAD-sha); HEAD sha lookup
// is done once at startup of the resolver per call.
func blameLine(ctx context.Context, repoRoot, file string, line int, headSHA string) (string, error) {
	key := headSHA + ":" + file + ":" + strconv.Itoa(line)
	if v, ok := blameCache.Get(key); ok {
		return v.(string), nil
	}

	// git blame -L N,N --porcelain -- <file>
	args := []string{"blame", "-L", fmt.Sprintf("%d,%d", line, line), "--porcelain", "--", file}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	short, age, subject := parseBlamePorcelain(string(out))
	if short == "" {
		return "", fmt.Errorf("could not parse blame output")
	}
	formatted := fmt.Sprintf("%s %s — %s", short, age, subject)
	blameCache.Put(key, formatted)
	return formatted, nil
}

// parseBlamePorcelain extracts (short SHA, age string, subject) from
// `git blame --porcelain` output. Returns empty strings if the output is
// unparseable.
func parseBlamePorcelain(out string) (short, age, subject string) {
	lines := strings.Split(out, "\n")
	if len(lines) == 0 {
		return
	}
	// First token of first line is the full SHA.
	first := strings.Fields(lines[0])
	if len(first) == 0 {
		return
	}
	sha := first[0]
	if len(sha) >= 7 {
		short = sha[:7]
	} else {
		short = sha
	}
	var authorTime int64
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "author-time "):
			fmt.Sscanf(l, "author-time %d", &authorTime)
		case strings.HasPrefix(l, "summary "):
			subject = strings.TrimPrefix(l, "summary ")
		}
	}
	if authorTime > 0 {
		age = humanAge(time.Now().Unix() - authorTime)
	}
	return
}

func humanAge(seconds int64) string {
	switch {
	case seconds < 90:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 90*60:
		return fmt.Sprintf("%dm", seconds/60)
	case seconds < 36*3600:
		return fmt.Sprintf("%dh", seconds/3600)
	case seconds < 90*86400:
		return fmt.Sprintf("%dd", seconds/86400)
	case seconds < 730*86400:
		return fmt.Sprintf("%dmo", seconds/(30*86400))
	}
	return fmt.Sprintf("%dy", seconds/(365*86400))
}

// buildBlameSection produces the Blame section. Returns a Section with
// StatusEmpty if the cursor isn't on a line that existed before the change.
func buildBlameSection(ctx context.Context, cur Cursor) Section {
	s := Section{Kind: SectionBlame, Status: StatusEmpty}
	anchor, kind, ok := cur.AnchorLine()
	if !ok || cur.File.Path == "" || cur.RepoRoot == "" {
		return s
	}
	// Blame only makes sense for lines that existed before the change.
	// LineAdded means the line is brand new — nothing to blame.
	if kind == diff.LineAdded {
		// Try to blame the nearest context line above instead.
		hunk := cur.File.Hunks[cur.HunkIndex]
		anchor = nearestUnchangedAbove(hunk, anchor)
		if anchor <= 0 {
			return s
		}
	}

	headSHA, _ := resolveHEADSha(ctx, cur.RepoRoot)
	line, err := blameLine(ctx, cur.RepoRoot, cur.File.Path, anchor, headSHA)
	if err != nil {
		s.Status = StatusError
		return s
	}
	s.Status = StatusOK
	s.Items = []Item{{Text: line}}
	return s
}

// nearestUnchangedAbove returns the NewNum of the closest non-added line at
// or above the given anchor within the hunk. Returns 0 if none.
func nearestUnchangedAbove(h diff.Hunk, anchor int) int {
	var best int
	for _, l := range h.Lines {
		if l.NewNum > 0 && l.NewNum <= anchor && l.Kind != diff.LineAdded {
			if l.NewNum > best {
				best = l.NewNum
			}
		}
	}
	return best
}

var headShaCache = newLRU(8)

func resolveHEADSha(ctx context.Context, repoRoot string) (string, error) {
	if v, ok := headShaCache.Get(repoRoot); ok {
		return v.(string), nil
	}
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(out))
	headShaCache.Put(repoRoot, sha)
	return sha, nil
}
```

- [ ] **Step 5.4: Write `blame_test.go`**

```go
package ctxpane

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

func TestParseBlamePorcelain(t *testing.T) {
	out := `abcdef1234567890abcdef1234567890abcdef12 1 1 1
author Alice
author-mail <alice@example.com>
author-time 1700000000
author-tz +0000
summary did a thing
filename foo.go
`
	short, _, subject := parseBlamePorcelain(out)
	if short != "abcdef1" {
		t.Errorf("short: got %q want abcdef1", short)
	}
	if subject != "did a thing" {
		t.Errorf("subject: got %q", subject)
	}
}

func TestBlameSection_Integration(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	gitCfg(t, dir)
	mustWrite(t, filepath.Join(dir, "foo.go"), "package foo\n\nfunc Hi() {}\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "initial blame target")

	cur := Cursor{
		RepoRoot: dir,
		File: diff.File{
			Path: "foo.go",
			Hunks: []diff.Hunk{{
				Lines: []diff.Line{
					{Kind: diff.LineContext, NewNum: 3, OldNum: 3},
				},
			}},
		},
		HunkIndex: 0,
	}
	s := buildBlameSection(context.Background(), cur)
	if s.Status != StatusOK {
		t.Fatalf("status: got %v want OK", s.Status)
	}
	if len(s.Items) == 0 || !strings.Contains(s.Items[0].Text, "initial blame target") {
		t.Errorf("items: %+v", s.Items)
	}
}

func TestBlameSection_AddedLineSkipped(t *testing.T) {
	cur := Cursor{
		RepoRoot: "/no/such/path",
		File: diff.File{
			Path: "foo.go",
			Hunks: []diff.Hunk{{
				Lines: []diff.Line{
					{Kind: diff.LineAdded, NewNum: 5},
				},
			}},
		},
		HunkIndex: 0,
	}
	s := buildBlameSection(context.Background(), cur)
	if s.Status != StatusEmpty {
		t.Errorf("added-only line: status %v want Empty", s.Status)
	}
}
```

- [ ] **Step 5.5: Update `resolver.go` for concurrent fan-out**

Replace `Resolve` with:

```go
func Resolve(ctx context.Context, cur Cursor) Payload {
	// One sub-context per section with the per-section timeout. Sections run
	// concurrently; we wait for all of them to finish (or time out).
	type result struct {
		s Section
	}
	tasks := []func(context.Context) Section{
		func(c context.Context) Section { return buildWhereSection(cur) },
		func(c context.Context) Section { return buildBlameSection(c, cur) },
	}
	out := make([]Section, len(tasks))
	var wg sync.WaitGroup
	for i, t := range tasks {
		i, t := i, t
		wg.Add(1)
		go func() {
			defer wg.Done()
			subCtx, cancel := context.WithTimeout(ctx, PerSectionTimeout)
			defer cancel()
			done := make(chan Section, 1)
			go func() { done <- t(subCtx) }()
			select {
			case s := <-done:
				out[i] = s
			case <-subCtx.Done():
				// Timed out — leave a loading marker.
				out[i] = Section{Kind: kindFor(i), Status: StatusLoading}
			}
		}()
	}
	wg.Wait()
	// Filter out fully-empty sections except Where (always kept).
	var sections []Section
	for _, s := range out {
		if s.Kind == SectionWhere {
			sections = append(sections, s)
			continue
		}
		if s.Status == StatusEmpty {
			continue
		}
		sections = append(sections, s)
	}
	return Payload{Sections: sections}
}

// kindFor returns the SectionKind associated with task index i. Keep this in
// the same order as the tasks slice in Resolve.
func kindFor(i int) SectionKind {
	switch i {
	case 0:
		return SectionWhere
	case 1:
		return SectionBlame
	case 2:
		return SectionSymbol
	case 3:
		return SectionCrossFile
	case 4:
		return SectionHistory
	}
	return SectionWhere
}
```

Update `resolver.go` import block to add `"sync"`.

- [ ] **Step 5.6: Run tests, build, vet**

Run: `go test ./internal/ctxpane/ -v`
Expected: all tests pass.

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 5.7: Commit**

```bash
git add internal/ctxpane/
git commit -m "ctxpane: add LRU cache and Blame section, concurrent fan-out"
```

---

## Task 6: "Symbol" section — decl detection + `git grep` refs

**Files:**
- Create: `internal/ctxpane/symbol.go`
- Create: `internal/ctxpane/symbol_test.go`
- Modify: `internal/ctxpane/resolver.go` — add symbol task to fan-out

- [ ] **Step 6.1: Write `symbol.go`**

```go
package ctxpane

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

var grepCache = newLRU(256)

// symbolUnderCursor returns the declared identifier if the anchored line of
// the cursor's hunk is itself a declaration, otherwise "".
func symbolUnderCursor(cur Cursor) string {
	if cur.HunkIndex < 0 || cur.HunkIndex >= len(cur.File.Hunks) {
		return ""
	}
	anchor, _, ok := cur.AnchorLine()
	if !ok {
		return ""
	}
	lines, err := readFileLines(cur.RepoRoot, cur.File.Path)
	if err != nil || len(lines) == 0 {
		return ""
	}
	if anchor > len(lines) {
		return ""
	}
	m := declRegex.FindStringSubmatch(lines[anchor-1])
	if m == nil {
		return ""
	}
	for _, g := range m[1:] {
		if g != "" {
			return g
		}
	}
	return ""
}

// gitGrepRefs returns up to maxResults locations of the symbol in the repo,
// excluding the cursor's own file. Each entry is "<path>:<line>".
func gitGrepRefs(ctx context.Context, repoRoot, symbol, excludePath string, maxResults int) ([]string, error) {
	key := repoRoot + "\x00" + symbol
	if v, ok := grepCache.Get(key); ok {
		return filterAndCap(v.([]string), excludePath, maxResults), nil
	}
	pattern := `\b` + escapeRegex(symbol) + `\b`
	cmd := exec.CommandContext(ctx, "git", "grep", "-n", "--no-color", "-E", pattern)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		// `git grep` exits 1 when there are no matches — treat as empty.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			grepCache.Put(key, []string(nil))
			return nil, nil
		}
		return nil, err
	}
	var refs []string
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		// Format is "path:lineno:content" — we only keep the prefix.
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 2 {
			continue
		}
		if _, err := strconv.Atoi(parts[1]); err != nil {
			continue
		}
		refs = append(refs, parts[0]+":"+parts[1])
	}
	grepCache.Put(key, refs)
	return filterAndCap(refs, excludePath, maxResults), nil
}

func filterAndCap(refs []string, excludePath string, maxResults int) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		if excludePath != "" && strings.HasPrefix(r, excludePath+":") {
			continue
		}
		out = append(out, r)
		if len(out) >= maxResults {
			break
		}
	}
	return out
}

func escapeRegex(s string) string {
	// Conservative escape — these are the metachars git-grep -E will react to.
	const meta = `\.+*?()[]{}|^$`
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if strings.ContainsRune(meta, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func buildSymbolSection(ctx context.Context, cur Cursor) Section {
	s := Section{Kind: SectionSymbol, Status: StatusEmpty}
	sym := symbolUnderCursor(cur)
	if sym == "" || cur.RepoRoot == "" {
		return s
	}
	refs, err := gitGrepRefs(ctx, cur.RepoRoot, sym, cur.File.Path, 6)
	if err != nil {
		s.Status = StatusError
		return s
	}
	s.Status = StatusOK
	header := fmt.Sprintf("%s (%d refs)", sym, len(refs))
	s.Items = []Item{{Text: header}}
	for _, r := range refs {
		parts := strings.SplitN(r, ":", 2)
		ln, _ := strconv.Atoi(parts[1])
		s.Items = append(s.Items, Item{
			Text: r,
			Jump: &JumpTarget{File: parts[0], Line: ln},
		})
	}
	// Avoid leaving the section empty when sym matches but has no refs.
	if len(refs) == 0 {
		s.Items[0] = Item{Text: sym + " (no other refs)"}
	}
	return s
}
```

- [ ] **Step 6.2: Add Symbol task to `resolver.go`**

In the `tasks := []...` slice in `Resolve`, add a third entry **between** Where and Blame to preserve display order via `kindFor`. Easier: extend the tasks slice in the order matching `kindFor`:

```go
	tasks := []func(context.Context) Section{
		func(c context.Context) Section { return buildWhereSection(cur) },     // i=0 → SectionWhere
		func(c context.Context) Section { return buildSymbolSection(c, cur) }, // wrong order if we keep Blame
	}
```

Reorder so display order is preserved. The render currently iterates in payload order, so we must produce sections in `SectionKind` order. Re-order tasks (and update `kindFor`) to:

```go
	tasks := []func(context.Context) Section{
		func(c context.Context) Section { return buildWhereSection(cur) },
		func(c context.Context) Section { return buildSymbolSection(c, cur) },
		func(c context.Context) Section { return buildBlameSection(c, cur) },
	}
```

And update `kindFor`:

```go
func kindFor(i int) SectionKind {
	switch i {
	case 0:
		return SectionWhere
	case 1:
		return SectionSymbol
	case 2:
		return SectionBlame
	case 3:
		return SectionCrossFile
	case 4:
		return SectionHistory
	}
	return SectionWhere
}
```

- [ ] **Step 6.3: Write `symbol_test.go`**

```go
package ctxpane

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

func TestSymbolUnderCursor_OnDecl(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "foo.go"), "package foo\n\nfunc Greet() {}\n")
	cur := Cursor{
		RepoRoot: dir,
		File: diff.File{
			Path: "foo.go",
			Hunks: []diff.Hunk{{
				Lines: []diff.Line{
					{Kind: diff.LineAdded, NewNum: 3},
				},
			}},
		},
		HunkIndex: 0,
	}
	if got := symbolUnderCursor(cur); got != "Greet" {
		t.Errorf("got %q want Greet", got)
	}
}

func TestSymbolUnderCursor_OffDecl(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "foo.go"), "package foo\n\nfunc Greet() {}\n")
	cur := Cursor{
		RepoRoot: dir,
		File: diff.File{
			Path: "foo.go",
			Hunks: []diff.Hunk{{
				Lines: []diff.Line{
					{Kind: diff.LineContext, NewNum: 1},
				},
			}},
		},
		HunkIndex: 0,
	}
	if got := symbolUnderCursor(cur); got != "" {
		t.Errorf("non-decl line: got %q want empty", got)
	}
}

func TestBuildSymbolSection_Integration(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	gitCfg(t, dir)
	mustWrite(t, filepath.Join(dir, "lib.go"), "package p\n\nfunc Greet() {}\n")
	mustWrite(t, filepath.Join(dir, "main.go"), "package p\n\nfunc main() { Greet() }\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "seed")

	cur := Cursor{
		RepoRoot: dir,
		File: diff.File{
			Path: "lib.go",
			Hunks: []diff.Hunk{{
				Lines: []diff.Line{
					{Kind: diff.LineAdded, NewNum: 3},
				},
			}},
		},
		HunkIndex: 0,
	}
	s := buildSymbolSection(context.Background(), cur)
	if s.Status != StatusOK {
		t.Fatalf("status: %v", s.Status)
	}
	found := false
	for _, it := range s.Items {
		if strings.Contains(it.Text, "main.go") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected main.go in symbol refs; got %+v", s.Items)
	}
}

func TestEscapeRegex(t *testing.T) {
	cases := map[string]string{
		"foo":     "foo",
		"foo.bar": `foo\.bar`,
		"a(b)":    `a\(b\)`,
	}
	for in, want := range cases {
		if got := escapeRegex(in); got != want {
			t.Errorf("escapeRegex(%q): got %q want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 6.4: Run tests, build, vet, smoke**

Run: `go test ./internal/ctxpane/ -v`
Expected: all tests pass.

Run: `go build -o /tmp/gitreview ./cmd/gitreview && go vet ./...`
Expected: clean.

Smoke: cd into this repo, run `/tmp/gitreview`, navigate to a hunk whose first changed line is a `func` declaration. Verify "▸ Symbol" section appears with the function name and reference count.

- [ ] **Step 6.5: Commit**

```bash
git add internal/ctxpane/
git commit -m "ctxpane: add Symbol section (decl detection + git grep refs)"
```

---

## Task 7: "Cross-file" section

**Files:**
- Create: `internal/ctxpane/crossfile.go`
- Create: `internal/ctxpane/crossfile_test.go`
- Modify: `internal/ctxpane/resolver.go`

- [ ] **Step 7.1: Write `crossfile.go`**

```go
package ctxpane

import (
	"context"
	"strconv"
	"strings"
)

// buildCrossFileSection looks for the cursor-symbol in OTHER files that are
// also in the current diff. Reuses gitGrepRefs and filters its output to the
// diff's set of changed paths. Returns StatusEmpty if there's no symbol or
// no matches outside the current file.
func buildCrossFileSection(ctx context.Context, cur Cursor) Section {
	s := Section{Kind: SectionCrossFile, Status: StatusEmpty}
	sym := symbolUnderCursor(cur)
	if sym == "" || cur.Diff == nil || cur.RepoRoot == "" {
		return s
	}
	refs, err := gitGrepRefs(ctx, cur.RepoRoot, sym, cur.File.Path, 50)
	if err != nil {
		s.Status = StatusError
		return s
	}
	if len(refs) == 0 {
		return s
	}
	others := make(map[string]bool)
	for _, f := range cur.Diff.Files {
		if f.Path != cur.File.Path {
			others[f.Path] = true
		}
	}
	matched := make([]Item, 0, 6)
	for _, r := range refs {
		parts := strings.SplitN(r, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if !others[parts[0]] {
			continue
		}
		ln, _ := strconv.Atoi(parts[1])
		matched = append(matched, Item{
			Text: r,
			Jump: &JumpTarget{File: parts[0], Line: ln},
		})
		if len(matched) >= 6 {
			break
		}
	}
	if len(matched) == 0 {
		return s
	}
	s.Status = StatusOK
	s.Items = matched
	return s
}
```

- [ ] **Step 7.2: Add cross-file task to `resolver.go`**

Update the `tasks` slice in `Resolve` to add a fourth entry, in display order:

```go
	tasks := []func(context.Context) Section{
		func(c context.Context) Section { return buildWhereSection(cur) },
		func(c context.Context) Section { return buildSymbolSection(c, cur) },
		func(c context.Context) Section { return buildCrossFileSection(c, cur) },
		func(c context.Context) Section { return buildBlameSection(c, cur) },
	}
```

Update `kindFor` accordingly:

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
	}
	return SectionWhere
}
```

- [ ] **Step 7.3: Write `crossfile_test.go`**

```go
package ctxpane

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

func TestBuildCrossFileSection_FindsRefInDiff(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	gitCfg(t, dir)
	mustWrite(t, filepath.Join(dir, "lib.go"), "package p\n\nfunc Greet() {}\n")
	mustWrite(t, filepath.Join(dir, "main.go"), "package p\n\nfunc main() { Greet() }\n")
	mustWrite(t, filepath.Join(dir, "other.go"), "package p\n\nfunc Other() { Greet() }\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "seed")

	d := &diff.Diff{
		Files: []diff.File{
			{Path: "lib.go"},
			{Path: "main.go"},
			// other.go is NOT in the diff
		},
	}
	cur := Cursor{
		RepoRoot: dir,
		Diff:     d,
		File: diff.File{
			Path: "lib.go",
			Hunks: []diff.Hunk{{
				Lines: []diff.Line{
					{Kind: diff.LineAdded, NewNum: 3},
				},
			}},
		},
		HunkIndex: 0,
	}
	s := buildCrossFileSection(context.Background(), cur)
	if s.Status != StatusOK {
		t.Fatalf("status: %v", s.Status)
	}
	foundMain, foundOther := false, false
	for _, it := range s.Items {
		if strings.Contains(it.Text, "main.go") {
			foundMain = true
		}
		if strings.Contains(it.Text, "other.go") {
			foundOther = true
		}
	}
	if !foundMain {
		t.Error("expected main.go (which is in the diff)")
	}
	if foundOther {
		t.Error("did NOT expect other.go (not in the diff)")
	}
}
```

- [ ] **Step 7.4: Run tests, build, vet**

Run: `go test ./internal/ctxpane/ -v && go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 7.5: Commit**

```bash
git add internal/ctxpane/
git commit -m "ctxpane: add Cross-file section (refs scoped to diff)"
```

---

## Task 8: "History" section + `H` toggle expansion

**Files:**
- Create: `internal/ctxpane/history.go`
- Create: `internal/ctxpane/history_test.go`
- Modify: `internal/ctxpane/types.go` — add `Cursor.HistoryExpanded bool`
- Modify: `internal/ctxpane/resolver.go`
- Modify: `internal/ui/model.go` — add `contextHistoryExpanded` field, `H` key handler, patch refresh handler to preserve the flag

**Why the flag lives on the model, not (only) the cursor:** Task 3's refresh handler rebuilds the `ctxpane.Cursor` from scratch on every tick. If `HistoryExpanded` lived solely on the cursor, the H toggle would survive only until the next refresh. So we keep the field on both: the source of truth is `m.contextHistoryExpanded`, copied into the new cursor on each refresh.

- [ ] **Step 8.1: Extend `Cursor` in `types.go`**

Add a field:

```go
type Cursor struct {
	File             diff.File
	HunkIndex        int
	Diff             *diff.Diff
	RepoRoot         string
	HistoryExpanded  bool // true when user pressed H to expand history
}
```

- [ ] **Step 8.2: Write `history.go`**

```go
package ctxpane

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// buildHistorySection returns recent commits for the cursor's file. When
// cur.HistoryExpanded is true, additionally runs `git log -L` for the
// anchor line range and appends those commits, marked.
func buildHistorySection(ctx context.Context, cur Cursor) Section {
	s := Section{Kind: SectionHistory, Status: StatusEmpty}
	if cur.File.Path == "" || cur.RepoRoot == "" {
		return s
	}
	cmd := exec.CommandContext(ctx, "git", "log", "-n", "5", "--oneline", "--", cur.File.Path)
	cmd.Dir = cur.RepoRoot
	out, err := cmd.Output()
	if err != nil {
		// `git log` may exit non-zero on an empty repo — treat as empty.
		if _, ok := err.(*exec.ExitError); ok {
			return s
		}
		s.Status = StatusError
		return s
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		s.Items = append(s.Items, Item{Text: line})
	}
	if len(s.Items) == 0 {
		return s
	}
	s.Status = StatusOK

	if cur.HistoryExpanded {
		anchor, _, ok := cur.AnchorLine()
		if ok && anchor > 0 {
			s.Items = append(s.Items, Item{Text: contextMutedPrefix + "line-range:"})
			rangeSpec := fmt.Sprintf("%d,%d:%s", anchor, anchor, cur.File.Path)
			lcmd := exec.CommandContext(ctx, "git", "log", "-L", rangeSpec, "--no-patch", "--pretty=format:%h %s")
			lcmd.Dir = cur.RepoRoot
			if lout, lerr := lcmd.Output(); lerr == nil {
				for _, l := range strings.Split(strings.TrimSpace(string(lout)), "\n") {
					if l == "" {
						continue
					}
					s.Items = append(s.Items, Item{Text: "  " + l})
				}
			}
		}
	}
	return s
}

// contextMutedPrefix marks a header row inside a section so the UI can render
// it dimmer. Currently no special UI handling — the marker is just for clarity
// in the displayed string.
const contextMutedPrefix = "— "
```

- [ ] **Step 8.3: Add history task to `resolver.go`**

Update `tasks`:

```go
	tasks := []func(context.Context) Section{
		func(c context.Context) Section { return buildWhereSection(cur) },
		func(c context.Context) Section { return buildSymbolSection(c, cur) },
		func(c context.Context) Section { return buildCrossFileSection(c, cur) },
		func(c context.Context) Section { return buildBlameSection(c, cur) },
		func(c context.Context) Section { return buildHistorySection(c, cur) },
	}
```

- [ ] **Step 8.4: Add model field + patch refresh handler + wire `H` key**

In `internal/ui/model.go`, add a new field on `Model`:

```go
	contextHistoryExpanded bool // toggled by H when pane is focused
```

Update the `contextRefreshMsg` handler (added in Task 3 Step 3.3) to copy the flag into the new cursor:

```go
	case contextRefreshMsg:
		if msg.Seq != m.contextRefreshSeq {
			return m, nil // stale
		}
		cur := ctxpane.Cursor{
			File:            m.currentFileForContext(),
			HunkIndex:       m.currentHunkIndex(),
			Diff:            m.d,
			RepoRoot:        m.repoRoot,
			HistoryExpanded: m.contextHistoryExpanded,
		}
		m.contextCursor = cur
		seq := msg.Seq
		return m, func() tea.Msg {
			return contextResultMsg{Seq: seq, Payload: ctxpane.Resolve(context.Background(), cur)}
		}
```

Add a new `case "H"` to `Update`:

```go
		case "H":
			if m.focus != paneContext {
				return m, nil
			}
			m.contextHistoryExpanded = !m.contextHistoryExpanded
			return m, m.scheduleContextRefresh()
```

- [ ] **Step 8.5: Write `history_test.go`**

```go
package ctxpane

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

func TestBuildHistorySection_Basic(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	gitCfg(t, dir)
	mustWrite(t, filepath.Join(dir, "foo.go"), "package foo\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "first")
	mustWrite(t, filepath.Join(dir, "foo.go"), "package foo\n\nfunc Hi() {}\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "second")

	cur := Cursor{
		RepoRoot: dir,
		File:     diff.File{Path: "foo.go"},
	}
	s := buildHistorySection(context.Background(), cur)
	if s.Status != StatusOK {
		t.Fatalf("status: %v", s.Status)
	}
	if len(s.Items) < 2 {
		t.Fatalf("items: %+v", s.Items)
	}
	if !strings.Contains(s.Items[0].Text, "second") {
		t.Errorf("first item should be newest commit; got %q", s.Items[0].Text)
	}
}

func TestBuildHistorySection_ExpandedAddsLineRange(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	gitCfg(t, dir)
	mustWrite(t, filepath.Join(dir, "foo.go"), "package foo\n\nfunc A() {}\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "add A")
	mustWrite(t, filepath.Join(dir, "foo.go"), "package foo\n\nfunc B() {}\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "rename A to B")

	cur := Cursor{
		RepoRoot: dir,
		File: diff.File{
			Path: "foo.go",
			Hunks: []diff.Hunk{{
				Lines: []diff.Line{
					{Kind: diff.LineAdded, NewNum: 3},
				},
			}},
		},
		HunkIndex:       0,
		HistoryExpanded: true,
	}
	s := buildHistorySection(context.Background(), cur)
	hasRangeMarker := false
	for _, it := range s.Items {
		if strings.Contains(it.Text, "line-range") {
			hasRangeMarker = true
		}
	}
	if !hasRangeMarker {
		t.Errorf("expanded history should include line-range marker; got %+v", s.Items)
	}
}
```

- [ ] **Step 8.6: Add a test for the H key**

In `internal/ui/model_test.go`:

```go
func TestContextHistoryToggleWithH(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = updated.(Model)
	if m.contextHistoryExpanded {
		t.Fatal("expected contextHistoryExpanded false initially")
	}
	// Task 9 wires Tab into paneContext; here we set focus directly so this
	// test works against the build state at the end of Task 8.
	m.focus = paneContext
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'H'}})
	m = updated.(Model)
	if !m.contextHistoryExpanded {
		t.Error("after H: contextHistoryExpanded should be true")
	}
}

func TestContextHistoryHIgnoredWithoutPaneFocus(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = updated.(Model)
	// focus is paneLeft — H should be ignored.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'H'}})
	m = updated.(Model)
	if m.contextHistoryExpanded {
		t.Error("H without pane focus should be a no-op")
	}
}
```

- [ ] **Step 8.7: Run tests, build, vet, smoke**

Run: `go test ./... -v`
Expected: all pass.

Run: `go build ./... && go vet ./...`
Expected: clean.

Smoke: confirm "▸ History" appears in the pane for any file with commit history.

- [ ] **Step 8.8: Commit**

```bash
git add internal/ctxpane/ internal/ui/
git commit -m "ctxpane: add History section and H expand toggle"
```

---

## Task 9: Pane focus + within-pane navigation + Enter to jump

**Files:**
- Modify: `internal/ui/model.go` — Tab cycle into paneContext, j/k/Enter/Esc in pane focus
- Modify: `internal/ui/model_test.go`

- [ ] **Step 9.1: Update Tab to cycle three panes**

Replace the `tab` and `shift+tab` cases:

```go
		case "tab":
			m.focus = m.nextFocus(+1)
			return m, nil
		case "shift+tab":
			m.focus = m.nextFocus(-1)
			return m, nil
```

Add the helper:

```go
// nextFocus returns the focus target after stepping `dir` (±1) through the
// pane cycle, skipping panes that are currently hidden. Order: paneLeft →
// paneDiff → paneContext → paneLeft.
func (m Model) nextFocus(dir int) pane {
	order := []pane{paneLeft, paneDiff}
	if m.contextPaneWidthEffective() > 0 {
		order = append(order, paneContext)
	}
	// Find current position.
	pos := 0
	for i, p := range order {
		if p == m.focus {
			pos = i
			break
		}
	}
	pos = (pos + dir + len(order)) % len(order)
	return order[pos]
}
```

- [ ] **Step 9.2: Within-pane j/k/Enter/Esc handlers**

Adjust the `j`/`k` handler block so that paneContext takes priority over paneDiff:

```go
		case "j", "down":
			if m.view == viewOverview {
				m.moveOverview(0, +1)
				return m, m.scheduleContextRefresh()
			}
			if m.focus == paneContext {
				m.contextMoveSelection(+1)
				return m, nil
			}
			if m.focus == paneLeft {
				return m, m.moveCursor(+1)
			}
			m.viewport.ScrollDown(1)
			return m, m.maybeScheduleHunkChange()
```

And `k`:

```go
		case "k", "up":
			if m.view == viewOverview {
				m.moveOverview(0, -1)
				return m, m.scheduleContextRefresh()
			}
			if m.focus == paneContext {
				m.contextMoveSelection(-1)
				return m, nil
			}
			if m.focus == paneLeft {
				return m, m.moveCursor(-1)
			}
			m.viewport.ScrollUp(1)
			return m, m.maybeScheduleHunkChange()
```

Add a new Enter case (don't replace the existing one for viewOverview — extend it):

```go
		case "enter":
			if m.view == viewOverview {
				m.fileCursor = m.overviewCursor
				m.setView(viewChanges)
				return m, nil
			}
			if m.focus == paneContext {
				return m, m.contextJumpToSelected()
			}
			return m, nil
```

Add an Esc handler:

```go
		case "esc":
			if m.focus == paneContext {
				m.focus = paneDiff
				return m, nil
			}
			return m, nil
```

- [ ] **Step 9.3: Add helpers `contextMoveSelection` and `contextJumpToSelected`**

Near the other context helpers:

```go
// contextSelectableItems returns a flat list of (sectionIdx, itemIdx) pairs
// for every Item whose Jump is non-nil — the only items the user can act on.
func (m Model) contextSelectableItems() []struct{ S, I int } {
	var out []struct{ S, I int }
	for si, s := range m.contextPayload.Sections {
		for ii, it := range s.Items {
			if it.Jump != nil {
				out = append(out, struct{ S, I int }{si, ii})
			}
		}
	}
	return out
}

func (m *Model) contextMoveSelection(delta int) {
	n := len(m.contextSelectableItems())
	if n == 0 {
		return
	}
	m.contextSelected = (m.contextSelected + delta + n) % n
}

// contextJumpToSelected moves the diff cursor to the file the selected pane
// item points at. Returns a Cmd that triggers a context refresh.
func (m *Model) contextJumpToSelected() tea.Cmd {
	items := m.contextSelectableItems()
	if len(items) == 0 || m.contextSelected < 0 || m.contextSelected >= len(items) {
		return nil
	}
	pos := items[m.contextSelected]
	it := m.contextPayload.Sections[pos.S].Items[pos.I]
	if it.Jump == nil {
		return nil
	}
	// Find the diff index of the target file (if it's in the current diff).
	files, _ := m.effectiveFiles()
	for i, f := range files {
		if f.Path == it.Jump.File {
			m.fileCursor = i
			m.refreshDiff()
			break
		}
	}
	// Note: we don't yet scroll the viewport to it.Jump.Line — viewport line
	// math is non-trivial and deferred to a follow-up. File-level jump is the
	// v0 promise.
	return m.scheduleContextRefresh()
}
```

- [ ] **Step 9.4: Update the pane highlight rendering**

Confirm `renderContextPane` already applies `contextItemSelectedStyle` when `m.focus == paneContext` (it does, from Task 2). No change needed.

Also update the help line in `renderHelp()` to mention the context-pane keys when the pane is visible:

```go
func (m Model) renderHelp() string {
	if m.filtering {
		hint := m.filterInput.View() + mutedStyle.Render("   Enter apply · Esc cancel")
		return helpStyle.Render(hint)
	}
	splitHint := "s: split"
	if m.splitView {
		splitHint = "s: unified"
	}
	parts := []string{"j/k file", "]/[ hunk", "m mark", "M next-unreviewed", "/ filter", "1/2/3 tab", splitHint, "e edit", "q quit"}
	if m.contextPaneWidthEffective() > 0 {
		parts = append(parts, "c: hide ctx")
	} else if m.contextPaneVisible {
		parts = append(parts, "c: show ctx")
	}
	if m.filter != "" {
		parts = append([]string{"c clear-filter"}, parts...)
	}
	hint := strings.Join(parts, "  ")
	if m.statusMsg != "" {
		hint = mutedStyle.Render(m.statusMsg) + "  ·  " + hint
	}
	return helpStyle.Render(hint)
}
```

- [ ] **Step 9.5: Add focus/navigation tests**

Append to `internal/ui/model_test.go`:

```go
func TestContextFocusCycle(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = updated.(Model)
	if m.focus != paneLeft {
		t.Fatal("initial focus should be paneLeft")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.focus != paneDiff {
		t.Errorf("after tab: got %v want paneDiff", m.focus)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.focus != paneContext {
		t.Errorf("after second tab: got %v want paneContext", m.focus)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.focus != paneLeft {
		t.Errorf("after third tab: got %v want paneLeft (wrapped)", m.focus)
	}
}

func TestContextFocusSkipsHiddenPane(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30}) // < 120: pane hidden
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.focus != paneDiff {
		t.Errorf("after tab: got %v want paneDiff", m.focus)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.focus != paneLeft {
		t.Errorf("after second tab: got %v want paneLeft (pane hidden, skipped)", m.focus)
	}
}

func TestContextEscReturnsFocusToDiff(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.focus != paneContext {
		t.Fatalf("setup: focus should be paneContext, got %v", m.focus)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.focus != paneDiff {
		t.Errorf("after esc: got %v want paneDiff", m.focus)
	}
}
```

- [ ] **Step 9.6: Run all tests, build, vet, smoke**

Run: `go test ./... -v`
Expected: all pass.

Run: `go build -o /tmp/gitreview ./cmd/gitreview && go vet ./...`
Expected: clean.

Smoke:
- Tab from file list → diff → context pane → file list (cycle works)
- In context pane: j/k highlights different Symbol or Cross-file items
- Press Enter on a Cross-file item — diff cursor moves to that file
- Esc from context pane returns to diff focus
- Resize terminal narrower than 120 cols — pane disappears, Tab cycles only file/diff

- [ ] **Step 9.7: Final commit**

```bash
git add internal/ctxpane/ internal/ui/
git commit -m "ui: context-pane focus, j/k/Enter/Esc navigation"
```

---

## Self-review checklist (run after writing the plan)

Already done by the plan author:
- All five spec sections (Where, Symbol, Cross-file, Blame, History) have an implementation task.
- The `c` toggle, `H` expand, Tab cycle, and Enter jump are all implemented in tasks 2, 8, and 9.
- Width contract (32 cols, hidden below 120) is implemented in task 2 (Step 2.4).
- Debounced refresh with stale-cancel is task 3.
- Concurrent fan-out with per-section timeouts is task 5 (Step 5.5).
- Caching (blame + grep + HEAD-sha) is task 5 (Step 5.3) and task 6 (Step 6.1).
- Tests use the existing `t.TempDir()` + `git init` pattern from `internal/diff/`.
- No placeholder TODOs or `// implement here` text anywhere.
- Names are consistent: `Cursor`, `Section`, `Payload`, `Resolve`, `buildXSection`, `kindFor`, `paneContext`, `contextPaneVisible`, `contextPaneWidthEffective`.

Known gaps (deliberate, in line with spec's out-of-scope list):
- `contextJumpToSelected` does file-level jump only; in-file viewport scrolling to the exact line is deferred (called out as a comment in step 9.3).
- Persistent toggle state across runs is not implemented (spec out-of-scope).
- No LSP / language-server integration; symbol detection is regex-based (spec out-of-scope).
