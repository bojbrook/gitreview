# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`gitreview` is a Go + Bubble Tea TUI for pre-flight code review. The user runs it inside any git repo to walk through their working-tree changes (and committed changes since the merge-base) in a keyboard-driven interface. Eventual goal: layer Claude-drafted review comments on top; for now the UI is a static reader.

## Build, run, test

```
go build ./...                    # compile everything
go build -o /tmp/gitreview ./cmd/gitreview   # install a binary somewhere callable
go test ./...                     # all tests
go test ./internal/diff -run TestParse -v    # single test by regex
go vet ./...
```

The binary diffs the **current working directory's git repo**, so always `cd` into the target repo before running it. There's no flag for repo path.

A scratch repo with a real branch diff lives at `/tmp/gr-smoke` (created during development) — useful for interactive smoke testing.

## Architecture

Two-package design with strict separation:

- **`internal/diff/`** — owns everything git-related. Shells out to `git` (no go-git). Produces a single `Diff` struct (`types.go`) regardless of mode:
  - `git.go` `Load(Options)` is the entry point. Switches on `Mode` to pick which git invocation(s) to run.
  - `parse.go` parses unified diff output into `[]File` → `[]Hunk` → `[]Line`. Handles renames, adds, deletes, blank context lines (which are `" \n"` in real git output).
  - `untracked.go` synthesizes diff entries for files git lists as untracked (`git ls-files --others --exclude-standard`) — they're rendered as fully-added hunks. Skips binaries and >1 MiB files with placeholder text. Critically: **no `git add -N` or index mutation** — we read file contents and fabricate the diff entry.
  - `commits.go` lists commits (`git log` with `%x1f`/`%x1e` field/record separators) and loads a single commit's diff (`git show --first-parent --pretty=format: <sha>`).

- **`internal/ui/`** — Bubble Tea model. Renders the `Diff` and `[]Commit` it's handed. Has no knowledge of git.
  - `model.go` is the whole TUI: two panes (left = tabbed Files/Commits list, center = scrollable diff viewport), two view modes (`viewChanges` and `viewCommits`), per-mode cursors. Commits' per-commit diffs are lazy-loaded and cached in `commitDiff` on selection.
  - `render.go` styles a `File` or a multi-file `Diff` into a string for the viewport.
  - `styles.go` centralizes lipgloss styles.
  - When clipping ANSI strings, use `truncateAnsi` (wraps `charmbracelet/x/ansi.Truncate`). The naive `truncateRaw` is only safe for plain text — slicing a styled string can leave an unterminated escape and bleed colors across rows.

`cmd/gitreview/main.go` is thin glue: parse flags, call `diff.Load` and `diff.LoadCommits`, hand both to `ui.New`, run the program.

## Diff modes (the non-obvious bit)

The tool's `Mode` enum determines which git invocation runs:

- **default / `ModeAll`** — `git diff <merge-base>` (no second ref, which makes git include working tree) + untracked. The "what would my PR look like if I committed everything now" view.
- **`--working` / `ModeWorking`** — `git diff HEAD` + untracked. Uncommitted only.
- **`--staged` / `ModeStaged`** — `git diff --cached`. Untracked excluded (they're not in the index).
- **`--committed` / `ModeCommitted`** — `<merge-base>..HEAD`. Working tree and untracked both excluded.

Special case: a repo with no `HEAD` (fresh `git init` with no commits) falls back to `git diff --cached` + untracked under default mode, so the tool still works.

`resolveBaseRef` tries `origin/HEAD` symbolic ref first, then `origin/main`, `origin/master`, `main`, `master`, `develop`, `trunk`. Override with `--base <ref>`.

## Invariants worth preserving

- **Read-only.** No `git add`, no `git commit`, no `gh` posting, no index mutation. Future Claude integration will write to a markdown file before any GitHub posting code lands. Don't introduce write paths casually.
- **Pre-flight first.** Inbound PR triage is deferred but planned; keep `Diff`/`File`/`Hunk`/`Commit` general enough that the same UI works for both. Don't add GitHub-specific types to `internal/diff/`.
- **No daemon, no async fetch.** Commit diffs are lazy-loaded sync on cursor move. This is fine because `git show` is fast on local repos; revisit only if it becomes a bottleneck.

## Commit conventions

When you commit on this repo:

- Use a single-line subject — no body unless the user asks for one.
- Subject format: `<area>: <imperative summary>`, e.g. `ui: render context pane as third column`, `pr: parse PR refs`.
- **Do not** add the `Co-Authored-By: Claude …` trailer.
- **Do not** add the `🤖 Generated with Claude Code` line.
- Use `git commit -m "<subject>"` directly. No HEREDOC body templates.

If the change genuinely needs more context than fits in the subject (rare), keep the body to 1–2 short sentences and still skip the Claude trailer.

## Persistent project memory

Long-lived context for this project (decisions, MVP scope, why the stack was chosen) lives in `~/.claude/projects/-Users-bowen-brooks-Documents-git-review/memory/`. Read `MEMORY.md` there at session start if catching up.
