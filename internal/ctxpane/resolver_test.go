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
