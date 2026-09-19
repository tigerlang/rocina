package bus

import (
	"context"
	"fmt"
	"sync"
	"time"

	"rocina/internal/id"
	"rocina/internal/store"
)

type Kind string

const (
	KindChief Kind = "chief"
	KindSub   Kind = "sub"
)

type State string

const (
	StateIdle    State = "idle"
	StateRunning State = "running"
	StateWaiting State = "waiting"
	StateStopped State = "stopped"
	StateError   State = "error"
)

type Envelope struct {
	ID   string    `json:"id"`
	From string    `json:"from"`
	To   string    `json:"to"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

type Event struct {
	ID     string    `json:"id"`
	Type   string    `json:"type"`
	Agent  string    `json:"agent"`
	Text   string    `json:"text"`
	Tool   string    `json:"tool,omitempty"`
	Args   string    `json:"args,omitempty"`
	CallID string    `json:"call_id,omitempty"`
	At     time.Time `json:"at"`
}

type Agent struct {
	ID        string
	Name      string
	Kind      Kind
	Role      string
	Parent    string
	Model     string
	CreatedAt time.Time

	Inbox chan Envelope
	done  chan struct{}

	closeOnce sync.Once
	mu        sync.RWMutex
	state     State
	lastError string
}

func (a *Agent) State() State {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state
}

func (a *Agent) LastError() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastError
}

func (a *Agent) setState(s State, err string) {
	a.mu.Lock()
	a.state = s
	a.lastError = err
	a.mu.Unlock()
}

type Bus struct {
	mu     sync.RWMutex
	agents map[string]*Agent
	order  []string
	store  *store.Store
	events chan Event

	eventsMu sync.RWMutex
	closed   bool
}

func New(st *store.Store) *Bus {
	return &Bus{
		agents: map[string]*Agent{},
		store:  st,
		events: make(chan Event, 8192),
	}
}

func (b *Bus) Store() *store.Store { return b.store }

func (b *Bus) Register(name string, kind Kind, role, parent, model string) *Agent {
	a := &Agent{
		ID:        id.New("agent"),
		Name:      name,
		Kind:      kind,
		Role:      role,
		Parent:    parent,
		Model:     model,
		CreatedAt: time.Now(),
		Inbox:     make(chan Envelope, 64),
		done:      make(chan struct{}),
		state:     StateIdle,
	}
	b.mu.Lock()
	b.agents[a.ID] = a
	b.order = append(b.order, a.ID)
	b.mu.Unlock()
	b.Publish(Event{Type: "agent.added", Agent: a.Name, Text: string(kind)})
	return a
}

func (b *Bus) Get(ref string) *Agent {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if a, ok := b.agents[ref]; ok {
		return a
	}
	for _, a := range b.agents {
		if a.Name == ref {
			return a
		}
	}
	return nil
}

func (b *Bus) List() []*Agent {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]*Agent, 0, len(b.order))
	for _, agentID := range b.order {
		if a, ok := b.agents[agentID]; ok {
			out = append(out, a)
		}
	}
	return out
}

func (b *Bus) Chief() *Agent {
	for _, a := range b.List() {
		if a.Kind == KindChief {
			return a
		}
	}
	return nil
}

func (b *Bus) SetState(a *Agent, s State, err string) {
	if a == nil {
		return
	}
	a.setState(s, err)
	b.Publish(Event{Type: "agent.state", Agent: a.Name, Text: string(s)})
}

func (b *Bus) Send(env Envelope) error {
	a := b.Get(env.To)
	if a == nil {
		return fmt.Errorf("unknown agent %q", env.To)
	}
	if env.ID == "" {
		env.ID = id.New("msg")
	}
	if env.At.IsZero() {
		env.At = time.Now()
	}
	select {
	case a.Inbox <- env:
		b.Publish(Event{Type: "message", Agent: env.To, Text: env.Text})
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("agent %q inbox is full", env.To)
	case <-a.done:
		return fmt.Errorf("agent %q is stopped", env.To)
	}
}

func (b *Bus) Collect(agentID string) []Envelope {
	a := b.Get(agentID)
	if a == nil {
		return nil
	}
	var out []Envelope
	for {
		select {
		case env := <-a.Inbox:
			out = append(out, env)
		default:
			return out
		}
	}
}

func (b *Bus) Wait(ctx context.Context, agentID string) (Envelope, error) {
	a := b.Get(agentID)
	if a == nil {
		return Envelope{}, fmt.Errorf("unknown agent %q", agentID)
	}
	b.SetState(a, StateWaiting, "")
	select {
	case env := <-a.Inbox:
		b.SetState(a, StateRunning, "")
		return env, nil
	case <-a.done:
		return Envelope{}, fmt.Errorf("agent %q stopped", a.Name)
	case <-ctx.Done():
		b.SetState(a, StateIdle, "")
		return Envelope{}, ctx.Err()
	}
}

func (b *Bus) StopWait(agentID, task string) error {
	a := b.Get(agentID)
	if a == nil {
		return fmt.Errorf("unknown agent %q", agentID)
	}
	return b.Send(Envelope{From: "chief", To: a.Name, Kind: "task", Text: task})
}

func (b *Bus) IdleWait(ctx context.Context, agentID string, timeout time.Duration) (Envelope, bool) {
	a := b.Get(agentID)
	if a == nil {
		return Envelope{}, false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case env := <-a.Inbox:
		return env, true
	case <-timer.C:
		return Envelope{}, false
	case <-a.done:
		return Envelope{}, false
	case <-ctx.Done():
		return Envelope{}, false
	}
}

func (b *Bus) Broadcast(from, text string) int {
	count := 0
	for _, a := range b.List() {
		if a.Name == from {
			continue
		}
		if err := b.Send(Envelope{From: from, To: a.Name, Kind: "broadcast", Text: text}); err == nil {
			count++
		}
	}
	return count
}

func (b *Bus) Stop(a *Agent) {
	if a == nil {
		return
	}
	a.closeOnce.Do(func() { close(a.done) })
	b.SetState(a, StateStopped, "")
}

func (b *Bus) ActiveSubs() int {
	n := 0
	for _, a := range b.List() {
		if a.Kind == KindSub && a.State() != StateStopped {
			n++
		}
	}
	return n
}

func (b *Bus) Events() <-chan Event { return b.events }

func (b *Bus) Publish(e Event) {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	b.eventsMu.RLock()
	defer b.eventsMu.RUnlock()
	if b.closed {
		return
	}
	select {
	case b.events <- e:
	default:
	}
}

func (b *Bus) Close() {
	b.eventsMu.Lock()
	defer b.eventsMu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	close(b.events)
}
