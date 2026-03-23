package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type startupEntry struct {
	text string
	done bool
}

var (
	startupTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("75"))
	startupDoneStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	startupCheckStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
)

func (m Model) renderStartupLog() string {
	var b strings.Builder
	width := m.width
	if width == 0 {
		width = 80
	}
	title := " prx "
	lineStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("237"))
	pad := (width - len(title)) / 2
	if pad < 2 {
		pad = 2
	}
	line := lineStyle.Render(strings.Repeat("─", pad)) + startupTitleStyle.Render(title) + lineStyle.Render(strings.Repeat("─", width-pad-len(title)))
	b.WriteString("\n" + line + "\n\n")

	entries := m.startupLog
	maxVisible := 12
	start := 0
	if len(entries) > maxVisible {
		start = len(entries) - maxVisible
	}
	for i := start; i < len(entries); i++ {
		entry := entries[i]
		if entry.done {
			fmt.Fprintf(&b, "  %s %s\n",
				startupCheckStyle.Render("✓"),
				startupDoneStyle.Render(entry.text))
		} else {
			fmt.Fprintf(&b, "  %s %s\n", m.spinner.View(), entry.text)
		}
	}
	if m.noPRs {
		b.WriteString("\n  Press any key to exit.\n")
	} else {
		b.WriteString("\n  Press q to quit.\n")
	}
	return b.String()
}

func (m *Model) logDone() {
	for i := len(m.startupLog) - 1; i >= 0; i-- {
		if !m.startupLog[i].done {
			m.startupLog[i].done = true
			return
		}
	}
}

func (m *Model) logStep(text string) {
	m.logDone()
	m.startupLog = append(m.startupLog, startupEntry{text: text})
}
