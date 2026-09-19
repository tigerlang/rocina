package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorText     = hexRGB("#CDD6F4")
	colorMuted    = hexRGB("#6C7086")
	colorFaint    = hexRGB("#3B3B52")
	colorSurface  = hexRGB("#1E1E1E")
	colorSurface2 = hexRGB("#2A2A2A")
	colorGood     = hexRGB("#A6E3A1")
	colorWarn     = hexRGB("#F9E2AF")
	colorBad      = hexRGB("#F38BA8")
	colorAccent   = plum
	colorAccent2  = sky

	textStyle      = fgStyle(colorText)
	mutedStyle     = fgStyle(colorMuted)
	faintStyle     = fgStyle(colorFaint)
	goodStyle      = fgStyle(colorGood)
	warnStyle      = fgStyle(colorWarn)
	badStyle       = fgStyle(colorBad)
	subtitleStyle  = fgStyle(colorMuted)
	inputStyle     = fgStyle(colorText)
	titleStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorAccent.hex()))
	labelStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorAccent.hex()))
	focusTitle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorAccent.hex()))
	assistantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#E8E8F2"))
	thinkingStyle  = fgStyle(hexRGB("#8A8FA8"))
)

func stateStyle(state string) lipgloss.Style {
	switch state {
	case "running":
		return goodStyle
	case "waiting":
		return warnStyle
	case "error":
		return badStyle
	case "stopped":
		return faintStyle
	default:
		return fgStyle(colorAccent2)
	}
}
