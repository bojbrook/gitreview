package ui

import (
	"strings"

	"github.com/bowenbrooks/gitreview/internal/ctxpane"
	"github.com/charmbracelet/lipgloss"
)

// threadEntry is one rendered row in the thread modal: a fetched comment or
// a draft. DraftIdx is -1 for fetched; otherwise it's the index into m.drafts
// so the user can edit / delete that specific draft via e / x.
type threadEntry struct {
	Author   string
	Age      string
	Body     string
	IsDraft  bool
	DraftIdx int
}

// buildThread returns the entries for the modal at (path, line, side),
// fetched-comments first then drafts in insertion order.
func buildThread(reviewComments []ctxpane.CommentRef, drafts []ctxpane.Draft, path string, line int, side string) []threadEntry {
	var out []threadEntry
	for _, c := range reviewComments {
		if c.Path == path && c.Line == line && c.Side == side {
			out = append(out, threadEntry{
				Author:   c.User,
				Age:      c.Age,
				Body:     c.Body,
				DraftIdx: -1,
			})
		}
	}
	for i, d := range drafts {
		if d.Path == path && d.Line == line && d.Side == side {
			out = append(out, threadEntry{
				Author:   "you",
				Age:      "draft",
				Body:     d.Body,
				IsDraft:  true,
				DraftIdx: i,
			})
		}
	}
	return out
}

// renderThreadModal renders the modal contents (not the overlay placement —
// caller composes that via lipgloss.Place).
func renderThreadModal(title string, entries []threadEntry, selected int, innerW int) string {
	var b strings.Builder
	b.WriteString(modalTitleLine(title))
	b.WriteString("\n\n")
	for i, e := range entries {
		header := e.Author + "  " + e.Age
		if e.IsDraft {
			header = prDraftStyle.Render("[DRAFT] you  " + e.Age)
		}
		if i == selected {
			header = cursorStyle.Render(header)
		}
		b.WriteString(header)
		b.WriteString("\n")
		b.WriteString(strings.Repeat("─", minInt(lipgloss.Width(header), innerW)))
		b.WriteString("\n")
		b.WriteString(wrapText(e.Body, innerW))
		b.WriteString("\n\n")
	}
	b.WriteString(mutedStyle.Render(" Esc close · e edit draft · x delete draft "))
	return b.String()
}

// wrapText hard-wraps body at width w, preserving paragraph breaks (lines
// that are exactly empty stay as paragraph separators).
func wrapText(body string, w int) string {
	if w <= 0 {
		return body
	}
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if line == "" {
			out = append(out, "")
			continue
		}
		words := strings.Fields(line)
		var cur string
		for _, word := range words {
			if cur == "" {
				cur = word
				continue
			}
			if len(cur)+1+len(word) > w {
				out = append(out, cur)
				cur = word
				continue
			}
			cur += " " + word
		}
		if cur != "" {
			out = append(out, cur)
		}
	}
	return strings.Join(out, "\n")
}

func modalTitleLine(s string) string {
	return lipgloss.NewStyle().Bold(true).Render(s)
}
