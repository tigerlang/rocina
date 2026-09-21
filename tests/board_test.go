package tests

import (
	"testing"

	"rocina/internal/store"
)

// Board growth must stay bounded no matter how many tasks and todos are created.
func TestBoardStaysBounded(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i := 0; i < 500; i++ {
		s.AddTask("chief", "sub-1", "assign", "task number with a reasonably long description body")
	}
	base := len(s.Board("sub-1"))
	for i := 0; i < 500; i++ {
		if _, err := s.CreateTodo(true, "", "todo title text here", "todo detail text here", "chief", i%5); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	board := s.Board("sub-1")
	if len(board) > 8000 {
		t.Fatalf("board grew too large despite caps: %d chars", len(board))
	}
	if len(board) < base {
		t.Fatalf("board unexpectedly shrank below task baseline")
	}
}

// A message routed through the bus must not also be duplicated on the board.
func TestBoardDoesNotDuplicateRoutedMessages(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s.AddTask("chief", "sub-1", "assign", "UNIQUE-PAYLOAD-MARKER")
	if got := s.Board("sub-1"); contains(got, "UNIQUE-PAYLOAD-MARKER") {
		t.Fatalf("task text leaked into the board and duplicates the bus message:\n%s", got)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
