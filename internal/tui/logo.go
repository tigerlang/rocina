package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const bigLogoWidth = 42

var bigGlyphs = map[rune][]string{
	'R': {"█████", "█   █", "█████", "█  █ ", "█   █", "█   █"},
	'O': {"█████", "█   █", "█   █", "█   █", "█   █", "█████"},
	'C': {"█████", "█    ", "█    ", "█    ", "█    ", "█████"},
	'I': {"█████", "  █  ", "  █  ", "  █  ", "  █  ", "█████"},
	'N': {"█   █", "██  █", "█ █ █", "█  ██", "█   █", "█   █"},
	'A': {" ███ ", "█   █", "█   █", "█████", "█   █", "█   █"},
}

func blockWord(word string, glyphs map[rune][]string) []string {
	height := 0
	padded := map[rune][]string{}
	for _, ch := range word {
		glyph, ok := glyphs[ch]
		if !ok {
			continue
		}
		width := 0
		for _, line := range glyph {
			if n := lipgloss.Width(line); n > width {
				width = n
			}
		}
		rows := make([]string, len(glyph))
		for i, line := range glyph {
			rows[i] = line + strings.Repeat(" ", width-lipgloss.Width(line))
		}
		padded[ch] = rows
		if len(rows) > height {
			height = len(rows)
		}
	}
	lines := make([]string, height)
	for i, ch := range word {
		rows, ok := padded[ch]
		if !ok {
			continue
		}
		if i > 0 {
			for r := range lines {
				lines[r] += " "
			}
		}
		for r := 0; r < height; r++ {
			if r < len(rows) {
				lines[r] += rows[r]
			}
		}
	}
	return lines
}

func renderLogo(width, frame int, tagline string) string {
	clip := lipgloss.NewStyle().MaxWidth(width)
	art := []string{"R O C I N A"}
	if width >= bigLogoWidth {
		art = blockWord("ROCINA", bigGlyphs)
	}
	phase := float64(frame) * 0.03
	out := []string{""}
	for row, line := range art {
		var b strings.Builder
		column := 0
		for _, ch := range line {
			if ch == ' ' {
				b.WriteRune(ch)
				column++
				continue
			}
			t := phase + float64(column)*0.025 + float64(row)*0.05
			b.WriteString(fgStyle(gradientAt(t)).Render(string(ch)))
			column++
		}
		out = append(out, lipgloss.PlaceHorizontal(width, lipgloss.Center, clip.Render(b.String())))
	}
	out = append(out, lipgloss.PlaceHorizontal(width, lipgloss.Center, clip.Render(mutedStyle.Render(tagline))))
	return strings.Join(out, "\n")
}
