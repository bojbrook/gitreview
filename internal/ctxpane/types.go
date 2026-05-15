package ctxpane

import "github.com/bowenbrooks/gitreview/internal/diff"

// SectionKind identifies which section a Section is. Order in the rendered
// pane matches the order of the constants below.
type SectionKind int

const (
	SectionWhere SectionKind = iota
	SectionSymbol
	SectionCrossFile
	SectionBlame
	SectionHistory
	SectionComments
)

func (k SectionKind) Title() string {
	switch k {
	case SectionWhere:
		return "Where"
	case SectionSymbol:
		return "Symbol"
	case SectionCrossFile:
		return "Cross-file"
	case SectionBlame:
		return "Blame"
	case SectionHistory:
		return "History"
	case SectionComments:
		return "Comments"
	}
	return "?"
}

// Status reflects how a section was computed.
type Status int

const (
	StatusOK Status = iota
	StatusEmpty
	StatusLoading
	StatusError
)

// Item is a single row inside a Section. Jump is optional; when present
// it tells the UI where Enter should send the diff cursor.
type Item struct {
	Text string
	Jump *JumpTarget
}

type JumpTarget struct {
	File string
	Line int
}

// Section is one labelled group of context rows. The UI renders the title
// from Kind.Title() and the items below it. An empty Items slice with
// StatusOK means "nothing to show here, skip rendering this section".
type Section struct {
	Kind   SectionKind
	Items  []Item
	Status Status
}

// Payload is the full set of sections returned by Resolve. Sections are
// always in SectionKind order; missing sections are simply absent.
type Payload struct {
	Sections []Section
}

// Cursor is the input to Resolve. It carries everything the resolver needs
// to compute its sections without reaching into UI internals.
type Cursor struct {
	File            diff.File  // the currently-selected file (zero value if none)
	HunkIndex       int        // 0-based index into File.Hunks; -1 if none
	Diff            *diff.Diff // the full diff (so cross-file sections can scan other files)
	RepoRoot        string     // absolute path to the working-tree root
	HistoryExpanded bool       // true when user pressed H to expand history

	// Comment-related inputs. ReviewComments is the full list for the PR;
	// the Comments section filters by (Path, Line, Side). Drafts are the
	// user's pending unsubmitted comments; same filter.
	ReviewComments []CommentRef
	Drafts         []Draft
}

// CommentRef is the package-internal shape of a review comment. The pr
// package's ReviewComment maps directly to this — kept here so ctxpane has
// no import on pr.
type CommentRef struct {
	User string
	Path string
	Line int
	Side string // "RIGHT" | "LEFT"
	Body string
	Age  string // pre-formatted relative time ("2h ago")
}

// Draft is a locally-authored comment not yet submitted to GitHub.
type Draft struct {
	Path string
	Line int
	Side string
	Body string
}

// AnchorLine returns the OldNum (for removed lines) or NewNum (otherwise)
// of the first non-context-blank line in the current hunk, plus its Kind.
// Returns (0, LineContext, false) if the hunk has nothing usable.
func (c Cursor) AnchorLine() (lineNum int, kind diff.LineKind, ok bool) {
	if c.HunkIndex < 0 || c.HunkIndex >= len(c.File.Hunks) {
		return 0, diff.LineContext, false
	}
	h := c.File.Hunks[c.HunkIndex]
	// Prefer the first added or removed line; fall back to first context line.
	// Check for added lines first, then removed.
	for _, l := range h.Lines {
		if l.Kind == diff.LineAdded && l.NewNum > 0 {
			return l.NewNum, l.Kind, true
		}
	}
	for _, l := range h.Lines {
		if l.Kind == diff.LineRemoved && l.OldNum > 0 {
			return l.OldNum, l.Kind, true
		}
	}
	for _, l := range h.Lines {
		if l.NewNum > 0 {
			return l.NewNum, l.Kind, true
		}
	}
	return 0, diff.LineContext, false
}
