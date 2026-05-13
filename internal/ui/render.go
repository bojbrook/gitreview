package ui

import (
	"fmt"
	"strings"

	"github.com/bowenbrooks/gitreview/internal/diff"
	"github.com/charmbracelet/x/ansi"
)

// renderFullDiff concatenates all files in a diff with file headers as separators.
// Used when viewing a single commit's whole diff.
func renderFullDiff(files []diff.File, width int) string {
	if len(files) == 0 {
		return mutedStyle.Render("(no changes)")
	}
	var b strings.Builder
	for i, f := range files {
		if i > 0 {
			b.WriteString("\n")
		}
		header := fmt.Sprintf("── %s %s ──", f.Status, f.Path)
		b.WriteString(titleStyle.Render(truncate(header, width)))
		b.WriteString("\n")
		b.WriteString(renderDiff(f, width))
		b.WriteString("\n")
	}
	return b.String()
}

// renderDiff produces a styled unified-diff string for a file.
func renderDiff(f diff.File, width int) string {
	if len(f.Hunks) == 0 {
		return mutedStyle.Render("(no hunks)")
	}

	var b strings.Builder
	for hi, h := range f.Hunks {
		if hi > 0 {
			b.WriteString("\n")
		}
		b.WriteString(mutedStyle.Render(truncate(h.Header, width)))
		b.WriteString("\n")

		for _, line := range h.Lines {
			b.WriteString(renderLine(line, width))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func renderLine(l diff.Line, width int) string {
	old := pad(l.OldNum)
	new := pad(l.NewNum)
	content := expandTabs(l.Content, 4)
	var sign string
	var styled string

	switch l.Kind {
	case diff.LineAdded:
		old = "    "
		sign = "+"
		styled = addedLineStyle.Render(sign + " " + content)
	case diff.LineRemoved:
		new = "    "
		sign = "-"
		styled = removedLineStyle.Render(sign + " " + content)
	default:
		sign = " "
		styled = "  " + content
	}

	gutter := gutterStyle.Render(fmt.Sprintf("%s %s ", old, new))
	combined := gutter + styled
	return truncate(combined, width)
}

// splitDivider is the BEFORE/AFTER column separator. Three visible cells —
// space, bar, space — using the LIGHT vertical (│ U+2502) which is always
// single-width on every terminal. Heavy chars like ┃ are East-Asian ambiguous
// and break alignment on some setups.
var splitDivider = splitDividerStyle.Render(" │ ")

// renderSplit renders a file's diff as side-by-side BEFORE | AFTER columns.
// Falls back to renderDiff if width is too narrow.
func renderSplit(f diff.File, width int) string {
	if len(f.Hunks) == 0 {
		return mutedStyle.Render("(no hunks)")
	}
	colW := (width - 3) / 2 // 3 for " │ " separator
	if colW < 16 {
		return renderDiff(f, width)
	}

	var b strings.Builder
	beforeLbl := mutedStyle.Render(padRight("BEFORE", colW))
	afterLbl := mutedStyle.Render(padRight("AFTER", colW))
	b.WriteString(beforeLbl)
	b.WriteString(splitDivider)
	b.WriteString(afterLbl)
	b.WriteString("\n")

	for hi, h := range f.Hunks {
		if hi > 0 {
			// Inter-hunk blank row — keep the divider rendered so the vertical
			// wall doesn't visually break between hunks.
			b.WriteString(strings.Repeat(" ", colW))
			b.WriteString(splitDivider)
			b.WriteString(strings.Repeat(" ", colW))
			b.WriteString("\n")
		}
		// Hunk header — span the BEFORE side, divider, blank AFTER side.
		hdr := padRight(truncatePlain(h.Header, colW), colW)
		b.WriteString(mutedStyle.Render(hdr))
		b.WriteString(splitDivider)
		b.WriteString(mutedStyle.Render(padRight("", colW)))
		b.WriteString("\n")

		rows := pairHunkLines(h)
		for _, sr := range rows {
			b.WriteString(renderSplitRow(sr, colW))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// truncatePlain truncates a plain (unstyled) string to a visible width.
// Use truncate() instead for already-styled strings.
func truncatePlain(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if len(s) <= w {
		return s
	}
	if w < 2 {
		return s[:w]
	}
	return s[:w-1] + "…"
}

type splitRow struct {
	leftNum   int
	leftText  string
	leftKind  diff.LineKind
	rightNum  int
	rightText string
	rightKind diff.LineKind
}

// pairHunkLines groups consecutive removed and added lines into the same row.
// Context lines appear identically on both sides.
func pairHunkLines(h diff.Hunk) []splitRow {
	var rows []splitRow
	var removed, added []diff.Line

	flush := func() {
		n := len(removed)
		if len(added) > n {
			n = len(added)
		}
		for i := 0; i < n; i++ {
			var sr splitRow
			if i < len(removed) {
				sr.leftNum = removed[i].OldNum
				sr.leftText = removed[i].Content
				sr.leftKind = diff.LineRemoved
			}
			if i < len(added) {
				sr.rightNum = added[i].NewNum
				sr.rightText = added[i].Content
				sr.rightKind = diff.LineAdded
			}
			rows = append(rows, sr)
		}
		removed = removed[:0]
		added = added[:0]
	}

	for _, l := range h.Lines {
		switch l.Kind {
		case diff.LineRemoved:
			removed = append(removed, l)
		case diff.LineAdded:
			added = append(added, l)
		default:
			flush()
			rows = append(rows, splitRow{
				leftNum: l.OldNum, leftText: l.Content, leftKind: l.Kind,
				rightNum: l.NewNum, rightText: l.Content, rightKind: l.Kind,
			})
		}
	}
	flush()
	return rows
}

func renderSplitRow(sr splitRow, colW int) string {
	left := renderSplitSide(sr.leftNum, sr.leftText, sr.leftKind, colW)
	right := renderSplitSide(sr.rightNum, sr.rightText, sr.rightKind, colW)
	return left + splitDivider + right
}

func renderSplitSide(num int, text string, kind diff.LineKind, colW int) string {
	textW := colW - 5 // 4 line-num + 1 space
	if textW < 1 {
		textW = 1
	}
	if num == 0 && text == "" {
		return strings.Repeat(" ", colW)
	}
	numStr := pad(num)
	// Tabs are 1 byte but render as multiple columns. Expand before measuring
	// width so truncation/padding stays aligned with what the terminal draws.
	body := expandTabs(text, 4)
	if len(body) > textW {
		body = body[:textW]
	}
	body += strings.Repeat(" ", textW-len(body))

	gutter := gutterStyle.Render(numStr + " ")
	switch kind {
	case diff.LineAdded:
		return gutter + addedLineStyle.Render(body)
	case diff.LineRemoved:
		return gutter + removedLineStyle.Render(body)
	default:
		return gutter + body
	}
}

// expandTabs replaces \t characters with the right number of spaces to reach
// the next tab stop at width n. Operates on byte-by-byte basis (assumes ASCII)
// which is fine for source code in this tool.
func expandTabs(s string, n int) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	col := 0
	for _, r := range s {
		if r == '\t' {
			pad := n - col%n
			for i := 0; i < pad; i++ {
				b.WriteByte(' ')
			}
			col += pad
		} else {
			b.WriteRune(r)
			col++
		}
	}
	return b.String()
}

func pad(n int) string {
	if n == 0 {
		return "    "
	}
	return fmt.Sprintf("%4d", n)
}

func padRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if len(s) >= w {
		return s[:w]
	}
	return s + strings.Repeat(" ", w-len(s))
}

// truncate clips a styled string to a visible width without leaving an
// unterminated ANSI escape. Use this for any line that contains styled
// segments; plain text can use truncateRaw.
func truncate(s string, width int) string {
	if width <= 0 {
		return s
	}
	if ansi.StringWidth(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "…")
}

// formatFileStats returns "+N −M" added/removed line counts for a file.
func formatFileStats(f diff.File) string {
	var add, del int
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			switch l.Kind {
			case diff.LineAdded:
				add++
			case diff.LineRemoved:
				del++
			}
		}
	}
	return fmt.Sprintf("+%d −%d", add, del)
}

// padBetweenAnsi places left/right with whitespace stretched to width.
// Both arguments may contain ANSI escapes.
func padBetweenAnsi(left, right string, width int) string {
	lw := ansi.StringWidth(left)
	rw := ansi.StringWidth(right)
	if lw+rw >= width {
		return left
	}
	return left + strings.Repeat(" ", width-lw-rw) + right
}
