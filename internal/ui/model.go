package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bowenbrooks/gitreview/internal/ctxpane"
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
	paneContext
)

type viewMode int

const (
	viewChanges viewMode = iota
	viewCommits
	viewOverview
)

type Model struct {
	d                      *diff.Diff
	commits                []diff.Commit
	commitDiff             map[string]*diff.Diff
	commitErr              map[string]error
	repoRoot               string
	view                   viewMode
	rowCursor              int
	commitCursor           int
	overviewCursor         int // index into the filtered Files list when in overview view
	overviewCols           int // computed at render time so j/k can move by row
	focus                  pane
	splitView              bool
	contextPaneVisible     bool // user-toggled; default true
	contextPayload         ctxpane.Payload
	contextCursor          ctxpane.Cursor
	contextSelected        int   // currently highlighted item index when pane is focused
	contextRefreshSeq      int   // monotonic; used to ignore stale debounced ticks
	contextHistoryExpanded bool  // toggled by H when pane is focused
	hunkOffsets            []int // viewport line indices of each hunk in the current file
	width                  int
	height                 int
	forcedWidth            int
	viewport               viewport.Model
	ready                  bool
	statusMsg              string

	// filter state for the file list
	filterInput textinput.Model
	filtering   bool   // currently editing the filter
	filter      string // committed substring filter (empty = no filter)

	// tree state for the file explorer (Changes view).
	treeRows           []treeRow
	treeCollapsed      map[string]bool
	preFilterCollapsed map[string]bool // snapshot on filter start, restored on clear
	pathPreFilter      string          // file under cursor when filter started; restored on clear

	// reviewed marks — files the user has explicitly marked as walked-through.
	// Keyed by file path. Persists for the lifetime of the program; not stored
	// to disk yet.
	reviewedFiles map[string]bool
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
		d:                  d,
		commits:            commits,
		commitDiff:         map[string]*diff.Diff{},
		commitErr:          map[string]error{},
		repoRoot:           repoRoot,
		focus:              paneLeft,
		filterInput:        ti,
		reviewedFiles:      map[string]bool{},
		contextPaneVisible: true,
		treeCollapsed:      map[string]bool{},
	}
}

// editorDoneMsg is dispatched when tea.ExecProcess returns from the editor.
type editorDoneMsg struct{ err error }

// contextRefreshMsg is fired by tea.Tick after the debounce window. The Seq
// must match the model's current contextRefreshSeq, otherwise the tick is
// stale (a newer move has scheduled a fresher one) and we no-op.
type contextRefreshMsg struct{ Seq int }

// contextResultMsg carries the computed payload back from the resolver Cmd.
type contextResultMsg struct {
	Seq     int
	Payload ctxpane.Payload
}

const contextDebounce = 150 * time.Millisecond

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
		m.clampFocusToVisiblePanes()
		m.refreshDiff()
		m.ready = true
		return m, m.scheduleContextRefresh()

	case tea.KeyMsg:
		// While the filter input is focused, every key goes to it (except a few escapes).
		if m.filtering {
			return m.handleFilterKey(msg)
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.focus = m.nextFocus(+1)
			return m, nil
		case "shift+tab":
			m.focus = m.nextFocus(-1)
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
				return m, m.scheduleContextRefresh()
			}
			if m.focus == paneContext {
				m.contextMoveSelection(+1)
				return m, nil
			}
			if m.focus == paneLeft {
				return m, m.moveCursor(+1)
			}
			m.viewport.ScrollDown(1)
			return m, m.maybeScheduleHunkChange()
		case "k", "up":
			if m.view == viewOverview {
				m.moveOverview(0, -1)
				return m, m.scheduleContextRefresh()
			}
			if m.focus == paneContext {
				m.contextMoveSelection(-1)
				return m, nil
			}
			if m.focus == paneLeft {
				return m, m.moveCursor(-1)
			}
			m.viewport.ScrollUp(1)
			return m, m.maybeScheduleHunkChange()
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
				files, _ := m.effectiveFiles()
				if m.overviewCursor >= 0 && m.overviewCursor < len(files) {
					target := files[m.overviewCursor].Path
					m.setView(viewChanges)
					if row := RowOfFile(m.treeRows, target); row >= 0 {
						m.rowCursor = row
						m.refreshDiff()
					}
				} else {
					m.setView(viewChanges)
				}
				return m, m.scheduleContextRefresh()
			}
			if m.focus == paneContext {
				return m, m.contextJumpToSelected()
			}
			return m, nil
		case "g":
			if m.focus == paneLeft {
				return m, m.setCursor(0)
			}
			m.viewport.GotoTop()
			return m, m.maybeScheduleHunkChange()
		case "G":
			if m.focus == paneLeft {
				return m, m.setCursor(m.maxCursor())
			}
			m.viewport.GotoBottom()
			return m, m.maybeScheduleHunkChange()
		case "ctrl+d":
			m.viewport.HalfPageDown()
			return m, m.maybeScheduleHunkChange()
		case "ctrl+u":
			m.viewport.HalfPageUp()
			return m, m.maybeScheduleHunkChange()
		case "]":
			m.jumpHunk(+1)
			return m, m.maybeScheduleHunkChange()
		case "[":
			m.jumpHunk(-1)
			return m, m.maybeScheduleHunkChange()
		case "/":
			if m.view == viewChanges {
				return m, m.startFiltering()
			}
			return m, nil
		case "c":
			if m.filter != "" {
				m.clearFilter()
				return m, nil
			}
			m.contextPaneVisible = !m.contextPaneVisible
			m.layout()
			m.clampFocusToVisiblePanes()
			m.refreshDiff()
			return m, nil
		case "m":
			m.toggleReviewed()
			return m, nil
		case "M":
			m.jumpToNextUnreviewed()
			return m, nil
		case "e":
			return m, m.openInEditor()
		case "s":
			if m.view == viewChanges {
				m.splitView = !m.splitView
				m.layout()
				m.clampFocusToVisiblePanes()
				m.refreshDiff()
			} else {
				m.statusMsg = "split view only available in Changes view"
			}
			return m, nil
		case "H":
			if m.focus != paneContext {
				return m, nil
			}
			m.contextHistoryExpanded = !m.contextHistoryExpanded
			return m, m.scheduleContextRefresh()
		case "esc":
			if m.focus == paneContext {
				m.focus = paneDiff
				return m, nil
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

	case contextRefreshMsg:
		if msg.Seq != m.contextRefreshSeq {
			return m, nil // stale
		}
		cur := ctxpane.Cursor{
			File:            m.currentFileForContext(),
			HunkIndex:       m.currentHunkIndex(),
			Diff:            m.d,
			RepoRoot:        m.repoRoot,
			HistoryExpanded: m.contextHistoryExpanded,
		}
		m.contextCursor = cur
		seq := msg.Seq
		return m, func() tea.Msg {
			return contextResultMsg{Seq: seq, Payload: ctxpane.Resolve(context.Background(), cur)}
		}

	case contextResultMsg:
		if msg.Seq != m.contextRefreshSeq {
			return m, nil // stale
		}
		m.contextPayload = msg.Payload
		m.contextSelected = 0
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
		fr, _, ok := m.currentFileRow()
		if !ok {
			return diff.File{}, 0, false
		}
		f = fr
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
		parts := []string{m.renderLeftPane(), m.renderDiffPane()}
		if m.contextPaneWidthEffective() > 0 {
			parts = append(parts, m.renderContextPane())
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, parts...)
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
	parts := []string{
		fmt.Sprintf("%d files", len(m.d.Files)),
		fmt.Sprintf("+%d −%d", add, del),
	}
	if r := m.reviewedCount(); r > 0 {
		parts = append(parts, fmt.Sprintf("✓ %d/%d", r, len(m.d.Files)))
	}
	return mutedStyle.Render(strings.Join(parts, " · "))
}

// renderOverviewBody renders the spine grid filling the body area.
func (m Model) renderOverviewBody() string {
	files, _ := m.effectiveFiles()
	bodyH := m.height - headerRows - helpHeight
	if bodyH < 6 {
		bodyH = 6
	}
	out, _, _ := renderOverview(files, m.reviewedFiles, m.width, bodyH, m.overviewCursor)
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

const (
	headerRows = 1
	spineColW  = 2 // 1 col spine + 1 col gap inside the diff pane
)

const (
	contextPaneWidth   = 32  // fixed width when visible
	contextPaneMinTerm = 120 // hide entirely below this terminal width
)

// --- filter ---

func (m *Model) startFiltering() tea.Cmd {
	m.filtering = true
	m.filterInput.SetValue(m.filter)
	m.filterInput.CursorEnd()
	return m.filterInput.Focus()
}

func (m *Model) commitFilter() {
	m.filter = strings.TrimSpace(m.filterInput.Value())
	m.filtering = false
	m.filterInput.Blur()
	m.rowCursor = 0
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
	m.rowCursor = 0
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
	m.rowCursor = 0
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

func (m *Model) moveCursor(delta int) tea.Cmd {
	c := m.cursor() + delta
	if c < 0 {
		c = 0
	}
	if c > m.maxCursor() {
		c = m.maxCursor()
	}
	return m.setCursor(c)
}

func (m *Model) cursor() int {
	switch m.view {
	case viewCommits:
		return m.commitCursor
	case viewOverview:
		return m.overviewCursor
	}
	return m.rowCursor
}

// rowAtCursor returns the row the cursor currently points at, or a zero-value
// row if the cursor is out of range (e.g. tree just rebuilt, treeRows empty).
func (m Model) rowAtCursor() treeRow {
	if m.rowCursor < 0 || m.rowCursor >= len(m.treeRows) {
		return treeRow{}
	}
	return m.treeRows[m.rowCursor]
}

// currentFileRow returns the underlying diff.File for the row under the
// cursor. ok is false when the cursor is on a dir row or out of range.
// fileIdx is the index into m.effectiveFiles() — useful for callers that
// need to index into the file slice directly.
func (m Model) currentFileRow() (f diff.File, fileIdx int, ok bool) {
	r := m.rowAtCursor()
	if r.Kind != rowFile {
		return diff.File{}, -1, false
	}
	files, _ := m.effectiveFiles()
	if r.FileIdx < 0 || r.FileIdx >= len(files) {
		return diff.File{}, -1, false
	}
	return files[r.FileIdx], r.FileIdx, true
}

func (m *Model) setCursor(c int) tea.Cmd {
	changed := false
	switch m.view {
	case viewCommits:
		if c != m.commitCursor {
			m.commitCursor = c
			m.refreshDiff()
			changed = true
		}
	case viewOverview:
		if c != m.overviewCursor {
			m.overviewCursor = c
			m.refreshDiff()
			changed = true
		}
	default:
		if c != m.rowCursor {
			m.rowCursor = c
			m.refreshDiff()
			changed = true
		}
	}
	if changed {
		return m.scheduleContextRefresh()
	}
	return nil
}

func (m *Model) maxCursor() int {
	if m.view == viewCommits {
		return len(m.commits) - 1
	}
	if m.view == viewChanges {
		return len(m.treeRows) - 1
	}
	files, _ := m.effectiveFiles()
	return len(files) - 1
}

// --- reviewed marks ---

// currentFileIndex returns the cursor's index into the effective file list,
// or -1 if the current view doesn't have a per-file cursor OR the cursor is
// on a dir row.
func (m Model) currentFileIndex() int {
	switch m.view {
	case viewChanges:
		_, fileIdx, ok := m.currentFileRow()
		if !ok {
			return -1
		}
		return fileIdx
	case viewOverview:
		return m.overviewCursor
	}
	return -1
}

func (m *Model) toggleReviewed() {
	f, _, ok := m.currentFileRow()
	if !ok {
		m.statusMsg = "m: select a file to mark"
		return
	}
	if m.reviewedFiles[f.Path] {
		delete(m.reviewedFiles, f.Path)
	} else {
		m.reviewedFiles[f.Path] = true
	}
}

func (m *Model) jumpToNextUnreviewed() {
	if m.view == viewChanges {
		n := len(m.treeRows)
		if n == 0 {
			return
		}
		start := m.rowCursor
		for i := 1; i <= n; i++ {
			next := (start + i) % n
			r := m.treeRows[next]
			if r.Kind != rowFile {
				continue
			}
			if !m.reviewedFiles[r.Path] {
				m.rowCursor = next
				m.refreshDiff()
				return
			}
		}
		m.statusMsg = "all files reviewed"
		return
	}
	// Existing overview path: walk by file index as before.
	files, _ := m.effectiveFiles()
	n := len(files)
	if n == 0 {
		return
	}
	start := m.overviewCursor
	for i := 1; i <= n; i++ {
		next := (start + i) % n
		if !m.reviewedFiles[files[next].Path] {
			m.overviewCursor = next
			return
		}
	}
	m.statusMsg = "all files reviewed"
}

// reviewedCount returns how many files in the effective list are marked reviewed.
func (m Model) reviewedCount() int {
	if len(m.reviewedFiles) == 0 {
		return 0
	}
	files, _ := m.effectiveFiles()
	n := 0
	for _, f := range files {
		if m.reviewedFiles[f.Path] {
			n++
		}
	}
	return n
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

// nextFocus returns the focus target after stepping `dir` (±1) through the
// pane cycle, skipping panes that are currently hidden. Order: paneLeft →
// paneDiff → paneContext → paneLeft.
func (m Model) nextFocus(dir int) pane {
	order := []pane{paneLeft, paneDiff}
	if m.contextPaneWidthEffective() > 0 {
		order = append(order, paneContext)
	}
	pos := 0
	for i, p := range order {
		if p == m.focus {
			pos = i
			break
		}
	}
	pos = (pos + dir + len(order)) % len(order)
	return order[pos]
}

// contextSelectableItems returns a flat list of (sectionIdx, itemIdx) pairs
// for every Item whose Jump is non-nil — the only items the user can act on.
func (m Model) contextSelectableItems() []struct{ S, I int } {
	var out []struct{ S, I int }
	for si, s := range m.contextPayload.Sections {
		for ii, it := range s.Items {
			if it.Jump != nil {
				out = append(out, struct{ S, I int }{si, ii})
			}
		}
	}
	return out
}

func (m *Model) contextMoveSelection(delta int) {
	n := len(m.contextSelectableItems())
	if n == 0 {
		return
	}
	m.contextSelected = (m.contextSelected + delta + n) % n
}

// contextJumpToSelected moves the diff cursor to the file the selected pane
// item points at. Returns a Cmd that triggers a context refresh.
func (m *Model) contextJumpToSelected() tea.Cmd {
	items := m.contextSelectableItems()
	if len(items) == 0 || m.contextSelected < 0 || m.contextSelected >= len(items) {
		return nil
	}
	pos := items[m.contextSelected]
	it := m.contextPayload.Sections[pos.S].Items[pos.I]
	if it.Jump == nil {
		return nil
	}
	// Check that the target file is in the diff before jumping.
	files, _ := m.effectiveFiles()
	inDiff := false
	for _, f := range files {
		if f.Path == it.Jump.File {
			inDiff = true
			break
		}
	}
	if inDiff {
		m.refreshDiff()
		if row := RowOfFile(m.treeRows, it.Jump.File); row >= 0 {
			m.rowCursor = row
		}
		m.refreshDiff()
	}
	// We don't scroll the viewport to it.Jump.Line — viewport line math is
	// non-trivial. File-level jump is the v0 promise.
	return m.scheduleContextRefresh()
}

// clampFocusToVisiblePanes ensures focus never points at the hidden context
// pane. Call this after any change that may hide the pane (toggle, split,
// resize).
func (m *Model) clampFocusToVisiblePanes() {
	if m.focus == paneContext && m.contextPaneWidthEffective() == 0 {
		m.focus = paneDiff
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
	contextW := m.contextPaneWidthEffective()
	centerW := m.width - leftW - contextW
	// Reserve 2 cols on the right of the diff pane for the file-spine column
	// (1 col spine + 1 col gap). Empty in commits view but keeps layout stable.
	innerW := centerW - 4 - spineColW
	innerH := bodyH - 2 - 1

	if !m.ready {
		m.viewport = viewport.New(innerW, innerH)
	} else {
		m.viewport.Width = innerW
		m.viewport.Height = innerH
	}
}

// contextPaneWidthEffective returns 0 when the pane is hidden (user toggled it
// off, split view forces it off, or terminal too narrow); otherwise the fixed
// pane width.
func (m Model) contextPaneWidthEffective() int {
	if !m.contextPaneVisible {
		return 0
	}
	if m.splitView {
		return 0
	}
	if m.width < contextPaneMinTerm {
		return 0
	}
	return contextPaneWidth
}

func (m *Model) refreshDiff() {
	d := m.currentDiff()
	if d == nil {
		m.viewport.SetContent(mutedStyle.Render("(no changes)"))
		m.hunkOffsets = nil
		return
	}

	// Rebuild the file-explorer tree first so currentFileRow() is accurate.
	if m.view == viewChanges {
		files, _ := m.effectiveFiles()
		m.treeRows = BuildTree(files, m.reviewedFiles, m.treeCollapsed, m.filter)
	} else {
		m.treeRows = nil
	}

	if m.view == viewChanges {
		files, _ := m.effectiveFiles()
		if len(files) == 0 {
			m.viewport.SetContent(mutedStyle.Render("(no matches)"))
			m.hunkOffsets = nil
			return
		}
		fr, _, ok := m.currentFileRow()
		if !ok {
			m.viewport.SetContent(mutedStyle.Render("(select a file)"))
			m.hunkOffsets = nil
			return
		}
		f := fr
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

func (m Model) paneWidths() (left, center, context int) {
	left = int(float64(m.width) * leftRatio)
	context = m.contextPaneWidthEffective()
	center = m.width - left - context
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
	rowW := leftW - 4 // borders + padding
	if rowW < 8 {
		rowW = 8
	}

	var lines []string
	var sub string
	if m.filter != "" {
		files, _ := m.effectiveFiles()
		sub = fmt.Sprintf("%d/%d files · /%s", len(files), len(m.d.Files), m.filter)
	} else {
		sub = fmt.Sprintf("%d files", len(m.d.Files))
		if m.d.Label != "" {
			sub = m.d.Label + " · " + sub
		}
	}
	lines = append(lines, mutedStyle.Render(truncateRaw(sub, rowW)))

	if len(m.treeRows) == 0 {
		lines = append(lines, mutedStyle.Render("(no matches)"))
		return strings.Join(lines, "\n")
	}

	const sparkW = 6
	showSpark := rowW >= 30

	for i, r := range m.treeRows {
		var rendered string
		switch r.Kind {
		case rowDir:
			rendered = renderTreeDir(r, m.treeCollapsed[r.Path], rowW)
		case rowFile:
			files, _ := m.effectiveFiles()
			if r.FileIdx < 0 || r.FileIdx >= len(files) {
				continue
			}
			f := files[r.FileIdx]
			reviewed := m.reviewedFiles[f.Path]
			rendered = renderTreeFile(r, f, reviewed, showSpark, sparkW, rowW)
		}
		if i == m.rowCursor {
			plain := stripAnsiForCursor(r, m, rowW, sparkW, showSpark)
			rendered = cursorStyle.Render(plain)
		}
		lines = append(lines, rendered)
	}
	return strings.Join(lines, "\n")
}

// renderTreeDir renders a dir row: marker + compact-path + right-aligned aggregate.
func renderTreeDir(r treeRow, collapsed bool, rowW int) string {
	marker := "▾ "
	if collapsed {
		marker = "▸ "
	}
	left := marker + r.Label
	right := ""
	if r.Total > 0 {
		if r.Reviewed == r.Total {
			right = addedLineStyle.Render("✓")
		} else if r.Reviewed > 0 {
			right = mutedStyle.Render(fmt.Sprintf("✓ %d/%d", r.Reviewed, r.Total))
		}
	}
	left = truncateRaw(left, rowW-ansi.StringWidth(right)-1)
	if right == "" {
		return left
	}
	return padBetweenAnsi(left, right, rowW)
}

// renderTreeFile renders a file row: 2-col indent + guide + status + filename, plus stats/sparkline.
func renderTreeFile(r treeRow, f diff.File, reviewed bool, showSpark bool, sparkW int, rowW int) string {
	const indent = "  │ "
	statsPlain := formatFileStats(f)
	reserve := len(indent) + 3 + len(statsPlain)
	if showSpark {
		reserve += sparkW + 2
	}
	nameMaxW := rowW - reserve
	if nameMaxW < 4 {
		nameMaxW = 4
	}
	name := compactPath(r.Label, nameMaxW)

	var marker string
	if reviewed {
		marker = "✓"
	} else {
		marker = statusMarker(f.Status)
	}
	left := indent + marker + " " + name
	if reviewed {
		left = mutedStyle.Render(indent + "✓ " + name)
	}
	var right string
	if showSpark {
		if reviewed {
			right = mutedStyle.Render(renderSparklinePlain(f, sparkW)) + "  " + mutedStyle.Render(statsPlain)
		} else {
			right = renderSparkline(f, sparkW) + "  " + mutedStyle.Render(statsPlain)
		}
	} else {
		right = mutedStyle.Render(statsPlain)
	}
	return padBetweenAnsi(left, right, rowW)
}

// stripAnsiForCursor builds the plain (no-ANSI) version of a row for the
// cursor highlight (cursorStyle's background needs uniform text underneath).
func stripAnsiForCursor(r treeRow, m Model, rowW, sparkW int, showSpark bool) string {
	switch r.Kind {
	case rowDir:
		marker := "▾ "
		if m.treeCollapsed[r.Path] {
			marker = "▸ "
		}
		left := marker + r.Label
		var right string
		if r.Total > 0 {
			if r.Reviewed == r.Total {
				right = "✓"
			} else if r.Reviewed > 0 {
				right = fmt.Sprintf("✓ %d/%d", r.Reviewed, r.Total)
			}
		}
		if right == "" {
			return padBetweenPlain(left, "", rowW)
		}
		return padBetweenPlain(left, right, rowW)
	case rowFile:
		files, _ := m.effectiveFiles()
		if r.FileIdx < 0 || r.FileIdx >= len(files) {
			return ""
		}
		f := files[r.FileIdx]
		const indent = "  │ "
		reviewed := m.reviewedFiles[f.Path]
		statsPlain := formatFileStats(f)
		reserve := len(indent) + 3 + len(statsPlain)
		if showSpark {
			reserve += sparkW + 2
		}
		nameMaxW := rowW - reserve
		if nameMaxW < 4 {
			nameMaxW = 4
		}
		name := compactPath(r.Label, nameMaxW)
		var marker string
		if reviewed {
			marker = "✓"
		} else {
			marker = f.Status.String()
		}
		left := indent + marker + " " + name
		var right string
		if showSpark {
			right = renderSparklinePlain(f, sparkW) + "  " + statsPlain
		} else {
			right = statsPlain
		}
		return padBetweenPlain(left, right, rowW)
	}
	return ""
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
	_, centerW, _ := m.paneWidths()
	bodyH := m.height - headerRows - helpHeight
	header := titleStyle.Render(m.diffTitle())
	body := m.attachSpineColumn(m.viewport.View())
	content := header + "\n" + body
	return m.paneStyleFor(paneDiff, centerW, bodyH).Render(content)
}

// attachSpineColumn glues a 1-col file-spine bar to the right edge of the
// viewport content (with a 1-col gap). Only meaningful in viewChanges; in
// other views the reserved cols stay blank so layout doesn't jump.
func (m Model) attachSpineColumn(body string) string {
	if m.view != viewChanges {
		// Pad blank cols so the pane still consumes the same width.
		gap := strings.Repeat(" ", spineColW)
		var b strings.Builder
		for i, line := range strings.Split(body, "\n") {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(line)
			b.WriteString(gap)
		}
		return b.String()
	}
	f, _, ok := m.currentFileRow()
	if !ok {
		return body
	}
	spineRows := renderFileSpine(f, m.viewport.Height, m.currentHunkIndex())
	if len(spineRows) == 0 {
		return body
	}
	lines := strings.Split(body, "\n")
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(line)
		b.WriteString(" ")
		if i < len(spineRows) {
			b.WriteString(spineRows[i])
		} else {
			b.WriteString(" ")
		}
	}
	return b.String()
}

// currentHunkIndex returns the index of the hunk whose offset is just at or
// above the viewport's current YOffset, or -1 if no hunks.
func (m Model) currentHunkIndex() int {
	if len(m.hunkOffsets) == 0 {
		return -1
	}
	cur := m.viewport.YOffset
	active := 0
	for i, off := range m.hunkOffsets {
		if off > cur {
			break
		}
		active = i
	}
	return active
}

// currentFileForContext returns the file the context pane should describe,
// or a zero-value File if no file is selected.
func (m Model) currentFileForContext() diff.File {
	if m.view != viewChanges {
		return diff.File{}
	}
	f, _, ok := m.currentFileRow()
	if !ok {
		return diff.File{}
	}
	return f
}

// scheduleContextRefresh bumps the refresh seqno and returns a Cmd that fires
// a contextRefreshMsg after the debounce window. Callers should compose this
// with any other Cmd they return.
func (m *Model) scheduleContextRefresh() tea.Cmd {
	m.contextRefreshSeq++
	seq := m.contextRefreshSeq
	return tea.Tick(contextDebounce, func(time.Time) tea.Msg {
		return contextRefreshMsg{Seq: seq}
	})
}

// maybeScheduleHunkChange returns a refresh Cmd if the current hunk index
// has changed since the last context refresh.
func (m *Model) maybeScheduleHunkChange() tea.Cmd {
	newHunk := m.currentHunkIndex()
	if newHunk == m.contextCursor.HunkIndex {
		return nil
	}
	return m.scheduleContextRefresh()
}

func (m Model) diffTitle() string {
	d := m.currentDiffReadonly()
	if d == nil || len(d.Files) == 0 {
		return "(no changes)"
	}
	if m.view == viewCommits {
		return d.Label
	}
	if m.view == viewChanges {
		fr, _, ok := m.currentFileRow()
		if !ok {
			return "(select a file)"
		}
		if fr.Status == diff.StatusRenamed && fr.OldPath != "" && fr.OldPath != fr.Path {
			return fmt.Sprintf("%s → %s", fr.OldPath, fr.Path)
		}
		return fr.Path
	}
	// overview and other views — fall back to first file
	f := d.Files[0]
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
	parts := []string{"j/k file", "]/[ hunk", "m mark", "M next-unreviewed", "/ filter", "1/2/3 tab", splitHint, "e edit", "q quit"}
	if m.filter != "" {
		parts = append([]string{"c clear-filter"}, parts...)
	} else {
		parts = append(parts, "c ctx")
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
	if maxW <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxW {
		return s
	}
	if maxW < 2 {
		return string(runes[:maxW])
	}
	return string(runes[:maxW-1]) + "…"
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
