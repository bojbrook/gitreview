package ui

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/ctxpane"
	"github.com/bowenbrooks/gitreview/internal/pr"
	tea "github.com/charmbracelet/bubbletea"
)

// captureSubmitter returns a submitter closure that records the event passed
// to it. The returned pointer holds the most recent value at any time.
func captureSubmitter() (func(ctx context.Context, body string, drafts []pr.SubmitDraft, event string) error, *atomic.Value) {
	var got atomic.Value
	got.Store("")
	return func(ctx context.Context, body string, drafts []pr.SubmitDraft, event string) error {
		got.Store(event)
		return nil
	}, &got
}

func TestVerdictDefaultIsComment(t *testing.T) {
	submitter, captured := captureSubmitter()
	m := New(fakeDiff(), nil, "", &PRBundle{
		Meta:      &pr.PRMeta{Number: 1, Author: "x", State: "open", Title: "t"},
		Submitter: submitter,
	})
	// Need at least one draft for submit to be enabled.
	m.drafts = append(m.drafts, ctxpane.Draft{Path: "main.go", Line: 2, Side: "RIGHT", Body: "nit"})

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = updated.(Model)
	// Open the submit modal via S.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	m = updated.(Model)
	if !m.composeOpen || m.composeKind != composeSubmit {
		t.Fatalf("expected submit modal open, got open=%v kind=%d", m.composeOpen, m.composeKind)
	}
	if m.composeVerdict != pr.EventComment {
		t.Errorf("default verdict should be COMMENT, got %q", m.composeVerdict)
	}
	// Submit immediately.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("Ctrl+s should return a submit Cmd")
	}
	cmd() // run the Cmd synchronously (it calls the submitter goroutine-free in our captured stub)
	if got := captured.Load().(string); got != pr.EventComment {
		t.Errorf("submitter event: got %q, want COMMENT", got)
	}
}

func TestVerdictCtrlTCycles(t *testing.T) {
	submitter, captured := captureSubmitter()
	m := New(fakeDiff(), nil, "", &PRBundle{
		Meta:      &pr.PRMeta{Number: 1, Author: "x", State: "open", Title: "t"},
		Submitter: submitter,
	})
	m.drafts = append(m.drafts, ctxpane.Draft{Path: "main.go", Line: 2, Side: "RIGHT", Body: "nit"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	m = updated.(Model)

	// Cycle: comment → approve
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = updated.(Model)
	if m.composeVerdict != pr.EventApprove {
		t.Errorf("after first Ctrl+t: got %q want APPROVE", m.composeVerdict)
	}
	// approve → request-changes
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = updated.(Model)
	if m.composeVerdict != pr.EventRequestChanges {
		t.Errorf("after second Ctrl+t: got %q want REQUEST_CHANGES", m.composeVerdict)
	}
	// request-changes → comment
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = updated.(Model)
	if m.composeVerdict != pr.EventComment {
		t.Errorf("after third Ctrl+t: got %q want COMMENT", m.composeVerdict)
	}

	// Cycle once more (to APPROVE) and submit; verify submitter received it.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = updated.(Model)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("Ctrl+s should return a Cmd")
	}
	cmd()
	if got := captured.Load().(string); got != pr.EventApprove {
		t.Errorf("submitter event: got %q want APPROVE", got)
	}
}

func TestVerdictPickerRenders(t *testing.T) {
	submitter, _ := captureSubmitter()
	m := New(fakeDiff(), nil, "", &PRBundle{
		Meta:      &pr.PRMeta{Number: 1, Author: "x", State: "open", Title: "t"},
		Submitter: submitter,
	})
	m.drafts = append(m.drafts, ctxpane.Draft{Path: "main.go", Line: 2, Side: "RIGHT", Body: "nit"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	m = updated.(Model)

	out := m.View()
	for _, want := range []string{"verdict:", "comment", "approve", "request-changes"} {
		if !strings.Contains(out, want) {
			t.Errorf("submit modal missing %q. Got:\n%s", want, out)
		}
	}
	// Default selection is comment.
	if !strings.Contains(out, "(•) comment") {
		t.Errorf("default verdict should be marked comment. Got:\n%s", out)
	}
}
