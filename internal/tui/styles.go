package tui

import "github.com/charmbracelet/lipgloss"

var (
	accent = lipgloss.Color("12")  // bright blue
	muted  = lipgloss.Color("240") // grey
	good   = lipgloss.Color("10")  // green
	bad    = lipgloss.Color("9")   // red
	dim    = lipgloss.Color("245")

	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(accent)
	mutedStyle    = lipgloss.NewStyle().Foreground(muted)
	dimStyle      = lipgloss.NewStyle().Foreground(dim)
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(accent)
	markedStyle   = lipgloss.NewStyle().Foreground(accent)
	labelStyle    = lipgloss.NewStyle().Foreground(dim)
	footerStyle   = lipgloss.NewStyle().Foreground(muted)
	goodStyle     = lipgloss.NewStyle().Foreground(good)
	badStyle      = lipgloss.NewStyle().Foreground(bad)
	disabledStyle = lipgloss.NewStyle().Foreground(muted).Strikethrough(true)
	fieldFocused  = lipgloss.NewStyle().Bold(true).Foreground(accent)
)

// pane returns a bordered box style sized to the given inner content area.
func pane(innerW, innerH int, focused bool) lipgloss.Style {
	bc := muted
	if focused {
		bc = accent
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(bc).
		Width(innerW).
		Height(innerH).
		Padding(0, 1)
}
