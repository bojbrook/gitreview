package ctxpane

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

func TestParseBlamePorcelain(t *testing.T) {
	out := `abcdef1234567890abcdef1234567890abcdef12 1 1 1
author Alice
author-mail <alice@example.com>
author-time 1700000000
author-tz +0000
summary did a thing
filename foo.go
`
	short, _, subject := parseBlamePorcelain(out)
	if short != "abcdef1" {
		t.Errorf("short: got %q want abcdef1", short)
	}
	if subject != "did a thing" {
		t.Errorf("subject: got %q", subject)
	}
}

func TestBlameSection_Integration(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	gitCfg(t, dir)
	mustWrite(t, filepath.Join(dir, "foo.go"), "package foo\n\nfunc Hi() {}\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "initial blame target")

	cur := Cursor{
		RepoRoot: dir,
		File: diff.File{
			Path: "foo.go",
			Hunks: []diff.Hunk{{
				Lines: []diff.Line{
					{Kind: diff.LineContext, NewNum: 3, OldNum: 3},
				},
			}},
		},
		HunkIndex: 0,
	}
	s := buildBlameSection(context.Background(), cur)
	if s.Status != StatusOK {
		t.Fatalf("status: got %v want OK", s.Status)
	}
	if len(s.Items) == 0 || !strings.Contains(s.Items[0].Text, "initial blame target") {
		t.Errorf("items: %+v", s.Items)
	}
}

func TestBlameSection_AddedLineSkipped(t *testing.T) {
	cur := Cursor{
		RepoRoot: "/no/such/path",
		File: diff.File{
			Path: "foo.go",
			Hunks: []diff.Hunk{{
				Lines: []diff.Line{
					{Kind: diff.LineAdded, NewNum: 5},
				},
			}},
		},
		HunkIndex: 0,
	}
	s := buildBlameSection(context.Background(), cur)
	if s.Status != StatusEmpty {
		t.Errorf("added-only line: status %v want Empty", s.Status)
	}
}
