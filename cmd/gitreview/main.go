package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bowenbrooks/gitreview/internal/diff"
	"github.com/bowenbrooks/gitreview/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	var (
		base      = flag.String("base", "", "base ref to diff against (defaults: origin/main, origin/master, main, master)")
		working   = flag.Bool("working", false, "show only uncommitted changes (staged + unstaged) vs HEAD")
		staged    = flag.Bool("staged", false, "show only staged changes vs HEAD")
		committed = flag.Bool("committed", false, "show only committed changes between merge-base and HEAD (no working tree)")
		width     = flag.Int("width", 0, "force terminal width (use when bubbletea reports the wrong size, e.g. inside tmux)")
	)
	flag.Parse()

	mode, err := resolveMode(*working, *staged, *committed)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitreview:", err)
		os.Exit(2)
	}

	d, err := diff.Load(diff.Options{Mode: mode, BaseRef: *base})
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitreview:", err)
		os.Exit(1)
	}

	commits, err := diff.LoadCommits(500)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitreview: warn:", err)
		commits = nil
	}

	if len(d.Files) == 0 && len(commits) == 0 {
		fmt.Println("no changes (" + d.Label + ") and no commits to browse")
		return
	}

	repoRoot, err := diff.RepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitreview: warn: could not resolve repo root:", err)
	}

	m := ui.New(d, commits, repoRoot)
	if *width > 0 {
		m.ForceWidth(*width)
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "gitreview:", err)
		os.Exit(1)
	}
}

func resolveMode(working, staged, committed bool) (diff.Mode, error) {
	count := 0
	for _, b := range []bool{working, staged, committed} {
		if b {
			count++
		}
	}
	if count > 1 {
		return 0, fmt.Errorf("--working, --staged, and --committed are mutually exclusive")
	}
	switch {
	case working:
		return diff.ModeWorking, nil
	case staged:
		return diff.ModeStaged, nil
	case committed:
		return diff.ModeCommitted, nil
	default:
		return diff.ModeAll, nil
	}
}
