package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"rocina/internal/bus"
	"rocina/internal/orchestrator"
	"rocina/internal/version"
)

func (m Model) View() string {
	if !m.ready {
		return lipgloss.Place(80, 24, lipgloss.Center, lipgloss.Center, titleStyle.Render("rocina"))
	}
	switch m.overlay {
	case overlaySessions:
		return fadeANSI(m.renderSessionsOverlay(), m.overlayOpacity())
	case overlaySettings:
		return fadeANSI(m.renderSettingsOverlay(), m.overlayOpacity())
	case overlayApproval:
		return fadeANSI(m.renderApprovalOverlay(), m.overlayOpacity())
	case overlayModels:
		return fadeANSI(m.renderModelsOverlay(), m.overlayOpacity())
	}

	view := m.activeView()
	if view == nil {
		return ""
	}
	l := m.computeLayout()
	var sections []string
	if l.logoH > 0 {
		sections = append(sections, fitHeight(renderLogo(m.width, m.frame, m.tagline()), l.logoH))
	}
	if l.aboveH > 0 {
		above := fadeBlock(m.renderSession(view, l), clamp01(view.ease))
		sections = append(sections, fitHeight(above, l.aboveH))
	}
	if l.statusH > 0 {
		sections = append(sections, fitHeight(m.renderStatus(l), l.statusH))
	}
	if l.stopH > 0 {
		sections = append(sections, fitHeight(fadeANSI(m.renderStopPanel(l), m.overlayOpacity()), l.stopH))
	}
	sections = append(sections, fitHeight(m.renderComposerBlock(), l.inputH))
	if l.belowH > 0 {
		sections = append(sections, fitHeight("", l.belowH))
	}
	return strings.Join(sections, "\n")
}

func (m Model) tagline() string {
	return fmt.Sprintf("rocina · chief · subagents · tools · %s", version.Version)
}

func (m Model) renderSession(view *sessionView, l layout) string {
	chat := m.renderChatPanel(view, m.focusedName(view), l.chatW, l.chatH, l.sideW > 0)
	if l.sideW > 0 {
		side := m.renderSidebar(view, l.sideW, l.chatH)
		return lipgloss.JoinHorizontal(lipgloss.Top, chat, side)
	}
	return chat
}

func (m Model) renderChatPanel(view *sessionView, name string, width, height int, hasSidebar bool) string {
	if width < 14 {
		width = 14
	}
	if height < 3 {
		height = 3
	}
	contentWidth := width - 2
	if contentWidth < 8 {
		contentWidth = 8
	}

	bodyHeight := height - 1
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	body, _ := m.buildChat(view, name, contentWidth, bodyHeight)

	// Pad every line to the full panel width so the block keeps its size and
	// the sidebar is pushed to the right edge regardless of the text length.
	lines := append([]string{m.renderAgentHeader(view, name, contentWidth)}, body...)
	for i := range lines {
		lines[i] = padRight(lines[i], contentWidth)
	}
	return fitHeight(strings.Join(lines, "\n"), height)
}

// renderAgentHeader draws the focused agent line on a full-width gray bar with
// the agent name emphasized. Lipgloss ends every rendered segment with a reset,
// which would punch black holes in the bar, so the background is re-asserted
// after each reset instead of being set per segment.
func (m Model) renderAgentHeader(view *sessionView, name string, width int) string {
	if width < 6 {
		width = 6
	}
	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(plum.hex())).Bold(true)
	label := nameStyle.Render(name)
	if agent := view.session.Orch.Bus().Get(name); agent != nil {
		label += "  " + stateStyle(string(agent.State())).Render(string(agent.State()))
		if agent.Model != "" {
			label += "  " + faintStyle.Render(agent.Model)
		}
	}
	label = lipgloss.NewStyle().MaxWidth(maxInt(1, width-1)).Render(label)
	pad := width - 1 - lipgloss.Width(label)
	if pad < 0 {
		pad = 0
	}
	bg := "\x1b[48;2;" + fmt.Sprintf("%d;%d;%d", clamp8(colorSurface2.r), clamp8(colorSurface2.g), clamp8(colorSurface2.b)) + "m"
	line := " " + label + strings.Repeat(" ", pad)
	line = bg + strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+bg)
	return line + "\x1b[0m"
}

func (m Model) buildChat(view *sessionView, agent string, width, height int) ([]string, []string) {
	var lines []string
	var keys []string
	addLine := func(line, key string) {
		lines = append(lines, line)
		keys = append(keys, key)
	}
	addText := func(text string, wrapWidth int, style lipgloss.Style, key string) {
		for _, line := range wrapText(text, wrapWidth) {
			addLine(style.Render(line), key)
		}
	}

	blocks := view.chats[agent]
	for i, b := range blocks {
		key := fmt.Sprintf("%s:%d", agent, i)
		switch b.Kind {
		case "user":
			for _, line := range renderUserBox(b.Text, width) {
				addLine(line, "")
			}
		case "text":
			rendered := renderMarkdown(b.Text, width)
			if !b.Done && len(rendered) > 0 {
				rendered[len(rendered)-1] += m.streamGlyph()
			}
			for _, line := range rendered {
				addLine(line, "")
			}
		case "reasoning":
			if strings.TrimSpace(b.Text) == "" && b.Done {
				continue
			}
			label := "▸ thinking"
			if view.expanded[key] {
				label = "▾ thinking"
			}
			if !b.Done {
				label += " …"
			}
			addLine(thinkingStyle.Render(label), key)
			if view.expanded[key] {
				addText(b.Text, width-2, thinkingStyle, key)
			}
		case "error":
			addLine(errorStyle.Render("provider error"), "")
			addText(b.Text, width, errorStyle, "")
		case "tool":
			for _, line := range m.renderToolBox(b, width) {
				addLine(line, "")
			}
		}
	}

	total := len(lines)
	offset := total - height - view.scroll
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	end := offset + height
	if end > total {
		end = total
	}
	lines = lines[offset:end]
	keys = keys[offset:end]
	for len(lines) < height {
		lines = append(lines, "")
		keys = append(keys, "")
	}
	return lines, keys
}

func (m Model) streamGlyph() string {
	frames := []string{"▏", "▎", "▍", "▌"}
	return fgStyle(gradientAt(float64(m.frame) * 0.05)).Render(frames[(m.frame/2)%len(frames)])
}

type toolLine struct {
	text string
	fg   rgb
}

// renderUserBox draws a user message as a filled gray block with a thin purple
// accent line on its left. Assistant output never uses this style.
func renderUserBox(text string, width int) []string {
	if width < 6 {
		width = 6
	}
	bar := fgStyle(plum).Render("▌")
	inner := width - 3
	if inner < 2 {
		inner = 2
	}
	var out []string
	for _, line := range wrapText(text, inner-2) {
		body := blockLine(line, colorText, colorSurface2, inner)
		out = append(out, " "+bar+body)
	}
	if len(out) == 0 {
		out = append(out, " "+bar+blockLine("", colorText, colorSurface2, inner))
	}
	return out
}

func blockLine(text string, fg, bg rgb, width int) string {
	inner := width - 2
	if inner < 1 {
		inner = 1
	}
	text = lipgloss.NewStyle().MaxWidth(inner).Render(text)
	pad := inner - lipgloss.Width(text)
	if pad < 0 {
		pad = 0
	}
	style := lipgloss.NewStyle().
		Background(lipgloss.Color(bg.hex())).
		Foreground(lipgloss.Color(fg.hex()))
	return style.Render(" " + text + strings.Repeat(" ", pad+1))
}

func (m Model) renderToolBox(b Block, width int) []string {
	if width < 8 {
		width = 8
	}
	title := strings.TrimSuffix(b.Tool, "_tool")
	if !b.Done {
		title += " …"
	}
	lines := []string{blockLine(title, colorText, colorSurface2, width)}
	for _, line := range toolBodyLines(b, width-2) {
		lines = append(lines, blockLine(line.text, line.fg, colorSurface, width))
	}
	return lines
}

func toolBodyLines(b Block, width int) []toolLine {
	fields := parseArgs(b.Args)
	var out []toolLine
	add := func(text string, fg rgb) { out = append(out, toolLine{text, fg}) }
	switch b.Tool {
	case "bash_tool", "terminal_tool":
		for _, line := range wrapText(fields["command"], width) {
			add(line, sky)
		}
	case "edit_file":
		if path := fields["path"]; path != "" {
			add(truncate(path, width), colorMuted)
		}
		for _, line := range splitLines(fields["old"]) {
			add("- "+line, colorBad)
		}
		for _, line := range splitLines(fields["new"]) {
			add("+ "+line, colorGood)
		}
	case "write_file":
		if path := fields["path"]; path != "" {
			add(truncate(path, width), colorMuted)
		}
		for _, line := range splitLines(fields["content"]) {
			add("+ "+line, colorGood)
		}
	default:
		for _, arg := range formatArgs(b.Args) {
			for _, line := range wrapText(arg, width) {
				add(line, sky)
			}
		}
	}
	if len(out) == 0 {
		add("(no arguments)", colorMuted)
	}
	if strings.TrimSpace(b.Result) != "" {
		add("", colorMuted)
		for _, line := range resultPreview(b.Result, width, 8) {
			add(line, colorMuted)
		}
	}
	return out
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func parseArgs(args string) map[string]string {
	out := map[string]string{}
	var object map[string]any
	if err := json.Unmarshal([]byte(args), &object); err != nil {
		return out
	}
	for key, value := range object {
		out[key] = stringify(value)
	}
	return out
}

func (m Model) stateGlyph(state string) string {
	switch state {
	case "running":
		brightness := 0.5 + 0.5*math.Sin(float64(m.frame)*0.35)
		return fgStyle(mix(colorGood, mint, brightness)).Render("●")
	case "waiting":
		frames := []string{"◐", "◓", "◑", "◒"}
		return warnStyle.Render(frames[(m.frame/3)%len(frames)])
	case "error":
		return badStyle.Render("▲")
	case "stopped":
		return faintStyle.Render("○")
	default:
		return fgStyle(colorAccent2).Render("○")
	}
}

func (m Model) renderSidebar(view *sessionView, width, height int) string {
	if width < 14 {
		width = 14
	}
	if height < 3 {
		height = 3
	}
	innerWidth := width - 3
	innerHeight := height - 2

	header := labelStyle.Render("AGENTS") + "  " + fgStyle(plum).Render("settings")

	lines := []string{header}
	available := innerHeight - 1
	agents := view.session.Orch.Bus().List()
	if len(agents) == 0 {
		lines = append(lines, mutedStyle.Render("no agents"))
	}
	for _, a := range agents {
		card := m.agentCard(view, a, innerWidth)
		cardLines := strings.Split(card, "\n")
		if progress, ok := view.spawn[a.Name]; ok && progress < 1 {
			cardLines = spawnLines(card, progress)
		}
		for _, line := range cardLines {
			if available <= 0 {
				break
			}
			lines = append(lines, line)
			available--
		}
		if available <= 0 {
			break
		}
	}
	lines = slideFocusBar(lines, view.focusPos)
	content := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colorFaint.hex())).
		Padding(0, 1).
		Width(innerWidth + 1).
		Height(height - 2)
	return style.Render(content)
}

// slideFocusBar paints one highlight bar over the agent list. focusPos eases
// toward the focused card, so the bar rolls from the old card to the new one
// along the rows between them instead of jumping or cross-fading.
func slideFocusBar(lines []string, focusPos float64) []string {
	const cardHeight = 3
	top := 1 + int(math.Round(focusPos*cardHeight))
	last := len(lines) - cardHeight
	if last < 1 {
		return lines
	}
	if top < 1 {
		top = 1
	}
	if top > last {
		top = last
	}
	bar := fgStyle(plum).Render("▍")
	for row := top; row < top+cardHeight && row < len(lines); row++ {
		if strings.HasPrefix(lines[row], " ") {
			lines[row] = bar + lines[row][1:]
		}
	}
	return lines
}

// spawnLines grows a freshly spawned card out of the previous one and fades it
// in, top-down, so the subagent appears to float out of the chief card above it
// instead of popping into the sidebar. Only the revealed lines take space while
// the card grows.
func spawnLines(card string, progress float64) []string {
	progress = clamp01(progress)
	lines := strings.Split(card, "\n")
	reveal := int(math.Ceil(progress * float64(len(lines))))
	if reveal < 1 {
		reveal = 1
	}
	if reveal > len(lines) {
		reveal = len(lines)
	}
	out := make([]string, 0, len(lines))
	for i := 0; i < reveal; i++ {
		out = append(out, fadeANSI(lines[i], progress))
	}
	return out
}

func (m Model) agentCard(view *sessionView, a *bus.Agent, width int) string {
	state := string(a.State())
	name := truncate(a.Name, maxInt(4, width-14))
	head := m.stateGlyph(state) + " " + textStyle.Render(name)
	if a.Kind == bus.KindChief {
		head = m.stateGlyph(state) + " " + fgStyle(plum).Bold(true).Render(name)
	}
	head = padRight(head, maxInt(0, width-12)) + stateStyle(state).Render(truncate(state, 9))
	preview := faintStyle.Render(lastPreview(view, a.Name, maxInt(4, width-2)))
	stats := faintStyle.Render(fmt.Sprintf("%d tool · %d msg", toolCount(view, a.Name), msgCount(view, a.Name)))
	return " " + head + "\n" + " " + preview + "\n" + " " + stats
}

func (m Model) renderComposerBlock() string {
	width := m.width
	lines := []string{blockLine("message", colorMuted, colorSurface2, width)}
	body := strings.Split(m.input.View(), "\n")
	for i := 0; i < 2; i++ {
		text := ""
		if i < len(body) {
			text = body[i]
		}
		lines = append(lines, inputLine(text, width))
	}
	lines = append(lines, blockLine("", colorMuted, colorSurface, width))
	return strings.Join(lines, "\n") + "\n" + m.renderHint()
}

func inputLine(text string, width int) string {
	inner := width - 2
	if inner < 1 {
		inner = 1
	}
	text = trimTrailingSpace(text)
	text = lipgloss.NewStyle().MaxWidth(inner).Render(text)
	pad := inner - lipgloss.Width(text)
	if pad < 0 {
		pad = 0
	}
	bgStyle := lipgloss.NewStyle().Background(lipgloss.Color(colorSurface.hex()))
	return bgStyle.Render(" ") + text + "\x1b[0m" + bgStyle.Render(strings.Repeat(" ", pad+1)) + "\x1b[0m"
}

var (
	ansiEscape         = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	cursorSpacePattern = regexp.MustCompile("(\x1b\\[7[0-9;]*m) (\x1b\\[0m)")
)

// trimTrailingSpace drops trailing spaces and the ANSI codes after the last
// visible rune, so textarea padding cannot leak the terminal's default background.
// A reversed space (the caret parked on an empty cell) is protected first.
func trimTrailingSpace(s string) string {
	s = cursorSpacePattern.ReplaceAllString(s, "${1}\x00${2}")
	last := -1
	for i := 0; i < len(s); {
		if loc := ansiEscape.FindStringIndex(s[i:]); loc != nil && loc[0] == 0 {
			i += loc[1]
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r != ' ' {
			last = i + size
		}
		i += size
	}
	if last < 0 {
		return ""
	}
	return strings.ReplaceAll(s[:last], "\x00", " ")
}

func (m Model) renderStopPanel(l layout) string {
	title := "STOP"
	if m.stopSubmenu {
		title = "STOP · subagents"
	}
	width := m.stopWidth()
	box := renderFramed(labelStyle.Render(title), strings.Join(m.stopLines(), "\n"), rect{x: 0, y: 0, w: width, h: l.stopH})
	lines := strings.Split(box, "\n")
	for index := range lines {
		lines[index] = "  " + lines[index]
	}
	return fitHeight(strings.Join(lines, "\n"), l.stopH)
}

func (m Model) renderStatus(l layout) string {
	width := maxInt(8, m.width-16)
	lines := []string{statusLabelStyle.Render("  you are in: ") + statusPathStyle.Render(truncate(m.userDir, width))}
	if l.statusH > 1 {
		path := ""
		if view := m.activeView(); view != nil {
			path = view.cwd[m.focusedName(view)]
			if path == "" {
				if abs, err := filepath.Abs(view.session.Orch.Config().Workspace); err == nil {
					path = abs
				}
			}
		}
		left := statusLabelStyle.Render("  agent now in: ") + statusPathStyle.Render(truncate(path, width))
		right := m.renderTokens()
		leftWidth := lipgloss.Width(left)
		rightWidth := lipgloss.Width(right)
		if leftWidth+rightWidth+4 <= m.width {
			left += strings.Repeat(" ", m.width-2-leftWidth-rightWidth) + right
		}
		lines = append(lines, left)
	}
	return fitHeight(strings.Join(lines, "\n"), l.statusH)
}

// renderTokens puts the session usage on the right side above the composer.
func (m Model) renderTokens() string {
	view := m.activeView()
	if view == nil {
		return ""
	}
	st := view.session.Orch.Stats()
	text := fmt.Sprintf("ctx %s · total %s · %d req · %.0f rpm · %s",
		humanTokens(st.ContextTokens), humanTokens(st.TotalTokens), st.Requests, st.RPM, costLabel(st))
	return hintStyle.Render(text)
}

func humanTokens(count int) string {
	switch {
	case count >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(count)/1_000_000)
	case count >= 1_000:
		return fmt.Sprintf("%.1fk", float64(count)/1_000)
	default:
		return fmt.Sprintf("%d", count)
	}
}

func costLabel(st orchestrator.Stats) string {
	if !st.CostKnown {
		return "cost n/a"
	}
	return fmt.Sprintf("$%.6f", st.Cost)
}

func (m Model) hintLines() []string {
	width := maxInt(8, m.width-2)
	if m.err != nil {
		return wrapText("error: "+m.err.Error(), width)
	}
	cfg := m.activeConfig()
	text := cfg.Provider + " · " + cfg.Model +
		"    enter send · shift+enter/ctrl+j newline · tab agent · ctrl+a models · ctrl+n new · ctrl+l sessions · ctrl+o settings · ctrl+c quit"
	return wrapText(text, width)
}

func (m Model) renderHint() string {
	lines := m.hintLines()
	style := mutedStyle
	if m.err != nil {
		style = badStyle
	}
	for i, line := range lines {
		lines[i] = style.Render("  " + line)
	}
	return strings.Join(lines, "\n")
}

func fadeBlock(s string, opacity float64) string {
	opacity = clamp01(opacity)
	if opacity >= 0.995 {
		return s
	}
	return fgStyle(mix(bgColor, colorText, opacity)).Render(stripANSI(s))
}

func fitHeight(s string, height int) string {
	if height <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func padRight(s string, width int) string {
	if lipgloss.Width(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-lipgloss.Width(s))
}

func truncate(s string, limit int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if limit < 1 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
