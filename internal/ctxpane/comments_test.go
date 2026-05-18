package ctxpane

import (
	"strings"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

func curOnLine(path string, line int, kind diff.LineKind) Cursor {
	f := diff.File{
		Path:  path,
		Hunks: []diff.Hunk{{Lines: []diff.Line{}}},
	}
	switch kind {
	case diff.LineAdded:
		f.Hunks[0].Lines = []diff.Line{{Kind: diff.LineAdded, NewNum: line}}
	case diff.LineRemoved:
		f.Hunks[0].Lines = []diff.Line{{Kind: diff.LineRemoved, OldNum: line}}
	default:
		f.Hunks[0].Lines = []diff.Line{{Kind: diff.LineContext, NewNum: line, OldNum: line}}
	}
	return Cursor{File: f, HunkIndex: 0}
}

func TestBuildCommentsSection_FiltersByAnchor(t *testing.T) {
	cur := curOnLine("src/a.go", 12, diff.LineAdded)
	cur.ReviewComments = []CommentRef{
		{User: "alice", Path: "src/a.go", Line: 12, Side: "RIGHT", Body: "looks good", Age: "2h"},
		{User: "bob", Path: "src/a.go", Line: 99, Side: "RIGHT", Body: "elsewhere", Age: "1h"},
		{User: "eve", Path: "other.go", Line: 12, Side: "RIGHT", Body: "wrong file", Age: "3h"},
	}
	s := buildCommentsSection(cur)
	if s.Status != StatusOK {
		t.Fatalf("status: got %v want OK", s.Status)
	}
	if len(s.Items) != 1 {
		t.Fatalf("items: got %d want 1 (%+v)", len(s.Items), s.Items)
	}
	if !strings.Contains(s.Items[0].Text, "alice") {
		t.Errorf("item text: got %q", s.Items[0].Text)
	}
}

func TestBuildCommentsSection_SideMatchesLineKind(t *testing.T) {
	cur := curOnLine("src/a.go", 5, diff.LineRemoved)
	cur.ReviewComments = []CommentRef{
		{User: "alice", Path: "src/a.go", Line: 5, Side: "LEFT", Body: "x", Age: "1h"},
		{User: "bob", Path: "src/a.go", Line: 5, Side: "RIGHT", Body: "y", Age: "1h"},
	}
	s := buildCommentsSection(cur)
	if len(s.Items) != 1 || !strings.Contains(s.Items[0].Text, "alice") {
		t.Errorf("items: got %+v", s.Items)
	}
}

func TestBuildCommentsSection_DraftsAfterFetched(t *testing.T) {
	cur := curOnLine("src/a.go", 12, diff.LineAdded)
	cur.ReviewComments = []CommentRef{
		{User: "alice", Path: "src/a.go", Line: 12, Side: "RIGHT", Body: "hi", Age: "2h"},
	}
	cur.Drafts = []Draft{
		{Path: "src/a.go", Line: 12, Side: "RIGHT", Body: "my reply"},
	}
	s := buildCommentsSection(cur)
	if len(s.Items) != 2 {
		t.Fatalf("items: got %d (%+v)", len(s.Items), s.Items)
	}
	if !strings.Contains(s.Items[0].Text, "alice") {
		t.Errorf("first item should be fetched comment: %q", s.Items[0].Text)
	}
	if !strings.Contains(s.Items[1].Text, "[DRAFT]") {
		t.Errorf("second item should be draft: %q", s.Items[1].Text)
	}
}

func TestTruncateBody(t *testing.T) {
	cases := map[string]string{
		"short":                 "short",
		"line one\nline two":    "line one line two",
		strings.Repeat("a", 60): strings.Repeat("a", 49) + "…",
	}
	for in, want := range cases {
		if got := truncateBody(in, 50); got != want {
			t.Errorf("truncateBody(%q): got %q want %q", in, got, want)
		}
	}
}
