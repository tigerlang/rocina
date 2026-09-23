package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

func renderMarkdown(text string, width int) []string {
	if width < 6 {
		width = 6
	}
	raw := strings.Split(text, "\n")
	var out []string
	inCode := false
	lang := ""
	var code []string

	flushCode := func() {
		if !inCode {
			return
		}
		label := lang
		if label == "" {
			label = "code"
		}
		out = append(out, blockLine(label, colorMuted, colorSurface2, width))
		for _, line := range code {
			for _, wrapped := range wrapText(line, width-2) {
				out = append(out, blockLine(wrapped, colorText, colorSurface, width))
			}
		}
		code = nil
		lang = ""
	}

	for _, line := range raw {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				flushCode()
				inCode = false
			} else {
				inCode = true
				lang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			}
			continue
		}
		if inCode {
			code = append(code, line)
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "### "):
			out = append(out, heading(strings.TrimPrefix(trimmed, "### "), width, colorAccent2))
		case strings.HasPrefix(trimmed, "## "):
			out = append(out, heading(strings.TrimPrefix(trimmed, "## "), width, lavender))
		case strings.HasPrefix(trimmed, "# "):
			out = append(out, heading(strings.TrimPrefix(trimmed, "# "), width, plum))
		case strings.HasPrefix(trimmed, "> "):
			for _, wrapped := range wrapText(strings.TrimPrefix(trimmed, "> "), width) {
				out = append(out, thinkingStyle.Render(wrapped))
			}
		case trimmed == "":
			out = append(out, "")
		default:
			for _, wrapped := range wrapText(line, width) {
				out = append(out, inline(wrapped))
			}
		}
	}
	if inCode {
		flushCode()
	}
	if len(out) == 0 {
		out = append(out, "")
	}
	return out
}

func heading(text string, width int, color rgb) string {
	style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color.hex()))
	lines := wrapText(text, width)
	for i, line := range lines {
		lines[i] = style.Render(line)
	}
	return strings.Join(lines, "\n")
}

var (
	mdCodeStyle = fgStyle(sky)
	mdBoldStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(plum.hex()))
)

// inline styles `code` spans and **bold** spans, leaving the rest in the base color.
func inline(line string) string {
	var b strings.Builder
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			b.WriteString(assistantStyle.Render(plain.String()))
			plain.Reset()
		}
	}
	for i := 0; i < len(line); {
		if strings.HasPrefix(line[i:], "**") {
			if end := strings.Index(line[i+2:], "**"); end >= 0 {
				flush()
				b.WriteString(mdBoldStyle.Render(line[i+2 : i+2+end]))
				i += 2 + end + 2
				continue
			}
		}
		if line[i] == '`' {
			if end := strings.IndexByte(line[i+1:], '`'); end >= 0 {
				flush()
				b.WriteString(mdCodeStyle.Render(line[i+1 : i+1+end]))
				i += 1 + end + 1
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		plain.WriteRune(r)
		i += size
	}
	flush()
	return b.String()
}
