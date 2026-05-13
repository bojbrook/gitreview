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
