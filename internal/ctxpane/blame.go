package ctxpane

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

var blameCache = newLRU(512)

// blameLine returns a one-line summary like "dbe587b 2d ago — reviewed marks"
// for the given file:line. Cached by (file, line, HEAD-sha); HEAD sha lookup
// is done once at startup of the resolver per call.
func blameLine(ctx context.Context, repoRoot, file string, line int, headSHA string) (string, error) {
	key := headSHA + ":" + file + ":" + strconv.Itoa(line)
	if v, ok := blameCache.Get(key); ok {
		return v.(string), nil
	}

	// git blame -L N,N --porcelain -- <file>
	args := []string{"blame", "-L", fmt.Sprintf("%d,%d", line, line), "--porcelain", "--", file}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	short, age, subject := parseBlamePorcelain(string(out))
	if short == "" {
		return "", fmt.Errorf("could not parse blame output")
	}
	formatted := fmt.Sprintf("%s %s — %s", short, age, subject)
	blameCache.Put(key, formatted)
	return formatted, nil
}

// parseBlamePorcelain extracts (short SHA, age string, subject) from
// `git blame --porcelain` output. Returns empty strings if the output is
// unparseable.
func parseBlamePorcelain(out string) (short, age, subject string) {
	lines := strings.Split(out, "\n")
	if len(lines) == 0 {
		return
	}
	// First token of first line is the full SHA.
	first := strings.Fields(lines[0])
	if len(first) == 0 {
		return
	}
	sha := first[0]
	if len(sha) >= 7 {
		short = sha[:7]
	} else {
		short = sha
	}
	var authorTime int64
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "author-time "):
			fmt.Sscanf(l, "author-time %d", &authorTime)
		case strings.HasPrefix(l, "summary "):
			subject = strings.TrimPrefix(l, "summary ")
		}
	}
	if authorTime > 0 {
		age = humanAge(time.Now().Unix() - authorTime)
	}
	return
}

func humanAge(seconds int64) string {
	switch {
	case seconds < 90:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 90*60:
		return fmt.Sprintf("%dm", seconds/60)
	case seconds < 36*3600:
		return fmt.Sprintf("%dh", seconds/3600)
	case seconds < 90*86400:
		return fmt.Sprintf("%dd", seconds/86400)
	case seconds < 730*86400:
		return fmt.Sprintf("%dmo", seconds/(30*86400))
	}
	return fmt.Sprintf("%dy", seconds/(365*86400))
}

// buildBlameSection produces the Blame section. Returns a Section with
// StatusEmpty if the cursor isn't on a line that existed before the change.
func buildBlameSection(ctx context.Context, cur Cursor) Section {
	s := Section{Kind: SectionBlame, Status: StatusEmpty}
	anchor, kind, ok := cur.AnchorLine()
	if !ok || cur.File.Path == "" || cur.RepoRoot == "" {
		return s
	}
	// Blame only makes sense for lines that existed before the change.
	// LineAdded means the line is brand new — nothing to blame.
	if kind == diff.LineAdded {
		// Try to blame the nearest context line above instead.
		hunk := cur.File.Hunks[cur.HunkIndex]
		anchor = nearestUnchangedAbove(hunk, anchor)
		if anchor <= 0 {
			return s
		}
	}

	headSHA, _ := resolveHEADSha(ctx, cur.RepoRoot)
	line, err := blameLine(ctx, cur.RepoRoot, cur.File.Path, anchor, headSHA)
	if err != nil {
		s.Status = StatusError
		return s
	}
	s.Status = StatusOK
	s.Items = []Item{{Text: line}}
	return s
}

// nearestUnchangedAbove returns the NewNum of the closest non-added line at
// or above the given anchor within the hunk. Returns 0 if none.
func nearestUnchangedAbove(h diff.Hunk, anchor int) int {
	var best int
	for _, l := range h.Lines {
		if l.NewNum > 0 && l.NewNum <= anchor && l.Kind != diff.LineAdded {
			if l.NewNum > best {
				best = l.NewNum
			}
		}
	}
	return best
}

var headShaCache = newLRU(8)

func resolveHEADSha(ctx context.Context, repoRoot string) (string, error) {
	if v, ok := headShaCache.Get(repoRoot); ok {
		return v.(string), nil
	}
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(out))
	headShaCache.Put(repoRoot, sha)
	return sha, nil
}
