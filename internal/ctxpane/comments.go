package ctxpane

import (
	"strings"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

// buildCommentsSection returns the Comments section for the cursor's anchor
// line, mixing fetched ReviewComments and user-authored Drafts. Drafts are
// always rendered last, prefixed with [DRAFT].
//
// The section is empty when no comment or draft anchors to (Path, Line, Side).
func buildCommentsSection(cur Cursor) Section {
	s := Section{Kind: SectionComments, Status: StatusEmpty}
	if cur.File.Path == "" {
		return s
	}
	line, kind, ok := cur.AnchorLine()
	if !ok {
		return s
	}
	side := "RIGHT"
	if kind == diff.LineRemoved {
		side = "LEFT"
	}

	var items []Item
	for _, c := range cur.ReviewComments {
		if c.Path != cur.File.Path || c.Line != line || c.Side != side {
			continue
		}
		items = append(items, Item{Text: formatCommentRow(c.User, c.Age, c.Body)})
	}
	for _, d := range cur.Drafts {
		if d.Path != cur.File.Path || d.Line != line || d.Side != side {
			continue
		}
		items = append(items, Item{Text: formatDraftRow(d.Body)})
	}
	if len(items) == 0 {
		return s
	}
	s.Status = StatusOK
	s.Items = items
	return s
}

func formatCommentRow(user, age, body string) string {
	return user + " " + age + ": " + truncateBody(body, 50)
}

func formatDraftRow(body string) string {
	return "[DRAFT] you: " + truncateBody(body, 50)
}

func truncateBody(body string, max int) string {
	flat := strings.Join(strings.Fields(body), " ")
	if len([]rune(flat)) <= max {
		return flat
	}
	r := []rune(flat)
	return string(r[:max-1]) + "…"
}
