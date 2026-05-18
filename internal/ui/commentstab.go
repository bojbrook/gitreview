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

// commentKind distinguishes the three row types on the [5 comments] tab:
// a submitted review summary, a top-level (issue) comment, or a thread of
// one or more inline review comments + drafts at a single anchor.
type commentKind int

const (
	commentReview commentKind = iota
	commentIssue
	commentThread
)

// threadReply is one comment inside a unifiedComment thread, in chrono order.
// Drafts are surfaced here so a "fresh" anchor (one with only the user's
// pending draft) still shows up as a thread row.
type threadReply struct {
	Author    string
	Age       string
	Body      string
	IsDraft   bool
	DraftIdx  int   // index into Model.drafts; -1 if not a draft
	CreatedAt int64 // 0 for drafts
}

// unifiedComment is one row on the [5 comments] tab. For threads, the
// Author/Age/Body fields reflect the *latest* reply (so the list snippet
// reads "what's the most recent thing said here"); CreatedAt reflects the
// *first* reply (so threads sort by when the conversation started, not when
// the last reply landed).
type unifiedComment struct {
	Kind      commentKind
	Author    string
	Age       string
	State     string // review state; "" for non-reviews
	Path      string // anchor path; "" for review/issue
	Line      int    // anchor line; 0 for review/issue
	Side      string // "RIGHT"|"LEFT"; "" for review/issue
	Body      string
	CreatedAt int64
	Replies   []threadReply // populated only for commentThread
	DraftOnly bool          // commentThread containing only drafts; pinned to the end
}

// threadKey is the grouping key for inline comments + drafts.
type threadKey struct {
	Path string
	Line int
	Side string
}

// unifyComments merges the three fetched streams plus local drafts. Inline
// comments and drafts at the same (path, line, side) collapse into one
// thread row. Reviews and top-level issue comments stay as one row each.
// Output is sorted chronologically; threads of only drafts pin to the end.
func unifyComments(
	reviewComments []ctxpane.CommentRef,
	issueComments []ctxpane.IssueCommentDisplay,
	reviews []ctxpane.ReviewDisplay,
	drafts []ctxpane.Draft,
) []unifiedComment {
	out := make([]unifiedComment, 0, len(issueComments)+len(reviews)+len(reviewComments))

	for _, r := range reviews {
		out = append(out, unifiedComment{
			Kind:      commentReview,
			Author:    r.User,
			Age:       r.Age,
			State:     r.State,
			Body:      r.Body,
			CreatedAt: r.CreatedAt,
		})
	}
	for _, c := range issueComments {
		out = append(out, unifiedComment{
			Kind:      commentIssue,
			Author:    c.User,
			Age:       c.Age,
			Body:      c.Body,
			CreatedAt: c.CreatedAt,
		})
	}

	// Group inline comments + drafts by anchor. Preserve discovery order via
	// a parallel keys slice so the deterministic-but-arbitrary anchor order
	// is stable across runs (later sorted by CreatedAt anyway).
	byAnchor := map[threadKey]*unifiedComment{}
	var keys []threadKey
	get := func(k threadKey) *unifiedComment {
		if t, ok := byAnchor[k]; ok {
			return t
		}
		t := &unifiedComment{
			Kind: commentThread,
			Path: k.Path,
			Line: k.Line,
			Side: k.Side,
		}
		byAnchor[k] = t
		keys = append(keys, k)
		return t
	}
	for _, c := range reviewComments {
		k := threadKey{Path: c.Path, Line: c.Line, Side: c.Side}
		t := get(k)
		t.Replies = append(t.Replies, threadReply{
			Author:    c.User,
			Age:       c.Age,
			Body:      c.Body,
			DraftIdx:  -1,
			CreatedAt: c.CreatedAt,
		})
	}
	for i, d := range drafts {
		k := threadKey{Path: d.Path, Line: d.Line, Side: d.Side}
		t := get(k)
		t.Replies = append(t.Replies, threadReply{
			Author:   "you",
			Age:      "draft",
			Body:     d.Body,
			IsDraft:  true,
			DraftIdx: i,
		})
	}

	// Materialize threads: sort replies oldest→newest (drafts sort to the
	// end within a thread since their CreatedAt is 0); compute thread-level
	// CreatedAt + summary from the first and last reply.
	for _, k := range keys {
		t := byAnchor[k]
		sort.SliceStable(t.Replies, func(i, j int) bool {
			ri, rj := t.Replies[i], t.Replies[j]
			if ri.IsDraft != rj.IsDraft {
				return !ri.IsDraft // non-drafts before drafts within the same anchor
			}
			return ri.CreatedAt < rj.CreatedAt
		})
		// Sort key: first non-draft reply's CreatedAt. For a draft-only
		// thread that stays 0 and we pin to the end via DraftOnly.
		t.DraftOnly = true
		for _, r := range t.Replies {
			if !r.IsDraft {
				t.CreatedAt = r.CreatedAt
				t.DraftOnly = false
				break
			}
		}
		last := t.Replies[len(t.Replies)-1]
		t.Author = last.Author
		t.Age = last.Age
		t.Body = last.Body
		out = append(out, *t)
	}

	sort.SliceStable(out, func(i, j int) bool {
		di := out[i].DraftOnly
		dj := out[j].DraftOnly
		if di != dj {
			return !di // non-draft-only before draft-only
		}
		return out[i].CreatedAt < out[j].CreatedAt
	})
	return out
}

// renderCommentsTab returns the rendered body of the [5 comments] tab.
// Two panes: anchor-grouped list on the left, full thread on the right.
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
		snippet := commentListSnippet(c, innerW)
		if i == selected {
			header = cursorStyle.Render(padPlainToWidth(ansi.Strip(header), innerW))
			snippet = cursorStyle.Render(padPlainToWidth(ansi.Strip(snippet), innerW))
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

// commentListHeader renders the first line of a list row. Reviews/issues
// lead with the author; threads lead with the anchor + reply count.
func commentListHeader(c unifiedComment, innerW int) string {
	var left, right string
	switch c.Kind {
	case commentReview:
		kindTag := "review"
		if c.State != "" {
			kindTag = strings.ToLower(c.State)
		}
		left = c.Author + " · " + kindTag
		right = c.Age
	case commentIssue:
		left = c.Author + " · comment"
		right = c.Age
	case commentThread:
		anchor := fmt.Sprintf("%s:%d", compactPath(c.Path, innerW-12), c.Line)
		left = anchor
		if n := len(c.Replies); n > 1 {
			right = fmt.Sprintf("·%d  %s", n, c.Age)
		} else {
			right = c.Age
		}
		if c.DraftOnly {
			left = prDraftStyle.Render("[DRAFT] ") + left
		}
	}
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

// commentListSnippet renders the second line of a list row: a single-line
// preview of the comment (review/issue) or of the latest reply (thread).
func commentListSnippet(c unifiedComment, innerW int) string {
	prefix := "> "
	if c.Kind == commentThread {
		prefix = "  " + c.Author + ": "
	}
	body := singleLine(c.Body)
	available := innerW - ansi.StringWidth(prefix)
	if available < 4 {
		available = 4
	}
	return mutedStyle.Render(prefix + truncateRaw(body, available))
}

func renderCommentDetail(c unifiedComment, files []diff.File, paneW, bodyH int, focused bool) string {
	innerW := paneW - 4
	if innerW < 16 {
		innerW = 16
	}
	var b strings.Builder
	// Header
	switch c.Kind {
	case commentReview:
		b.WriteString(titleStyle.Render(c.Author))
		b.WriteString("  ")
		b.WriteString(mutedStyle.Render(c.Age))
		if c.State != "" {
			b.WriteString("  ")
			b.WriteString(titleStyle.Render(c.State))
		}
	case commentIssue:
		b.WriteString(titleStyle.Render(c.Author))
		b.WriteString("  ")
		b.WriteString(mutedStyle.Render(c.Age))
	case commentThread:
		b.WriteString(titleStyle.Render(fmt.Sprintf("%s:%d", c.Path, c.Line)))
		b.WriteString("  ")
		b.WriteString(mutedStyle.Render(fmt.Sprintf("(%s)", c.Side)))
	}
	b.WriteString("\n\n")

	if c.Kind == commentThread {
		// Code context.
		if ctx := commentCodeContext(files, c.Path, c.Line, c.Side, innerW); ctx != "" {
			b.WriteString(ctx)
			b.WriteString("\n\n")
		}
		// Replies.
		for i, r := range c.Replies {
			if i > 0 {
				b.WriteString(mutedStyle.Render(strings.Repeat("─", innerW-2)))
				b.WriteString("\n")
			}
			head := r.Author + "  " + mutedStyle.Render(r.Age)
			if r.IsDraft {
				head = prDraftStyle.Render("[DRAFT] you  " + r.Age)
			}
			b.WriteString(head)
			b.WriteString("\n")
			body := strings.TrimSpace(r.Body)
			if body == "" {
				b.WriteString(mutedStyle.Render("(no body)"))
			} else {
				b.WriteString(wrapText(body, innerW-2))
			}
			b.WriteString("\n")
		}
	} else {
		body := strings.TrimSpace(c.Body)
		if body == "" {
			b.WriteString(mutedStyle.Render("(no body)"))
		} else {
			b.WriteString(indent(wrapText(body, innerW-2), "> "))
		}
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
