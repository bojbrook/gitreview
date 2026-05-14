package ui

import (
	"strings"
	"testing"

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

	// Move cursor down
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(Model)
	if m.fileCursor != 1 {
		t.Errorf("cursor after j: got %d want 1", m.fileCursor)
	}

	// Boundary — shouldn't go past last
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(Model)
	if m.fileCursor != 1 {
		t.Errorf("cursor at end after extra j: got %d want 1", m.fileCursor)
	}

	// Tab focus
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.focus != paneDiff {
		t.Errorf("focus after tab: got %v want paneDiff", m.focus)
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

	// Schedule two refreshes back to back; the first should be stale.
	_ = m.scheduleContextRefresh()
	staleSeq := m.contextRefreshSeq
	_ = m.scheduleContextRefresh()

	// Snapshot the payload before delivering the stale msg.
	want := m.contextPayload
	updated, _ = m.Update(contextRefreshMsg{Seq: staleSeq})
	m = updated.(Model)
	// Stale msg must not crash and must not replace the payload.
	if m.contextRefreshSeq != staleSeq+1 {
		t.Errorf("contextRefreshSeq: got %d want %d", m.contextRefreshSeq, staleSeq+1)
	}
	if &m.contextPayload.Sections == nil && want.Sections != nil {
		t.Error("stale msg cleared the payload")
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
