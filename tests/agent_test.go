package tests

import (
	"context"
	"testing"
	"time"

	"rocina/internal/agent"
	"rocina/internal/bus"
	"rocina/internal/llm"
	"rocina/internal/store"
	"rocina/internal/tools"
)

func newRuntime(a *bus.Agent, provider llm.Provider, b *bus.Bus, st *store.Store) *agent.Runtime {
	env := &tools.Env{Agent: a, Bus: b, Store: st}
	return &agent.Runtime{
		Agent:    a,
		Provider: provider,
		Model:    "test",
		System:   "system",
		MaxSteps: 5,
		Bus:      b,
		Store:    st,
		Registry: tools.NewRegistry(a.Kind),
		Env:      env,
	}
}

func TestSubagentReportsToChief(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	b := bus.New(st)
	b.Register("chief", bus.KindChief, "chief", "", "m")
	sub := b.Register("sub-1", bus.KindSub, "worker", "chief", "m")

	provider := newScripted(
		assistantCall("c1", "send_to_chief", map[string]string{"text": "payload"}),
		assistantText("finished"),
	)
	runtime := newRuntime(sub, provider, b, st)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.RunSub(ctx, "do work") }()

	waitForMessage(t, b, "chief", "payload")
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("subagent runtime did not stop")
	}
}

func TestChiefDelegatesToSubagent(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	b := bus.New(st)
	chief := b.Register("chief", bus.KindChief, "chief", "", "m")
	b.Register("sub-1", bus.KindSub, "worker", "chief", "m")

	provider := newScripted(
		assistantCall("c1", "send_to_sub", map[string]string{"agent": "sub-1", "task": "build"}),
		assistantText("delegated"),
	)
	runtime := newRuntime(chief, provider, b, st)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.RunChief(ctx, "ship it") }()

	waitForMessage(t, b, "sub-1", "build")
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("chief runtime did not stop")
	}
}

func TestRuntimeRecordsToolUseOnBoard(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	b := bus.New(st)
	chief := b.Register("chief", bus.KindChief, "chief", "", "m")
	provider := newScripted(
		assistantCall("c1", "public_todo", map[string]string{"title": "record me"}),
		assistantText("done"),
	)
	runtime := newRuntime(chief, provider, b, st)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := runtime.RunChief(ctx, "goal"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(st.PublicTodos()) != 1 {
		t.Fatalf("tool use did not update the shared board")
	}
}

func TestChiefRepliesWithoutTools(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	b := bus.New(st)
	chief := b.Register("chief", bus.KindChief, "chief", "", "m")
	provider := newScripted(assistantText("привет!"))
	runtime := newRuntime(chief, provider, b, st)

	if err := runtime.RunChief(context.Background(), "привет"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.requestCount() != 1 {
		t.Fatalf("expected a single model call, got %d", provider.requestCount())
	}
	if len(st.PublicTodos()) != 0 {
		t.Fatal("a greeting must not create todos")
	}
}

func waitForMessage(t *testing.T, b *bus.Bus, agentName, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, env := range b.Collect(b.Get(agentName).ID) {
			if env.Text == want {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("agent %s never received %q", agentName, want)
}
