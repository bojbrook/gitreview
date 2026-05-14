package ui

import (
	"testing"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

func makeFiles(paths ...string) []diff.File {
	out := make([]diff.File, len(paths))
	for i, p := range paths {
		out[i] = diff.File{Path: p, Status: diff.StatusModified}
	}
	return out
}

func TestBuildTree_GroupsByDir(t *testing.T) {
	files := makeFiles(
		"internal/ctxpane/blame.go",
		"internal/ctxpane/cache.go",
		"internal/ui/model.go",
	)
	rows := BuildTree(files, nil, nil, "")

	// Expect: dir "internal/ctxpane" (2 files) + 2 file rows; dir "internal/ui" (1 file) + 1 file row.
	if len(rows) != 5 {
		t.Fatalf("row count: got %d want 5; rows=%+v", len(rows), rows)
	}
	if rows[0].Kind != rowDir || rows[0].Path != "internal/ctxpane" || rows[0].Total != 2 {
		t.Errorf("row 0: got %+v", rows[0])
	}
	if rows[1].Kind != rowFile || rows[1].Label != "blame.go" || rows[1].FileIdx != 0 {
		t.Errorf("row 1: got %+v", rows[1])
	}
	if rows[3].Kind != rowDir || rows[3].Path != "internal/ui" {
		t.Errorf("row 3: got %+v", rows[3])
	}
}

func TestBuildTree_RootFiles(t *testing.T) {
	files := makeFiles("README.md", "Makefile")
	rows := BuildTree(files, nil, nil, "")
	if len(rows) != 3 {
		t.Fatalf("row count: got %d want 3; rows=%+v", len(rows), rows)
	}
	if rows[0].Kind != rowDir || rows[0].Path != "" || rows[0].Label != "(root)" {
		t.Errorf("root dir row: got %+v", rows[0])
	}
}

func TestBuildTree_CollapsedDirHidesFiles(t *testing.T) {
	files := makeFiles(
		"a/x.go",
		"a/y.go",
		"b/z.go",
	)
	collapsed := map[string]bool{"a": true}
	rows := BuildTree(files, nil, collapsed, "")
	// Expect: a (no children) + b + b/z.go = 3 rows.
	if len(rows) != 3 {
		t.Fatalf("row count: got %d want 3; rows=%+v", len(rows), rows)
	}
	if rows[0].Path != "a" || rows[0].Kind != rowDir {
		t.Errorf("row 0: got %+v", rows[0])
	}
	if rows[1].Path != "b" || rows[1].Kind != rowDir {
		t.Errorf("row 1 should be dir b: got %+v", rows[1])
	}
	if rows[2].Kind != rowFile || rows[2].Path != "b/z.go" {
		t.Errorf("row 2: got %+v", rows[2])
	}
}

func TestBuildTree_FilterForceExpands(t *testing.T) {
	files := makeFiles("a/x.go", "a/y.go")
	collapsed := map[string]bool{"a": true}
	// Filter non-empty: collapsed state is overridden, files become visible.
	rows := BuildTree(files, nil, collapsed, "x")
	if len(rows) != 3 {
		t.Fatalf("row count: got %d want 3; rows=%+v", len(rows), rows)
	}
	if rows[1].Kind != rowFile || rows[2].Kind != rowFile {
		t.Errorf("expected both files visible under filter; got %+v", rows)
	}
}

func TestBuildTree_ReviewedAggregation(t *testing.T) {
	files := makeFiles("a/x.go", "a/y.go", "a/z.go")
	reviewed := map[string]bool{"a/x.go": true, "a/y.go": true}
	rows := BuildTree(files, reviewed, nil, "")
	if rows[0].Reviewed != 2 || rows[0].Total != 3 {
		t.Errorf("dir aggregation: got reviewed=%d total=%d want 2/3", rows[0].Reviewed, rows[0].Total)
	}
}

func TestBuildTree_PreservesFirstAppearanceOrder(t *testing.T) {
	files := makeFiles(
		"z/file.go",
		"a/file.go",
		"z/other.go",
	)
	rows := BuildTree(files, nil, nil, "")
	if rows[0].Path != "z" {
		t.Errorf("first dir: got %q want z", rows[0].Path)
	}
	// All z files should appear before the a dir (group order = first-appearance).
	if rows[3].Path != "a" {
		t.Errorf("second dir: got %q want a (row index 3 after z + 2 z-files)", rows[3].Path)
	}
}

func TestFirstFileRow(t *testing.T) {
	rows := []treeRow{
		{Kind: rowDir, Path: "a"},
		{Kind: rowFile, Path: "a/x.go"},
		{Kind: rowFile, Path: "a/y.go"},
	}
	if got := FirstFileRow(rows); got != 1 {
		t.Errorf("got %d want 1", got)
	}
	if got := FirstFileRow(nil); got != -1 {
		t.Errorf("nil rows: got %d want -1", got)
	}
}

func TestRowOfFile(t *testing.T) {
	rows := []treeRow{
		{Kind: rowDir, Path: "a"},
		{Kind: rowFile, Path: "a/x.go"},
		{Kind: rowFile, Path: "a/y.go"},
	}
	if got := RowOfFile(rows, "a/y.go"); got != 2 {
		t.Errorf("got %d want 2", got)
	}
	if got := RowOfFile(rows, "missing"); got != -1 {
		t.Errorf("missing: got %d want -1", got)
	}
}
