package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/bowenbrooks/gitreview/internal/ctxpane"
	"github.com/bowenbrooks/gitreview/internal/diff"
	"github.com/bowenbrooks/gitreview/internal/pr"
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
	viewPR
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

	prMeta *pr.PRMeta // non-nil when running in PR mode

	// PR comment state — non-empty only in PR mode.
	reviewComments []ctxpane.CommentRef // fetched, mapped from pr.ReviewComment
	drafts         []ctxpane.Draft      // in-memory; cleared on submit
	issueComments  []ctxpane.IssueCommentDisplay
	reviews        []ctxpane.ReviewDisplay
	reviewBody     string // composed via B; consumed by S
	submitter      func(ctx context.Context, body string, drafts []pr.SubmitDraft) error
	refetcher      func(ctx context.Context) (*RefetcherResult, error)

	// Thread modal state.
	modalOpen     bool
	modalEntries  []threadEntry
	modalSelected int
	modalAnchor   modalAnchor
}

type modalAnchor struct {
	Path string
	Line int
	Side string
}

// ForceWidth overrides the terminal width bubbletea reports. Useful when
// running inside a multiplexer that reports a stale or wrong size.
func (m *Model) ForceWidth(w int) {
	m.forcedWidth = w
}

// PRBundle is the optional PR data ui.New accepts. nil in pre-flight mode.
type PRBundle struct {
	Meta           *pr.PRMeta
	ReviewComments []ctxpane.CommentRef
	IssueComments  []ctxpane.IssueCommentDisplay
	Reviews        []ctxpane.ReviewDisplay
	Submitter      func(ctx context.Context, body string, drafts []pr.SubmitDraft) error
	// Refetcher re-pulls the three comment streams from GitHub. Called after
	// successful submit so just-posted comments appear without re-launch.
	Refetcher func(ctx context.Context) (*RefetcherResult, error)
}

// RefetcherResult is what the Refetcher closure returns: the three comment
// streams already mapped into UI-domain display types.
type RefetcherResult struct {
	ReviewComments []ctxpane.CommentRef
	IssueComments  []ctxpane.IssueCommentDisplay
	Reviews        []ctxpane.ReviewDisplay
}

func New(d *diff.Diff, commits []diff.Commit, repoRoot string, pb *PRBundle) Model {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.Placeholder = "filter files…"
	ti.CharLimit = 100
	m := Model{
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
	if pb != nil {
		m.prMeta = pb.Meta
		m.reviewComments = pb.ReviewComments
		m.issueComments = pb.IssueComments
		m.reviews = pb.Reviews
		m.submitter = pb.Submitter
		m.refetcher = pb.Refetcher
	}
	return m
}

// renderPRTabBody renders the [4 PR] tab body. For v1, no internal scrolling
// (content fits in most terminals); add a viewport in a follow-up if needed.
func (m Model) renderPRTabBody() string {
	innerW := m.width - 4
	if innerW < 20 {
		innerW = 20
	}
	content := renderPRTabBody(m.prMeta, m.issueComments, m.reviews, len(m.drafts), m.reviewBody, innerW)
	bodyH := m.height - headerRows - helpHeight - 2
	return paneStyle.Width(m.width - 2).Height(bodyH).Render(content)
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
		// Modal traps all keys when open.
		if m.modalOpen {
			switch msg.String() {
			case "esc":
				m.modalOpen = false
				return m, nil
			case "j", "down":
				if m.modalSelected+1 < len(m.modalEntries) {
					m.modalSelected++
				}
				return m, nil
			case "k", "up":
				if m.modalSelected > 0 {
					m.modalSelected--
				}
				return m, nil
			case "x":
				if m.modalSelected >= 0 && m.modalSelected < len(m.modalEntries) {
					e := m.modalEntries[m.modalSelected]
					if e.IsDraft && e.DraftIdx >= 0 && e.DraftIdx < len(m.drafts) {
						m.drafts = append(m.drafts[:e.DraftIdx], m.drafts[e.DraftIdx+1:]...)
						m.modalEntries = buildThread(m.reviewComments, m.drafts, m.modalAnchor.Path, m.modalAnchor.Line, m.modalAnchor.Side)
						if m.modalSelected >= len(m.modalEntries) {
							m.modalSelected = maxInt(0, len(m.modalEntries)-1)
						}
						if len(m.modalEntries) == 0 {
							m.modalOpen = false
						}
						return m, m.scheduleContextRefresh()
					}
				}
				return m, nil
			case "e":
				if m.modalSelected >= 0 && m.modalSelected < len(m.modalEntries) {
					e := m.modalEntries[m.modalSelected]
					if e.IsDraft {
						return m, m.editDraft(e.DraftIdx)
					}
				}
				return m, nil
			}
			// Any other key while modal is open: swallow.
			return m, nil
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
		case "4":
			if m.prMeta == nil {
				m.statusMsg = "4: only in PR mode"
				return m, nil
			}
			m.setView(viewPR)
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
			if m.view == viewChanges && m.focus == paneLeft {
				r := m.rowAtCursor()
				if r.Kind == rowDir && !m.treeCollapsed[r.Path] {
					m.toggleDirCollapsed(r.Path)
					return m, nil
				}
				if r.Kind == rowFile {
					parent := path.Dir(r.Path)
					if parent == "." {
						parent = ""
					}
					if row := indexOfDirRow(m.treeRows, parent); row >= 0 {
						return m, m.setCursor(row)
					}
				}
				return m, nil
			}
			return m, nil
		case "l", "right":
			if m.view == viewOverview {
				m.moveOverview(+1, 0)
				return m, nil
			}
			if m.view == viewChanges && m.focus == paneLeft {
				r := m.rowAtCursor()
				if r.Kind == rowDir {
					if m.treeCollapsed[r.Path] {
						m.toggleDirCollapsed(r.Path)
					} else if m.rowCursor+1 < len(m.treeRows) && m.treeRows[m.rowCursor+1].Kind == rowFile {
						return m, m.setCursor(m.rowCursor + 1)
					}
					return m, nil
				}
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
			if m.view == viewChanges && m.focus == paneLeft {
				r := m.rowAtCursor()
				if r.Kind == rowDir {
					m.toggleDirCollapsed(r.Path)
					return m, nil
				}
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
		case "O":
			if m.prMeta == nil {
				return m, nil
			}
			if err := openBrowser(m.prMeta.HTMLURL); err != nil {
				m.statusMsg = "browser: " + err.Error()
			}
			return m, nil
		case "C":
			if m.prMeta == nil {
				return m, nil
			}
			if m.view != viewChanges {
				m.statusMsg = "C: switch to changes view first"
				return m, nil
			}
			fr, _, ok := m.currentFileRow()
			if !ok {
				m.statusMsg = "C: place cursor on a diff line first"
				return m, nil
			}
			cur := ctxpane.Cursor{File: fr, HunkIndex: m.currentHunkIndex()}
			line, kind, ok := cur.AnchorLine()
			if !ok || line == 0 {
				m.statusMsg = "C: no anchor line in this hunk"
				return m, nil
			}
			side := "RIGHT"
			if kind == diff.LineRemoved {
				side = "LEFT"
			}
			return m, m.composeDraft(fr.Path, line, side)
		case "B":
			if m.prMeta == nil {
				return m, nil
			}
			return m, m.composeReviewBody()
		case "S":
			if m.prMeta == nil {
				return m, nil
			}
			if m.submitter == nil {
				m.statusMsg = "S: submit unavailable (auth failed at startup)"
				return m, nil
			}
			if len(m.drafts) == 0 {
				m.statusMsg = "S: no drafts to submit"
				return m, nil
			}
			return m, m.composeAndSubmit()
		case "t":
			if m.prMeta == nil || m.view != viewChanges {
				return m, nil
			}
			fr, _, ok := m.currentFileRow()
			if !ok {
				return m, nil
			}
			cur := ctxpane.Cursor{File: fr, HunkIndex: m.currentHunkIndex()}
			line, kind, ok := cur.AnchorLine()
			if !ok || line == 0 {
				return m, nil
			}
			side := "RIGHT"
			if kind == diff.LineRemoved {
				side = "LEFT"
			}
			entries := buildThread(m.reviewComments, m.drafts, fr.Path, line, side)
			if len(entries) == 0 {
				return m, nil
			}
			m.modalOpen = true
			m.modalEntries = entries
			m.modalSelected = 0
			m.modalAnchor = modalAnchor{Path: fr.Path, Line: line, Side: side}
			return m, nil
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

	case draftComposedMsg:
		if msg.err != nil {
			m.statusMsg = "compose: " + msg.err.Error()
			return m, nil
		}
		if msg.draft.Body == "" {
			m.statusMsg = "compose: cancelled (empty)"
			return m, nil
		}
		m.drafts = append(m.drafts, msg.draft)
		m.statusMsg = fmt.Sprintf("draft saved (%d total)", len(m.drafts))
		return m, m.scheduleContextRefresh()

	case reviewBodyComposedMsg:
		if msg.err != nil {
			m.statusMsg = "B: " + msg.err.Error()
			return m, nil
		}
		m.reviewBody = msg.body
		m.statusMsg = "review body saved"
		return m, nil

	case submitDoneMsg:
		if msg.err != nil {
			m.statusMsg = "submit failed: " + msg.err.Error()
			return m, nil
		}
		m.drafts = nil
		m.reviewBody = ""
		m.statusMsg = fmt.Sprintf("submitted %d %s", msg.n, plural("comment", msg.n))
		if m.refetcher != nil {
			return m, m.runRefetch()
		}
		return m, m.scheduleContextRefresh()

	case refetchDoneMsg:
		if msg.err != nil {
			m.statusMsg = "refetch: " + msg.err.Error()
			return m, m.scheduleContextRefresh()
		}
		if msg.res != nil {
			m.reviewComments = msg.res.ReviewComments
			m.issueComments = msg.res.IssueComments
			m.reviews = msg.res.Reviews
		}
		return m, m.scheduleContextRefresh()

	case draftEditedMsg:
		if msg.err != nil {
			m.statusMsg = "edit: " + msg.err.Error()
			return m, nil
		}
		if msg.idx < 0 || msg.idx >= len(m.drafts) {
			return m, nil
		}
		if msg.body == "" {
			m.drafts = append(m.drafts[:msg.idx], m.drafts[msg.idx+1:]...)
		} else {
			m.drafts[msg.idx].Body = msg.body
		}
		if m.modalOpen {
			m.modalEntries = buildThread(m.reviewComments, m.drafts, m.modalAnchor.Path, m.modalAnchor.Line, m.modalAnchor.Side)
			if m.modalSelected >= len(m.modalEntries) {
				m.modalSelected = maxInt(0, len(m.modalEntries)-1)
			}
			if len(m.modalEntries) == 0 {
				m.modalOpen = false
			}
		}
		return m, m.scheduleContextRefresh()

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
			ReviewComments:  m.reviewComments,
			Drafts:          m.drafts,
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
// openBrowser opens the given URL in the user's default browser.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	return cmd.Start()
}

// composeDraft spawns $EDITOR on an empty temp file. On editor exit, the
// file contents (after stripping #-comment lines + trimming) become the
// draft body. Empty bodies are dropped without state change.
func (m *Model) composeDraft(path string, line int, side string) tea.Cmd {
	f, err := os.CreateTemp("", "gitreview-draft-*.md")
	if err != nil {
		m.statusMsg = "compose: " + err.Error()
		return nil
	}
	// Pre-populate with a context header so the editor isn't a blank slate.
	// All lines are #-prefixed and stripped by stripDraftComments on save.
	if fr, _, ok := m.currentFileRow(); ok {
		header := commentContextHeader(fr, line, side)
		if _, err := f.WriteString(header); err != nil {
			f.Close()
			os.Remove(f.Name())
			m.statusMsg = "compose: " + err.Error()
			return nil
		}
	}
	f.Close()
	cmd := editorCmd(f.Name(), 1)
	if cmd == nil {
		m.statusMsg = "compose: no editor found (set $EDITOR)"
		_ = os.Remove(f.Name())
		return nil
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(f.Name())
		if err != nil {
			return draftComposedMsg{err: err}
		}
		raw, readErr := os.ReadFile(f.Name())
		if readErr != nil {
			return draftComposedMsg{err: readErr}
		}
		body := stripDraftComments(string(raw))
		return draftComposedMsg{
			draft: ctxpane.Draft{Path: path, Line: line, Side: side, Body: body},
		}
	})
}

// commentContextHeader returns a #-prefixed header showing the file, line,
// side, and ~6 surrounding diff lines (cursor line marked with >). All lines
// start with "#" so stripDraftComments removes them on save.
func commentContextHeader(f diff.File, line int, side string) string {
	type entry struct {
		prefix   string // " " "+" "-"
		num      int
		text     string
		isAnchor bool
	}
	var entries []entry
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			prefix := " "
			num := l.NewNum
			switch l.Kind {
			case diff.LineAdded:
				prefix = "+"
			case diff.LineRemoved:
				prefix = "-"
				num = l.OldNum
			}
			isAnchor := false
			if side == "RIGHT" && l.Kind != diff.LineRemoved && l.NewNum == line {
				isAnchor = true
			} else if side == "LEFT" && l.Kind != diff.LineAdded && l.OldNum == line {
				isAnchor = true
			}
			entries = append(entries, entry{prefix, num, l.Content, isAnchor})
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Commenting on %s:%d (%s side)\n#\n", f.Path, line, side)

	anchorIdx := -1
	for i, e := range entries {
		if e.isAnchor {
			anchorIdx = i
			break
		}
	}
	if anchorIdx >= 0 {
		fmt.Fprintln(&b, "# Context (cursor line marked >):")
		fmt.Fprintln(&b, "#")
		start := anchorIdx - 3
		if start < 0 {
			start = 0
		}
		end := anchorIdx + 4
		if end > len(entries) {
			end = len(entries)
		}
		for i := start; i < end; i++ {
			e := entries[i]
			marker := " "
			if i == anchorIdx {
				marker = ">"
			}
			fmt.Fprintf(&b, "# %s  %s %4d  %s\n", marker, e.prefix, e.num, e.text)
		}
		fmt.Fprintln(&b, "#")
	}

	fmt.Fprintln(&b, "# Write your comment below. Lines starting with # are stripped.")
	fmt.Fprintln(&b)
	return b.String()
}

// stripDraftComments removes lines starting with # and trims surrounding space.
func stripDraftComments(s string) string {
	var keep []string
	for _, ln := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimLeft(ln, " \t"), "#") {
			continue
		}
		keep = append(keep, ln)
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

// draftComposedMsg is delivered after the compose editor exits.
type draftComposedMsg struct {
	draft ctxpane.Draft
	err   error
}

// editDraft re-spawns $EDITOR with the existing draft body. Empty save = delete.
func (m *Model) editDraft(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.drafts) {
		return nil
	}
	d := m.drafts[idx]
	f, err := os.CreateTemp("", "gitreview-draft-*.md")
	if err != nil {
		m.statusMsg = "edit: " + err.Error()
		return nil
	}
	// Re-prepend context header so the user still sees what they're editing.
	var initial string
	for _, file := range m.d.Files {
		if file.Path == d.Path {
			initial = commentContextHeader(file, d.Line, d.Side)
			break
		}
	}
	initial += d.Body
	if _, err := f.WriteString(initial); err != nil {
		f.Close()
		os.Remove(f.Name())
		m.statusMsg = "edit: " + err.Error()
		return nil
	}
	f.Close()
	cmd := editorCmd(f.Name(), 1)
	if cmd == nil {
		m.statusMsg = "edit: no editor found (set $EDITOR)"
		os.Remove(f.Name())
		return nil
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(f.Name())
		if err != nil {
			return draftEditedMsg{idx: idx, err: err}
		}
		raw, readErr := os.ReadFile(f.Name())
		if readErr != nil {
			return draftEditedMsg{idx: idx, err: readErr}
		}
		return draftEditedMsg{idx: idx, body: stripDraftComments(string(raw))}
	})
}

type draftEditedMsg struct {
	idx  int
	body string
	err  error
}

// composeReviewBody opens $EDITOR with the current m.reviewBody as the
// starting buffer; on save, the result replaces m.reviewBody.
func (m *Model) composeReviewBody() tea.Cmd {
	f, err := os.CreateTemp("", "gitreview-review-body-*.md")
	if err != nil {
		m.statusMsg = "B: " + err.Error()
		return nil
	}
	if _, err := f.WriteString(m.reviewBody); err != nil {
		f.Close()
		os.Remove(f.Name())
		m.statusMsg = "B: " + err.Error()
		return nil
	}
	f.Close()
	cmd := editorCmd(f.Name(), 1)
	if cmd == nil {
		m.statusMsg = "B: no editor found (set $EDITOR)"
		os.Remove(f.Name())
		return nil
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(f.Name())
		if err != nil {
			return reviewBodyComposedMsg{err: err}
		}
		raw, readErr := os.ReadFile(f.Name())
		if readErr != nil {
			return reviewBodyComposedMsg{err: readErr}
		}
		return reviewBodyComposedMsg{body: stripDraftComments(string(raw))}
	})
}

type reviewBodyComposedMsg struct {
	body string
	err  error
}

// composeAndSubmit opens $EDITOR with the templated body, then POSTs via
// the configured submitter. On success: clears drafts + reviewBody and
// triggers a context-pane refresh. On failure: drafts kept, status shown.
func (m *Model) composeAndSubmit() tea.Cmd {
	tpl := "# Review body (optional — leave empty for no overall comment).\n# Lines starting with # are stripped.\n#\n"
	tpl += fmt.Sprintf("# %d inline drafts:\n", len(m.drafts))
	for _, d := range m.drafts {
		tpl += fmt.Sprintf("#   %s:%d  %q\n", d.Path, d.Line, truncForTemplate(d.Body, 60))
	}
	if m.reviewBody != "" {
		tpl += "\n" + m.reviewBody
	}
	f, err := os.CreateTemp("", "gitreview-submit-*.md")
	if err != nil {
		m.statusMsg = "S: " + err.Error()
		return nil
	}
	if _, err := f.WriteString(tpl); err != nil {
		f.Close()
		os.Remove(f.Name())
		m.statusMsg = "S: " + err.Error()
		return nil
	}
	f.Close()
	cmd := editorCmd(f.Name(), 1)
	if cmd == nil {
		m.statusMsg = "S: no editor found (set $EDITOR)"
		os.Remove(f.Name())
		return nil
	}
	draftsSnap := make([]pr.SubmitDraft, len(m.drafts))
	for i, d := range m.drafts {
		draftsSnap[i] = pr.SubmitDraft{Path: d.Path, Line: d.Line, Side: d.Side, Body: d.Body}
	}
	submitter := m.submitter
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(f.Name())
		if err != nil {
			return submitDoneMsg{err: err}
		}
		raw, readErr := os.ReadFile(f.Name())
		if readErr != nil {
			return submitDoneMsg{err: readErr}
		}
		body := stripDraftComments(string(raw))
		if err := submitter(context.Background(), body, draftsSnap); err != nil {
			return submitDoneMsg{err: err}
		}
		return submitDoneMsg{n: len(draftsSnap)}
	})
}

func truncForTemplate(s string, n int) string {
	flat := strings.Join(strings.Fields(s), " ")
	if len([]rune(flat)) <= n {
		return flat
	}
	r := []rune(flat)
	return string(r[:n-1]) + "…"
}

type submitDoneMsg struct {
	n   int
	err error
}

// runRefetch kicks off the configured refetcher in a Cmd; result delivers
// as refetchDoneMsg.
func (m *Model) runRefetch() tea.Cmd {
	refetch := m.refetcher
	return func() tea.Msg {
		res, err := refetch(context.Background())
		return refetchDoneMsg{res: res, err: err}
	}
}

type refetchDoneMsg struct {
	res *RefetcherResult
	err error
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (m *Model) openInEditor() tea.Cmd {
	f, line, ok := m.selectedEditTarget()
	if !ok {
		if r := m.rowAtCursor(); r.Kind == rowDir {
			m.statusMsg = "e: select a file to open"
		} else {
			m.statusMsg = "nothing to edit here"
		}
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
	switch m.view {
	case viewOverview:
		body = m.renderOverviewBody()
	case viewPR:
		body = m.renderPRTabBody()
	default:
		parts := []string{m.renderLeftPane(), m.renderDiffPane()}
		if m.contextPaneWidthEffective() > 0 {
			parts = append(parts, m.renderContextPane())
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	}
	view := lipgloss.JoinVertical(lipgloss.Left, header, body, m.renderHelp())
	if m.modalOpen {
		modalW := minInt(m.width-4, 80)
		innerW := modalW - 6
		title := fmt.Sprintf("Thread: %s:%d", m.modalAnchor.Path, m.modalAnchor.Line)
		content := renderThreadModal(title, m.modalEntries, m.modalSelected, innerW)
		modal := modalStyle.Width(modalW).Render(content)
		view = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal,
			lipgloss.WithWhitespaceChars(" "))
	}
	return view
}

// renderTopHeader is a single-line strip showing the three view tabs and PR
// stats. Always visible at the top of the screen.
func (m Model) renderTopHeader() string {
	tabs := m.renderTabsGlobal()
	stats := m.renderStats()
	if m.prMeta == nil {
		return padBetweenAnsi(tabs, stats, m.width)
	}
	prStrip := prHeaderStyle.Render(fmt.Sprintf(
		"PR #%d · %s · %s   ",
		m.prMeta.Number, m.prMeta.Author, m.prMeta.State,
	))
	openHint := mutedStyle.Render("   O: open in browser")
	return padBetweenAnsi(prStrip+tabs, stats+openHint, m.width)
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
	if m.prMeta != nil {
		parts = append(parts, style(m.view == viewPR).Render("[4 PR]"))
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
	if !m.filtering {
		// Snapshot the file under the cursor (path is stable across rebuilds;
		// the row index is not). Snapshot collapsed state so dirs come back on
		// clear-filter.
		if f, _, ok := m.currentFileRow(); ok {
			m.pathPreFilter = f.Path
		} else {
			m.pathPreFilter = ""
		}
		m.preFilterCollapsed = copyStringBoolMap(m.treeCollapsed)
	}
	m.filtering = true
	m.filterInput.SetValue(m.filter)
	m.filterInput.CursorEnd()
	return m.filterInput.Focus()
}

func copyStringBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (m *Model) commitFilter() {
	m.filter = strings.TrimSpace(m.filterInput.Value())
	m.filtering = false
	m.filterInput.Blur()
	m.refreshDiff()
	// Land on the first file row after the rebuild.
	if row := FirstFileRow(m.treeRows); row >= 0 {
		m.rowCursor = row
	} else {
		m.rowCursor = 0
	}
}

func (m *Model) cancelFilter() {
	m.filtering = false
	m.filterInput.Blur()
	m.filterInput.SetValue(m.filter) // restore committed value
}

func (m *Model) clearFilter() {
	m.filter = ""
	m.filterInput.SetValue("")
	// Restore pre-filter expansion state, then rebuild.
	if m.preFilterCollapsed != nil {
		m.treeCollapsed = m.preFilterCollapsed
		m.preFilterCollapsed = nil
	}
	m.refreshDiff()
	// Try to restore the cursor to the same file path. If that file is no
	// longer visible, land on the first file row (or row 0 as a last resort).
	if m.pathPreFilter != "" {
		if row := RowOfFile(m.treeRows, m.pathPreFilter); row >= 0 {
			m.rowCursor = row
			m.pathPreFilter = ""
			return
		}
	}
	if row := FirstFileRow(m.treeRows); row >= 0 {
		m.rowCursor = row
	} else {
		m.rowCursor = 0
	}
	m.pathPreFilter = ""
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
	// Live update: re-filter, rebuild, and land on the first matching file.
	m.filter = strings.TrimSpace(m.filterInput.Value())
	m.refreshDiff()
	if row := FirstFileRow(m.treeRows); row >= 0 {
		m.rowCursor = row
	} else {
		m.rowCursor = 0
	}
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
	if v == viewPR && m.prMeta == nil {
		m.statusMsg = "no PR loaded"
		return
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

// toggleDirCollapsed flips a directory's expansion state and rebuilds the tree.
// When collapsing, the cursor snaps to the dir row itself (so it doesn't end
// up on a now-hidden child).
func (m *Model) toggleDirCollapsed(dir string) {
	wasCollapsed := m.treeCollapsed[dir]
	if wasCollapsed {
		delete(m.treeCollapsed, dir)
	} else {
		m.treeCollapsed[dir] = true
	}
	m.refreshDiff()
	if !wasCollapsed {
		// Just collapsed: snap cursor to the dir row.
		if row := indexOfDirRow(m.treeRows, dir); row >= 0 {
			m.rowCursor = row
		} else {
			m.rowCursor = clamp(m.rowCursor, 0, len(m.treeRows)-1)
		}
	}
}

// indexOfDirRow returns the row index of the dir row with the given path,
// or -1 if not found.
func indexOfDirRow(rows []treeRow, dir string) int {
	for i, r := range rows {
		if r.Kind == rowDir && r.Path == dir {
			return i
		}
	}
	return -1
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
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
	files, _ := m.effectiveFiles()
	inDiff := false
	for _, f := range files {
		if f.Path == it.Jump.File {
			inDiff = true
			break
		}
	}
	if inDiff {
		// First refresh: rebuild treeRows so RowOfFile can find the target row.
		// Second refresh: re-render the diff for the new rowCursor.
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
		if m.splitView {
			m.viewport.SetContent(renderSplit(fr, m.viewport.Width))
			m.hunkOffsets = hunkOffsetsSplit(fr)
		} else {
			m.viewport.SetContent(renderDiff(fr, m.viewport.Width))
			m.hunkOffsets = hunkOffsetsUnified(fr)
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
	files, _ := m.effectiveFiles()

	for i, r := range m.treeRows {
		var rendered string
		switch r.Kind {
		case rowDir:
			rendered = renderTreeDir(r, m.treeCollapsed[r.Path], rowW)
		case rowFile:
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

	var left string
	if reviewed {
		left = mutedStyle.Render(indent + "✓ " + name)
	} else {
		marker := statusMarker(f.Status)
		left = indent + marker + " " + name
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

// stripAnsiForCursor returns the row's rendered text with ANSI escapes
// removed. cursorStyle's background applies a uniform highlight, so any
// nested escape codes inside its argument would visually fight the bg.
func stripAnsiForCursor(r treeRow, m Model, rowW, sparkW int, showSpark bool) string {
	switch r.Kind {
	case rowDir:
		return ansi.Strip(renderTreeDir(r, m.treeCollapsed[r.Path], rowW))
	case rowFile:
		files, _ := m.effectiveFiles()
		if r.FileIdx < 0 || r.FileIdx >= len(files) {
			return ""
		}
		f := files[r.FileIdx]
		reviewed := m.reviewedFiles[f.Path]
		return ansi.Strip(renderTreeFile(r, f, reviewed, showSpark, sparkW, rowW))
	}
	return ""
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
	if m.prMeta != nil {
		parts = append(parts, "C: comment", "S: submit", "t: thread", "B: body", "O: browser")
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
