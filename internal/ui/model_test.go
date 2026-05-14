package ui

import (
	"strings"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/ctxpane"
	"github.com/bowenbrooks/gitreview/internal/diff"
	tea "github.com/charmbracelet/bubbletea"
)

func fakeDiff() *diff.Diff {
	return &diff.Diff{
		BaseRef: "main",
		HeadRef: "HEAD",
		Files: []diff.File{
			{
				Path:   "main.go",
				Status: diff.StatusModified,
				Hunks: []diff.Hunk{{
					Header:   "@@ -1,3 +1,4 @@",
					OldStart: 1, OldLines: 3, NewStart: 1, NewLines: 4,
					Lines: []diff.Line{
						{Kind: diff.LineContext, Content: "package main", OldNum: 1, NewNum: 1},
						{Kind: diff.LineRemoved, Content: "old()", OldNum: 2},
						{Kind: diff.LineAdded, Content: "new()", NewNum: 2},
						{Kind: diff.LineAdded, Content: "extra()", NewNum: 3},
					},
				}},
			},
			{
				Path:   "added.go",
				Status: diff.StatusAdded,
				Hunks:  nil,
			},
		},
	}
}

func TestModelRenders(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	out := m.View()
	if out == "" {
		t.Fatal("View returned empty string")
	}
	if !strings.Contains(out, "main.go") {
		t.Errorf("View missing file name. Got:\n%s", out)
	}
	if !strings.Contains(out, "[1 changes]") {
		t.Errorf("View missing global tabs. Got:\n%s", out)
	}
	if !strings.Contains(out, "2 files") {
		t.Errorf("View missing file count subheader. Got:\n%s", out)
	}
}

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

	// Move further — 2 files + 1 dir = 3 rows. Last index = 2.
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
	// Context pane populates via the async path. Drive it by delivering a
	// contextRefreshMsg (which returns a Cmd), then calling that Cmd to get the
	// contextResultMsg, then delivering that.
	updated, cmd := m.Update(contextRefreshMsg{Seq: m.contextRefreshSeq})
	m = updated.(Model)
	if cmd != nil {
		if resultMsg := cmd(); resultMsg != nil {
			updated, _ = m.Update(resultMsg)
			m = updated.(Model)
		}
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
		t.Error("initial: pane should be visible")
	}
	// Toggle off with c
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = updated.(Model)
	if m.contextPaneVisible {
		t.Error("after first c: pane should be hidden")
	}
	// Toggle back on with c
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

func TestContextRefreshStaleCancel(t *testing.T) {
	m := New(fakeDiff(), nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = updated.(Model)

	// Prime a known payload so we can detect overwrites.
	m.contextPayload = ctxpane.Payload{
		Sections: []ctxpane.Section{{Kind: ctxpane.SectionWhere, Status: ctxpane.StatusOK, Items: []ctxpane.Item{{Text: "sentinel"}}}},
	}

	// Schedule two refreshes back to back; the first should be stale.
	_ = m.scheduleContextRefresh()
	staleSeq := m.contextRefreshSeq
	_ = m.scheduleContextRefresh()

	// Delivering the stale msg must not crash and must not replace the payload.
	updated, _ = m.Update(contextRefreshMsg{Seq: staleSeq})
	m = updated.(Model)
	if m.contextRefreshSeq != staleSeq+1 {
		t.Errorf("contextRefreshSeq: got %d want %d", m.contextRefreshSeq, staleSeq+1)
	}
	if len(m.contextPayload.Sections) == 0 || m.contextPayload.Sections[0].Items[0].Text != "sentinel" {
		t.Errorf("stale msg overwrote payload; sections=%+v", m.contextPayload.Sections)
	}
}

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
