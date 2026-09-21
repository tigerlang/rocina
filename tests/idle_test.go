package tests

import (
	"context"
	"testing"
	"time"

	"rocina/internal/agent"
	"rocina/internal/bus"
	"rocina/internal/store"
	"rocina/internal/tools"
)

// While the chief waits on subagents it must not spend a model request per timeout.
func TestChiefIdleWaitDoesNotSpendRequests(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	b := bus.New(st)
	chief := b.Register("chief", bus.KindChief, "chief", "", "m")
	sub := b.Register("sub-1", bus.KindSub, "worker", "chief", "m")
	b.SetState(sub, bus.StateRunning, "")

	provider := newScripted(assistantText("idle"))
	env := &tools.Env{Agent: chief, Bus: b, Store: st}
	runtime := &agent.Runtime{
		Agent: chief, Provider: provider, Model: "m", System: "s", MaxSteps: 3,
		Bus: b, Store: st, Registry: tools.NewRegistry(bus.KindChief), Env: env,
		IdleTimeout: 20 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = runtime.RunChief(ctx, "goal")
	if got := provider.requestCount(); got != 1 {
		t.Fatalf("idle chief spent %d model requests, want 1", got)
	}
}
