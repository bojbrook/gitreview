# gitreview

A keyboard-driven terminal UI for reviewing your own changes before you push,
and for reading other people's pull requests without leaving the shell.

Built in Go with [Bubble Tea](https://github.com/charmbracelet/bubbletea).
Shells out to `git` — no in-process git library, no index mutation, no writes.

## Install

```sh
go install github.com/bowenbrooks/gitreview/cmd/gitreview@latest
```

Or build locally:

```sh
go build -o /tmp/gitreview ./cmd/gitreview
```

## Usage

Run inside the repo you want to review. There is no flag for repo path.

### Pre-flight (your own changes)

```sh
gitreview              # changes since merge-base with origin/HEAD, incl. working tree + untracked
gitreview --working    # uncommitted changes only (staged + unstaged) vs HEAD
gitreview --staged     # staged changes only
gitreview --committed  # committed changes between merge-base and HEAD (excludes working tree)
gitreview --base main  # override the base ref
```

Default base-ref resolution walks: `origin/HEAD` → `origin/main` → `origin/master`
→ `main` → `master` → `develop` → `trunk`.

### Pull-request review

```sh
gitreview pr 1234              # check out PR #1234 in a temporary worktree and browse it
gitreview pr 1234 --refresh    # bypass on-disk PR API cache
```

PR mode loads the diff plus reviews, review comments, and issue comments from
GitHub. If a token is available (via `gh auth token` or `GITHUB_TOKEN`), you
can also draft and submit a review from inside the TUI.

## Layout

Three panes:

- **Left** — tabbed list of files and commits.
- **Center** — the diff, scrollable, with per-line cursor and gutter markers
  on commented lines.
- **Right** — context pane (PR mode): metadata, reviews, comments.

Press `?` inside the TUI for the full keybinding list.

## Status

v0 is pre-flight + read/submit PR review. See `CLAUDE.md` for architecture
notes and invariants worth preserving.
