package main

import "charm.land/lipgloss/v2"

// styles uses the 16 ANSI colors, so the terminal theme decides the
// actual colors in light and dark mode.
type styles struct {
	app, badge, header, muted, accent, selected, selectedDim, err, title  lipgloss.Style
	divider, sheet, tabActive, tabInactive, key, help, helpKey, inputLine lipgloss.Style
	jsonKey, jsonString, jsonNumber, jsonLiteral                          lipgloss.Style
}

func newStyles() styles {
	var (
		accent = lipgloss.Cyan
		muted  = lipgloss.BrightBlack
	)

	return styles{
		badge:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Black).Background(accent).Padding(0, 1),
		header:      lipgloss.NewStyle().Bold(true),
		muted:       lipgloss.NewStyle().Foreground(muted),
		accent:      lipgloss.NewStyle().Foreground(accent),
		selected:    lipgloss.NewStyle().Bold(true).Foreground(accent),
		selectedDim: lipgloss.NewStyle().Bold(true),
		err:         lipgloss.NewStyle().Foreground(lipgloss.Red),
		title:       lipgloss.NewStyle().Bold(true),
		divider:     lipgloss.NewStyle().Foreground(muted),
		sheet:       lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(0, 1),
		tabActive:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Black).Background(accent).Padding(0, 1),
		tabInactive: lipgloss.NewStyle().Foreground(muted).Padding(0, 1),
		key:         lipgloss.NewStyle().Foreground(lipgloss.Yellow),
		help:        lipgloss.NewStyle().Foreground(muted),
		helpKey:     lipgloss.NewStyle().Foreground(lipgloss.White).Bold(true),
		inputLine:   lipgloss.NewStyle().Foreground(accent),
		jsonKey:     lipgloss.NewStyle().Foreground(lipgloss.Blue),
		jsonString:  lipgloss.NewStyle().Foreground(lipgloss.Green),
		jsonNumber:  lipgloss.NewStyle().Foreground(lipgloss.Magenta),
		jsonLiteral: lipgloss.NewStyle().Foreground(lipgloss.Yellow),
	}
}
