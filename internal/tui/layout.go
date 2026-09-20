package tui

import "math"

type rect struct{ x, y, w, h int }

func (r rect) contains(px, py int) bool {
	return r.w > 0 && r.h > 0 && px >= r.x && px < r.x+r.w && py >= r.y && py < r.y+r.h
}

type layout struct {
	logoH  int
	aboveY int
	aboveH int
	inputY int
	inputH int
	belowH int

	chatX int
	chatY int
	chatW int
	chatH int

	sideX int
	sideW int
}

func (m Model) logoHeight() int {
	if m.width >= bigLogoWidth {
		return 8
	}
	return 3
}

func (m Model) computeLayout() layout {
	var l layout
	hintCount := len(m.hintLines())
	if hintCount < 1 {
		hintCount = 1
	}
	l.inputH = 5 + hintCount
	ease := 0.0
	if view := m.activeView(); view != nil {
		ease = clamp01(view.ease)
	}

	fullLogo := m.logoHeight()
	l.logoH = int(math.Round(float64(fullLogo) * (1 - ease)))
	if l.logoH < 0 {
		l.logoH = 0
	}
	usable := m.height - l.inputH
	if usable < 0 {
		usable = 0
	}
	l.aboveH = int(math.Round(float64(usable) * ease))
	if l.aboveH < 0 {
		l.aboveH = 0
	}
	l.aboveY = l.logoH
	l.inputY = l.logoH + l.aboveH
	l.belowH = m.height - l.inputY - l.inputH
	if l.belowH < 0 {
		l.belowH = 0
	}

	l.sideW = 0
	if m.width >= 80 {
		l.sideW = int(math.Round(34 * ease))
	}
	if l.sideW > m.width/2 {
		l.sideW = m.width / 2
	}
	l.sideX = m.width - l.sideW
	l.chatX = 0
	l.chatY = l.aboveY
	l.chatW = m.width - l.sideW
	l.chatH = l.aboveH
	return l
}

func (m Model) agentCardRects(l layout) []rect {
	view := m.activeView()
	if l.sideW == 0 || l.aboveH < 4 || view == nil {
		return nil
	}
	agents := view.session.Orch.Bus().List()
	rects := make([]rect, 0, len(agents))
	const cardHeight = 3
	startY := l.chatY + 2
	for i := range agents {
		y := startY + i*cardHeight
		if y+cardHeight > l.chatY+l.chatH {
			break
		}
		rects = append(rects, rect{x: l.sideX + 1, y: y, w: l.sideW - 2, h: cardHeight})
	}
	return rects
}
