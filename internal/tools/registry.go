package tools

import (
	"context"
	"encoding/json"
	"sync"

	"rocina/internal/bus"
	"rocina/internal/llm"
	"rocina/internal/store"
)

type SpawnSpec struct {
	Name  string
	Role  string
	Model string
	Task  string
}

type Hooks struct {
	Spawn      func(SpawnSpec) (string, error)
	Checkpoint func(agent, action, name string) (string, error)
}

type Env struct {
	Agent     *bus.Agent
	Bus       *bus.Bus
	Store     *store.Store
	Workspace string
	Hooks     Hooks
	Policy    Policy
	Approve   Approver

	mu        sync.Mutex
	terminals map[string]*Terminal
}

func (e *Env) terminal() (*Terminal, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.terminals == nil {
		e.terminals = map[string]*Terminal{}
	}
	if t, ok := e.terminals[e.Agent.ID]; ok {
		return t, nil
	}
	t, err := newTerminal()
	if err != nil {
		return nil, err
	}
	e.terminals[e.Agent.ID] = t
	return t, nil
}

type Tool struct {
	Def llm.ToolDef
	Run func(ctx context.Context, env *Env, args json.RawMessage) (string, error)
}

type Registry struct {
	defs  []llm.ToolDef
	tools map[string]*Tool
}

func NewRegistry(kind bus.Kind) *Registry {
	r := &Registry{tools: map[string]*Tool{}}
	registerBase(r)
	registerFS(r)
	registerShared(r)
	if kind == bus.KindChief {
		registerChief(r)
	} else {
		registerSub(r)
	}
	return r
}

func (r *Registry) add(t Tool) {
	r.tools[t.Def.Name] = &t
	r.defs = append(r.defs, t.Def)
}

func (r *Registry) Defs() []llm.ToolDef { return r.defs }

func (r *Registry) Get(name string) (*Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}
