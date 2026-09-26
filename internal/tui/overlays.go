package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) overlayGeom(rows int) rect {
	width := m.width - 8
	if width > 80 {
		width = 80
	}
	if width < 30 {
		width = 30
	}
	height := rows + 4
	if height > m.height-4 {
		height = m.height - 4
	}
	if height < 6 {
		height = 6
	}
	return rect{x: (m.width - width) / 2, y: (m.height - height) / 2, w: width, h: height}
}

func renderFramed(title, body string, r rect) string {
	inner := r.w - 4
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colorAccent.hex())).
		Padding(0, 1).
		Width(inner + 2).
		Height(r.h - 2)
	return style.Render(labelStyle.Render(title) + "\n" + body)
}

func (m Model) placeOverlay(box string) string {
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) renderSessionsOverlay() string {
	active := m.activeView()
	rows := len(m.views) + 1
	geom := m.overlayGeom(rows)
	var b strings.Builder
	for i, v := range m.views {
		marker := "  "
		name := textStyle.Render(v.session.Name)
		if i == m.sessionCursor {
			marker = fgStyle(plum).Render("▍ ")
			name = fgStyle(plum).Bold(true).Render(v.session.Name)
		}
		tag := ""
		if active != nil && v.session.ID == active.session.ID {
			tag = goodStyle.Render(" ●")
		}
		line := marker + padRight(name, 22) + faintStyle.Render(v.session.ID) + " " + mutedStyle.Render(v.session.Created.Format("15:04:05")) + tag
		b.WriteString(truncate(line, geom.w-4) + "\n")
	}
	title := "SESSIONS   " + faintStyle.Render("enter switch · d delete · ctrl+n new · esc close")
	return m.placeOverlay(renderFramed(title, strings.TrimRight(b.String(), "\n"), geom))
}

func (m Model) sessionRects() []rect {
	geom := m.overlayGeom(len(m.views) + 1)
	rects := make([]rect, 0, len(m.views))
	for i := range m.views {
		rects = append(rects, rect{x: geom.x + 1, y: geom.y + 2 + i, w: geom.w - 2, h: 1})
	}
	return rects
}

func (m Model) renderSettingsOverlay() string {
	s := m.settings
	rows := s.settingsRows()
	geom := m.overlayGeom(rows)

	tabs := []string{"security", "tui config", "hotkeys"}
	labels := make([]string, len(tabs))
	for i, name := range tabs {
		if i == s.tab {
			labels[i] = fgStyle(plum).Bold(true).Render(name)
		} else {
			labels[i] = mutedStyle.Render(name)
		}
	}
	header := strings.Join(labels, "   ")

	var body strings.Builder
	body.WriteString(header + "\n")
	body.WriteString(settingsMarker(tabs, s.tabPos) + "\n")
	switch s.tab {
	case 0:
		cfg := m.mgr.Config()
		for i, item := range securityItems {
			checked := false
			switch i {
			case 0:
				checked = cfg.Security.BlockUserPaths
			case 1:
				checked = cfg.Security.BlockSystemPaths
			case 2:
				checked = cfg.Security.AskBeforeRun
			}
			box := faintStyle.Render("[ ]")
			if checked {
				box = goodStyle.Render("[x]")
			}
			cursor := "  "
			text := textStyle.Render(item)
			if i == s.cursor {
				cursor = fgStyle(plum).Render("▍ ")
				text = fgStyle(plum).Bold(true).Render(item)
			}
			body.WriteString(cursor + box + " " + truncate(text, geom.w-10) + "\n")
		}
		body.WriteString("\n" + faintStyle.Render("space/enter toggle · tab switch · esc close"))
	case 1:
		body.WriteString(s.editor.View() + "\n")
		body.WriteString(faintStyle.Render("ctrl+s save · tab switch · esc close"))
	default:
		for _, line := range hotkeyLines {
			body.WriteString(mutedStyle.Render("  "+truncate(line, geom.w-6)) + "\n")
		}
		body.WriteString("\n" + faintStyle.Render("tab switch · esc close"))
	}
	if s.status != "" {
		body.WriteString("\n" + goodStyle.Render(truncate(s.status, geom.w-6)))
	}
	title := "SETTINGS   " + faintStyle.Render(configPathLabel())
	return m.placeOverlay(renderFramed(title, body.String(), geom))
}

// settingsMarker draws the bar under the tab row. tabPos eases toward the
// selected tab, so the underline rolls to the new tab like the agent sidebar.
func settingsMarker(tabs []string, tabPos float64) string {
	const gap = "   "
	starts := make([]int, len(tabs))
	column := 0
	for i, name := range tabs {
		starts[i] = column
		column += len([]rune(name)) + len(gap)
	}
	last := len(tabs) - 1
	pos := tabPos
	if pos < 0 {
		pos = 0
	}
	if pos > float64(last) {
		pos = float64(last)
	}
	low := int(math.Floor(pos))
	if low < 0 {
		low = 0
	}
	high := low + 1
	if high > last {
		high = last
	}
	fraction := pos - float64(low)
	x := float64(starts[low]) + (float64(starts[high])-float64(starts[low]))*fraction
	width := len([]rune(tabs[low]))
	start := int(math.Round(x))
	line := make([]rune, start+width)
	for i := range line {
		line[i] = ' '
	}
	for i := start; i < start+width && i < len(line); i++ {
		line[i] = '▔'
	}
	shade := 1.0
	if last > 0 {
		shade = pos / float64(last)
	}
	return fgStyle(mix(colorMuted, plum, shade)).Render(string(line))
}

func (m Model) renderModelsOverlay() string {
	geom := m.overlayGeom(len(m.models) + 1)
	current := ""
	if view := m.activeView(); view != nil {
		current = m.targetModel(view)
	}
	var b strings.Builder
	start := m.modelWindowStart()
	shown := 0
	if m.modelsErr != "" {
		b.WriteString(badStyle.Render(truncate(m.modelsErr, geom.w-6)) + "\n")
	} else if len(m.models) == 0 {
		b.WriteString(mutedStyle.Render("loading…"))
	}
	for i := start; i < len(m.models); i++ {
		if geom.y+2+shown >= geom.y+geom.h-1 {
			break
		}
		name := truncate(m.models[i], geom.w-12)
		cursor := "  "
		styled := textStyle.Render(name)
		if i == m.modelCursor {
			cursor = fgStyle(plum).Render("▍ ")
			styled = fgStyle(plum).Bold(true).Render(name)
		}
		mark := ""
		if m.models[i] == current {
			mark = goodStyle.Render(" ●")
		}
		b.WriteString(cursor + styled + mark + "\n")
		shown++
	}
	target := "chief"
	if m.modelTarget == 1 {
		target = "subagents"
	}
	title := "MODELS · target: " + fgStyle(plum).Bold(true).Render(target) + "   " + faintStyle.Render("tab switch · enter select · esc close")
	return m.placeOverlay(renderFramed(title, strings.TrimRight(b.String(), "\n"), geom))
}

func (m Model) modelRects() []rect {
	geom := m.overlayGeom(len(m.models) + 1)
	start := m.modelWindowStart()
	rects := make([]rect, len(m.models))
	for i := start; i < len(m.models); i++ {
		y := geom.y + 2 + (i - start)
		if y >= geom.y+geom.h-1 {
			break
		}
		rects[i] = rect{x: geom.x + 1, y: y, w: geom.w - 2, h: 1}
	}
	return rects
}

func configPathLabel() string {
	return "~/.config/rocina/config.json"
}

func (m Model) renderApprovalOverlay() string {
	if m.approval == nil {
		return m.placeOverlay("")
	}
	geom := m.overlayGeom(6)
	allow := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorGood.hex())).Render("[ y  allow ]")
	deny := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorBad.hex())).Render("[ n  deny ]")
	var b strings.Builder
	b.WriteString(labelStyle.Render(m.approval.approval.Action) + "\n")
	for _, line := range wrapText(m.approval.approval.Detail, geom.w-8) {
		b.WriteString(textStyle.Render(line) + "\n")
	}
	b.WriteString("\n" + allow + "    " + deny)
	return m.placeOverlay(renderFramed("PERMISSION REQUIRED", b.String(), geom))
}

func (m Model) approvalRects() ([2]rect, bool) {
	if m.approval == nil {
		return [2]rect{}, false
	}
	geom := m.overlayGeom(6)
	y := geom.y + geom.h - 3
	return [2]rect{
		{x: geom.x + 2, y: y, w: 12, h: 1},
		{x: geom.x + 18, y: y, w: 12, h: 1},
	}, true
}
