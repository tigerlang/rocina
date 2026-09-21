package tests

import (
	"context"
	"testing"
	"time"

	"rocina/internal/bus"
	"rocina/internal/config"
	"rocina/internal/session"
)

func TestSubmitToRoutesToAgent(t *testing.T) {
	cfg := config.Default()
	cfg.Model = "test-model"
	cfg.DataDir = t.TempDir()
	cfg.Workspace = t.TempDir()

	mgr, err := session.NewManager(context.Background(), cfg)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	orch := mgr.Active().Orch
	orch.Bus().SetState(orch.Bus().Chief(), bus.StateRunning, "")
	sub := orch.Bus().Register("sub-1", bus.KindSub, "worker", "chief", "test-model")

	if err := orch.SubmitTo("sub-1", "hello sub"); err != nil {
		t.Fatalf("submit to sub: %v", err)
	}
	got := orch.Bus().Collect(sub.ID)
	if len(got) != 1 || got[0].Text != "hello sub" {
		t.Fatalf("subagent did not receive the message: %+v", got)
	}
	if err := orch.SubmitTo("ghost", "x"); err == nil {
		t.Fatal("expected unknown agent error")
	}
}

func TestStopAgentMarksStopped(t *testing.T) {
	cfg := config.Default()
	cfg.Model = "test-model"
	cfg.DataDir = t.TempDir()
	cfg.Workspace = t.TempDir()

	mgr, err := session.NewManager(context.Background(), cfg)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	orch := mgr.Active().Orch
	chief := orch.Bus().Chief()
	if !orch.StopAgent(chief.Name) {
		t.Fatal("stop agent returned false")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if chief.State() == bus.StateStopped {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("expected stopped, got %s", chief.State())
}
