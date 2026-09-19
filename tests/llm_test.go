package tests

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rocina/internal/llm"
)

func TestOpenAIProviderRoundTrip(t *testing.T) {
	var path, auth string
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		auth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hi","tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash_tool","arguments":"{\"command\":\"ls\"}"}}]}}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`)
	}))
	defer srv.Close()

	p := llm.NewOpenAI("test", srv.URL, "secret", nil)
	resp, err := p.Chat(context.Background(), llm.ChatRequest{
		Model:    "gpt-test",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "go"}},
		Tools:    []llm.ToolDef{{Name: "bash_tool", Description: "run", Parameters: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if path != "/chat/completions" {
		t.Fatalf("unexpected path %q", path)
	}
	if auth != "Bearer secret" {
		t.Fatalf("unexpected auth %q", auth)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Name != "bash_tool" {
		t.Fatalf("tool calls not parsed: %+v", resp.Message)
	}
	if resp.Usage.PromptTokens != 3 || resp.Usage.CompletionTokens != 4 {
		t.Fatalf("usage not parsed: %+v", resp.Usage)
	}
	if _, ok := payload["tools"]; !ok {
		t.Fatalf("tools were not sent")
	}
}

func TestOpenAIProviderReportsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"bad key"}}`)
	}))
	defer srv.Close()

	p := llm.NewOpenAI("test", srv.URL, "secret", nil)
	if _, err := p.Chat(context.Background(), llm.ChatRequest{Model: "m"}); err == nil {
		t.Fatal("expected error for non-2xx response")
	}
}

func TestAnthropicProviderRoundTrip(t *testing.T) {
	var payload map[string]any
	var versionHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		versionHeader = r.Header.Get("anthropic-version")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"working"},{"type":"tool_use","id":"toolu_1","name":"bash_tool","input":{"command":"ls"}}],"usage":{"input_tokens":5,"output_tokens":6}}`)
	}))
	defer srv.Close()

	p := llm.NewAnthropic("secret", srv.URL)
	resp, err := p.Chat(context.Background(), llm.ChatRequest{
		Model: "claude-test",
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "be brief"},
			{Role: llm.RoleUser, Content: "go"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "toolu_0", Name: "bash_tool", Arguments: `{"command":"pwd"}`}}},
			{Role: llm.RoleTool, ToolCallID: "toolu_0", Content: "/tmp"},
		},
		Tools: []llm.ToolDef{{Name: "bash_tool", Description: "run", Parameters: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if versionHeader != "2023-06-01" {
		t.Fatalf("unexpected anthropic-version %q", versionHeader)
	}
	if payload["system"] != "be brief" {
		t.Fatalf("system prompt not extracted: %v", payload["system"])
	}
	if resp.Message.Content != "working" {
		t.Fatalf("text not parsed: %q", resp.Message.Content)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Arguments != `{"command":"ls"}` {
		t.Fatalf("tool use not parsed: %+v", resp.Message.ToolCalls)
	}
	messages, _ := payload["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("expected 3 anthropic messages, got %d", len(messages))
	}
}

func TestOpenAIStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(chunk string) {
			_, _ = io.WriteString(w, "data: "+chunk+"\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		write(`{"choices":[{"delta":{"reasoning_content":"hmm "}}]}`)
		write(`{"choices":[{"delta":{"content":"Hel"}}]}`)
		write(`{"choices":[{"delta":{"content":"lo"}}]}`)
		write(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"bash_tool","arguments":"{\"command\":"}}]}}]}`)
		write(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]}}]}`)
		write(`[DONE]`)
	}))
	defer srv.Close()

	p := llm.NewOpenAI("test", srv.URL, "k", nil)
	var text, reasoning, args strings.Builder
	var names []string
	resp, err := p.ChatStream(context.Background(), llm.ChatRequest{Model: "m"}, func(d llm.Delta) {
		switch d.Kind {
		case "text":
			text.WriteString(d.Text)
		case "reasoning":
			reasoning.WriteString(d.Text)
		case "tool_name":
			names = append(names, d.Text)
		case "tool_args":
			args.WriteString(d.Text)
		}
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if text.String() != "Hello" || reasoning.String() != "hmm " {
		t.Fatalf("text=%q reasoning=%q", text.String(), reasoning.String())
	}
	if len(names) != 1 || names[0] != "bash_tool" {
		t.Fatalf("tool names: %+v", names)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Arguments != `{"command":"ls"}` {
		t.Fatalf("tool calls: %+v", resp.Message.ToolCalls)
	}
	if args.String() != `{"command":"ls"}` {
		t.Fatalf("args delta: %q", args.String())
	}
}

func TestAnthropicStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(chunk string) {
			_, _ = io.WriteString(w, "data: "+chunk+"\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		write(`{"type":"message_start","message":{"usage":{"input_tokens":4}}}`)
		write(`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`)
		write(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`)
		write(`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"bash_tool"}}`)
		write(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"ls\"}"}}`)
		write(`{"type":"message_delta","usage":{"output_tokens":7}}`)
		write(`{"type":"message_stop"}`)
	}))
	defer srv.Close()

	p := llm.NewAnthropic("k", srv.URL)
	var text strings.Builder
	var names []string
	resp, err := p.ChatStream(context.Background(), llm.ChatRequest{Model: "m"}, func(d llm.Delta) {
		switch d.Kind {
		case "text":
			text.WriteString(d.Text)
		case "tool_name":
			names = append(names, d.Text)
		}
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if text.String() != "Hi" || len(names) != 1 || names[0] != "bash_tool" {
		t.Fatalf("text=%q names=%+v", text.String(), names)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Arguments != `{"command":"ls"}` {
		t.Fatalf("tool calls: %+v", resp.Message.ToolCalls)
	}
	if resp.Usage.PromptTokens != 4 || resp.Usage.CompletionTokens != 7 {
		t.Fatalf("usage: %+v", resp.Usage)
	}
}
