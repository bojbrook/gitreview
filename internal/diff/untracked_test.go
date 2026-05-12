package diff

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLoadUntracked(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)

	mustWrite(t, filepath.Join(dir, "new.txt"), "hello\nworld\n")
	mustWrite(t, filepath.Join(dir, "binary.bin"), "data\x00with\x00nulls")
	mustWrite(t, filepath.Join(dir, "ignored.log"), "should be skipped")
	mustWrite(t, filepath.Join(dir, ".gitignore"), "*.log\n")

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	files, err := loadUntracked()
	if err != nil {
		t.Fatalf("loadUntracked: %v", err)
	}

	got := map[string]File{}
	for _, f := range files {
		got[f.Path] = f
	}

	if _, ok := got["ignored.log"]; ok {
		t.Errorf("ignored.log should be excluded by .gitignore")
	}
	if _, ok := got["new.txt"]; !ok {
		t.Errorf("new.txt missing from untracked output")
	}
	if got["new.txt"].Status != StatusAdded {
		t.Errorf("new.txt status: got %v want added", got["new.txt"].Status)
	}
	if n := len(got["new.txt"].Hunks[0].Lines); n != 2 {
		t.Errorf("new.txt line count: got %d want 2", n)
	}
	if bin, ok := got["binary.bin"]; !ok {
		t.Errorf("binary.bin missing")
	} else if len(bin.Hunks[0].Lines) != 1 || bin.Hunks[0].Lines[0].Content != "(binary file — not shown)" {
		t.Errorf("binary.bin should be placeholder; got %+v", bin.Hunks[0].Lines)
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
