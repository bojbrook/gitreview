package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// editorCmd builds an *exec.Cmd that opens absPath at the given 1-based line.
// Respects $EDITOR (preferred), falling back to nvim, vim, then nano.
// Line positioning uses the vi-family `+<line>` syntax, which most $EDITORs
// in this audience are. Returns nil if no usable editor is found.
func editorCmd(absPath string, line int) *exec.Cmd {
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	candidates := []string{editor, "nvim", "vim", "nano"}

	for _, e := range candidates {
		if e == "" {
			continue
		}
		// Honor $EDITOR even when it includes flags (e.g. "code --wait").
		parts := strings.Fields(e)
		bin, err := exec.LookPath(parts[0])
		if err != nil {
			continue
		}
		var args []string
		args = append(args, parts[1:]...)
		if isViFamily(parts[0]) && line > 0 {
			args = append(args, "+"+strconv.Itoa(line))
		}
		args = append(args, absPath)
		return exec.Command(bin, args...)
	}
	return nil
}

func isViFamily(name string) bool {
	base := filepath.Base(name)
	switch base {
	case "nvim", "vim", "vi", "view", "nview":
		return true
	}
	return false
}
