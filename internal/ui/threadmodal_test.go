package ui

import (
	"strings"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/ctxpane"
)

func TestBuildThread(t *testing.T) {
	rcs := []ctxpane.CommentRef{
		{User: "alice", Path: "a.go", Line: 1, Side: "RIGHT", Body: "hi", Age: "2h"},
		{User: "bob", Path: "b.go", Line: 1, Side: "RIGHT", Body: "elsewhere", Age: "1h"},
	}
	drafts := []ctxpane.Draft{
		{Path: "a.go", Line: 1, Side: "RIGHT", Body: "my draft"},
	}
	got := buildThread(rcs, drafts, "a.go", 1, "RIGHT")
	if len(got) != 2 {
		t.Fatalf("entries: got %d want 2 (%+v)", len(got), got)
	}
	if got[0].Author != "alice" || got[0].IsDraft {
		t.Errorf("entry 0: %+v", got[0])
	}
	if !got[1].IsDraft || got[1].DraftIdx != 0 {
		t.Errorf("entry 1: %+v", got[1])
	}
}

func TestRenderThreadModal(t *testing.T) {
	entries := []threadEntry{
		{Author: "alice", Age: "2h", Body: "hello world", DraftIdx: -1},
		{IsDraft: true, Author: "you", Age: "draft", Body: "my draft", DraftIdx: 0},
	}
	out := renderThreadModal("Thread: a.go:1", entries, 1, 40)
	if !strings.Contains(out, "Thread: a.go:1") {
		t.Errorf("missing title: %s", out)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("missing alice: %s", out)
	}
	if !strings.Contains(out, "[DRAFT]") {
		t.Errorf("missing draft marker: %s", out)
	}
	if !strings.Contains(out, "Esc close") {
		t.Errorf("missing help line: %s", out)
	}
}

func TestWrapText(t *testing.T) {
	in := "one two three four five six seven eight nine ten"
	got := wrapText(in, 12)
	for _, line := range strings.Split(got, "\n") {
		if len(line) > 12 {
			t.Errorf("line too long (%d): %q", len(line), line)
		}
	}
}
