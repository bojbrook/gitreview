package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bowenbrooks/gitreview/internal/ctxpane"
	"github.com/bowenbrooks/gitreview/internal/diff"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// commentKind tags the underlying source of a unifiedComment so the renderer
// can pick the right row prefix and detail block.
type commentKind int

const (
	commentReview commentKind = iota
	commentIssue
	commentInline
	commentDraft
)

// unifiedComment is the merged row shape for the [5 comments] tab: a chrono-
// logically-sortable wrapper around any of the four comment sources.
type unifiedComment struct {
	Kind      commentKind
	Author    string
	Age       string
	State     string // review state; empty for non-reviews
	Path      string // anchor path; empty for review/issue
	Line      int    // anchor line; 0 for review/issue
	Side      string // "RIGHT"|"LEFT"; empty for review/issue
	Body      string
	CreatedAt int64
	DraftIdx  int // index into Model.drafts; -1 for non-draft
}

// unifyComments merges the three fetched streams plus local drafts and sorts
// them oldest-first with drafts pinned at the end (drafts have no real
// timestamp; pinning keeps "what am I about to submit" visible together).
func unifyComments(
	reviewComments []ctxpane.CommentRef,
	issueComments []ctxpane.IssueCommentDisplay,
	reviews []ctxpane.ReviewDisplay,
	drafts []ctxpane.Draft,
) []unifiedComment {
	out := make([]unifiedComment, 0, len(reviewComments)+len(issueComments)+len(reviews)+len(drafts))
	for _, r := range reviews {
		out = append(out, unifiedComment{
			Kind:      commentReview,
			Author:    r.User,
			Age:       r.Age,
			State:     r.State,
			Body:      r.Body,
			CreatedAt: r.CreatedAt,
			DraftIdx:  -1,
		})
	}
	for _, c := range issueComments {
		out = append(out, unifiedComment{
			Kind:      commentIssue,
			Author:    c.User,
			Age:       c.Age,
			Body:      c.Body,
			CreatedAt: c.CreatedAt,
			DraftIdx:  -1,
		})
	}
	for _, c := range reviewComments {
		out = append(out, unifiedComment{
			Kind:      commentInline,
			Author:    c.User,
			Age:       c.Age,
			Path:      c.Path,
			Line:      c.Line,
			Side:      c.Side,
			Body:      c.Body,
			CreatedAt: c.CreatedAt,
			DraftIdx:  -1,
		})
	}
	for i, d := range drafts {
		out = append(out, unifiedComment{
			Kind:     commentDraft,
			Author:   "you",
			Age:      "draft",
			Path:     d.Path,
			Line:     d.Line,
			Side:     d.Side,
			Body:     d.Body,
			DraftIdx: i,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		di := out[i].Kind == commentDraft
		dj := out[j].Kind == commentDraft
		if di != dj {
			return !di // non-drafts first; drafts pinned at the end
		}
		return out[i].CreatedAt < out[j].CreatedAt
	})
	return out
}

// renderCommentsTab returns the rendered body of the [5 comments] tab.
// Two panes: list on the left, detail on the right.
func renderCommentsTab(items []unifiedComment, selected int, files []diff.File, width, height int, listFocused bool) string {
	if len(items) == 0 {
		return paneStyle.Width(width - 2).Height(height - 2).Render(mutedStyle.Render("(no comments)"))
	}
	listW := commentListWidth(width)
	detailW := width - listW
	sel := clamp(selected, 0, len(items)-1)
	list := renderCommentList(items, sel, listW, height, listFocused)
	detail := renderCommentDetail(items[sel], files, detailW, height, !listFocused)
	return lipgloss.JoinHorizontal(lipgloss.Top, list, detail)
}

// commentListWidth picks a list-pane width that scales with the terminal but
// stays in a comfortable band (24–44 visible cols).
func commentListWidth(totalW int) int {
	w := totalW / 3
	if w < 24 {
		w = 24
	}
	if w > 44 {
		w = 44
	}
	if w > totalW-30 {
		w = totalW - 30
	}
	if w < 12 {
		w = 12
	}
	return w
}

func renderCommentList(items []unifiedComment, selected, paneW, bodyH int, focused bool) string {
	innerW := paneW - 4
	if innerW < 8 {
		innerW = 8
	}
	var b strings.Builder
	for i, c := range items {
		header := commentListHeader(c, innerW)
		snippet := "> " + truncateRaw(singleLine(c.Body), innerW-2)
		switch {
		case i == selected:
			header = cursorStyle.Render(padPlainToWidth(ansi.Strip(header), innerW))
			snippet = cursorStyle.Render(padPlainToWidth(snippet, innerW))
		case c.Kind == commentDraft:
			snippet = mutedStyle.Render(snippet)
		default:
			snippet = mutedStyle.Render(snippet)
		}
		b.WriteString(header)
		b.WriteString("\n")
		b.WriteString(snippet)
		b.WriteString("\n")
		if i < len(items)-1 {
			b.WriteString("\n")
		}
	}
	style := paneStyle
	if focused {
		style = paneFocusStyle
	}
	return style.Width(paneW - 2).Height(bodyH - 2).Render(b.String())
}

// commentListHeader renders the first line of a list row: "author · kind-tag"
// on the left, age on the right, justified to innerW.
func commentListHeader(c unifiedComment, innerW int) string {
	var kindTag string
	switch c.Kind {
	case commentReview:
		kindTag = "review"
		if c.State != "" {
			kindTag = strings.ToLower(c.State)
		}
	case commentIssue:
		kindTag = "comment"
	case commentInline:
		kindTag = fmt.Sprintf("%s:%d", compactPath(c.Path, 16), c.Line)
	case commentDraft:
		kindTag = fmt.Sprintf("draft %s:%d", compactPath(c.Path, 12), c.Line)
	}
	author := c.Author
	if c.Kind == commentDraft {
		author = prDraftStyle.Render(author)
	}
	left := author + " · " + kindTag
	right := c.Age
	lw := ansi.StringWidth(left)
	rw := ansi.StringWidth(right)
	if lw+rw+1 > innerW {
		left = truncateAnsi(left, innerW-rw-1)
		lw = ansi.StringWidth(left)
	}
	gap := innerW - lw - rw
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + mutedStyle.Render(right)
}

func renderCommentDetail(c unifiedComment, files []diff.File, paneW, bodyH int, focused bool) string {
	innerW := paneW - 4
	if innerW < 16 {
		innerW = 16
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(c.Author))
	b.WriteString("  ")
	b.WriteString(mutedStyle.Render(c.Age))
	if c.State != "" {
		b.WriteString("  ")
		b.WriteString(titleStyle.Render(c.State))
	}
	if c.Path != "" {
		b.WriteString("  ")
		b.WriteString(mutedStyle.Render(fmt.Sprintf("%s:%d (%s)", c.Path, c.Line, c.Side)))
	}
	b.WriteString("\n\n")

	if c.Path != "" && c.Line > 0 {
		ctx := commentCodeContext(files, c.Path, c.Line, c.Side, innerW)
		if ctx != "" {
			b.WriteString(ctx)
			b.WriteString("\n\n")
		}
	}

	body := strings.TrimSpace(c.Body)
	if body == "" {
		b.WriteString(mutedStyle.Render("(no body)"))
	} else {
		b.WriteString(indent(wrapText(body, innerW-2), "> "))
	}

	style := paneStyle
	if focused {
		style = paneFocusStyle
	}
	return style.Width(paneW - 2).Height(bodyH - 2).Render(b.String())
}

// commentCodeContext returns up to 3 lines of context above and below the
// anchor line drawn from the loaded diff, with the anchor line marked. Falls
// back to "" if the file isn't in the diff scope.
func commentCodeContext(files []diff.File, path string, line int, side string, width int) string {
	const radius = 3
	for _, f := range files {
		if f.Path != path {
			continue
		}
		type row struct {
			gutter string
			body   string
			anchor bool
		}
		var rows []row
		anchorIdx := -1
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				var num int
				var match bool
				if side == "LEFT" {
					num = l.OldNum
					match = l.Kind == diff.LineRemoved || l.Kind == diff.LineContext
				} else {
					num = l.NewNum
					match = l.Kind == diff.LineAdded || l.Kind == diff.LineContext
				}
				if num == 0 {
					continue
				}
				r := row{
					gutter: fmt.Sprintf("%4d ", num),
					body:   strings.TrimRight(expandTabs(l.Content, 4), "\n"),
				}
				if match && num == line {
					r.anchor = true
					anchorIdx = len(rows)
				}
				rows = append(rows, r)
			}
		}
		if anchorIdx < 0 {
			return ""
		}
		lo := anchorIdx - radius
		if lo < 0 {
			lo = 0
		}
		hi := anchorIdx + radius + 1
		if hi > len(rows) {
			hi = len(rows)
		}
		var b strings.Builder
		for i := lo; i < hi; i++ {
			r := rows[i]
			s := truncateRaw(r.gutter+r.body, width)
			if r.anchor {
				b.WriteString(cursorStyle.Render(padPlainToWidth(s, width)))
			} else {
				b.WriteString(mutedStyle.Render(s))
			}
			b.WriteString("\n")
		}
		return strings.TrimRight(b.String(), "\n")
	}
	return ""
}

func singleLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.Join(strings.Fields(s), " ")
}

// padPlainToWidth pads a plain (no ANSI) string with spaces to width w, or
// truncates if longer. Used to fill a row before applying cursorStyle so the
// highlight bar reaches the right edge.
func padPlainToWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if len(s) >= w {
		return s[:w]
	}
	return s + strings.Repeat(" ", w-len(s))
}
