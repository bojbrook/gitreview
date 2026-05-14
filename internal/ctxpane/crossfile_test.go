package ctxpane

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

func TestBuildCrossFileSection_FindsRefInDiff(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	gitCfg(t, dir)
	mustWrite(t, filepath.Join(dir, "lib.go"), "package p\n\nfunc Greet() {}\n")
	mustWrite(t, filepath.Join(dir, "main.go"), "package p\n\nfunc main() { Greet() }\n")
	mustWrite(t, filepath.Join(dir, "other.go"), "package p\n\nfunc Other() { Greet() }\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "seed")

	d := &diff.Diff{
		Files: []diff.File{
			{Path: "lib.go"},
			{Path: "main.go"},
			// other.go is NOT in the diff
		},
	}
	cur := Cursor{
		RepoRoot: dir,
		Diff:     d,
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
	s := buildCrossFileSection(context.Background(), cur)
	if s.Status != StatusOK {
		t.Fatalf("status: %v", s.Status)
	}
	foundMain, foundOther := false, false
	for _, it := range s.Items {
		if strings.Contains(it.Text, "main.go") {
			foundMain = true
		}
		if strings.Contains(it.Text, "other.go") {
			foundOther = true
		}
	}
	if !foundMain {
		t.Error("expected main.go (which is in the diff)")
	}
	if foundOther {
		t.Error("did NOT expect other.go (not in the diff)")
	}
}
