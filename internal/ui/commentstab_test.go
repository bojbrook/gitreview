package ui

import (
	"strings"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/ctxpane"
	"github.com/bowenbrooks/gitreview/internal/pr"
	tea "github.com/charmbracelet/bubbletea"
)

func TestUnifyCommentsOrderingAndDraftsPinned(t *testing.T) {
	reviews := []ctxpane.ReviewDisplay{
		{User: "alice", State: "APPROVED", Age: "3h", Body: "LGTM", CreatedAt: 300},
	}
	issues := []ctxpane.IssueCommentDisplay{
		{User: "bob", Age: "5h", Body: "bumping", CreatedAt: 100},
		{User: "alice", Age: "2h", Body: "ship it", CreatedAt: 400},
	}
	inline := []ctxpane.CommentRef{
		{User: "carol", Path: "main.go", Line: 42, Side: "RIGHT", Body: "non-nil?", Age: "4h", CreatedAt: 200},
	}
	drafts := []ctxpane.Draft{
		{Path: "api.go", Line: 7, Side: "RIGHT", Body: "I think this can be cleaner"},
	}

	got := unifyComments(inline, issues, reviews, drafts)
	if len(got) != 5 {
		t.Fatalf("expected 5 unified items, got %d", len(got))
	}

	wantAuthors := []string{"bob", "carol", "alice", "alice", "you"}
	for i, w := range wantAuthors {
		if got[i].Author != w {
			t.Errorf("order[%d]: want author %q, got %q (kind=%d)", i, w, got[i].Author, got[i].Kind)
		}
	}

	if got[len(got)-1].Kind != commentDraft {
		t.Errorf("draft should be pinned last, got kind=%d", got[len(got)-1].Kind)
	}
}

func TestUnifyCommentsEmpty(t *testing.T) {
	got := unifyComments(nil, nil, nil, nil)
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %d items", len(got))
	}
}

func TestCommentListHeaderKinds(t *testing.T) {
	tests := []struct {
		name     string
		c        unifiedComment
		contains []string
	}{
		{
			name:     "review with state",
			c:        unifiedComment{Kind: commentReview, Author: "alice", Age: "1h", State: "APPROVED"},
			contains: []string{"alice", "approved"},
		},
		{
			name:     "issue comment",
			c:        unifiedComment{Kind: commentIssue, Author: "bob", Age: "30m"},
			contains: []string{"bob", "comment"},
		},
		{
			name:     "inline with anchor",
			c:        unifiedComment{Kind: commentInline, Author: "carol", Age: "2h", Path: "main.go", Line: 42},
			contains: []string{"carol", "main.go:42"},
		},
		{
			name:     "draft",
			c:        unifiedComment{Kind: commentDraft, Author: "you", Age: "draft", Path: "api.go", Line: 7},
			contains: []string{"you", "draft", "api.go:7"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := commentListHeader(tt.c, 60)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("header %q missing %q", got, want)
				}
			}
		})
	}
}

func TestCommentsTabKeyOnlyInPRMode(t *testing.T) {
	m := New(fakeDiff(), nil, "", nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m = updated.(Model)
	if m.view == viewComments {
		t.Error("pressing 5 in non-PR mode should not switch to viewComments")
	}
}

func TestCommentsTabRenders(t *testing.T) {
	m := New(fakeDiff(), nil, "", &PRBundle{
		Meta: &pr.PRMeta{Number: 1, Author: "x", State: "open", Title: "t"},
		IssueComments: []ctxpane.IssueCommentDisplay{
			{User: "alice", Age: "2h", Body: "ship it", CreatedAt: 100},
		},
		ReviewComments: []ctxpane.CommentRef{
			{User: "bob", Path: "main.go", Line: 2, Side: "RIGHT", Body: "non-nil?", Age: "1h", CreatedAt: 200},
		},
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m = updated.(Model)
	if m.view != viewComments {
		t.Fatalf("expected viewComments after '5', got %d", m.view)
	}
	out := m.View()
	for _, want := range []string{"[5 comments]", "alice", "bob", "main.go", "ship it"} {
		if !strings.Contains(out, want) {
			t.Errorf("comments view missing %q. Got:\n%s", want, out)
		}
	}
}

func TestCommentsJumpToInlineAnchor(t *testing.T) {
	m := New(fakeDiff(), nil, "", &PRBundle{
		Meta: &pr.PRMeta{Number: 1, Author: "x", State: "open", Title: "t"},
		ReviewComments: []ctxpane.CommentRef{
			{User: "bob", Path: "main.go", Line: 2, Side: "RIGHT", Body: "non-nil?", Age: "1h", CreatedAt: 200},
		},
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.view != viewChanges {
		t.Fatalf("expected viewChanges after enter on inline anchor, got %d", m.view)
	}
	fr, _, ok := m.currentFileRow()
	if !ok || fr.Path != "main.go" {
		t.Errorf("expected file cursor on main.go, got ok=%v path=%q", ok, fr.Path)
	}
}
