package tests

import (
	"context"
	"testing"
	"time"

	"rocina/internal/bus"
	"rocina/internal/config"
	"rocina/internal/session"
	"rocina/internal/tools"
)

func TestSubagentsPersistAcrossRestart(t *testing.T) {
	cfg := config.Default()
	cfg.Model = "test-model"
	cfg.DataDir = t.TempDir()
	cfg.Workspace = t.TempDir()
	provider := newScripted(assistantText("idle"))

	mgr, err := session.NewManagerWithProvider(context.Background(), cfg, provider)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	orch := mgr.Active().Orch
	if _, err := orch.Spawn(tools.SpawnSpec{Name: "sub-1", Role: "worker"}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	waitForState(t, orch.Bus().Get("sub-1"), bus.StateWaiting)

	// The session manager persists the index; reopen without a provider to reuse
	// the real one, then confirm the worker was rebuilt.
	reopened, err := session.NewManagerWithProvider(context.Background(), cfg, provider)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	sub := reopened.Active().Orch.Bus().Get("sub-1")
	if sub == nil {
		t.Fatal("subagent was not restored")
	}
	if sub.Kind != bus.KindSub {
		t.Fatalf("restored agent has wrong kind %s", sub.Kind)
	}
}

func TestSubmitToStoppedAgents(t *testing.T) {
	cfg := config.Default()
	cfg.Model = "test-model"
	cfg.DataDir = t.TempDir()
	cfg.Workspace = t.TempDir()
	provider := newScripted()

	mgr, err := session.NewManagerWithProvider(context.Background(), cfg, provider)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	orch := mgr.Active().Orch

	if err := orch.SubmitTo("chief", "hi chief"); err != nil {
		t.Fatalf("chief idle submit: %v", err)
	}
	orch.StopAgent("chief")
	if err := orch.SubmitTo("chief", "are you there"); err != nil {
		t.Fatalf("chief stopped submit: %v", err)
	}

	if _, err := orch.Spawn(tools.SpawnSpec{Name: "sub-1", Role: "worker"}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	sub := orch.Bus().Get("sub-1")
	waitForState(t, sub, bus.StateWaiting)
	orch.StopAgent("sub-1")
	if err := orch.SubmitTo("sub-1", "resume work"); err != nil {
		t.Fatalf("sub stopped submit: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if sub.State() == bus.StateWaiting || sub.State() == bus.StateRunning {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("stopped subagent was not revived, state=%s", sub.State())
}
