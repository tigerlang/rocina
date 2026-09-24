package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"rocina/internal/id"
)

type Status string

const (
	Pending Status = "pending"
	Active  Status = "in_progress"
	Done    Status = "done"
	Blocked Status = "blocked"
)

func validStatus(s Status) bool {
	switch s {
	case Pending, Active, Done, Blocked:
		return true
	default:
		return false
	}
}

type Todo struct {
	ID        string    `json:"id"`
	Public    bool      `json:"public"`
	Owner     string    `json:"owner"`
	Title     string    `json:"title"`
	Detail    string    `json:"detail"`
	Status    Status    `json:"status"`
	Priority  int       `json:"priority"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Revision  int64     `json:"revision"`
}

type Journal struct {
	ID       string    `json:"id"`
	Agent    string    `json:"agent"`
	Tool     string    `json:"tool"`
	Summary  string    `json:"summary"`
	At       time.Time `json:"at"`
	Revision int64     `json:"revision"`
}

type Task struct {
	ID     string    `json:"id"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	Kind   string    `json:"kind"`
	Text   string    `json:"text"`
	Status string    `json:"status"`
	At     time.Time `json:"at"`
}

type Snapshot struct {
	Agent    string            `json:"agent"`
	Public   []Todo            `json:"public"`
	Private  []Todo            `json:"private"`
	Inbox    []Task            `json:"inbox"`
	Outbox   []Task            `json:"outbox"`
	Journal  []Journal         `json:"journal"`
	Shared   map[string]string `json:"shared"`
	Revision int64             `json:"revision"`
}

type TodoPatch struct {
	Title    *string
	Detail   *string
	Status   *Status
	Priority *int
}

type Store struct {
	mu       sync.RWMutex
	path     string
	todos    map[string]*Todo
	tasks    map[string]*Task
	journal  []Journal
	shared   map[string]string
	revision int64
}

func New(dataDir string) *Store {
	return &Store{
		path:   filepath.Join(dataDir, "store.json"),
		todos:  map[string]*Todo{},
		tasks:  map[string]*Task{},
		shared: map[string]string{},
	}
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	s := New(dataDir)
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

type persisted struct {
	Todos    []*Todo           `json:"todos"`
	Tasks    []*Task           `json:"tasks"`
	Journal  []Journal         `json:"journal"`
	Shared   map[string]string `json:"shared"`
	Revision int64             `json:"revision"`
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}
	var p persisted
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	for _, t := range p.Todos {
		s.todos[t.ID] = t
	}
	for _, t := range p.Tasks {
		s.tasks[t.ID] = t
	}
	s.journal = p.Journal
	if p.Shared != nil {
		s.shared = p.Shared
	}
	s.revision = p.Revision
	return nil
}

func (s *Store) saveLocked() error {
	list := make([]*Todo, 0, len(s.todos))
	for _, t := range s.todos {
		list = append(list, t)
	}
	tasks := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, t)
	}
	data, err := json.MarshalIndent(persisted{
		Todos:    list,
		Tasks:    tasks,
		Journal:  s.journal,
		Shared:   s.shared,
		Revision: s.revision,
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) appendJournalLocked(agent, tool, summary string) Journal {
	j := Journal{ID: id.New("jrn"), Agent: agent, Tool: tool, Summary: summary, At: time.Now(), Revision: s.revision}
	s.journal = append(s.journal, j)
	if len(s.journal) > 2000 {
		s.journal = s.journal[len(s.journal)-2000:]
	}
	return j
}

func (s *Store) Revision() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revision
}

func (s *Store) CreateTodo(public bool, owner, title, detail, by string, priority int) (*Todo, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision++
	now := time.Now()
	t := &Todo{
		ID:        id.New("todo"),
		Public:    public,
		Owner:     owner,
		Title:     title,
		Detail:    detail,
		Status:    Pending,
		Priority:  priority,
		CreatedBy: by,
		CreatedAt: now,
		UpdatedAt: now,
		Revision:  s.revision,
	}
	s.todos[t.ID] = t
	s.appendJournalLocked(by, "todo.create", t.ID+": "+title)
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	cp := *t
	return &cp, nil
}

func (s *Store) UpdateTodo(by, todoID string, patch TodoPatch) (*Todo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.todos[todoID]
	if !ok {
		return nil, fmt.Errorf("todo %q not found", todoID)
	}
	if patch.Title != nil {
		if v := strings.TrimSpace(*patch.Title); v != "" {
			t.Title = v
		}
	}
	if patch.Detail != nil {
		t.Detail = *patch.Detail
	}
	if patch.Status != nil {
		if !validStatus(*patch.Status) {
			return nil, fmt.Errorf("invalid status %q", *patch.Status)
		}
		t.Status = *patch.Status
	}
	if patch.Priority != nil {
		t.Priority = *patch.Priority
	}
	s.revision++
	t.Revision = s.revision
	t.UpdatedAt = time.Now()
	s.appendJournalLocked(by, "todo.update", t.ID+" -> "+string(t.Status))
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	cp := *t
	return &cp, nil
}

func (s *Store) GetTodo(todoID string) (*Todo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.todos[todoID]
	if !ok {
		return nil, false
	}
	cp := *t
	return &cp, true
}

func (s *Store) PublicTodos() []Todo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Todo
	for _, t := range s.todos {
		if t.Public {
			out = append(out, *t)
		}
	}
	sortTodos(out)
	return out
}

func (s *Store) PrivateTodos(owner string) []Todo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Todo
	for _, t := range s.todos {
		if !t.Public && t.Owner == owner {
			out = append(out, *t)
		}
	}
	sortTodos(out)
	return out
}

func (s *Store) AddTask(from, to, kind, text string) Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision++
	t := Task{ID: id.New("task"), From: from, To: to, Kind: kind, Text: text, Status: "queued", At: time.Now()}
	s.tasks[t.ID] = &t
	s.appendJournalLocked(from, "task."+kind, from+" -> "+to)
	_ = s.saveLocked()
	return t
}

func (s *Store) RecordTool(agent, tool, summary string) (Journal, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision++
	j := s.appendJournalLocked(agent, tool, summary)
	_ = s.saveLocked()
	return j, s.revision
}

func (s *Store) SharedSet(key, value, by string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision++
	s.shared[key] = value
	s.appendJournalLocked(by, "shared.set", key)
	_ = s.saveLocked()
}

func (s *Store) SharedDelete(key, by string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision++
	delete(s.shared, key)
	s.appendJournalLocked(by, "shared.delete", key)
	_ = s.saveLocked()
}

func (s *Store) SharedGet(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.shared[key]
	return v, ok
}

func (s *Store) SharedAll() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.shared))
	for k, v := range s.shared {
		out[k] = v
	}
	return out
}

func (s *Store) Snapshot(agent string) Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := Snapshot{Agent: agent, Revision: s.revision, Shared: map[string]string{}}
	for _, t := range s.todos {
		switch {
		case t.Public:
			snap.Public = append(snap.Public, *t)
		case t.Owner == agent:
			snap.Private = append(snap.Private, *t)
		}
	}
	for _, t := range s.tasks {
		if t.To == agent {
			snap.Inbox = append(snap.Inbox, *t)
		}
		if t.From == agent {
			snap.Outbox = append(snap.Outbox, *t)
		}
	}
	for _, j := range s.journal {
		if j.Agent == agent {
			snap.Journal = append(snap.Journal, j)
		}
	}
	for k, v := range s.shared {
		snap.Shared[k] = v
	}
	sortTodos(snap.Public)
	sortTodos(snap.Private)
	return snap
}

// The board is appended to every agent's system prompt on every request, so its
// caps decide a fixed per-request cost. Keep it small enough to stay a summary.
const (
	boardTodoLimit   = 12
	boardSharedLimit = 12
	boardDetailLimit = 80
	boardValueLimit  = 100
)

func (s *Store) Board(agent string) string {
	snap := s.Snapshot(agent)
	var b strings.Builder
	fmt.Fprintf(&b, "COORDINATION BOARD (revision %d)\n", snap.Revision)
	writeTodos := func(title string, list []Todo) {
		open := list[:0:0]
		for _, t := range list {
			if t.Status != Done {
				open = append(open, t)
			}
		}
		if len(open) == 0 {
			return
		}
		b.WriteString(title + ":\n")
		shown := open
		hidden := 0
		if len(shown) > boardTodoLimit {
			hidden = len(shown) - boardTodoLimit
			shown = shown[:boardTodoLimit]
		}
		for _, t := range shown {
			fmt.Fprintf(&b, "- [%s] %s (id=%s, prio=%d)", t.Status, t.Title, t.ID, t.Priority)
			if t.Detail != "" {
				b.WriteString(" :: " + truncate(t.Detail, boardDetailLimit))
			}
			b.WriteString("\n")
		}
		if hidden > 0 {
			fmt.Fprintf(&b, "- (+%d more open todos)\n", hidden)
		}
	}
	writeTodos("public todos", snap.Public)
	writeTodos("private todos", snap.Private)
	if len(snap.Shared) > 0 {
		b.WriteString("shared memory:\n")
		keys := make([]string, 0, len(snap.Shared))
		for k := range snap.Shared {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		shown := keys
		if len(shown) > boardSharedLimit {
			shown = shown[:boardSharedLimit]
		}
		for _, k := range shown {
			fmt.Fprintf(&b, "- %s = %s\n", k, truncate(snap.Shared[k], boardValueLimit))
		}
		if hidden := len(keys) - len(shown); hidden > 0 {
			fmt.Fprintf(&b, "- (+%d more keys)\n", hidden)
		}
	}
	return b.String()
}

func sortTodos(list []Todo) {
	rank := map[Status]int{Pending: 0, Active: 1, Blocked: 2, Done: 3}
	sort.SliceStable(list, func(i, j int) bool {
		if rank[list[i].Status] != rank[list[j].Status] {
			return rank[list[i].Status] < rank[list[j].Status]
		}
		if list[i].Priority != list[j].Priority {
			return list[i].Priority > list[j].Priority
		}
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
