package tests

import (
	"strings"
	"testing"

	"rocina/internal/store"
)

func TestStoreTodoLifecycle(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	pub, err := s.CreateTodo(true, "", "shared plan", "detail", "chief", 5)
	if err != nil {
		t.Fatalf("create public: %v", err)
	}
	if !pub.Public || pub.Status != store.Pending {
		t.Fatalf("unexpected public todo %+v", pub)
	}
	priv, err := s.CreateTodo(false, "sub-1", "local note", "", "sub-1", 1)
	if err != nil {
		t.Fatalf("create private: %v", err)
	}
	if len(s.PublicTodos()) != 1 {
		t.Fatalf("expected one public todo")
	}
	if len(s.PrivateTodos("sub-1")) != 1 {
		t.Fatalf("expected one private todo for sub-1")
	}
	if len(s.PrivateTodos("sub-2")) != 0 {
		t.Fatalf("unexpected private todo for sub-2")
	}

	status := store.Done
	updated, err := s.UpdateTodo("sub-1", priv.ID, store.TodoPatch{Status: &status})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Status != store.Done {
		t.Fatalf("expected done, got %s", updated.Status)
	}
	if _, err := s.UpdateTodo("sub-1", "missing", store.TodoPatch{}); err == nil {
		t.Fatalf("expected missing todo error")
	}
	if _, err := s.CreateTodo(true, "", "  ", "", "chief", 0); err == nil {
		t.Fatalf("expected empty title error")
	}

	reopened, err := store.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if len(reopened.PublicTodos()) != 1 || len(reopened.PrivateTodos("sub-1")) != 1 {
		t.Fatalf("persistence did not restore todos")
	}
}

func TestStoreRecordToolBumpsRevision(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	before := s.Revision()
	journal, rev := s.RecordTool("chief", "bash_tool", `{"command":"ls"}`)
	if rev <= before {
		t.Fatalf("revision did not advance: %d <= %d", rev, before)
	}
	if journal.Tool != "bash_tool" {
		t.Fatalf("unexpected journal %+v", journal)
	}
}

func TestStoreBoardReflectsTodos(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := s.CreateTodo(true, "", "ship feature", "public detail", "chief", 3); err != nil {
		t.Fatalf("create: %v", err)
	}
	s.SharedSet("stack", "go", "chief")
	board := s.Board("chief")
	for _, want := range []string{"COORDINATION BOARD", "ship feature", "stack = go"} {
		if !strings.Contains(board, want) {
			t.Fatalf("board missing %q:\n%s", want, board)
		}
	}
}

func TestStoreTaskRouting(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s.AddTask("chief", "sub-1", "assign", "do work")
	snap := s.Snapshot("sub-1")
	if len(snap.Inbox) != 1 || snap.Inbox[0].Text != "do work" {
		t.Fatalf("unexpected inbox %+v", snap.Inbox)
	}
	snap = s.Snapshot("chief")
	if len(snap.Outbox) != 1 {
		t.Fatalf("unexpected outbox %+v", snap.Outbox)
	}
}
