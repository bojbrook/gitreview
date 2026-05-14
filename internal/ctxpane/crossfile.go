package ctxpane

import (
	"context"
	"strconv"
	"strings"
)

// buildCrossFileSection looks for the cursor-symbol in OTHER files that are
// also in the current diff. Reuses gitGrepRefs and filters its output to the
// diff's set of changed paths. Returns StatusEmpty if there's no symbol or
// no matches outside the current file.
func buildCrossFileSection(ctx context.Context, cur Cursor) Section {
	s := Section{Kind: SectionCrossFile, Status: StatusEmpty}
	sym := symbolUnderCursor(cur)
	if sym == "" || cur.Diff == nil || cur.RepoRoot == "" {
		return s
	}
	refs, err := gitGrepRefs(ctx, cur.RepoRoot, sym, cur.File.Path, 50)
	if err != nil {
		s.Status = StatusError
		return s
	}
	if len(refs) == 0 {
		return s
	}
	others := make(map[string]bool)
	for _, f := range cur.Diff.Files {
		if f.Path != cur.File.Path {
			others[f.Path] = true
		}
	}
	matched := make([]Item, 0, 6)
	for _, r := range refs {
		parts := strings.SplitN(r, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if !others[parts[0]] {
			continue
		}
		ln, _ := strconv.Atoi(parts[1])
		matched = append(matched, Item{
			Text: r,
			Jump: &JumpTarget{File: parts[0], Line: ln},
		})
		if len(matched) >= 6 {
			break
		}
	}
	if len(matched) == 0 {
		return s
	}
	s.Status = StatusOK
	s.Items = matched
	return s
}
