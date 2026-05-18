package ui

import (
	"fmt"
	"strings"

	"github.com/bowenbrooks/gitreview/internal/ctxpane"
	"github.com/bowenbrooks/gitreview/internal/pr"
)

// renderPRTabBody returns the scrollable body of the [4 PR] tab.
func renderPRTabBody(meta *pr.PRMeta, issueComments []ctxpane.IssueCommentDisplay, reviews []ctxpane.ReviewDisplay, draftCount int, reviewBody string, width int) string {
	var b strings.Builder
	if meta == nil {
		b.WriteString(mutedStyle.Render("(no PR loaded)"))
		return b.String()
	}
	fmt.Fprintf(&b, "PR #%d — %s — %s\n", meta.Number, meta.Author, meta.State)
	if meta.HTMLURL != "" {
		fmt.Fprintf(&b, "%s\n\n", mutedStyle.Render(meta.HTMLURL))
	}
	fmt.Fprintf(&b, "  %s\n\n", titleStyle.Render(meta.Title))
	if strings.TrimSpace(meta.Body) != "" {
		b.WriteString(indent(wrapText(meta.Body, width-4), "  "))
		b.WriteString("\n\n")
	}

	b.WriteString(sectionRule(fmt.Sprintf("Issue comments (%d)", len(issueComments)), width))
	b.WriteString("\n")
	if len(issueComments) == 0 {
		b.WriteString("  " + mutedStyle.Render("(none)") + "\n")
	}
	for _, c := range issueComments {
		fmt.Fprintf(&b, "  %s  %s\n", c.User, mutedStyle.Render(c.Age))
		b.WriteString(indent(wrapText(c.Body, width-4), "  "))
		b.WriteString("\n\n")
	}

	b.WriteString(sectionRule(fmt.Sprintf("Reviews (%d)", len(reviews)), width))
	b.WriteString("\n")
	if len(reviews) == 0 {
		b.WriteString("  " + mutedStyle.Render("(none)") + "\n")
	}
	for _, r := range reviews {
		fmt.Fprintf(&b, "  %s  %s  %s\n", r.User, r.State, mutedStyle.Render(r.Age))
		if strings.TrimSpace(r.Body) != "" {
			b.WriteString(indent(wrapText(r.Body, width-4), "  > "))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(sectionRule("Pending review", width))
	b.WriteString("\n")
	if draftCount == 0 && reviewBody == "" {
		b.WriteString("  " + mutedStyle.Render("(no drafts)") + "\n")
	} else {
		fmt.Fprintf(&b, "  %d draft inline %s\n", draftCount, plural("comment", draftCount))
		if reviewBody != "" {
			b.WriteString("\n  review body:\n")
			b.WriteString(indent(wrapText(reviewBody, width-4), "  > "))
			b.WriteString("\n")
		}
		b.WriteString("\n  ")
		b.WriteString(mutedStyle.Render("Press S to submit · Press B to add review body"))
		b.WriteString("\n")
	}
	return b.String()
}

func sectionRule(title string, width int) string {
	const prefix = "──── "
	suffix := strings.Repeat("─", maxInt(0, width-len(title)-len(prefix)-1))
	return mutedStyle.Render(prefix + title + " " + suffix)
}

func indent(s, with string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = with + l
	}
	return strings.Join(lines, "\n")
}

func plural(noun string, n int) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}
