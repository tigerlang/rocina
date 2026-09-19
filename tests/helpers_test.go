package tests

import (
	"context"
	"encoding/json"
	"sync"

	"rocina/internal/llm"
)

type scriptedProvider struct {
	mu        sync.Mutex
	responses []llm.Message
	calls     int
	requests  []llm.ChatRequest
}

func newScripted(responses ...llm.Message) *scriptedProvider {
	return &scriptedProvider{responses: responses}
}

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	if p.calls >= len(p.responses) {
		return &llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}, nil
	}
	msg := p.responses[p.calls]
	p.calls++
	return &llm.ChatResponse{Message: msg}, nil
}

func (p *scriptedProvider) requestCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

func (p *scriptedProvider) ChatStream(ctx context.Context, req llm.ChatRequest, emit llm.StreamFunc) (*llm.ChatResponse, error) {
	resp, err := p.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	if emit != nil {
		if resp.Message.Content != "" {
			emit(llm.Delta{Kind: "text", Text: resp.Message.Content})
		}
		for _, tc := range resp.Message.ToolCalls {
			emit(llm.Delta{Kind: "tool_name", Text: tc.Name, ID: tc.ID})
			if tc.Arguments != "" {
				emit(llm.Delta{Kind: "tool_args", Text: tc.Arguments, ID: tc.ID})
			}
		}
	}
	return resp, nil
}

func assistantText(text string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Content: text}
}

func assistantCall(id, name string, args any) llm.Message {
	raw, _ := json.Marshal(args)
	return llm.Message{
		Role:      llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{ID: id, Name: name, Arguments: string(raw)}},
	}
}
