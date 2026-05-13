package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/bowenbrooks/gitreview/internal/diff"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
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
	viewOverview
)

type Model struct {
	d            *diff.Diff
	commits      []diff.Commit
	commitDiff   map[string]*diff.Diff
	commitErr    map[string]error
	repoRoot     string
	view         viewMode
	fileCursor   int
	commitCursor   int
	overviewCursor int // index into the filtered Files list when in overview view
	overviewCols   int // computed at render time so j/k can move by row
	focus          pane
	splitView      bool
	hunkOffsets    []int // viewport line indices of each hunk in the current file
	width        int
	height       int
	forcedWidth  int
	viewport     viewport.Model
	ready        bool
	statusMsg    string

	// filter state for the file list
	filterInput     textinput.Model
	filtering       bool   // currently editing the filter
	filter          string // committed substring filter (empty = no filter)
	cursorPreFilter int    // fileCursor before filter began, restored on clear
}

// ForceWidth overrides the terminal width bubbletea reports. Useful when
// running inside a multiplexer that reports a stale or wrong size.
func (m *Model) ForceWidth(w int) {
	m.forcedWidth = w
}

func New(d *diff.Diff, commits []diff.Commit, repoRoot string) Model {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.Placeholder = "filter files…"
	ti.CharLimit = 100
	return Model{
		d:           d,
		commits:     commits,
		commitDiff:  map[string]*diff.Diff{},
		commitErr:   map[string]error{},
		repoRoot:    repoRoot,
		focus:       paneLeft,
		filterInput: ti,
	}
}

// editorDoneMsg is dispatched when tea.ExecProcess returns from the editor.
type editorDoneMsg struct{ err error }

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Query the OS directly for terminal size. If it reports a smaller
		// width than bubbletea (which can happen inside tmux or when the
		// shell's COLUMNS is stale), trust the OS — content wider than the
		// physical terminal causes the per-line wrap we've been chasing.
		if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
			if w > 0 && w < m.width {
				m.width = w
			}
			if h > 0 && h < m.height {
				m.height = h
			}
		}
		if m.forcedWidth > 0 {
			m.width = m.forcedWidth
		}
		m.layout()
		m.refreshDiff()
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		// While the filter input is focused, every key goes to it (except a few escapes).
		if m.filtering {
			return m.handleFilterKey(msg)
		}
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
		case "3", "o":
			m.setView(viewOverview)
			return m, nil
		case "j", "down":
			if m.view == viewOverview {
				m.moveOverview(0, +1)
				return m, nil
			}
			if m.focus == paneLeft {
				m.moveCursor(+1)
				return m, nil
			}
			m.viewport.ScrollDown(1)
			return m, nil
		case "k", "up":
			if m.view == viewOverview {
				m.moveOverview(0, -1)
				return m, nil
			}
			if m.focus == paneLeft {
				m.moveCursor(-1)
				return m, nil
			}
			m.viewport.ScrollUp(1)
			return m, nil
		case "h", "left":
			if m.view == viewOverview {
				m.moveOverview(-1, 0)
				return m, nil
			}
			return m, nil
		case "l", "right":
			if m.view == viewOverview {
				m.moveOverview(+1, 0)
				return m, nil
			}
			return m, nil
		case "enter":
			if m.view == viewOverview {
				m.fileCursor = m.overviewCursor
				m.setView(viewChanges)
				return m, nil
			}
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
		case "]":
			m.jumpHunk(+1)
			return m, nil
		case "[":
			m.jumpHunk(-1)
			return m, nil
		case "/":
			if m.view == viewChanges {
				return m, m.startFiltering()
			}
			return m, nil
		case "c":
			if m.filter != "" {
				m.clearFilter()
			}
			return m, nil
		case "e":
			return m, m.openInEditor()
		case "s":
			if m.view == viewChanges {
				m.splitView = !m.splitView
				m.refreshDiff()
			} else {
				m.statusMsg = "split view only available in Changes view"
			}
			return m, nil
		}

	case editorDoneMsg:
		if msg.err != nil {
			m.statusMsg = "editor: " + msg.err.Error()
		} else {
			m.statusMsg = "edit done — quit and re-run to refresh"
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// openInEditor launches $EDITOR on the currently selected file at its first
// hunk's new-side line. Returns nil (no-op) if there's no editable file.
func (m *Model) openInEditor() tea.Cmd {
	f, line, ok := m.selectedEditTarget()
	if !ok {
		m.statusMsg = "nothing to edit here"
		return nil
	}
	if m.repoRoot == "" {
		m.statusMsg = "no repo root"
		return nil
	}
	abs := m.repoRoot + "/" + f.Path
	cmd := editorCmd(abs, line)
	if cmd == nil {
		m.statusMsg = "no editor found (set $EDITOR)"
		return nil
	}
	m.statusMsg = ""
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return editorDoneMsg{err: err}
	})
}

// selectedEditTarget returns the file and line to jump to in the editor.
// Picks the first hunk's first added/context line (new side). Returns ok=false
// for deleted files or when no file is selected.
func (m *Model) selectedEditTarget() (diff.File, int, bool) {
	d := m.currentDiffReadonly()
	if d == nil || len(d.Files) == 0 {
		return diff.File{}, 0, false
	}
	var f diff.File
	if m.view == viewCommits {
		// In commits view there's no file cursor — open the first file of the commit's diff.
		f = d.Files[0]
	} else {
		files, _ := m.effectiveFiles()
		if m.fileCursor >= len(files) {
			return diff.File{}, 0, false
		}
		f = files[m.fileCursor]
	}
	if f.Status == diff.StatusDeleted {
		return diff.File{}, 0, false
	}
	line := 1
	if len(f.Hunks) > 0 {
		h := f.Hunks[0]
		if h.NewStart > 0 {
			line = h.NewStart
		}
		for _, l := range h.Lines {
			if l.Kind == diff.LineAdded && l.NewNum > 0 {
				line = l.NewNum
				break
			}
		}
	}
	return f, line, true
}

func (m Model) View() string {
	if !m.ready {
		return "loading…"
	}
	header := m.renderTopHeader()
	var body string
	if m.view == viewOverview {
		body = m.renderOverviewBody()
	} else {
		body = lipgloss.JoinHorizontal(
			lipgloss.Top,
			m.renderLeftPane(),
			m.renderDiffPane(),
		)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, m.renderHelp())
}

// renderTopHeader is a single-line strip showing the three view tabs and PR
// stats. Always visible at the top of the screen.
func (m Model) renderTopHeader() string {
	tabs := m.renderTabsGlobal()
	stats := m.renderStats()
	return padBetweenAnsi(tabs, stats, m.width)
}

func (m Model) renderTabsGlobal() string {
	style := func(active bool) lipgloss.Style {
		if active {
			return activeTabStyle
		}
		return inactiveTabStyle
	}
	parts := []string{
		style(m.view == viewChanges).Render("[1 changes]"),
		style(m.view == viewCommits).Render("[2 commits]"),
		style(m.view == viewOverview).Render("[3 overview]"),
	}
	return strings.Join(parts, " ")
}

func (m Model) renderStats() string {
	if m.d == nil {
		return ""
	}
	var add, del int
	for _, f := range m.d.Files {
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
	}
	return mutedStyle.Render(fmt.Sprintf("%d files · +%d −%d", len(m.d.Files), add, del))
}

// renderOverviewBody renders the spine grid filling the body area.
func (m Model) renderOverviewBody() string {
	files, _ := m.effectiveFiles()
	bodyH := m.height - headerRows - helpHeight
	if bodyH < 6 {
		bodyH = 6
	}
	out, _, _ := renderOverview(files, m.width, bodyH, m.overviewCursor)
	return out
}

// overviewColsAtWidth returns how many spine cells fit per row at the current
// terminal width. Used by moveOverview for row math (j/k = ± cols).
func (m Model) overviewColsAtWidth() int {
	cols := m.width / spineCellW
	if cols < 1 {
		cols = 1
	}
	return cols
}

const headerRows = 1

// --- filter ---

func (m *Model) startFiltering() tea.Cmd {
	if !m.filtering {
		m.cursorPreFilter = m.fileCursor
	}
	m.filtering = true
	m.filterInput.SetValue(m.filter)
	m.filterInput.CursorEnd()
	return m.filterInput.Focus()
}

func (m *Model) commitFilter() {
	m.filter = strings.TrimSpace(m.filterInput.Value())
	m.filtering = false
	m.filterInput.Blur()
	m.fileCursor = 0
	m.refreshDiff()
}

func (m *Model) cancelFilter() {
	m.filtering = false
	m.filterInput.Blur()
	m.filterInput.SetValue(m.filter) // restore committed value
}

func (m *Model) clearFilter() {
	m.filter = ""
	m.filterInput.SetValue("")
	m.fileCursor = m.cursorPreFilter
	m.refreshDiff()
}

func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.cancelFilter()
		return m, nil
	case "enter":
		m.commitFilter()
		return m, nil
	}
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	// Live update: re-filter and refresh the diff for the first matching file.
	m.filter = strings.TrimSpace(m.filterInput.Value())
	m.fileCursor = 0
	m.refreshDiff()
	return m, cmd
}

// effectiveFiles returns the file list to display (filtered if a filter is set)
// along with the index map back to m.d.Files.
func (m Model) effectiveFiles() (files []diff.File, indexMap []int) {
	if m.d == nil {
		return nil, nil
	}
	if m.filter == "" {
		files = m.d.Files
		indexMap = make([]int, len(files))
		for i := range files {
			indexMap[i] = i
		}
		return
	}
	needle := strings.ToLower(m.filter)
	for i, f := range m.d.Files {
		if strings.Contains(strings.ToLower(f.Path), needle) {
			files = append(files, f)
			indexMap = append(indexMap, i)
		}
	}
	return
}

// --- hunk jump ---

func (m *Model) jumpHunk(dir int) {
	if len(m.hunkOffsets) == 0 {
		return
	}
	cur := m.viewport.YOffset
	if dir > 0 {
		for _, off := range m.hunkOffsets {
			if off > cur {
				m.viewport.SetYOffset(off)
				return
			}
		}
		// already past last hunk — go to last
		m.viewport.SetYOffset(m.hunkOffsets[len(m.hunkOffsets)-1])
		return
	}
	target := m.hunkOffsets[0]
	for _, off := range m.hunkOffsets {
		if off >= cur {
			break
		}
		target = off
	}
	m.viewport.SetYOffset(target)
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
	if v == viewOverview {
		files, _ := m.effectiveFiles()
		if len(files) == 0 {
			m.statusMsg = "no files to overview"
			return
		}
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
	switch m.view {
	case viewCommits:
		return m.commitCursor
	case viewOverview:
		return m.overviewCursor
	}
	return m.fileCursor
}

func (m *Model) setCursor(c int) {
	switch m.view {
	case viewCommits:
		if c != m.commitCursor {
			m.commitCursor = c
			m.refreshDiff()
		}
	case viewOverview:
		if c != m.overviewCursor {
			m.overviewCursor = c
			m.refreshDiff()
		}
	default:
		if c != m.fileCursor {
			m.fileCursor = c
			m.refreshDiff()
		}
	}
}

func (m *Model) maxCursor() int {
	if m.view == viewCommits {
		return len(m.commits) - 1
	}
	files, _ := m.effectiveFiles()
	return len(files) - 1
}

// moveOverview moves the 2D cursor in the spine grid by (dx, dy) cells.
// Wraps on rows (so going past the right edge jumps to the next row).
func (m *Model) moveOverview(dx, dy int) {
	files, _ := m.effectiveFiles()
	if len(files) == 0 {
		return
	}
	cols := m.overviewColsAtWidth()
	c := m.overviewCursor + dx + dy*cols
	if c < 0 {
		c = 0
	}
	if c >= len(files) {
		c = len(files) - 1
	}
	if c != m.overviewCursor {
		m.overviewCursor = c
	}
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
	bodyH := m.height - headerRows - helpHeight
	leftW := int(float64(m.width) * leftRatio)
	centerW := m.width - leftW
	innerW := centerW - 4 // borders (2) + horizontal padding (2)
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
	if d == nil {
		m.viewport.SetContent(mutedStyle.Render("(no changes)"))
		m.hunkOffsets = nil
		return
	}
	if m.view == viewChanges {
		files, _ := m.effectiveFiles()
		if len(files) == 0 {
			m.viewport.SetContent(mutedStyle.Render("(no matches)"))
			m.hunkOffsets = nil
			return
		}
		if m.fileCursor >= len(files) {
			m.fileCursor = 0
		}
		f := files[m.fileCursor]
		if m.splitView {
			m.viewport.SetContent(renderSplit(f, m.viewport.Width))
			m.hunkOffsets = hunkOffsetsSplit(f)
		} else {
			m.viewport.SetContent(renderDiff(f, m.viewport.Width))
			m.hunkOffsets = hunkOffsetsUnified(f)
		}
	} else {
		if len(d.Files) == 0 {
			m.viewport.SetContent(mutedStyle.Render("(no changes)"))
			m.hunkOffsets = nil
			return
		}
		m.viewport.SetContent(renderFullDiff(d.Files, m.viewport.Width))
		m.hunkOffsets = nil
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
	bodyH := m.height - headerRows - helpHeight

	var listContent string
	if m.view == viewCommits {
		listContent = m.renderCommitsList(leftW)
	} else {
		listContent = m.renderFilesList(leftW)
	}
	return m.paneStyleFor(paneLeft, leftW, bodyH).Render(listContent)
}

func (m Model) renderFilesList(leftW int) string {
	if m.d == nil || len(m.d.Files) == 0 {
		return mutedStyle.Render("(no files)")
	}
	rowW := leftW - 4 // borders (2) + horizontal padding (2)
	if rowW < 8 {
		rowW = 8
	}

	files, _ := m.effectiveFiles()

	var lines []string
	var sub string
	if m.filter != "" {
		sub = fmt.Sprintf("%d/%d files · /%s", len(files), len(m.d.Files), m.filter)
	} else {
		sub = fmt.Sprintf("%d files", len(m.d.Files))
		if m.d.Label != "" {
			sub = m.d.Label + " · " + sub
		}
	}
	lines = append(lines, mutedStyle.Render(truncateRaw(sub, rowW)))

	if len(files) == 0 {
		lines = append(lines, mutedStyle.Render("(no matches)"))
		return strings.Join(lines, "\n")
	}

	for i, f := range files {
		statsPlain := formatFileStats(f)
		pathMaxW := rowW - 3 - len(statsPlain)
		if pathMaxW < 4 {
			pathMaxW = 4
		}
		name := compactPath(f.Path, pathMaxW)

		if i == m.fileCursor {
			plainLeft := fmt.Sprintf("%s %s", f.Status, name)
			row := padBetweenPlain(plainLeft, statsPlain, rowW)
			lines = append(lines, cursorStyle.Render(row))
		} else {
			coloredLeft := fmt.Sprintf("%s %s", statusMarker(f.Status), name)
			coloredStats := mutedStyle.Render(statsPlain)
			row := padBetweenAnsi(coloredLeft, coloredStats, rowW)
			lines = append(lines, row)
		}
	}
	return strings.Join(lines, "\n")
}

func padBetweenPlain(left, right string, width int) string {
	if len(left)+len(right) >= width {
		return left
	}
	return left + strings.Repeat(" ", width-len(left)-len(right)) + right
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
	bodyH := m.height - headerRows - helpHeight
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
	// While filtering, replace the help line with the live input.
	if m.filtering {
		hint := m.filterInput.View() + mutedStyle.Render("   Enter apply · Esc cancel")
		return helpStyle.Render(hint)
	}
	splitHint := "s: split"
	if m.splitView {
		splitHint = "s: unified"
	}
	parts := []string{"j/k file", "]/[ hunk", "/ filter", "tab focus", "1/2 tab", splitHint, "e edit", "q quit"}
	if m.filter != "" {
		parts = append([]string{"c clear-filter"}, parts...)
	}
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
