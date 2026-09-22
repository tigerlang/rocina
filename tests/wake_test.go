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

func newWakeManager(t *testing.T) *session.Manager {
	t.Helper()
	cfg := config.Default()
	cfg.Model = "test-model"
	cfg.DataDir = t.TempDir()
	cfg.Workspace = t.TempDir()
	provider := newScripted(assistantText("idle"))
	mgr, err := session.NewManagerWithProvider(context.Background(), cfg, provider)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	return mgr
}

func TestWakeAgentRevivesErrorState(t *testing.T) {
	mgr := newWakeManager(t)
	orch := mgr.Active().Orch
	if _, err := orch.Spawn(tools.SpawnSpec{Name: "sub-1", Role: "worker"}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	sub := orch.Bus().Get("sub-1")
	waitForState(t, sub, bus.StateWaiting)

	orch.Bus().SetState(sub, bus.StateError, "boom")
	name, err := orch.WakeAgent("sub-1", "resume")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if name != "sub-1" {
		t.Fatalf("unexpected name %q", name)
	}
	if sub.State() == bus.StateError || sub.State() == bus.StateStopped {
		t.Fatalf("agent should leave the failed state, got %s", sub.State())
	}
}

func TestWakeAgentRejectsActive(t *testing.T) {
	mgr := newWakeManager(t)
	orch := mgr.Active().Orch
	if _, err := orch.Spawn(tools.SpawnSpec{Name: "sub-1", Role: "worker"}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	sub := orch.Bus().Get("sub-1")
	waitForState(t, sub, bus.StateWaiting)
	orch.Bus().SetState(sub, bus.StateRunning, "")
	if _, err := orch.WakeAgent("sub-1", ""); err == nil {
		t.Fatal("expected wake to reject an active agent")
	}
}

func TestWakeToolsAreChiefOnly(t *testing.T) {
	chief := tools.NewRegistry(bus.KindChief)
	sub := tools.NewRegistry(bus.KindSub)
	for _, name := range []string{"wake_up", "wake_up_all"} {
		if _, ok := chief.Get(name); !ok {
			t.Fatalf("chief is missing %q", name)
		}
		if _, ok := sub.Get(name); ok {
			t.Fatalf("subagent must not receive %q", name)
		}
	}
}

func TestWakeAgentRecoversAfterStop(t *testing.T) {
	mgr := newWakeManager(t)
	orch := mgr.Active().Orch
	if _, err := orch.Spawn(tools.SpawnSpec{Name: "sub-1", Role: "worker"}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	sub := orch.Bus().Get("sub-1")
	waitForState(t, sub, bus.StateWaiting)
	orch.StopAgent("sub-1")
	if _, err := orch.WakeAgent("sub-1", "again"); err != nil {
		t.Fatalf("wake stopped agent: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if sub.State() == bus.StateWaiting {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("revived subagent did not enter its loop, state=%s", sub.State())
}
