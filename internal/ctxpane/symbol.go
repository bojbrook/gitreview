package ctxpane

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

var grepCache = newLRU(256)

// symbolUnderCursor returns the declared identifier if the anchored line of
// the cursor's hunk is itself a declaration, otherwise "".
func symbolUnderCursor(cur Cursor) string {
	if cur.HunkIndex < 0 || cur.HunkIndex >= len(cur.File.Hunks) {
		return ""
	}
	anchor, _, ok := cur.AnchorLine()
	if !ok {
		return ""
	}
	lines, err := readFileLines(cur.RepoRoot, cur.File.Path)
	if err != nil || len(lines) == 0 {
		return ""
	}
	if anchor > len(lines) {
		return ""
	}
	m := declRegex.FindStringSubmatch(lines[anchor-1])
	if m == nil {
		return ""
	}
	for _, g := range m[1:] {
		if g != "" {
			return g
		}
	}
	return ""
}

// gitGrepRefs returns up to maxResults locations of the symbol in the repo,
// excluding the cursor's own file. Each entry is "<path>:<line>".
func gitGrepRefs(ctx context.Context, repoRoot, symbol, excludePath string, maxResults int) ([]string, error) {
	key := repoRoot + "\x00" + symbol
	if v, ok := grepCache.Get(key); ok {
		return filterAndCap(v.([]string), excludePath, maxResults), nil
	}
	cmd := exec.CommandContext(ctx, "git", "grep", "-n", "--no-color", "-w", "-F", symbol)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		// `git grep` exits 1 when there are no matches — treat as empty.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			grepCache.Put(key, []string(nil))
			return nil, nil
		}
		return nil, err
	}
	var refs []string
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		// Format is "path:lineno:content" — we only keep the prefix.
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 2 {
			continue
		}
		if _, err := strconv.Atoi(parts[1]); err != nil {
			continue
		}
		refs = append(refs, parts[0]+":"+parts[1])
	}
	grepCache.Put(key, refs)
	return filterAndCap(refs, excludePath, maxResults), nil
}

func filterAndCap(refs []string, excludePath string, maxResults int) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		if excludePath != "" && strings.HasPrefix(r, excludePath+":") {
			continue
		}
		out = append(out, r)
		if len(out) >= maxResults {
			break
		}
	}
	return out
}

func escapeRegex(s string) string {
	// Conservative escape — these are the metachars git-grep -E will react to.
	const meta = `\.+*?()[]{}|^$`
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if strings.ContainsRune(meta, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func buildSymbolSection(ctx context.Context, cur Cursor) Section {
	s := Section{Kind: SectionSymbol, Status: StatusEmpty}
	sym := symbolUnderCursor(cur)
	if sym == "" || cur.RepoRoot == "" {
		return s
	}
	refs, err := gitGrepRefs(ctx, cur.RepoRoot, sym, cur.File.Path, 6)
	if err != nil {
		s.Status = StatusError
		return s
	}
	s.Status = StatusOK
	header := fmt.Sprintf("%s (%d refs)", sym, len(refs))
	s.Items = []Item{{Text: header}}
	for _, r := range refs {
		parts := strings.SplitN(r, ":", 2)
		ln, _ := strconv.Atoi(parts[1])
		s.Items = append(s.Items, Item{
			Text: r,
			Jump: &JumpTarget{File: parts[0], Line: ln},
		})
	}
	// Avoid leaving the section empty when sym matches but has no refs.
	if len(refs) == 0 {
		s.Items[0] = Item{Text: sym + " (no other refs)"}
	}
	return s
}
