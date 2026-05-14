package ctxpane

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

func TestSymbolUnderCursor_OnDecl(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "foo.go"), "package foo\n\nfunc Greet() {}\n")
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
		HunkIndex: 0,
	}
	if got := symbolUnderCursor(cur); got != "Greet" {
		t.Errorf("got %q want Greet", got)
	}
}

func TestSymbolUnderCursor_OffDecl(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "foo.go"), "package foo\n\nfunc Greet() {}\n")
	cur := Cursor{
		RepoRoot: dir,
		File: diff.File{
			Path: "foo.go",
			Hunks: []diff.Hunk{{
				Lines: []diff.Line{
					{Kind: diff.LineContext, NewNum: 1},
				},
			}},
		},
		HunkIndex: 0,
	}
	if got := symbolUnderCursor(cur); got != "" {
		t.Errorf("non-decl line: got %q want empty", got)
	}
}

func TestBuildSymbolSection_Integration(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	gitCfg(t, dir)
	mustWrite(t, filepath.Join(dir, "lib.go"), "package p\n\nfunc Greet() {}\n")
	mustWrite(t, filepath.Join(dir, "main.go"), "package p\n\nfunc main() { Greet() }\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "seed")

	cur := Cursor{
		RepoRoot: dir,
		File: diff.File{
			Path: "lib.go",
			Hunks: []diff.Hunk{{
				Lines: []diff.Line{
					{Kind: diff.LineAdded, NewNum: 3},
				},
			}},
		},
		HunkIndex: 0,
	}
	s := buildSymbolSection(context.Background(), cur)
	if s.Status != StatusOK {
		t.Fatalf("status: %v", s.Status)
	}
	found := false
	for _, it := range s.Items {
		if strings.Contains(it.Text, "main.go") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected main.go in symbol refs; got %+v", s.Items)
	}
}

func TestEscapeRegex(t *testing.T) {
	cases := map[string]string{
		"foo":     "foo",
		"foo.bar": `foo\.bar`,
		"a(b)":    `a\(b\)`,
	}
	for in, want := range cases {
		if got := escapeRegex(in); got != want {
			t.Errorf("escapeRegex(%q): got %q want %q", in, got, want)
		}
	}
}
