package tests

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"rocina/internal/config"
	"rocina/internal/session"
	"rocina/internal/tui"
)

func newTUI(t *testing.T) tea.Model {
	t.Helper()
	cfg := config.Default()
	cfg.Model = "test-model"
	cfg.DataDir = t.TempDir()
	cfg.Workspace = t.TempDir()
	manager, err := session.NewManager(context.Background(), cfg)
	if err != nil {
		t.Fatalf("session manager: %v", err)
	}
	return tui.New(manager)
}

func TestTUIRendersWithinWidth(t *testing.T) {
	model := newTUI(t)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	view := updated.View()
	if !strings.Contains(view, "rocina") {
		t.Fatalf("logo tagline missing:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > 120 {
			t.Fatalf("line exceeds terminal width (%d): %q", width, line)
		}
	}
}

func TestTUIRendersNarrow(t *testing.T) {
	model := newTUI(t)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 50, Height: 24})
	view := updated.View()
	if strings.TrimSpace(view) == "" {
		t.Fatal("narrow view is empty")
	}
	for _, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > 50 {
			t.Fatalf("narrow line exceeds width (%d): %q", width, line)
		}
	}
}

func TestTUIModelPickerTabSwitchesTarget(t *testing.T) {
	model := newTUI(t)
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if view := model.(tui.Model).View(); !strings.Contains(view, "target: chief") {
		t.Fatalf("picker should start on chief:\n%s", view)
	}
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	if view := model.(tui.Model).View(); !strings.Contains(view, "target: subagents") {
		t.Fatalf("tab should switch to subagents:\n%s", view)
	}
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	if view := model.(tui.Model).View(); !strings.Contains(view, "target: chief") {
		t.Fatalf("tab should switch back to chief:\n%s", view)
	}
}

func TestTUIInputAcceptsTyping(t *testing.T) {
	model := newTUI(t)
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello chief")})
	if view := model.(tui.Model).View(); !strings.Contains(view, "hello chief") {
		t.Fatalf("typed text was not rendered:\n%s", view)
	}
}
