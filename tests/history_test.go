package tests

import (
	"context"
	"strings"
	"testing"

	"rocina/internal/agent"
	"rocina/internal/bus"
	"rocina/internal/llm"
	"rocina/internal/store"
	"rocina/internal/tools"
)

func promptChars(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content) + len(m.ToolCallID)
		for _, tc := range m.ToolCalls {
			n += len(tc.Arguments) + len(tc.Name)
		}
	}
	return n
}

// A long run must not resend an unbounded history on every step.
func TestHistoryStaysBounded(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	b := bus.New(st)
	chief := b.Register("chief", bus.KindChief, "chief", "", "m")

	payload := strings.Repeat("x", 4000)
	provider := newScripted()
	for i := 0; i < 80; i++ {
		provider.responses = append(provider.responses, assistantCall("c", "think", map[string]string{"thought": payload}))
	}
	env := &tools.Env{Agent: chief, Bus: b, Store: st}
	runtime := &agent.Runtime{
		Agent: chief, Provider: provider, Model: "m", System: "sys", MaxSteps: 300,
		Bus: b, Store: st, Registry: tools.NewRegistry(bus.KindChief), Env: env,
	}
	if err := runtime.RunChief(context.Background(), "goal"); err != nil {
		t.Fatalf("run: %v", err)
	}

	for step, req := range provider.requests {
		chars := promptChars(req.Messages)
		if chars > 70000 {
			t.Fatalf("step %d prompt is unbounded: %d chars", step, chars)
		}
		// every tool result must still follow its tool call within the prompt
		for i, m := range req.Messages {
			if m.Role != llm.RoleTool {
				continue
			}
			found := false
			for j := i - 1; j >= 0; j-- {
				for _, tc := range req.Messages[j].ToolCalls {
					if tc.ID == m.ToolCallID {
						found = true
					}
				}
				if found {
					break
				}
			}
			if !found {
				t.Fatalf("step %d has an orphaned tool result %q", step, m.ToolCallID)
			}
		}
	}
}
