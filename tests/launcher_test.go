package tests

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"rocina/internal/config"
	"rocina/internal/session"
	"rocina/internal/tui"
)

func newLauncher(t *testing.T, seed bool) (tea.Model, *session.Manager) {
	t.Helper()
	cfg := config.Default()
	cfg.Model = "test-model"
	cfg.DataDir = t.TempDir()
	cfg.Workspace = t.TempDir()
	mgr, err := session.NewManager(context.Background(), cfg)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	if seed {
		store := `{"chief":[{"kind":"user","text":"previous","done":true}]}`
		path := filepath.Join(mgr.Active().DataDir, "messages.json")
		if err := os.WriteFile(path, []byte(store), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	model := tea.Model(tui.New(mgr))
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return model, mgr
}

func TestLauncherSubmitReusesPristineSession(t *testing.T) {
	model, mgr := newLauncher(t, false)
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("first goal")})
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(mgr.List()) != 1 {
		t.Fatalf("pristine launcher should reuse main, sessions=%d", len(mgr.List()))
	}
}

func TestLauncherSubmitStartsNewSession(t *testing.T) {
	model, mgr := newLauncher(t, true)
	before := mgr.Active().ID
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("new goal")})
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(mgr.List()) != 2 {
		t.Fatalf("submit over history should start a new session, sessions=%d", len(mgr.List()))
	}
	if mgr.Active().ID == before {
		t.Fatal("submit must not land in the previously loaded session")
	}
}
