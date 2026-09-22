package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"rocina/internal/agent"
	"rocina/internal/bus"
	"rocina/internal/config"
	"rocina/internal/llm"
	"rocina/internal/store"
	"rocina/internal/tools"
)

type Orchestrator struct {
	cfg      config.Config
	provider llm.Provider
	bus      *bus.Bus
	store    *store.Store

	mu       sync.Mutex
	runtimes map[string]*agent.Runtime
	cancels  map[string]context.CancelFunc
	wg       sync.WaitGroup
	ctx      context.Context

	secMu    sync.RWMutex
	security config.Security
	approver tools.Approver
}

func New(cfg config.Config) (*Orchestrator, error) {
	provider, err := buildProvider(cfg)
	if err != nil {
		return nil, err
	}
	return newWith(cfg, provider)
}

// NewWithProvider builds an orchestrator around a supplied provider, used by
// tests and embedding to bypass credential checks.
func NewWithProvider(cfg config.Config, provider llm.Provider) (*Orchestrator, error) {
	return newWith(cfg, provider)
}

func newWith(cfg config.Config, provider llm.Provider) (*Orchestrator, error) {
	if cfg.Model == "" && (cfg.ChiefModel == "" || cfg.SubModel == "") {
		return nil, fmt.Errorf("model is not configured: set -model, or chief_model and sub_model in the config")
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	return &Orchestrator{
		cfg:      cfg,
		provider: provider,
		bus:      bus.New(st),
		store:    st,
		runtimes: map[string]*agent.Runtime{},
		cancels:  map[string]context.CancelFunc{},
		security: cfg.Security,
	}, nil
}

func buildProvider(cfg config.Config) (llm.Provider, error) {
	switch cfg.Kind() {
	case config.KindClaude:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("an api key is required for the claude provider")
		}
		return llm.NewAnthropic(cfg.APIKey, cfg.BaseURL), nil
	case config.KindOpenRouter:
		return llm.NewOpenRouter(cfg.APIKey, nil), nil
	case config.KindOther:
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("a base url is required for the other provider")
		}
		return llm.NewOpenAI("other", cfg.BaseURL, cfg.APIKey, nil), nil
	default:
		return llm.NewOpenAI("openai", cfg.BaseURL, cfg.APIKey, nil), nil
	}
}

func (o *Orchestrator) Bus() *bus.Bus       { return o.bus }
func (o *Orchestrator) Store() *store.Store { return o.store }
func (o *Orchestrator) Config() config.Config {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.cfg
}
func (o *Orchestrator) Provider() llm.Provider { return o.provider }

func (o *Orchestrator) SetChiefModel(model string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cfg.Model = model
	o.cfg.ChiefModel = ""
	for _, runtime := range o.runtimes {
		if runtime.Agent.Kind == bus.KindChief {
			runtime.Model = model
			runtime.Agent.Model = model
		}
	}
}

func (o *Orchestrator) SetSubModel(model string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cfg.SubModel = model
	for _, runtime := range o.runtimes {
		if runtime.Agent.Kind == bus.KindSub {
			runtime.Model = model
			runtime.Agent.Model = model
		}
	}
}

func (o *Orchestrator) SetConfig(cfg config.Config) {
	o.mu.Lock()
	cfg.DataDir = o.cfg.DataDir
	o.cfg = cfg
	if provider, err := buildProvider(cfg); err == nil {
		o.provider = provider
	}
	for _, runtime := range o.runtimes {
		runtime.Provider = o.provider
		runtime.MaxSteps = o.cfg.MaxSteps
		runtime.Temperature = o.cfg.Temperature
		runtime.Env.Workspace = o.cfg.Workspace
		if runtime.Agent.Kind == bus.KindChief {
			runtime.Model = o.chiefModel()
		} else {
			runtime.Model = o.subModel()
		}
	}
	o.mu.Unlock()
	o.SetSecurity(cfg.Security)
}

type Stats struct {
	ContextTokens int
	TotalTokens   int
	Requests      int
	Cost          float64
	CostKnown     bool
	RPM           float64
}

func (o *Orchestrator) Stats() Stats {
	o.mu.Lock()
	runtimes := make([]*agent.Runtime, 0, len(o.runtimes))
	for _, runtime := range o.runtimes {
		runtimes = append(runtimes, runtime)
	}
	o.mu.Unlock()
	var out Stats
	for _, runtime := range runtimes {
		usage := runtime.Usage()
		out.ContextTokens += usage.ContextTokens
		out.TotalTokens += usage.TotalTokens
		out.Requests += usage.Requests
		out.Cost += usage.Cost
		out.CostKnown = out.CostKnown || usage.CostKnown
		out.RPM += usage.RPM
	}
	return out
}

func (o *Orchestrator) policy() tools.Policy {
	o.secMu.RLock()
	defer o.secMu.RUnlock()
	return tools.Policy{
		BlockUserPaths: o.security.BlockUserPaths,
		AskBeforeRun:   o.security.AskBeforeRun,
		AllowedRoots:   o.security.AllowedRoots,
		Workspace:      o.cfg.Workspace,
	}
}

func (o *Orchestrator) currentApprover() tools.Approver {
	o.secMu.RLock()
	defer o.secMu.RUnlock()
	return o.approver
}

func (o *Orchestrator) SetSecurity(sec config.Security) {
	o.secMu.Lock()
	o.security = sec
	o.secMu.Unlock()
	policy := o.policy()
	o.mu.Lock()
	for _, runtime := range o.runtimes {
		runtime.Env.Policy = policy
	}
	o.mu.Unlock()
}

func (o *Orchestrator) SetApprover(approve tools.Approver) {
	o.secMu.Lock()
	o.approver = approve
	o.secMu.Unlock()
	o.mu.Lock()
	for _, runtime := range o.runtimes {
		runtime.Env.Approve = approve
	}
	o.mu.Unlock()
}

func (o *Orchestrator) Start(ctx context.Context) error {
	o.ctx = ctx
	chief := o.bus.Register("chief", bus.KindChief, "chief", "", o.chiefModel())
	runtime := o.newRuntime(chief, chiefPrompt(), o.chiefModel())
	runtime.LoadHistory()
	o.mu.Lock()
	o.runtimes[chief.ID] = runtime
	o.mu.Unlock()
	if o.cfg.Goal == "" {
		o.bus.SetState(chief, bus.StateIdle, "")
		return nil
	}
	o.launch(chief, func(runCtx context.Context) error { return runtime.RunChief(runCtx, o.cfg.Goal) })
	return nil
}

func (o *Orchestrator) Submit(text string) error {
	chief := o.bus.Chief()
	if chief == nil {
		return fmt.Errorf("no chief agent is registered")
	}
	return o.SubmitTo(chief.Name, text)
}

// SubmitTo routes a user message to a specific agent. A resting chief is
// restarted with the text as its goal, everything else receives an envelope.
func (o *Orchestrator) SubmitTo(target, text string) error {
	a := o.bus.Get(target)
	if a == nil {
		return fmt.Errorf("unknown agent %q", target)
	}
	if a.Kind == bus.KindChief {
		o.mu.Lock()
		runtime := o.runtimes[a.ID]
		o.mu.Unlock()
		if runtime == nil {
			return fmt.Errorf("runtime is missing for %q", target)
		}
		switch a.State() {
		case bus.StateIdle, bus.StateError:
			o.store.AddTask("user", a.Name, "goal", text)
			o.launch(a, func(runCtx context.Context) error { return runtime.RunChief(runCtx, text) })
			return nil
		}
	}
	return o.bus.Send(bus.Envelope{From: "user", To: a.Name, Kind: "message", Text: text})
}

func (o *Orchestrator) launch(a *bus.Agent, run func(context.Context) error) {
	o.mu.Lock()
	parent := o.ctx
	o.mu.Unlock()
	if parent == nil {
		parent = context.Background()
	}
	runCtx, cancel := context.WithCancel(parent)
	gen := a.BumpRun()
	o.mu.Lock()
	o.cancels[a.ID] = cancel
	o.mu.Unlock()
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		defer func() {
			if a.RunGen() != gen {
				return
			}
			o.mu.Lock()
			delete(o.cancels, a.ID)
			o.mu.Unlock()
		}()
		err := run(runCtx)
		if a.RunGen() != gen {
			return
		}
		if runCtx.Err() != nil {
			o.bus.SetState(a, bus.StateStopped, "")
			return
		}
		if err != nil {
			o.bus.SetState(a, bus.StateError, err.Error())
		}
	}()
}

func (o *Orchestrator) Wait() { o.wg.Wait() }

func (o *Orchestrator) StopAgent(name string) bool {
	a := o.bus.Get(name)
	if a == nil {
		return false
	}
	o.mu.Lock()
	cancel := o.cancels[a.ID]
	o.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	o.bus.Stop(a)
	return true
}

func (o *Orchestrator) StopSubs() int {
	count := 0
	for _, a := range o.bus.List() {
		if a.Kind == bus.KindSub && a.State() != bus.StateStopped && o.StopAgent(a.Name) {
			count++
		}
	}
	return count
}

func (o *Orchestrator) StopAll() int {
	count := 0
	for _, a := range o.bus.List() {
		if a.State() != bus.StateStopped && o.StopAgent(a.Name) {
			count++
		}
	}
	return count
}

func (o *Orchestrator) WorkingAgents() []*bus.Agent {
	var out []*bus.Agent
	for _, a := range o.bus.List() {
		if a.State() == bus.StateRunning || a.State() == bus.StateWaiting {
			out = append(out, a)
		}
	}
	return out
}

// WakeAgent revives a stuck or failed agent and restarts its run loop. It is
// the escape hatch for error states and for hangs such as an endless thinking
// loop or a terminal command that never returns.
func (o *Orchestrator) WakeAgent(name, task string) (string, error) {
	a := o.bus.Get(name)
	if a == nil {
		return "", fmt.Errorf("unknown agent %q", name)
	}
	o.mu.Lock()
	runtime := o.runtimes[a.ID]
	o.mu.Unlock()
	if runtime == nil {
		return "", fmt.Errorf("runtime is missing for %q", name)
	}
	if a.State() == bus.StateRunning || a.State() == bus.StateWaiting {
		return "", fmt.Errorf("%s is already active", a.Name)
	}
	// Cancel any leftover run and detach it before reviving.
	a.BumpRun()
	o.mu.Lock()
	cancel := o.cancels[a.ID]
	o.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	o.bus.Revive(a)
	if task == "" {
		task = "Resume your task. If you hit an error, review the last tool result and continue."
	}
	if a.Kind == bus.KindChief {
		o.launch(a, func(runCtx context.Context) error { return runtime.RunChief(runCtx, task) })
	} else {
		o.launch(a, func(runCtx context.Context) error { return runtime.RunSub(runCtx, task) })
	}
	return a.Name, nil
}

func (o *Orchestrator) WakeSubs(task string) []string {
	var woken []string
	for _, a := range o.bus.List() {
		if a.Kind != bus.KindSub {
			continue
		}
		if a.State() == bus.StateRunning || a.State() == bus.StateWaiting {
			continue
		}
		if _, err := o.WakeAgent(a.Name, task); err == nil {
			woken = append(woken, a.Name)
		}
	}
	return woken
}

func (o *Orchestrator) newRuntime(a *bus.Agent, system, model string) *agent.Runtime {
	env := &tools.Env{
		Agent:     a,
		Bus:       o.bus,
		Store:     o.store,
		Workspace: o.cfg.Workspace,
		Policy:    o.policy(),
		Approve:   o.currentApprover(),
		Hooks: tools.Hooks{
			Spawn:      o.spawnSub,
			Checkpoint: o.checkpoint,
			Wake:       o.WakeAgent,
			WakeAll:    o.wakeAll,
		},
	}
	return &agent.Runtime{
		Agent:       a,
		Provider:    o.provider,
		Model:       model,
		System:      system,
		MaxSteps:    o.cfg.MaxSteps,
		Temperature: o.cfg.Temperature,
		Bus:         o.bus,
		Store:       o.store,
		Registry:    tools.NewRegistry(a.Kind),
		Env:         env,
		HistoryPath: filepath.Join(o.cfg.DataDir, "agents", a.Name+".json"),
	}
}

func (o *Orchestrator) Spawn(spec tools.SpawnSpec) (string, error) {
	return o.spawnSub(spec)
}

func (o *Orchestrator) spawnSub(spec tools.SpawnSpec) (string, error) {
	o.mu.Lock()
	count := 0
	for _, rt := range o.runtimes {
		if rt.Agent.Kind == bus.KindSub {
			count++
		}
	}
	if count >= o.cfg.MaxSubagents {
		o.mu.Unlock()
		return "", fmt.Errorf("subagent limit reached (%d)", o.cfg.MaxSubagents)
	}
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		name = fmt.Sprintf("sub-%d", count+1)
	}
	if o.bus.Get(name) != nil {
		o.mu.Unlock()
		return "", fmt.Errorf("agent %q already exists", name)
	}
	model := spec.Model
	if model == "" {
		model = o.subModel()
	}
	sub := o.bus.Register(name, bus.KindSub, spec.Role, "chief", model)
	runtime := o.newRuntime(sub, subPrompt(spec.Role), model)
	o.runtimes[sub.ID] = runtime
	o.mu.Unlock()

	o.store.AddTask("chief", name, "spawn", spec.Role)
	o.launch(sub, func(runCtx context.Context) error { return runtime.RunSub(runCtx, spec.Task) })
	return sub.Name, nil
}

func (o *Orchestrator) runtimeByName(name string) *agent.Runtime {
	a := o.bus.Get(name)
	if a == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.runtimes[a.ID]
}

func (o *Orchestrator) wakeAll(task string) ([]string, error) {
	return o.WakeSubs(task), nil
}

func (o *Orchestrator) checkpoint(agentName, action, name string) (string, error) {
	runtime := o.runtimeByName(agentName)
	if runtime == nil {
		return "", fmt.Errorf("unknown agent %q", agentName)
	}
	dir := filepath.Join(o.cfg.DataDir, "checkpoints")
	switch action {
	case "save":
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		if name == "" {
			name = fmt.Sprintf("%s-%d", agentName, time.Now().Unix())
		}
		path := filepath.Join(dir, name+".json")
		data, err := json.MarshalIndent(runtime.History(), "", "  ")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return "", err
		}
		return "checkpoint saved: " + path, nil
	case "load":
		if name == "" {
			return "", fmt.Errorf("name is required to load a checkpoint")
		}
		path := filepath.Join(dir, name+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		var messages []llm.Message
		if err := json.Unmarshal(data, &messages); err != nil {
			return "", err
		}
		runtime.Restore(messages)
		return fmt.Sprintf("restored %d messages from %s", len(messages), path), nil
	case "list":
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return "no checkpoints", nil
			}
			return "", err
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				names = append(names, strings.TrimSuffix(e.Name(), ".json"))
			}
		}
		sort.Strings(names)
		if len(names) == 0 {
			return "no checkpoints", nil
		}
		return strings.Join(names, "\n"), nil
	default:
		return "", fmt.Errorf("unknown checkpoint action %q", action)
	}
}

func (o *Orchestrator) chiefModel() string {
	if o.cfg.ChiefModel != "" {
		return o.cfg.ChiefModel
	}
	return o.cfg.Model
}

func (o *Orchestrator) subModel() string {
	if o.cfg.SubModel != "" {
		return o.cfg.SubModel
	}
	return o.cfg.Model
}

func chiefPrompt() string {
	return strings.TrimSpace(`
You are the chief agent of Rocina, coordinating subagents that do real work.

Decide what the message needs before acting:
- If it is a greeting, a question, or small talk that needs no repository or tool action, reply directly in plain text and call no tools at all. Do not spawn subagents, create todos or call anything for such messages.
- Only plan, create todos and delegate when the message asks for concrete work.

When work is required:
- Create workers with spawn_subagent, assign work with send_to_sub and release a waiting worker with stop_wait.
- Subagent reports arrive in your context as "[name -> chief] ...". Read them before deciding the next move.
- Keep intent on the shared board with public_todo and update_public_todo; every tool call refreshes the board for all agents.
- Use private_todo for your own scratch plan and think for short reasoning with no side effects.
- Use shared_memory for facts the whole team must agree on, list_agents to inspect state, broadcast to reach everyone.
- You can see every agent's state via list_agents. If a subagent is stuck in error, loops forever or a terminal command never returns, call wake_up for that agent or wake_up_all to revive every failed subagent. Waking restarts its loop and optionally hands it a fresh instruction.
- Prefer delegating concrete coding work to subagents; do not leave a subagent blocked when you can unblock it.
- Finish with a concise summary when every public todo is done.

A plain text answer is a valid and preferred outcome when no action is needed.
`)
}

func subPrompt(role string) string {
	base := strings.TrimSpace(`
You are a Rocina subagent. Do the assigned work with the real tools available to you.
Operating rules:
- bash_tool, terminal_tool, web_tool and the file tools perform actual work.
- Report results, questions and blockers with send_to_chief.
- wait_for_chief blocks your turn until the chief sends data; while waiting you cannot write, only receive.
- Track your own steps with private_todo, and shared plans with public_todo.
- Use think for short reasoning with no side effects and shared_memory for durable facts the team needs.`)
	if strings.TrimSpace(role) == "" {
		return base
	}
	return base + "\n\nYour specialisation: " + role
}
