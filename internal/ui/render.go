package ui

import (
	"fmt"
	"strings"

	"github.com/bowenbrooks/gitreview/internal/diff"
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

// renderDiff produces a styled string for a file's hunks.
// Width is the inner width available for the diff pane.
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
	var sign string
	var styled string

	switch l.Kind {
	case diff.LineAdded:
		old = "    "
		sign = "+"
		styled = addedLineStyle.Render(sign + " " + l.Content)
	case diff.LineRemoved:
		new = "    "
		sign = "-"
		styled = removedLineStyle.Render(sign + " " + l.Content)
	default:
		sign = " "
		styled = "  " + l.Content
	}

	gutter := gutterStyle.Render(fmt.Sprintf("%s %s ", old, new))
	// Truncate combined gutter+content to width to avoid wraps that break layout.
	combined := gutter + styled
	return truncate(combined, width)
}

func pad(n int) string {
	if n == 0 {
		return "    "
	}
	return fmt.Sprintf("%4d", n)
}

// truncate drops anything that overflows width (ignoring style escape codes is hard;
// for v0 we approximate by trimming the raw string before styling where possible,
// or letting lipgloss handle clipping when this is set on a styled pane).
func truncate(s string, width int) string {
	if width <= 0 {
		return s
	}
	// Naive truncate on rune count of raw bytes; lipgloss truncation in pane handles ANSI safely.
	if len(s) <= width*4 {
		return s
	}
	return s[:width*4]
}
