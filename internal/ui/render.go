package ui

import (
	"fmt"
	"strings"

	"github.com/bowenbrooks/gitreview/internal/diff"
	"github.com/charmbracelet/lipgloss"
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

const (
	spineCellW = 14 // total width of a spine cell (bar + label + padding)
	spineBarW  = 6  // width of the bar within a cell
)

// renderOverview lays out file spines side-by-side in a grid that wraps to
// multiple rows. Returns the joined string and the (rowCount, colCount) grid
// dimensions so the caller can interpret the cursor index.
func renderOverview(files []diff.File, reviewed map[string]bool, width, height, cursor int) (string, int, int) {
	if len(files) == 0 {
		return mutedStyle.Render("(no files)"), 0, 0
	}
	cols := width / spineCellW
	if cols < 1 {
		cols = 1
	}
	rowCount := (len(files) + cols - 1) / cols
	// Height available for spine bars: total height − cell chrome (label + stats + border rows)
	const chromeRows = 4 // file name (1) + stats (1) + top border (1) + bottom border (1)
	spineRows := height/rowCount - chromeRows
	if spineRows < 4 {
		spineRows = 4
	}

	cells := make([]string, len(files))
	for i, f := range files {
		cells[i] = renderSpineCell(f, spineRows, spineBarW, i == cursor, reviewed[f.Path])
	}

	// Lay out grid: stack cells horizontally per row, then join rows vertically.
	var rows []string
	for r := 0; r < rowCount; r++ {
		start := r * cols
		end := start + cols
		if end > len(cells) {
			end = len(cells)
		}
		row := lipgloss.JoinHorizontal(lipgloss.Top, cells[start:end]...)
		rows = append(rows, row)
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...), rowCount, cols
}

// renderSpineCell renders a single file's spine + label + stats inside a
// bordered cell. Focused cell uses the focus border color. Reviewed files get
// a ✓ prefix on the label and a dimmer spine bar.
func renderSpineCell(f diff.File, rows, barW int, focused, reviewed bool) string {
	spine := renderSpine(f, rows, barW)

	labelW := spineCellW - 4
	if labelW < 4 {
		labelW = 4
	}
	label := compactPath(f.Path, labelW)
	if reviewed {
		// Prefix with ✓ (eats 2 visible cols of the label area)
		label = compactPath(f.Path, labelW-2)
		label = "✓ " + label
	}
	stats := formatFileStats(f)

	var b strings.Builder
	if reviewed {
		b.WriteString(mutedStyle.Render(label))
	} else {
		b.WriteString(spineLabelStyle.Render(label))
	}
	b.WriteString("\n")
	for _, s := range spine {
		if reviewed {
			// Replace styled spine cells with a dim, monochrome version.
			b.WriteString(mutedStyle.Render(strings.Repeat("·", barW)))
		} else {
			b.WriteString(s)
		}
		b.WriteString("\n")
	}
	b.WriteString(mutedStyle.Render(stats))

	style := paneStyle
	if focused {
		style = paneFocusStyle
	}
	return style.
		Width(spineCellW - 2).
		Height(rows + 3).
		Render(b.String())
}

// fileLength returns the post-change line count of a file, used as the file's
// "extent" when projecting changes onto a spine. Approximated as max NewNum
// (or OldNum for deleted files) across all hunk lines.
func fileLength(f diff.File) int {
	max := 0
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			n := l.NewNum
			if n == 0 {
				n = l.OldNum
			}
			if n > max {
				max = n
			}
		}
	}
	if max == 0 {
		return 1
	}
	return max
}

// spineBucket represents one row of a file spine: how many added and removed
// lines from the file fall into this row's slice of the file's extent.
type spineBucket struct {
	added   int
	removed int
}

// computeSpine projects a file's diff onto `rows` vertical buckets and returns
// the per-bucket add/remove counts. Bucket 0 = top of file, last bucket = end.
func computeSpine(f diff.File, rows int) []spineBucket {
	if rows < 1 {
		return nil
	}
	buckets := make([]spineBucket, rows)
	length := fileLength(f)
	if length < 1 {
		return buckets
	}
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Kind == diff.LineContext {
				continue
			}
			n := l.NewNum
			if n == 0 {
				n = l.OldNum
			}
			if n < 1 {
				continue
			}
			row := (n - 1) * rows / length
			if row >= rows {
				row = rows - 1
			}
			switch l.Kind {
			case diff.LineAdded:
				buckets[row].added++
			case diff.LineRemoved:
				buckets[row].removed++
			}
		}
	}
	return buckets
}

// renderSpine returns a vertical bar of `rows` rows, `barW` cols wide. Each
// row's character + color encodes the bucket's add/remove density.
func renderSpine(f diff.File, rows, barW int) []string {
	buckets := computeSpine(f, rows)
	out := make([]string, rows)
	// Determine the max density across the file for normalization.
	maxD := 0
	for _, b := range buckets {
		d := b.added + b.removed
		if d > maxD {
			maxD = d
		}
	}
	for i, b := range buckets {
		out[i] = spineCell(b, maxD, barW)
	}
	return out
}

func spineCell(b spineBucket, maxD, w int) string {
	total := b.added + b.removed
	if total == 0 || maxD == 0 {
		return strings.Repeat("·", w)
	}
	// Pick a char by relative density.
	ratio := float64(total) / float64(maxD)
	var ch string
	switch {
	case ratio < 0.25:
		ch = "░"
	case ratio < 0.5:
		ch = "▒"
	case ratio < 0.75:
		ch = "▓"
	default:
		ch = "█"
	}
	bar := strings.Repeat(ch, w)
	// Color: if mostly added → green; mostly removed → red; mixed → orange.
	switch {
	case b.removed == 0:
		return addedLineStyle.Render(bar)
	case b.added == 0:
		return removedLineStyle.Render(bar)
	default:
		return lipgloss.NewStyle().Foreground(colStatusMod).Render(bar)
	}
}

// renderSparkline returns a short horizontal histogram of where changes cluster
// along a file's length. Each cell is one of `░▒▓█` (or `·` if empty), tinted
// green/red/orange by net direction. Used inline next to file paths.
func renderSparkline(f diff.File, width int) string {
	if width < 1 {
		return ""
	}
	buckets := computeSpine(f, width) // reuse the spine math, projected horizontally
	maxD := 0
	for _, b := range buckets {
		d := b.added + b.removed
		if d > maxD {
			maxD = d
		}
	}
	var b strings.Builder
	for _, bk := range buckets {
		b.WriteString(spineCell(bk, maxD, 1))
	}
	return b.String()
}

// renderFileSpine returns a vertical bar of `height` rows for a file's full
// extent. activeHunkIx (or -1) gets a bright marker; other hunk-rows are
// faintly dotted; non-hunk rows are blank. Suitable for placement on the right
// edge of the diff pane.
func renderFileSpine(f diff.File, height int, activeHunkIx int) []string {
	if height < 1 || len(f.Hunks) == 0 {
		return nil
	}
	length := fileLength(f)
	if length < 1 {
		length = 1
	}

	hunkRows := make(map[int]bool)
	activeRow := -1
	for i, h := range f.Hunks {
		n := h.NewStart
		if n == 0 {
			n = h.OldStart
		}
		if n < 1 {
			n = 1
		}
		row := (n - 1) * height / length
		if row >= height {
			row = height - 1
		}
		hunkRows[row] = true
		if i == activeHunkIx {
			activeRow = row
		}
	}

	out := make([]string, height)
	for i := 0; i < height; i++ {
		switch {
		case i == activeRow:
			out[i] = cursorBarStyle.Render("◀")
		case hunkRows[i]:
			out[i] = mutedStyle.Render("·")
		default:
			out[i] = " "
		}
	}
	return out
}

// renderSparklinePlain is like renderSparkline but without any color/ANSI
// escapes. Suitable for inclusion inside a cursorStyle.Render(...) row whose
// background fill mustn't be interrupted.
func renderSparklinePlain(f diff.File, width int) string {
	if width < 1 {
		return ""
	}
	buckets := computeSpine(f, width)
	maxD := 0
	for _, b := range buckets {
		d := b.added + b.removed
		if d > maxD {
			maxD = d
		}
	}
	var b strings.Builder
	for _, bk := range buckets {
		total := bk.added + bk.removed
		if total == 0 || maxD == 0 {
			b.WriteRune('·')
			continue
		}
		ratio := float64(total) / float64(maxD)
		switch {
		case ratio < 0.25:
			b.WriteRune('░')
		case ratio < 0.5:
			b.WriteRune('▒')
		case ratio < 0.75:
			b.WriteRune('▓')
		default:
			b.WriteRune('█')
		}
	}
	return b.String()
}

// hunkOffsetsUnified returns the viewport line index of each hunk header in
// the output of renderDiff(f, _). One entry per hunk.
func hunkOffsetsUnified(f diff.File) []int {
	if len(f.Hunks) == 0 {
		return nil
	}
	out := make([]int, len(f.Hunks))
	out[0] = 0
	for i := 1; i < len(f.Hunks); i++ {
		// Previous hunk: 1 header + N content lines, plus 1 blank line written by
		// the `b.WriteString("\n")` in renderDiff between hunks.
		out[i] = out[i-1] + 1 + len(f.Hunks[i-1].Lines) + 1
	}
	return out
}

// hunkOffsetsSplit returns the viewport line index of each hunk header in the
// output of renderSplit(f, _). Accounts for the BEFORE/AFTER header row and
// the inter-hunk blank row.
func hunkOffsetsSplit(f diff.File) []int {
	if len(f.Hunks) == 0 {
		return nil
	}
	out := make([]int, len(f.Hunks))
	out[0] = 1 // row 0 is the BEFORE/AFTER header; first hunk header is row 1
	for i := 1; i < len(f.Hunks); i++ {
		// 1 (hunk header) + paired-row count + 1 (inter-hunk blank).
		out[i] = out[i-1] + 1 + len(pairHunkLines(f.Hunks[i-1])) + 1
	}
	return out
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
			b.WriteString(renderLine(line, f.Language, width))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func renderLine(l diff.Line, lang string, width int) string {
	old := pad(l.OldNum)
	new := pad(l.NewNum)
	content := expandTabs(l.Content, 4)

	var body, sign string
	switch l.Kind {
	case diff.LineAdded:
		old = "    "
		sign = addedLineStyle.Render("+ ")
		body = highlightCode(content, lang)
	case diff.LineRemoved:
		new = "    "
		sign = removedLineStyle.Render("- ")
		// Uniform "before" tint — no chroma so the removed line visually recedes.
		body = beforeBodyStyle.Render(content)
	default:
		sign = "  "
		body = highlightCode(content, lang)
	}

	accent := lineAccent(l.Kind)
	gutter := gutterStyle.Render(fmt.Sprintf("%s %s ", old, new))
	return truncate(accent+gutter+sign+body, width)
}

// lineAccent returns a 1-col vertical bar tinted by line kind. Green for adds,
// red for removes, blank for context. The visible signal that survives even
// when chroma is applying its own foreground colors to the line body.
func lineAccent(k diff.LineKind) string {
	switch k {
	case diff.LineAdded:
		return addedLineStyle.Render("▎")
	case diff.LineRemoved:
		return removedLineStyle.Render("▎")
	default:
		return " "
	}
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
			b.WriteString(renderSplitRow(sr, f.Language, colW))
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

func renderSplitRow(sr splitRow, lang string, colW int) string {
	left := renderSplitSide(sr.leftNum, sr.leftText, sr.leftKind, lang, colW, true)
	right := renderSplitSide(sr.rightNum, sr.rightText, sr.rightKind, lang, colW, false)
	return left + splitDivider + right
}

// renderSplitSide renders one column of the split view. isBefore=true tints the
// whole body with beforeBodyStyle (uniform dim red, no syntax highlighting) so
// the OLD version is visually distinct from the syntax-highlighted NEW one.
func renderSplitSide(num int, text string, kind diff.LineKind, lang string, colW int, isBefore bool) string {
	textW := colW - 6 // accent (1) + numStr (4) + space (1) + body
	if textW < 1 {
		textW = 1
	}
	if num == 0 && text == "" {
		return strings.Repeat(" ", colW)
	}
	numStr := pad(num)
	expanded := expandTabs(text, 4)

	var body string
	if isBefore {
		// Plain text first — width is byte count for ASCII.
		clipped := expanded
		if len(clipped) > textW {
			clipped = clipped[:textW]
		}
		clipped += strings.Repeat(" ", textW-len(clipped))
		body = beforeBodyStyle.Render(clipped)
	} else {
		styled := highlightCode(expanded, lang)
		if ansi.StringWidth(styled) > textW {
			styled = ansi.Truncate(styled, textW, "")
		}
		padN := textW - ansi.StringWidth(styled)
		if padN > 0 {
			styled += strings.Repeat(" ", padN)
		}
		body = styled
	}

	accent := lineAccent(kind)
	gutter := gutterStyle.Render(numStr + " ")
	return accent + gutter + body
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
