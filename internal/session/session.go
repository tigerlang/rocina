package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"rocina/internal/config"
	"rocina/internal/id"
	"rocina/internal/orchestrator"
)

type Session struct {
	ID      string
	Name    string
	Orch    *orchestrator.Orchestrator
	Created time.Time
	DataDir string
	cancel  context.CancelFunc
}

type entry struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	DataDir string    `json:"data_dir"`
}

type index struct {
	Active   string  `json:"active"`
	Sessions []entry `json:"sessions"`
}

type Manager struct {
	base config.Config
	ctx  context.Context

	mu     sync.Mutex
	items  []*Session
	active int
}

func NewManager(ctx context.Context, cfg config.Config) (*Manager, error) {
	m := &Manager{base: cfg, ctx: ctx}
	saved := m.loadIndex()
	if len(saved.Sessions) == 0 {
		if _, err := m.NewSession("main"); err != nil {
			return nil, err
		}
		return m, nil
	}
	for _, e := range saved.Sessions {
		s, err := m.restore(e)
		if err != nil {
			return nil, err
		}
		m.items = append(m.items, s)
	}
	m.active = len(m.items) - 1
	if saved.Active != "" {
		m.SetActive(saved.Active)
	}
	return m, nil
}

func (m *Manager) indexPath() string {
	return filepath.Join(m.base.DataDir, "sessions.json")
}

func (m *Manager) loadIndex() index {
	data, err := os.ReadFile(m.indexPath())
	if err != nil {
		return index{}
	}
	var saved index
	if err := json.Unmarshal(data, &saved); err != nil {
		return index{}
	}
	return saved
}

func (m *Manager) persistLocked() {
	saved := index{}
	if m.active >= 0 && m.active < len(m.items) {
		saved.Active = m.items[m.active].ID
	}
	for _, s := range m.items {
		saved.Sessions = append(saved.Sessions, entry{ID: s.ID, Name: s.Name, Created: s.Created, DataDir: s.DataDir})
	}
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(m.base.DataDir, 0o755); err != nil {
		return
	}
	tmp := m.indexPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, m.indexPath())
}

func (m *Manager) restore(e entry) (*Session, error) {
	cfg := m.base
	cfg.DataDir = e.DataDir
	orch, err := orchestrator.New(cfg)
	if err != nil {
		return nil, err
	}
	sessionCtx, cancel := context.WithCancel(m.ctx)
	if err := orch.Start(sessionCtx); err != nil {
		cancel()
		return nil, err
	}
	return &Session{ID: e.ID, Name: e.Name, Orch: orch, Created: e.Created, DataDir: e.DataDir, cancel: cancel}, nil
}

func (m *Manager) NewSession(name string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sessionID := id.New("ses")
	cfg := m.base
	cfg.DataDir = filepath.Join(m.base.DataDir, "sessions", sessionID)
	orch, err := orchestrator.New(cfg)
	if err != nil {
		return nil, err
	}
	sessionCtx, cancel := context.WithCancel(m.ctx)
	if err := orch.Start(sessionCtx); err != nil {
		cancel()
		return nil, err
	}
	if name == "" {
		if len(m.items) == 0 {
			name = "main"
		} else {
			name = fmt.Sprintf("session %d", len(m.items)+1)
		}
	}
	s := &Session{ID: sessionID, Name: name, Orch: orch, Created: time.Now(), DataDir: cfg.DataDir, cancel: cancel}
	m.items = append(m.items, s)
	m.active = len(m.items) - 1
	m.persistLocked()
	return s, nil
}

func (m *Manager) List() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, len(m.items))
	copy(out, m.items)
	return out
}

func (m *Manager) Active() *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.items) == 0 {
		return nil
	}
	return m.items[m.active]
}

func (m *Manager) SetActive(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.items {
		if s.ID == sessionID {
			m.active = i
			m.persistLocked()
			return true
		}
	}
	return false
}

func (m *Manager) DeleteSession(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.items {
		if s.ID != sessionID {
			continue
		}
		if s.cancel != nil {
			s.cancel()
		}
		s.Orch.Bus().Close()
		if s.DataDir != "" {
			_ = os.RemoveAll(s.DataDir)
		}
		m.items = append(m.items[:i], m.items[i+1:]...)
		if m.active >= len(m.items) {
			m.active = len(m.items) - 1
		}
		if m.active < 0 {
			m.active = 0
		}
		m.persistLocked()
		return true
	}
	return false
}

func (m *Manager) Config() config.Config { return m.base }

func (m *Manager) ApplyConfig(cfg config.Config) {
	m.mu.Lock()
	cfg.DataDir = m.base.DataDir
	m.base = cfg
	items := make([]*Session, len(m.items))
	copy(items, m.items)
	m.mu.Unlock()
	for _, s := range items {
		s.Orch.SetConfig(cfg)
	}
}

func (m *Manager) ApplySecurity(sec config.Security) {
	m.mu.Lock()
	m.base.Security = sec
	items := make([]*Session, len(m.items))
	copy(items, m.items)
	m.mu.Unlock()
	for _, s := range items {
		s.Orch.SetSecurity(sec)
	}
}
