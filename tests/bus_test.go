package tests

import (
	"context"
	"testing"
	"time"

	"rocina/internal/bus"
	"rocina/internal/store"
)

func newBus(t *testing.T) *bus.Bus {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return bus.New(st)
}

func TestBusSendAndCollect(t *testing.T) {
	b := newBus(t)
	b.Register("chief", bus.KindChief, "chief", "", "m")
	sub := b.Register("sub-1", bus.KindSub, "worker", "chief", "m")

	if err := b.Send(bus.Envelope{From: "chief", To: "sub-1", Text: "hello"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	got := b.Collect(sub.ID)
	if len(got) != 1 || got[0].Text != "hello" {
		t.Fatalf("unexpected inbox %+v", got)
	}
	if err := b.Send(bus.Envelope{From: "chief", To: "ghost", Text: "x"}); err == nil {
		t.Fatalf("expected unknown agent error")
	}
}

func TestBusWaitReleasedByStopWait(t *testing.T) {
	b := newBus(t)
	b.Register("chief", bus.KindChief, "chief", "", "m")
	sub := b.Register("sub-1", bus.KindSub, "worker", "chief", "m")

	results := make(chan bus.Envelope, 1)
	go func() {
		env, err := b.Wait(context.Background(), sub.ID)
		if err == nil {
			results <- env
		}
	}()

	waitForState(t, sub, bus.StateWaiting)
	if err := b.StopWait("sub-1", "new task"); err != nil {
		t.Fatalf("stop_wait: %v", err)
	}
	select {
	case env := <-results:
		if env.Text != "new task" {
			t.Fatalf("unexpected task %q", env.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("wait did not return after stop_wait")
	}
}

func TestBusWaitUnblocksOnStop(t *testing.T) {
	b := newBus(t)
	b.Register("chief", bus.KindChief, "chief", "", "m")
	sub := b.Register("sub-1", bus.KindSub, "worker", "chief", "m")

	errs := make(chan error, 1)
	go func() {
		_, err := b.Wait(context.Background(), sub.ID)
		errs <- err
	}()
	waitForState(t, sub, bus.StateWaiting)
	b.Stop(sub)
	select {
	case err := <-errs:
		if err == nil {
			t.Fatal("expected wait to fail when stopped")
		}
	case <-time.After(time.Second):
		t.Fatal("wait did not unblock on stop")
	}
}

func TestBusBroadcast(t *testing.T) {
	b := newBus(t)
	b.Register("chief", bus.KindChief, "chief", "", "m")
	b.Register("sub-1", bus.KindSub, "worker", "chief", "m")
	b.Register("sub-2", bus.KindSub, "worker", "chief", "m")
	if got := b.Broadcast("chief", "all hands"); got != 2 {
		t.Fatalf("expected 2 recipients, got %d", got)
	}
	for _, name := range []string{"sub-1", "sub-2"} {
		if len(b.Collect(b.Get(name).ID)) != 1 {
			t.Fatalf("%s did not receive broadcast", name)
		}
	}
}

func waitForState(t *testing.T, a *bus.Agent, want bus.State) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if a.State() == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("agent %s did not reach state %s (got %s)", a.Name, want, a.State())
}
