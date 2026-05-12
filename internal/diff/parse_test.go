package diff

import (
	"testing"
)

const sampleDiff = "diff --git a/foo.go b/foo.go\n" +
	"index 1234567..89abcde 100644\n" +
	"--- a/foo.go\n" +
	"+++ b/foo.go\n" +
	"@@ -1,4 +1,4 @@\n" +
	" package foo\n" +
	" \n" +
	"-func Old() {}\n" +
	"+func New() {}\n" +
	"+func Extra() {}\n" +
	"diff --git a/bar.go b/bar.go\n" +
	"new file mode 100644\n" +
	"index 0000000..ffffff\n" +
	"--- /dev/null\n" +
	"+++ b/bar.go\n" +
	"@@ -0,0 +1,3 @@\n" +
	"+package bar\n" +
	"+\n" +
	"+func Bar() {}\n" +
	"diff --git a/old.go b/renamed.go\n" +
	"similarity index 80%\n" +
	"rename from old.go\n" +
	"rename to renamed.go\n" +
	"--- a/old.go\n" +
	"+++ b/renamed.go\n" +
	"@@ -1,2 +1,2 @@\n" +
	"-package old\n" +
	"+package renamed\n" +
	" \n"

func TestParseSampleDiff(t *testing.T) {
	files, err := Parse(sampleDiff)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := len(files), 3; got != want {
		t.Fatalf("file count: got %d want %d", got, want)
	}

	// File 0: modified foo.go
	f0 := files[0]
	if f0.Path != "foo.go" || f0.Status != StatusModified {
		t.Errorf("file 0: got %+v", f0)
	}
	if len(f0.Hunks) != 1 {
		t.Fatalf("file 0 hunks: got %d want 1", len(f0.Hunks))
	}
	h := f0.Hunks[0]
	if h.OldStart != 1 || h.OldLines != 4 || h.NewStart != 1 || h.NewLines != 4 {
		t.Errorf("file 0 hunk header parsed wrong: %+v", h)
	}
	wantKinds := []LineKind{LineContext, LineContext, LineRemoved, LineAdded, LineAdded}
	if len(h.Lines) != len(wantKinds) {
		t.Fatalf("file 0 line count: got %d want %d", len(h.Lines), len(wantKinds))
	}
	for i, k := range wantKinds {
		if h.Lines[i].Kind != k {
			t.Errorf("file 0 line %d kind: got %v want %v", i, h.Lines[i].Kind, k)
		}
	}
	if h.Lines[0].OldNum != 1 || h.Lines[0].NewNum != 1 {
		t.Errorf("line 0 numbers: got old=%d new=%d", h.Lines[0].OldNum, h.Lines[0].NewNum)
	}
	if h.Lines[2].OldNum != 3 {
		t.Errorf("removed line oldnum: got %d want 3", h.Lines[2].OldNum)
	}
	if h.Lines[3].NewNum != 3 || h.Lines[4].NewNum != 4 {
		t.Errorf("added line newnums: got %d, %d", h.Lines[3].NewNum, h.Lines[4].NewNum)
	}

	// File 1: added bar.go
	f1 := files[1]
	if f1.Path != "bar.go" || f1.Status != StatusAdded {
		t.Errorf("file 1: got %+v", f1)
	}

	// File 2: renamed
	f2 := files[2]
	if f2.Status != StatusRenamed {
		t.Errorf("file 2 status: got %v want renamed", f2.Status)
	}
	if f2.OldPath != "old.go" || f2.Path != "renamed.go" {
		t.Errorf("file 2 paths: old=%q new=%q", f2.OldPath, f2.Path)
	}
}

func TestParseEmpty(t *testing.T) {
	files, err := Parse("")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}
