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

	wantKinds := []commentKind{commentIssue, commentThread, commentReview, commentIssue, commentThread}
	for i, w := range wantKinds {
		if got[i].Kind != w {
			t.Errorf("order[%d]: want kind %d, got %d", i, w, got[i].Kind)
		}
	}

	last := got[len(got)-1]
	if !last.DraftOnly {
		t.Errorf("expected last item to be draft-only thread, got DraftOnly=%v Kind=%d", last.DraftOnly, last.Kind)
	}
}

func TestUnifyCommentsGroupsThreadByAnchor(t *testing.T) {
	inline := []ctxpane.CommentRef{
		{User: "bob", Path: "main.go", Line: 42, Side: "RIGHT", Body: "non-nil?", Age: "2h", CreatedAt: 200},
		{User: "alice", Path: "main.go", Line: 42, Side: "RIGHT", Body: "good catch", Age: "1h", CreatedAt: 300},
	}
	drafts := []ctxpane.Draft{
		{Path: "main.go", Line: 42, Side: "RIGHT", Body: "let me check"},
	}

	got := unifyComments(inline, nil, nil, drafts)
	if len(got) != 1 {
		t.Fatalf("expected 1 grouped thread, got %d", len(got))
	}
	thread := got[0]
	if thread.Kind != commentThread {
		t.Fatalf("expected commentThread, got %d", thread.Kind)
	}
	if len(thread.Replies) != 3 {
		t.Fatalf("expected 3 replies (2 fetched + 1 draft), got %d", len(thread.Replies))
	}
	if thread.Replies[0].Author != "bob" || thread.Replies[1].Author != "alice" {
		t.Errorf("replies out of chrono order: %s, %s", thread.Replies[0].Author, thread.Replies[1].Author)
	}
	if !thread.Replies[2].IsDraft {
		t.Errorf("draft should sort to end of thread replies")
	}
	if thread.DraftOnly {
		t.Errorf("thread has fetched comments; should not be DraftOnly")
	}
	if thread.Author != "you" {
		t.Errorf("thread summary author should be latest reply (\"you\" draft), got %s", thread.Author)
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
			name: "thread with replies",
			c: unifiedComment{
				Kind: commentThread, Path: "main.go", Line: 42, Age: "1h",
				Replies: []threadReply{{Author: "a"}, {Author: "b"}, {Author: "c"}},
			},
			contains: []string{"main.go:42", "·3"},
		},
		{
			name: "draft-only thread",
			c: unifiedComment{
				Kind: commentThread, Path: "api.go", Line: 7, Age: "draft", DraftOnly: true,
				Replies: []threadReply{{IsDraft: true}},
			},
			contains: []string{"api.go:7", "DRAFT"},
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
			{User: "carol", Path: "main.go", Line: 2, Side: "RIGHT", Body: "fixed", Age: "30m", CreatedAt: 300},
		},
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m = updated.(Model)
	if m.view != viewComments {
		t.Fatalf("expected viewComments after '5', got %d", m.view)
	}
	// Cursor to the thread (second item; first is alice's issue comment).
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(Model)
	out := m.View()
	for _, want := range []string{"[5 comments]", "alice", "main.go:2", "non-nil?", "fixed"} {
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
		t.Fatalf("expected viewChanges after enter on thread anchor, got %d", m.view)
	}
	fr, _, ok := m.currentFileRow()
	if !ok || fr.Path != "main.go" {
		t.Errorf("expected file cursor on main.go, got ok=%v path=%q", ok, fr.Path)
	}
}
