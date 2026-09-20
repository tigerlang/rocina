package tests

import (
	"context"
	"testing"

	"rocina/internal/bus"
	"rocina/internal/config"
	"rocina/internal/session"
)

func TestManagerPersistsSessions(t *testing.T) {
	cfg := config.Default()
	cfg.Model = "test-model"
	cfg.DataDir = t.TempDir()
	cfg.Workspace = t.TempDir()

	mgr, err := session.NewManager(context.Background(), cfg)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	if _, err := mgr.NewSession("work"); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if len(mgr.List()) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(mgr.List()))
	}

	reopened, err := session.NewManager(context.Background(), cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	names := map[string]bool{}
	for _, s := range reopened.List() {
		names[s.Name] = true
	}
	if len(names) != 2 || !names["main"] || !names["work"] {
		t.Fatalf("sessions not persisted: %+v", names)
	}
}

func TestStopAgent(t *testing.T) {
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
	if chief == nil {
		t.Fatal("no chief agent")
	}
	if !orch.StopAgent(chief.Name) {
		t.Fatal("stop agent returned false")
	}
	if chief.State() != bus.StateStopped {
		t.Fatalf("expected stopped, got %s", chief.State())
	}
}
