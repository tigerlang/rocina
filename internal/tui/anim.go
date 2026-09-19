package tui

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type rgb struct{ r, g, b float64 }

var (
	plum     = hexRGB("#CBA6F7")
	lavender = hexRGB("#B4BEFE")
	sky      = hexRGB("#89DCEB")
	mint     = hexRGB("#94E2D5")
	pink     = hexRGB("#F5C2E7")
	peach    = hexRGB("#FAB387")
	bgColor  = hexRGB("#11111B")
)

func hexRGB(h string) rgb {
	h = strings.TrimPrefix(h, "#")
	if len(h) != 6 {
		return rgb{}
	}
	r, _ := strconv.ParseInt(h[0:2], 16, 64)
	g, _ := strconv.ParseInt(h[2:4], 16, 64)
	b, _ := strconv.ParseInt(h[4:6], 16, 64)
	return rgb{float64(r), float64(g), float64(b)}
}

func (c rgb) hex() string {
	return fmt.Sprintf("#%02X%02X%02X", clamp8(c.r), clamp8(c.g), clamp8(c.b))
}

func clamp8(v float64) int {
	if v < 0 {
		v = 0
	}
	if v > 255 {
		v = 255
	}
	return int(v + 0.5)
}

func clamp01(t float64) float64 {
	if t < 0 {
		return 0
	}
	if t > 1 {
		return 1
	}
	return t
}

func mix(a, b rgb, t float64) rgb {
	t = clamp01(t)
	return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
}

func easeOut(t float64) float64 {
	t = clamp01(t)
	return 1 - math.Pow(1-t, 3)
}

func easeOutBack(t float64) float64 {
	const c1 = 1.70158
	const c3 = c1 + 1
	t = clamp01(t)
	return 1 + c3*math.Pow(t-1, 3) + c1*math.Pow(t-1, 2)
}

var gradientPalette = []rgb{plum, lavender, sky, mint, pink, plum}

func gradientAt(t float64) rgb {
	t = t - math.Floor(t)
	segments := len(gradientPalette) - 1
	scaled := t * float64(segments)
	index := int(scaled)
	if index >= segments {
		index = segments - 1
	}
	return mix(gradientPalette[index], gradientPalette[index+1], scaled-float64(index))
}

var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;?]*[a-zA-Z]")

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

func fgStyle(c rgb) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c.hex()))
}
