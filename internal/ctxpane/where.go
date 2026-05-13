package ctxpane

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// declRegex matches lines that open a function/class/method-like declaration
// in the languages we want coarse support for. Intentionally permissive: the
// goal is "name the enclosing thing", not perfect parsing.
var declRegex = regexp.MustCompile(`^\s*(?:func\s+(?:\([^)]+\)\s+)?([A-Za-z_][A-Za-z0-9_]*)|(?:def|class|function|fn)\s+([A-Za-z_][A-Za-z0-9_]*)|([A-Za-z_][A-Za-z0-9_]*)\s*=\s*function)`)

// containingDecl walks the file content backwards from anchorLine looking for
// the most recent declaration line. Returns the declared identifier and the
// 1-based line number, or ("", 0) if no decl was found above.
//
// anchorLine is 1-based; lines is the full file content split by '\n'.
func containingDecl(lines []string, anchorLine int) (name string, line int) {
	if anchorLine <= 0 {
		return "", 0
	}
	if anchorLine > len(lines) {
		anchorLine = len(lines)
	}
	for i := anchorLine - 1; i >= 0; i-- {
		m := declRegex.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		// Prefer group 1 (func/def/class style); fall back to group 2 (assignment style).
		for _, g := range m[1:] {
			if g != "" {
				return g, i + 1
			}
		}
	}
	return "", 0
}

// readFileLines loads a file as a slice of lines (no trailing newline per line).
// Returns nil + nil error when the path is empty or the file doesn't exist —
// containingDecl will simply return ("", 0).
func readFileLines(repoRoot, relPath string) ([]string, error) {
	if relPath == "" {
		return nil, nil
	}
	full := relPath
	if repoRoot != "" && !filepath.IsAbs(full) {
		full = filepath.Join(repoRoot, relPath)
	}
	f, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // allow long lines
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err()
}

// buildWhereSection always returns a non-empty Section (the spec guarantees
// "▸ Where is always present"). When no file is selected, falls back to a
// muted placeholder item.
func buildWhereSection(cur Cursor) Section {
	s := Section{Kind: SectionWhere, Status: StatusOK}
	if cur.File.Path == "" {
		s.Items = []Item{{Text: "(no file selected)"}}
		return s
	}

	s.Items = append(s.Items, Item{Text: cur.File.Path})
	if len(cur.File.Hunks) > 0 && cur.HunkIndex >= 0 {
		s.Items = append(s.Items, Item{
			Text: fmt.Sprintf("hunk %d of %d", cur.HunkIndex+1, len(cur.File.Hunks)),
		})
	}

	anchor, _, ok := cur.AnchorLine()
	if !ok || anchor <= 0 {
		return s
	}
	lines, err := readFileLines(cur.RepoRoot, cur.File.Path)
	if err != nil {
		s.Items = append(s.Items, Item{Text: "(read error)"})
		return s
	}
	if len(lines) == 0 {
		return s
	}
	name, declLine := containingDecl(lines, anchor)
	if name != "" {
		s.Items = append(s.Items, Item{
			Text: "in: " + name + " (" + fmt.Sprintf("L%d", declLine) + ")",
			Jump: &JumpTarget{File: cur.File.Path, Line: declLine},
		})
	}
	return s
}
