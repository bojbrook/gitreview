package ui

import (
	"fmt"
	"strings"

	"github.com/bowenbrooks/gitreview/internal/diff"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type pane int

const (
	paneLeft pane = iota
	paneDiff
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
		focus:      paneLeft,
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
			m.focus = (m.focus + 1) % 2
			return m, nil
		case "shift+tab":
			m.focus = (m.focus + 1) % 2
			return m, nil
		case "v":
			m.toggleView()
			return m, nil
		case "1":
			m.setView(viewChanges)
			return m, nil
		case "2":
			m.setView(viewCommits)
			return m, nil
		case "j", "down":
			if m.focus == paneLeft {
				m.moveCursor(+1)
				return m, nil
			}
			m.viewport.ScrollDown(1)
			return m, nil
		case "k", "up":
			if m.focus == paneLeft {
				m.moveCursor(-1)
				return m, nil
			}
			m.viewport.ScrollUp(1)
			return m, nil
		case "g":
			if m.focus == paneLeft {
				m.setCursor(0)
			} else {
				m.viewport.GotoTop()
			}
			return m, nil
		case "G":
			if m.focus == paneLeft {
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
	)
	return lipgloss.JoinVertical(lipgloss.Left, body, m.renderHelp())
}

// --- mode + cursor helpers ---

func (m *Model) toggleView() {
	if m.view == viewChanges {
		m.setView(viewCommits)
	} else {
		m.setView(viewChanges)
	}
}

func (m *Model) setView(v viewMode) {
	if v == viewCommits && len(m.commits) == 0 {
		m.statusMsg = "no commits to browse"
		return
	}
	if v == m.view {
		return
	}
	m.view = v
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
	leftRatio  = 0.28
	helpHeight = 1
)

func (m *Model) layout() {
	if m.width < 40 || m.height < 10 {
		return
	}
	bodyH := m.height - helpHeight
	leftW := int(float64(m.width) * leftRatio)
	centerW := m.width - leftW
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
		if m.fileCursor >= len(d.Files) {
			m.fileCursor = 0
		}
		f := d.Files[m.fileCursor]
		m.viewport.SetContent(renderDiff(f, m.viewport.Width))
	} else {
		m.viewport.SetContent(renderFullDiff(d.Files, m.viewport.Width))
	}
	m.viewport.GotoTop()
}

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

func (m Model) paneWidths() (left, center int) {
	left = int(float64(m.width) * leftRatio)
	center = m.width - left
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
	leftW, _ := m.paneWidths()
	bodyH := m.height - helpHeight

	tabs := m.renderTabs(leftW - 4)
	var listContent string
	if m.view == viewCommits {
		listContent = m.renderCommitsList(leftW)
	} else {
		listContent = m.renderFilesList(leftW)
	}
	content := tabs + "\n" + listContent
	return m.paneStyleFor(paneLeft, leftW, bodyH).Render(content)
}

func (m Model) renderTabs(maxW int) string {
	style := func(active bool) lipgloss.Style {
		if active {
			return activeTabStyle
		}
		return inactiveTabStyle
	}
	filesTab := style(m.view == viewChanges).Render("Files")
	commitsTab := style(m.view == viewCommits).Render("Commits")
	row := filesTab + tabSepStyle.Render(" │ ") + commitsTab
	return truncateAnsi(row, maxW)
}

func (m Model) renderFilesList(leftW int) string {
	if m.d == nil || len(m.d.Files) == 0 {
		return mutedStyle.Render("(no files)")
	}
	var lines []string
	sub := fmt.Sprintf("%d files", len(m.d.Files))
	if m.d.Label != "" {
		sub = m.d.Label + " · " + sub
	}
	lines = append(lines, mutedStyle.Render(truncateRaw(sub, leftW-4)))
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
	if len(m.commits) == 0 {
		return mutedStyle.Render("(no commits)")
	}
	var lines []string
	lines = append(lines, mutedStyle.Render(fmt.Sprintf("%d commits", len(m.commits))))
	for i, c := range m.commits {
		summary := compactPath(c.Subject, leftW-12)
		var row string
		if i == m.commitCursor {
			row = cursorStyle.Render(fmt.Sprintf("%s %s", c.ShortSHA, summary))
		} else {
			row = fmt.Sprintf("%s %s", mutedStyle.Render(c.ShortSHA), summary)
		}
		lines = append(lines, row)
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderDiffPane() string {
	_, centerW := m.paneWidths()
	bodyH := m.height - helpHeight
	header := titleStyle.Render(m.diffTitle())
	body := m.viewport.View()
	content := header + "\n" + body
	return m.paneStyleFor(paneDiff, centerW, bodyH).Render(content)
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
	parts := []string{"j/k: nav", "J/K or ctrl+d/u: scroll", "tab: focus", "1/2 or v: switch tab", "q: quit"}
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

// truncateAnsi cuts a string with embedded ANSI escapes to maxW visible columns,
// preserving the escapes so styles never bleed into following lines.
func truncateAnsi(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= maxW {
		return s
	}
	return ansi.Truncate(s, maxW, "…")
}
