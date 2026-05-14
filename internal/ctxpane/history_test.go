package ctxpane

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

func TestBuildHistorySection_Basic(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	gitCfg(t, dir)
	mustWrite(t, filepath.Join(dir, "foo.go"), "package foo\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "first")
	mustWrite(t, filepath.Join(dir, "foo.go"), "package foo\n\nfunc Hi() {}\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "second")

	cur := Cursor{
		RepoRoot: dir,
		File:     diff.File{Path: "foo.go"},
	}
	s := buildHistorySection(context.Background(), cur)
	if s.Status != StatusOK {
		t.Fatalf("status: %v", s.Status)
	}
	if len(s.Items) < 2 {
		t.Fatalf("items: %+v", s.Items)
	}
	if !strings.Contains(s.Items[0].Text, "second") {
		t.Errorf("first item should be newest commit; got %q", s.Items[0].Text)
	}
}

func TestBuildHistorySection_ExpandedAddsLineRange(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	gitCfg(t, dir)
	mustWrite(t, filepath.Join(dir, "foo.go"), "package foo\n\nfunc A() {}\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "add A")
	mustWrite(t, filepath.Join(dir, "foo.go"), "package foo\n\nfunc B() {}\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "rename A to B")

	cur := Cursor{
		RepoRoot: dir,
		File: diff.File{
			Path: "foo.go",
			Hunks: []diff.Hunk{{
				Lines: []diff.Line{
					{Kind: diff.LineAdded, NewNum: 3},
				},
			}},
		},
		HunkIndex:       0,
		HistoryExpanded: true,
	}
	s := buildHistorySection(context.Background(), cur)
	hasRangeMarker := false
	for _, it := range s.Items {
		if strings.Contains(it.Text, "line-range") {
			hasRangeMarker = true
		}
	}
	if !hasRangeMarker {
		t.Errorf("expanded history should include line-range marker; got %+v", s.Items)
	}
}
