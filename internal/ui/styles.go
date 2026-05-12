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
			BorderForeground(colBorder)

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

	activeTabStyle = lipgloss.NewStyle().
			Foreground(colBorderFocus).
			Bold(true).
			Underline(true)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(colMuted)

	tabSepStyle = lipgloss.NewStyle().Foreground(colBorder)
)
