package ctxpane

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// buildHistorySection returns recent commits for the cursor's file. When
// cur.HistoryExpanded is true, additionally runs `git log -L` for the
// anchor line range and appends those commits, marked.
func buildHistorySection(ctx context.Context, cur Cursor) Section {
	s := Section{Kind: SectionHistory, Status: StatusEmpty}
	if cur.File.Path == "" || cur.RepoRoot == "" {
		return s
	}
	cmd := exec.CommandContext(ctx, "git", "log", "-n", "5", "--oneline", "--", cur.File.Path)
	cmd.Dir = cur.RepoRoot
	out, err := cmd.Output()
	if err != nil {
		// `git log` may exit non-zero on an empty repo — treat as empty.
		if _, ok := err.(*exec.ExitError); ok {
			return s
		}
		s.Status = StatusError
		return s
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		s.Items = append(s.Items, Item{Text: line})
	}
	if len(s.Items) == 0 {
		return s
	}
	s.Status = StatusOK

	if cur.HistoryExpanded {
		anchor, _, ok := cur.AnchorLine()
		if ok && anchor > 0 {
			s.Items = append(s.Items, Item{Text: contextMutedPrefix + "line-range:"})
			rangeSpec := fmt.Sprintf("%d,%d:%s", anchor, anchor, cur.File.Path)
			lcmd := exec.CommandContext(ctx, "git", "log", "-L", rangeSpec, "--no-patch", "--pretty=format:%h %s")
			lcmd.Dir = cur.RepoRoot
			if lout, lerr := lcmd.Output(); lerr == nil {
				for _, l := range strings.Split(strings.TrimSpace(string(lout)), "\n") {
					if l == "" {
						continue
					}
					s.Items = append(s.Items, Item{Text: "  " + l})
				}
			}
		}
	}
	return s
}

// contextMutedPrefix marks a header row inside a section. Currently no
// special UI handling — the marker is just for clarity in the displayed
// string.
const contextMutedPrefix = "— "
