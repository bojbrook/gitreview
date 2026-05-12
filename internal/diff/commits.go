package diff

import (
	"fmt"
	"strings"
)

type Commit struct {
	SHA      string
	ShortSHA string
	Subject  string
	Body     string
	Author   string
	Email    string
	RelDate  string
	IsoDate  string
	Parents  []string
}

func (c Commit) IsRoot() bool   { return len(c.Parents) == 0 }
func (c Commit) IsMerge() bool  { return len(c.Parents) > 1 }

// LoadCommits returns up to limit commits reachable from HEAD, most recent first.
// Returns an empty slice if the repo has no HEAD yet (not an error).
func LoadCommits(limit int) ([]Commit, error) {
	if !revExists("HEAD") {
		return nil, nil
	}
	// Field separator: \x1f (unit separator). Record separator: \x1e (record separator).
	format := "%H%x1f%h%x1f%an%x1f%ae%x1f%ar%x1f%aI%x1f%P%x1f%s%x1f%b%x1e"
	out, err := run("git", "log",
		fmt.Sprintf("--max-count=%d", limit),
		"--pretty=format:"+format,
	)
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}

	var commits []Commit
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		fields := strings.Split(rec, "\x1f")
		if len(fields) < 9 {
			continue
		}
		commits = append(commits, Commit{
			SHA:      fields[0],
			ShortSHA: fields[1],
			Author:   fields[2],
			Email:    fields[3],
			RelDate:  fields[4],
			IsoDate:  fields[5],
			Parents:  splitParents(fields[6]),
			Subject:  fields[7],
			Body:     strings.TrimSpace(fields[8]),
		})
	}
	return commits, nil
}

// LoadCommitDiff returns the diff for a single commit (vs first parent, or empty tree for root).
func LoadCommitDiff(c Commit) (*Diff, error) {
	raw, err := run("git", "show",
		"--no-color", "--no-ext-diff", "-M",
		"--pretty=format:",
		"--first-parent",
		c.SHA,
	)
	if err != nil {
		return nil, fmt.Errorf("git show %s: %w", c.ShortSHA, err)
	}
	files, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	parent := "(root)"
	if !c.IsRoot() {
		parent = c.Parents[0][:7]
	}
	return &Diff{
		Label:   fmt.Sprintf("%s · %s", c.ShortSHA, c.Subject),
		BaseRef: parent,
		HeadRef: c.ShortSHA,
		Files:   files,
	}, nil
}

func splitParents(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}
