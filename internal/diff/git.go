package diff

import (
	"fmt"
	"os/exec"
	"strings"
)

// Load runs the appropriate `git diff` for the requested mode and parses it.
func Load(opts Options) (*Diff, error) {
	switch opts.Mode {
	case ModeWorking:
		return loadWorking()
	case ModeStaged:
		return loadStaged()
	case ModeCommitted:
		return loadAgainstBase(opts.BaseRef, true)
	default:
		return loadAgainstBase(opts.BaseRef, false)
	}
}

// loadAgainstBase computes a diff against the merge-base of HEAD and baseRef.
// If committedOnly is true, the diff is <merge-base>..HEAD (excludes working tree).
// Otherwise it's just <merge-base> so working-tree changes are included.
func loadAgainstBase(baseRef string, committedOnly bool) (*Diff, error) {
	// Fresh repo with no commits: show staged + untracked since there's nothing to diff against.
	if !committedOnly && !revExists("HEAD") {
		stagedRaw, err := run("git", "diff", "--no-color", "--no-ext-diff", "-M", "--cached")
		if err != nil {
			return nil, fmt.Errorf("git diff --cached: %w", err)
		}
		staged, err := Parse(stagedRaw)
		if err != nil {
			return nil, err
		}
		untracked, err := loadUntracked()
		if err != nil {
			return nil, fmt.Errorf("list untracked: %w", err)
		}
		return &Diff{
			Mode:    ModeAll,
			Label:   "pre-commit (no HEAD yet)",
			BaseRef: "(none)",
			HeadRef: "(working tree)",
			Files:   append(staged, untracked...),
		}, nil
	}

	resolved, err := resolveBaseRef(baseRef)
	if err != nil {
		return nil, err
	}

	mergeBase, err := run("git", "merge-base", "HEAD", resolved)
	if err != nil {
		return nil, fmt.Errorf("merge-base %s..HEAD: %w", resolved, err)
	}
	mergeBase = strings.TrimSpace(mergeBase)

	args := []string{"diff", "--no-color", "--no-ext-diff", "-M"}
	var label string
	if committedOnly {
		args = append(args, mergeBase+"..HEAD")
		label = fmt.Sprintf("committed vs %s", resolved)
	} else {
		args = append(args, mergeBase)
		label = fmt.Sprintf("all changes vs %s", resolved)
	}

	raw, err := run("git", args...)
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	files, err := Parse(raw)
	if err != nil {
		return nil, err
	}

	mode := ModeAll
	if committedOnly {
		mode = ModeCommitted
	} else {
		untracked, err := loadUntracked()
		if err != nil {
			return nil, fmt.Errorf("list untracked: %w", err)
		}
		files = append(files, untracked...)
	}
	return &Diff{
		Mode:    mode,
		Label:   label,
		BaseRef: resolved,
		HeadRef: "HEAD",
		Files:   files,
	}, nil
}

func loadWorking() (*Diff, error) {
	if !revExists("HEAD") {
		return nil, fmt.Errorf("--working needs a HEAD commit, but this repo has none yet. " +
			"Stage files and use --staged instead")
	}
	raw, err := run("git", "diff", "--no-color", "--no-ext-diff", "-M", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("git diff HEAD: %w", err)
	}
	files, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	untracked, err := loadUntracked()
	if err != nil {
		return nil, fmt.Errorf("list untracked: %w", err)
	}
	files = append(files, untracked...)
	return &Diff{
		Mode:    ModeWorking,
		Label:   "uncommitted vs HEAD",
		BaseRef: "HEAD",
		HeadRef: "(working tree)",
		Files:   files,
	}, nil
}

func loadStaged() (*Diff, error) {
	raw, err := run("git", "diff", "--no-color", "--no-ext-diff", "-M", "--cached")
	if err != nil {
		return nil, fmt.Errorf("git diff --cached: %w", err)
	}
	files, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	return &Diff{
		Mode:    ModeStaged,
		Label:   "staged vs HEAD",
		BaseRef: "HEAD",
		HeadRef: "(staged)",
		Files:   files,
	}, nil
}

func resolveBaseRef(baseRef string) (string, error) {
	if baseRef != "" {
		if !revExists(baseRef) {
			return "", fmt.Errorf("base ref %q not found", baseRef)
		}
		return baseRef, nil
	}

	// 1. Honor origin's declared default branch if present.
	if out, err := run("git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		ref := strings.TrimSpace(out)
		if ref != "" && revExists(ref) {
			return ref, nil
		}
	}

	// 2. Common defaults.
	candidates := []string{"origin/main", "origin/master", "main", "master", "develop", "trunk"}
	for _, c := range candidates {
		if revExists(c) {
			return c, nil
		}
	}

	// 3. Nothing worked — give an actionable error.
	if !revExists("HEAD") {
		return "", fmt.Errorf("this repo has no commits yet — nothing to diff against. " +
			"Commit something, or stage files and use --staged")
	}
	return "", fmt.Errorf("no base ref found (tried origin/HEAD, origin/main, origin/master, main, master, develop, trunk). " +
		"Use --base <ref> to pick one, or --working / --staged to skip comparing against a base")
}

func revExists(ref string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", ref)
	return cmd.Run() == nil
}

func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("%s: %s", strings.Join(append([]string{name}, args...), " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}
