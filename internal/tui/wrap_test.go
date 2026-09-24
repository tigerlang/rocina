package tui

import (
	"strings"
	"testing"
)

// A raw provider error body has no spaces. It must be hard-wrapped so it cannot
// overflow the chat panel and push the agent sidebar off screen.
func TestWrapTextHardWrapsLongTokens(t *testing.T) {
	blob := strings.Repeat("x", 250)
	lines := wrapText("error: "+blob, 40)
	for _, line := range lines {
		if len([]rune(line)) > 40 {
			t.Fatalf("line exceeds width: %d %q", len([]rune(line)), line)
		}
	}
	if len(lines) < 2 {
		t.Fatalf("expected the blob to wrap, got %d line(s)", len(lines))
	}
}
