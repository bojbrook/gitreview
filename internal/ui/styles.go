package ui

import "github.com/charmbracelet/lipgloss"

var (
	colBorder      = lipgloss.Color("240")
	colBorderFocus = lipgloss.Color("69")
	colMuted       = lipgloss.Color("244")
	colAdded       = lipgloss.Color("42")
	colRemoved     = lipgloss.Color("203")
	colAddedBg     = lipgloss.Color("22")
	colRemovedBg   = lipgloss.Color("52")
	colCursor      = lipgloss.Color("39")
	colStatusAdd   = lipgloss.Color("42")
	colStatusDel   = lipgloss.Color("203")
	colStatusMod   = lipgloss.Color("214")
	colStatusRen   = lipgloss.Color("141")
)

var (
	paneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colBorder).
			Padding(0, 1)

	paneFocusStyle = paneStyle.
			BorderForeground(colBorderFocus)

	titleStyle = lipgloss.NewStyle().
			Foreground(colMuted).
			Bold(true)

	cursorStyle = lipgloss.NewStyle().
			Background(colCursor).
			Foreground(lipgloss.Color("15")).
			Bold(true)

	mutedStyle = lipgloss.NewStyle().Foreground(colMuted)

	helpStyle = lipgloss.NewStyle().
			Foreground(colMuted).
			Padding(0, 1)

	addedLineStyle   = lipgloss.NewStyle().Foreground(colAdded)
	removedLineStyle = lipgloss.NewStyle().Foreground(colRemoved)
	gutterStyle      = lipgloss.NewStyle().Foreground(colMuted)

	// beforeBodyStyle is the uniform tint applied to the BEFORE (removed/old)
	// content. Dimmer red so it visually recedes vs. the syntax-highlighted
	// AFTER (new) content.
	beforeBodyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("167"))

	activeTabStyle = lipgloss.NewStyle().
			Foreground(colBorderFocus).
			Bold(true).
			Underline(true)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(colMuted)

	tabSepStyle = lipgloss.NewStyle().Foreground(colBorder)

	// spineLabelStyle is the small bold file name above each overview spine cell.
	spineLabelStyle = lipgloss.NewStyle().Bold(true)

	// cursorBarStyle is the bright accent used for spine markers indicating
	// the currently-focused hunk.
	cursorBarStyle = lipgloss.NewStyle().Foreground(colCursor).Bold(true)

	// splitDividerStyle is the BEFORE/AFTER divider in split view — bright + bold
	// so it reads as a hard boundary, not a subtle separator.
	splitDividerStyle = lipgloss.NewStyle().
				Foreground(colBorderFocus).
				Bold(true)

	// contextPaneStyle is the rounded-border box around the third column.
	contextPaneStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colBorder).
				Padding(0, 1)

	contextPaneFocusStyle = contextPaneStyle.
				BorderForeground(colBorderFocus)

	// contextSectionHeaderStyle is the "▸ Where" / "▸ Symbol" label.
	contextSectionHeaderStyle = lipgloss.NewStyle().
					Foreground(colBorderFocus).
					Bold(true)

	contextItemStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))

	contextMutedStyle = lipgloss.NewStyle().Foreground(colMuted)

	contextItemSelectedStyle = lipgloss.NewStyle().
					Background(colCursor).
					Foreground(lipgloss.Color("15"))

	// prHeaderStyle is the "PR #N · author · state" strip in the top header
	// when running in PR mode.
	prHeaderStyle = lipgloss.NewStyle().
			Foreground(colBorderFocus).
			Bold(true)
)
