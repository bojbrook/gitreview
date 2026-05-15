package ui

import (
	"path"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

type rowKind int

const (
	rowDir rowKind = iota
	rowFile
)

// treeRow is one visible row in the file-explorer pane. Rows are flat:
// dir rows at depth 0, their file children at depth 1. The renderer
// iterates these directly — no recursion required.
type treeRow struct {
	Kind     rowKind
	Path     string // dir: parent path (e.g. "internal/ui", "" for root files); file: full diff.File.Path
	Label    string // what to render (dir path or filename)
	Depth    int    // 0 for dirs, 1 for files
	FileIdx  int    // file rows only: index into the `files` argument to BuildTree (-1 for dirs)
	Reviewed int    // dir rows only: count of reviewed files in this dir
	Total    int    // dir rows only: total files in this dir
}

// BuildTree returns the visible rows for the given inputs.
//
//   - files: the file slice to render (already filtered for view/mode; this
//     function does NOT apply the user's text filter again — pass a
//     pre-filtered slice when needed and the filter argument for force-expand).
//   - reviewed: set of file paths the user has marked reviewed.
//   - collapsed: set of dir paths the user has collapsed (presence = collapsed).
//     Dirs default to expanded; only explicitly-collapsed dirs hide children.
//   - filter: when non-empty, every dir is force-expanded regardless of
//     `collapsed`. The caller is responsible for narrowing `files` to matches.
//
// Row order: dirs appear in the order their first file appears in `files`.
// Files within a dir appear in `files` order. Stable + predictable.
func BuildTree(files []diff.File, reviewed map[string]bool, collapsed map[string]bool, filter string) []treeRow {
	type group struct {
		dir   string
		files []int // indices into `files`
	}
	var groups []*group
	byDir := map[string]*group{}

	for i, f := range files {
		d := dirOf(f.Path)
		g, ok := byDir[d]
		if !ok {
			g = &group{dir: d}
			byDir[d] = g
			groups = append(groups, g)
		}
		g.files = append(g.files, i)
	}

	var rows []treeRow
	for _, g := range groups {
		rev := 0
		for _, i := range g.files {
			if reviewed[files[i].Path] {
				rev++
			}
		}
		rows = append(rows, treeRow{
			Kind:     rowDir,
			Path:     g.dir,
			Label:    dirLabel(g.dir),
			Depth:    0,
			FileIdx:  -1,
			Reviewed: rev,
			Total:    len(g.files),
		})
		if filter == "" && collapsed[g.dir] {
			continue
		}
		for _, i := range g.files {
			f := files[i]
			rows = append(rows, treeRow{
				Kind:    rowFile,
				Path:    f.Path,
				Label:   path.Base(f.Path),
				Depth:   1,
				FileIdx: i,
				Total:   1,
			})
		}
	}
	return rows
}

// dirOf returns the directory containing the given file path. Files at the
// repo root return "" so we can render them under a "(root)" pseudo-dir.
func dirOf(filePath string) string {
	d := path.Dir(filePath)
	if d == "." {
		return ""
	}
	return d
}

// dirLabel returns the user-visible label for a dir path. "" becomes "(root)";
// everything else is returned unchanged.
func dirLabel(dirPath string) string {
	if dirPath == "" {
		return "(root)"
	}
	return dirPath
}

// FirstFileRow returns the index of the first file row in rows, or -1 if
// there isn't one.
func FirstFileRow(rows []treeRow) int {
	for i, r := range rows {
		if r.Kind == rowFile {
			return i
		}
	}
	return -1
}

// RowOfFile returns the index of the row whose Path matches the given file
// path, or -1 if no such row is visible.
func RowOfFile(rows []treeRow, filePath string) int {
	for i, r := range rows {
		if r.Kind == rowFile && r.Path == filePath {
			return i
		}
	}
	return -1
}
