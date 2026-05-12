package diff

type FileStatus int

const (
	StatusModified FileStatus = iota
	StatusAdded
	StatusDeleted
	StatusRenamed
)

func (s FileStatus) String() string {
	switch s {
	case StatusAdded:
		return "A"
	case StatusDeleted:
		return "D"
	case StatusRenamed:
		return "R"
	default:
		return "M"
	}
}

type LineKind int

const (
	LineContext LineKind = iota
	LineAdded
	LineRemoved
)

type File struct {
	Path     string
	OldPath  string
	Status   FileStatus
	Language string
	Hunks    []Hunk
}

type Hunk struct {
	Header   string
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Lines    []Line
}

type Line struct {
	Kind    LineKind
	Content string
	OldNum  int
	NewNum  int
}

type Mode int

const (
	ModeAll       Mode = iota // everything since merge-base, including working tree
	ModeWorking               // uncommitted (staged + unstaged) vs HEAD
	ModeStaged                // staged only
	ModeCommitted             // committed work only: <merge-base>..HEAD
)

func (m Mode) String() string {
	switch m {
	case ModeWorking:
		return "working"
	case ModeStaged:
		return "staged"
	case ModeCommitted:
		return "committed"
	default:
		return "all"
	}
}

type Options struct {
	Mode    Mode
	BaseRef string // used only for ModeAll and ModeCommitted
}

type Diff struct {
	Mode    Mode
	Label   string // human-readable description of what's being compared
	BaseRef string
	HeadRef string
	Files   []File
}
