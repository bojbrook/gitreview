package diff

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	maxUntrackedBytes = 1 << 20 // 1 MiB hard cap per file
	binaryProbeBytes  = 8192
)

// loadUntracked returns synthetic File entries for files git considers
// untracked (respecting .gitignore). They're rendered as fully-added.
// Binary or oversized files appear as a single placeholder line.
func loadUntracked() ([]File, error) {
	out, err := run("git", "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}

	repoRoot, err := run("git", "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	repoRoot = strings.TrimSpace(repoRoot)

	paths := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	files := make([]File, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		f, ok := buildUntrackedFile(repoRoot, p)
		if ok {
			files = append(files, f)
		}
	}
	return files, nil
}

func buildUntrackedFile(repoRoot, relPath string) (File, bool) {
	abs := filepath.Join(repoRoot, relPath)
	info, err := os.Lstat(abs)
	if err != nil {
		return File{}, false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		// Show symlinks as a single placeholder line; don't follow.
		return synthFile(relPath, []string{"(symlink — not shown)"}), true
	}
	if !info.Mode().IsRegular() {
		return File{}, false
	}

	if info.Size() > maxUntrackedBytes {
		return synthFile(relPath, []string{"(file too large to preview)"}), true
	}

	content, err := os.ReadFile(abs)
	if err != nil {
		return synthFile(relPath, []string{"(unreadable: " + err.Error() + ")"}), true
	}
	if isBinary(content) {
		return synthFile(relPath, []string{"(binary file — not shown)"}), true
	}

	lines := splitLines(string(content))
	return synthFile(relPath, lines), true
}

func synthFile(path string, addedLines []string) File {
	hunkLines := make([]Line, len(addedLines))
	for i, l := range addedLines {
		hunkLines[i] = Line{Kind: LineAdded, Content: l, NewNum: i + 1}
	}
	return File{
		Path:     path,
		Status:   StatusAdded,
		Language: languageFromPath(path),
		Hunks: []Hunk{{
			Header:   "@@ -0,0 +1," + strconv.Itoa(len(addedLines)) + " @@ (untracked)",
			OldStart: 0, OldLines: 0,
			NewStart: 1, NewLines: len(addedLines),
			Lines: hunkLines,
		}},
	}
}

func isBinary(b []byte) bool {
	n := len(b)
	if n > binaryProbeBytes {
		n = binaryProbeBytes
	}
	return bytes.IndexByte(b[:n], 0) >= 0
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

