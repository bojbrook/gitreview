package ctxpane

import (
	"strings"
	"testing"

	"github.com/bowenbrooks/gitreview/internal/diff"
)

func TestContainingDecl_Go(t *testing.T) {
	body := `package foo

import "fmt"

func Outer() {
	fmt.Println("hi")
}

func Inner(x int) int {
	return x + 1
}
`
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	name, line := containingDecl(lines, 6)
	if name != "Outer" || line != 5 {
		t.Errorf("got (%q, %d) want (Outer, 5)", name, line)
	}
	name, line = containingDecl(lines, 10)
	if name != "Inner" || line != 9 {
		t.Errorf("got (%q, %d) want (Inner, 9)", name, line)
	}
}

func TestContainingDecl_Python(t *testing.T) {
	body := `class Foo:
    def bar(self):
        return 1

def baz():
    return 2
`
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	name, _ := containingDecl(lines, 3)
	if name != "bar" {
		t.Errorf("py method: got %q want bar", name)
	}
	name, _ = containingDecl(lines, 6)
	if name != "baz" {
		t.Errorf("py top-level: got %q want baz", name)
	}
}

func TestContainingDecl_NoMatch(t *testing.T) {
	lines := []string{"plain text", "more text"}
	name, line := containingDecl(lines, 2)
	if name != "" || line != 0 {
		t.Errorf("got (%q, %d) want empty", name, line)
	}
}

func TestBuildWhereSection_HappyPath(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir+"/foo.go", `package foo

func Hello() string {
	return "hi"
}
`)
	cur := Cursor{
		RepoRoot: dir,
		File: diff.File{
			Path: "foo.go",
			Hunks: []diff.Hunk{{
				Lines: []diff.Line{
					{Kind: diff.LineContext, NewNum: 4, OldNum: 4},
				},
			}},
		},
		HunkIndex: 0,
	}
	s := buildWhereSection(cur)
	if s.Kind != SectionWhere || s.Status != StatusOK {
		t.Fatalf("kind/status: %+v", s)
	}
	if len(s.Items) < 3 {
		t.Fatalf("expected ≥3 items; got %d (%+v)", len(s.Items), s.Items)
	}
	last := s.Items[len(s.Items)-1].Text
	if !strings.Contains(last, "Hello") {
		t.Errorf("decl item: got %q want contains 'Hello'", last)
	}
}

func TestBuildWhereSection_NoFile(t *testing.T) {
	s := buildWhereSection(Cursor{})
	if s.Kind != SectionWhere {
		t.Fatalf("kind: %v", s.Kind)
	}
	if len(s.Items) != 1 || !strings.Contains(s.Items[0].Text, "no file") {
		t.Errorf("items: %+v", s.Items)
	}
}
