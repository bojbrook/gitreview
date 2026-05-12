package ui

import (
	"fmt"
	"strings"

	"github.com/bowenbrooks/gitreview/internal/diff"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type pane int

const (
	paneFiles pane = iota
	paneDiff
	paneComments
)

type viewMode int

const (
	viewChanges viewMode = iota
	viewCommits
)

type Model struct {
	d            *diff.Diff
	commits      []diff.Commit
	commitDiff   map[string]*diff.Diff
	commitErr    map[string]error
	view         viewMode
	fileCursor   int
	commitCursor int
	focus        pane
	width        int
	height       int
	viewport     viewport.Model
	ready        bool
	statusMsg    string
}

func New(d *diff.Diff, commits []diff.Commit) Model {
	return Model{
		d:          d,
		commits:    commits,
		commitDiff: map[string]*diff.Diff{},
		commitErr:  map[string]error{},
		focus:      paneFiles,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		m.refreshDiff()
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.focus = (m.focus + 1) % 3
			return m, nil
		case "shift+tab":
			m.focus = (m.focus + 2) % 3
			return m, nil
		case "v":
			m.toggleView()
			return m, nil
		case "j", "down":
			if m.focus == paneFiles {
				m.moveCursor(+1)
				return m, nil
			}
			m.viewport.ScrollDown(1)
			return m, nil
		case "k", "up":
			if m.focus == paneFiles {
				m.moveCursor(-1)
				return m, nil
			}
			m.viewport.ScrollUp(1)
			return m, nil
		case "g":
			if m.focus == paneFiles {
				m.setCursor(0)
			} else {
				m.viewport.GotoTop()
			}
			return m, nil
		case "G":
			if m.focus == paneFiles {
				m.setCursor(m.maxCursor())
			} else {
				m.viewport.GotoBottom()
			}
			return m, nil
		case "ctrl+d":
			m.viewport.HalfPageDown()
			return m, nil
		case "ctrl+u":
			m.viewport.HalfPageUp()
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	if !m.ready {
		return "loading…"
	}
	body := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.renderLeftPane(),
		m.renderDiffPane(),
		m.renderRightPane(),
	)
	return lipgloss.JoinVertical(lipgloss.Left, body, m.renderHelp())
}

// --- mode + cursor helpers ---

func (m *Model) toggleView() {
	if m.view == viewChanges {
		if len(m.commits) == 0 {
			m.statusMsg = "no commits to browse"
			return
		}
		m.view = viewCommits
	} else {
		m.view = viewChanges
	}
	m.statusMsg = ""
	m.refreshDiff()
}

func (m *Model) moveCursor(delta int) {
	c := m.cursor() + delta
	if c < 0 {
		c = 0
	}
	if c > m.maxCursor() {
		c = m.maxCursor()
	}
	m.setCursor(c)
}

func (m *Model) cursor() int {
	if m.view == viewCommits {
		return m.commitCursor
	}
	return m.fileCursor
}

func (m *Model) setCursor(c int) {
	if m.view == viewCommits {
		if c != m.commitCursor {
			m.commitCursor = c
			m.refreshDiff()
		}
		return
	}
	if c != m.fileCursor {
		m.fileCursor = c
		m.refreshDiff()
	}
}

func (m *Model) maxCursor() int {
	if m.view == viewCommits {
		return len(m.commits) - 1
	}
	if m.d == nil {
		return 0
	}
	return len(m.d.Files) - 1
}

// --- layout ---

const (
	leftRatio   = 0.22
	centerRatio = 0.53
	helpHeight  = 1
)

func (m *Model) layout() {
	if m.width < 40 || m.height < 10 {
		return
	}
	bodyH := m.height - helpHeight
	centerW := int(float64(m.width) * centerRatio)
	innerW := centerW - 2
	innerH := bodyH - 2 - 1

	if !m.ready {
		m.viewport = viewport.New(innerW, innerH)
	} else {
		m.viewport.Width = innerW
		m.viewport.Height = innerH
	}
}

func (m *Model) refreshDiff() {
	d := m.currentDiff()
	if d == nil || len(d.Files) == 0 {
		m.viewport.SetContent(mutedStyle.Render("(no changes)"))
		return
	}
	if m.view == viewChanges {
		f := d.Files[m.fileCursor]
		m.viewport.SetContent(renderDiff(f, m.viewport.Width))
	} else {
		m.viewport.SetContent(renderFullDiff(d.Files, m.viewport.Width))
	}
	m.viewport.GotoTop()
}

// currentDiff returns the diff being displayed for the current view. In commits view
// this lazily loads (and caches) the diff for the selected commit.
func (m *Model) currentDiff() *diff.Diff {
	if m.view == viewChanges {
		return m.d
	}
	if len(m.commits) == 0 {
		return nil
	}
	c := m.commits[m.commitCursor]
	if cached, ok := m.commitDiff[c.SHA]; ok {
		return cached
	}
	if _, failed := m.commitErr[c.SHA]; failed {
		return nil
	}
	d, err := diff.LoadCommitDiff(c)
	if err != nil {
		m.commitErr[c.SHA] = err
		return nil
	}
	m.commitDiff[c.SHA] = d
	return d
}

// --- panes ---

func (m Model) paneWidths() (left, center, right int) {
	left = int(float64(m.width) * leftRatio)
	center = int(float64(m.width) * centerRatio)
	right = m.width - left - center
	return
}

func (m Model) paneStyleFor(p pane, w, h int) lipgloss.Style {
	s := paneStyle
	if m.focus == p {
		s = paneFocusStyle
	}
	return s.Width(w - 2).Height(h - 2)
}

func (m Model) renderLeftPane() string {
	leftW, _, _ := m.paneWidths()
	bodyH := m.height - helpHeight
	var content string
	if m.view == viewCommits {
		content = m.renderCommitsList(leftW)
	} else {
		content = m.renderFilesList(leftW)
	}
	return m.paneStyleFor(paneFiles, leftW, bodyH).Render(content)
}

func (m Model) renderFilesList(leftW int) string {
	var lines []string
	header := fmt.Sprintf("Files (%d)", len(m.d.Files))
	if m.d.Label != "" {
		header += " · " + m.d.Label
	}
	lines = append(lines, titleStyle.Render(truncateRaw(header, leftW-4)))

	for i, f := range m.d.Files {
		marker := statusMarker(f.Status)
		name := compactPath(f.Path, leftW-6)
		row := fmt.Sprintf("%s %s", marker, name)
		if i == m.fileCursor {
			row = cursorStyle.Render(row)
		}
		lines = append(lines, row)
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderCommitsList(leftW int) string {
	var lines []string
	header := fmt.Sprintf("Commits (%d)", len(m.commits))
	lines = append(lines, titleStyle.Render(header))

	for i, c := range m.commits {
		summary := compactPath(c.Subject, leftW-12)
		row := fmt.Sprintf("%s %s", mutedStyle.Render(c.ShortSHA), summary)
		if i == m.commitCursor {
			row = cursorStyle.Render(fmt.Sprintf("%s %s", c.ShortSHA, summary))
		}
		lines = append(lines, row)
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderDiffPane() string {
	_, centerW, _ := m.paneWidths()
	bodyH := m.height - helpHeight
	header := titleStyle.Render(m.diffTitle())
	body := m.viewport.View()
	content := header + "\n" + body
	return m.paneStyleFor(paneDiff, centerW, bodyH).Render(content)
}

func (m Model) renderRightPane() string {
	_, _, rightW := m.paneWidths()
	bodyH := m.height - helpHeight
	var content string
	if m.view == viewCommits && len(m.commits) > 0 {
		c := m.commits[m.commitCursor]
		content = renderCommitMeta(c, rightW-4)
	} else {
		content = titleStyle.Render("Comments") + "\n\n" + mutedStyle.Render("(no draft yet — Claude pending)")
	}
	return m.paneStyleFor(paneComments, rightW, bodyH).Render(content)
}

func (m Model) diffTitle() string {
	d := m.currentDiffReadonly()
	if d == nil || len(d.Files) == 0 {
		return "(no changes)"
	}
	if m.view == viewCommits {
		return d.Label
	}
	f := d.Files[m.fileCursor]
	if f.Status == diff.StatusRenamed && f.OldPath != "" && f.OldPath != f.Path {
		return fmt.Sprintf("%s → %s", f.OldPath, f.Path)
	}
	return f.Path
}

// currentDiffReadonly returns the cached diff without triggering a load. Used by render code
// that runs after refreshDiff has populated the viewport.
func (m Model) currentDiffReadonly() *diff.Diff {
	if m.view == viewChanges {
		return m.d
	}
	if len(m.commits) == 0 {
		return nil
	}
	return m.commitDiff[m.commits[m.commitCursor].SHA]
}

func (m Model) renderHelp() string {
	parts := []string{"j/k: nav", "J/K or ctrl+d/u: scroll", "tab: focus", "v: changes/commits", "q: quit"}
	hint := strings.Join(parts, "  ")
	if m.statusMsg != "" {
		hint = mutedStyle.Render(m.statusMsg) + "  ·  " + hint
	}
	return helpStyle.Render(hint)
}

func statusMarker(s diff.FileStatus) string {
	switch s {
	case diff.StatusAdded:
		return lipgloss.NewStyle().Foreground(colStatusAdd).Render("A")
	case diff.StatusDeleted:
		return lipgloss.NewStyle().Foreground(colStatusDel).Render("D")
	case diff.StatusRenamed:
		return lipgloss.NewStyle().Foreground(colStatusRen).Render("R")
	default:
		return lipgloss.NewStyle().Foreground(colStatusMod).Render("M")
	}
}

func compactPath(p string, maxW int) string {
	if maxW <= 0 || len(p) <= maxW {
		return p
	}
	if maxW < 4 {
		return p[:maxW]
	}
	return "…" + p[len(p)-(maxW-1):]
}

func truncateRaw(s string, maxW int) string {
	if maxW <= 0 || len(s) <= maxW {
		return s
	}
	if maxW < 2 {
		return s[:maxW]
	}
	return s[:maxW-1] + "…"
}

func renderCommitMeta(c diff.Commit, maxW int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(c.ShortSHA))
	b.WriteString("\n")
	b.WriteString(c.Subject)
	b.WriteString("\n\n")
	b.WriteString(mutedStyle.Render(fmt.Sprintf("%s <%s>", c.Author, c.Email)))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(c.RelDate + " · " + c.IsoDate))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("parents: "))
	if c.IsRoot() {
		b.WriteString(mutedStyle.Render("(root)"))
	} else {
		shortParents := make([]string, len(c.Parents))
		for i, p := range c.Parents {
			if len(p) > 7 {
				shortParents[i] = p[:7]
			} else {
				shortParents[i] = p
			}
		}
		b.WriteString(strings.Join(shortParents, " "))
	}
	if c.Body != "" {
		b.WriteString("\n\n")
		b.WriteString(c.Body)
	}
	return b.String()
}
