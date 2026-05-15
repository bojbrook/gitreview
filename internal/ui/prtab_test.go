package ui

import (
	"strings"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/ctxpane"
	"github.com/bowenbrooks/gitreview/internal/pr"
	tea "github.com/charmbracelet/bubbletea"
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
