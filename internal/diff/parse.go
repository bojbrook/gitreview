package diff

import (
	"bufio"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// Parse converts the output of `git diff` (unified format) into Files.
func Parse(raw string) ([]File, error) {
	var files []File
	var cur *File
	var curHunk *Hunk
	var oldLine, newLine int

	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)

	flushHunk := func() {
		if cur != nil && curHunk != nil {
			cur.Hunks = append(cur.Hunks, *curHunk)
			curHunk = nil
		}
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			files = append(files, *cur)
			cur = nil
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			cur = &File{Status: StatusModified}
			// "diff --git a/foo b/foo"
			parts := strings.SplitN(line[len("diff --git "):], " ", 2)
			if len(parts) == 2 {
				a := strings.TrimPrefix(parts[0], "a/")
				b := strings.TrimPrefix(parts[1], "b/")
				cur.OldPath = a
				cur.Path = b
				cur.Language = languageFromPath(b)
			}
		case cur == nil:
			continue
		case strings.HasPrefix(line, "new file mode"):
			cur.Status = StatusAdded
			cur.OldPath = ""
		case strings.HasPrefix(line, "deleted file mode"):
			cur.Status = StatusDeleted
		case strings.HasPrefix(line, "rename from "):
			cur.Status = StatusRenamed
			cur.OldPath = strings.TrimPrefix(line, "rename from ")
		case strings.HasPrefix(line, "rename to "):
			cur.Path = strings.TrimPrefix(line, "rename to ")
			cur.Language = languageFromPath(cur.Path)
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "index "), strings.HasPrefix(line, "similarity index "), strings.HasPrefix(line, "old mode "), strings.HasPrefix(line, "new mode "):
			// metadata; ignore
		case strings.HasPrefix(line, "@@"):
			flushHunk()
			h, oStart, nStart, err := parseHunkHeader(line)
			if err != nil {
				return nil, err
			}
			curHunk = h
			oldLine = oStart
			newLine = nStart
		case curHunk == nil:
			continue
		case strings.HasPrefix(line, "+"):
			curHunk.Lines = append(curHunk.Lines, Line{Kind: LineAdded, Content: line[1:], NewNum: newLine})
			newLine++
		case strings.HasPrefix(line, "-"):
			curHunk.Lines = append(curHunk.Lines, Line{Kind: LineRemoved, Content: line[1:], OldNum: oldLine})
			oldLine++
		case strings.HasPrefix(line, " "):
			curHunk.Lines = append(curHunk.Lines, Line{Kind: LineContext, Content: line[1:], OldNum: oldLine, NewNum: newLine})
			oldLine++
			newLine++
		case strings.HasPrefix(line, "\\ "):
			// "\ No newline at end of file" — drop
		}
	}
	flushFile()

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan diff: %w", err)
	}
	return files, nil
}

// parseHunkHeader extracts ranges from a header like "@@ -1,3 +5,7 @@ optional context".
func parseHunkHeader(header string) (*Hunk, int, int, error) {
	// Strip trailing context after the second "@@"
	end := strings.Index(header[2:], "@@")
	if end < 0 {
		return nil, 0, 0, fmt.Errorf("invalid hunk header: %s", header)
	}
	body := strings.TrimSpace(header[2 : 2+end])
	parts := strings.Fields(body)
	if len(parts) < 2 {
		return nil, 0, 0, fmt.Errorf("invalid hunk header: %s", header)
	}
	oldStart, oldLines, err := parseRange(parts[0])
	if err != nil {
		return nil, 0, 0, err
	}
	newStart, newLines, err := parseRange(parts[1])
	if err != nil {
		return nil, 0, 0, err
	}
	return &Hunk{
		Header:   header,
		OldStart: oldStart,
		OldLines: oldLines,
		NewStart: newStart,
		NewLines: newLines,
	}, oldStart, newStart, nil
}

// parseRange parses "-12,3" or "+5,7" (count defaults to 1 if absent).
func parseRange(s string) (start, count int, err error) {
	if len(s) == 0 {
		return 0, 0, fmt.Errorf("empty range")
	}
	s = s[1:] // drop sign
	count = 1
	if i := strings.Index(s, ","); i >= 0 {
		count, err = strconv.Atoi(s[i+1:])
		if err != nil {
			return 0, 0, fmt.Errorf("parse count: %w", err)
		}
		s = s[:i]
	}
	start, err = strconv.Atoi(s)
	if err != nil {
		return 0, 0, fmt.Errorf("parse start: %w", err)
	}
	return start, count, nil
}

func languageFromPath(p string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(p), "."))
	if ext == "" {
		return ""
	}
	return ext
}
