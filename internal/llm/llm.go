package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// httpError turns a provider error response into a short message. Providers
// return a JSON body such as {"error":{"message":"...","type":"..."}} or
// {"message":"..."}; showing that raw body inline is unreadable and huge, so
// only the human message is extracted and the rest is dropped.
func httpError(name string, status int, body []byte) error {
	message := errorMessage(body)
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	message = strings.ReplaceAll(message, "\n", " ")
	if len(message) > 500 {
		message = message[:500] + "…"
	}
	if message == "" {
		return fmt.Errorf("%s: status %d", name, status)
	}
	return fmt.Errorf("%s: status %d: %s", name, status, message)
}

func errorMessage(body []byte) string {
	var parsed struct {
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	switch {
	case parsed.Error != nil && parsed.Error.Message != "":
		return strings.TrimSpace(parsed.Error.Message)
	case parsed.Message != "":
		return strings.TrimSpace(parsed.Message)
	default:
		return ""
	}
}

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
	TotalTokens      int
	CostUSD          float64
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
