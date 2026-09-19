package session

import (
	"context"
	"fmt"
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
	cancel  context.CancelFunc
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
	if _, err := m.NewSession("main"); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) NewSession(name string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sessionID := id.New("ses")
	cfg := m.base
	if len(m.items) == 0 {
		sessionID = "main"
	} else {
		cfg.DataDir = filepath.Join(m.base.DataDir, "sessions", sessionID)
	}
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
	s := &Session{ID: sessionID, Name: name, Orch: orch, Created: time.Now(), cancel: cancel}
	m.items = append(m.items, s)
	m.active = len(m.items) - 1
	return s, nil
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
		m.items = append(m.items[:i], m.items[i+1:]...)
		if m.active >= len(m.items) {
			m.active = len(m.items) - 1
		}
		if m.active < 0 {
			m.active = 0
		}
		return true
	}
	return false
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
			return true
		}
	}
	return false
}

func (m *Manager) Config() config.Config { return m.base }

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
