package llm

import (
	"context"
	"encoding/json"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ChatRequest struct {
	Model       string
	Messages    []Message
	Tools       []ToolDef
	Temperature float64
	MaxTokens   int
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

type ChatResponse struct {
	Message Message
	Usage   Usage
}

// Delta is one streaming fragment. Kind is text, reasoning, tool_name or tool_args.
type Delta struct {
	Kind  string
	Text  string
	Index int
	ID    string
}

type StreamFunc func(Delta)

type Provider interface {
	Name() string
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	ChatStream(ctx context.Context, req ChatRequest, emit StreamFunc) (*ChatResponse, error)
}

// ModelLister is implemented by providers that can enumerate their models.
type ModelLister interface {
	Models(ctx context.Context) ([]string, error)
}
