package tui

import (
	"encoding/json"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"rocina/internal/config"
)

var securityItems = []string{
	"Block user paths (Linux, Windows, macOS)",
	"Always ask before running a terminal/file command",
}

var hotkeyLines = []string{
	"enter                send",
	"shift+enter/ctrl+j   newline",
	"tab / shift+tab      switch agent",
	"ctrl+n               new session",
	"ctrl+l               sessions (d delete)",
	"ctrl+o               settings",
	"ctrl+a               pick a model",
	"wheel                scroll chat",
	"click thinking       expand reasoning",
	"click agent card     focus agent",
	"ctrl+c               quit",
}

type settingsState struct {
	tab    int
	cursor int
	editor textarea.Model
	status string
}

func newSettings(cfg config.Config) settingsState {
	editor := textarea.New()
	editor.ShowLineNumbers = false
	editor.CharLimit = 20000
	editor.SetHeight(12)
	editor.SetPromptFunc(0, func(int) string { return "" })
	editor.FocusedStyle.Base = lipgloss.NewStyle()
	editor.FocusedStyle.CursorLine = lipgloss.NewStyle()
	editor.FocusedStyle.Text = textStyle
	editor.FocusedStyle.Placeholder = faintStyle
	editor.BlurredStyle = editor.FocusedStyle
	editor.Focus()
	s := settingsState{editor: editor}
	s.load(cfg)
	return s
}

func (s *settingsState) load(cfg config.Config) {
	data, _ := json.MarshalIndent(cfg, "", "  ")
	s.editor.SetValue(string(data))
	s.status = ""
}

func (s *settingsState) resize(width int) {
	s.editor.SetWidth(maxInt(20, width-20))
}

func (m *Model) toggleSecurity(index int) {
	cfg := m.mgr.Config()
	switch index {
	case 0:
		cfg.Security.BlockUserPaths = !cfg.Security.BlockUserPaths
	case 1:
		cfg.Security.AskBeforeRun = !cfg.Security.AskBeforeRun
	}
	m.mgr.ApplySecurity(cfg.Security)
	if err := config.Save(config.UserConfigPath(), cfg); err != nil {
		m.settings.status = "saved in memory; " + err.Error()
		return
	}
	m.settings.status = "saved to " + config.UserConfigPath()
}

func (m *Model) saveConfigEditor() {
	var cfg config.Config
	if err := json.Unmarshal([]byte(m.settings.editor.Value()), &cfg); err != nil {
		m.settings.status = "invalid json: " + err.Error()
		return
	}
	if err := config.Save(config.UserConfigPath(), cfg); err != nil {
		m.settings.status = err.Error()
		return
	}
	m.mgr.ApplyConfig(cfg)
	m.settings.status = "saved and applied: " + config.UserConfigPath()
}

func (s *settingsState) handleKey(msg tea.KeyMsg, m *Model) (bool, tea.Cmd) {
	switch msg.String() {
	case "ctrl+o", "esc":
		m.overlay = overlayNone
		return true, nil
	case "tab", "shift+tab":
		s.tab = (s.tab + 1) % 3
		return true, nil
	}
	if s.tab == 0 {
		switch msg.String() {
		case "up", "k":
			if s.cursor > 0 {
				s.cursor--
			}
		case "down", "j":
			if s.cursor < len(securityItems)-1 {
				s.cursor++
			}
		case " ", "enter":
			m.toggleSecurity(s.cursor)
		}
		return true, nil
	}
	if s.tab == 1 {
		if msg.String() == "ctrl+s" {
			m.saveConfigEditor()
			return true, nil
		}
		updated, cmd := s.editor.Update(msg)
		s.editor = updated
		return true, cmd
	}
	return true, nil
}

func (s *settingsState) handleMouse(msg tea.MouseMsg, m *Model) tea.Cmd {
	geom := m.overlayGeom(s.settingsRows())
	row := msg.Y - (geom.y + 3)
	if row < 0 {
		return nil
	}
	if s.tab == 0 {
		if row < len(securityItems) {
			s.cursor = row
			m.toggleSecurity(row)
		}
		return nil
	}
	if s.tab == 1 {
		updated, cmd := s.editor.Update(msg)
		s.editor = updated
		return cmd
	}
	return nil
}

func (s *settingsState) settingsRows() int {
	switch s.tab {
	case 0:
		return len(securityItems) + 4
	case 1:
		return s.editor.Height() + 5
	default:
		return len(hotkeyLines) + 4
	}
}
