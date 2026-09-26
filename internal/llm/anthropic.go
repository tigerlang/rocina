package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type anthropicProvider struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewAnthropic(apiKey, baseURL string) Provider {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &anthropicProvider{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 10 * time.Minute},
	}
}

func (p *anthropicProvider) Name() string { return "claude" }

func (p *anthropicProvider) Models(ctx context.Context) ([]string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	resp, err := p.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, httpError("claude", resp.StatusCode, data)
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("claude: decode models: %w", err)
	}
	models := make([]string, 0, len(parsed.Data))
	for _, model := range parsed.Data {
		if model.ID != "" {
			models = append(models, model.ID)
		}
	}
	return models, nil
}

func (p *anthropicProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	body, err := p.buildPayload(req, false)
	if err != nil {
		return nil, err
	}
	data, err := p.do(ctx, body)
	if err != nil {
		return nil, err
	}
	return parseAnthropicResponse(data)
}

func (p *anthropicProvider) ChatStream(ctx context.Context, req ChatRequest, emit StreamFunc) (*ChatResponse, error) {
	body, err := p.buildPayload(req, true)
	if err != nil {
		return nil, err
	}
	httpReq, err := p.newRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	resp, err := p.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, httpError("claude", resp.StatusCode, data)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if err != nil {
			return nil, err
		}
		out, err := parseAnthropicResponse(data)
		if err != nil {
			return nil, err
		}
		if emit != nil && out.Message.Content != "" {
			emit(Delta{Kind: "text", Text: out.Message.Content})
		}
		return out, nil
	}

	assembler := &anthropicAssembler{calls: map[int]*ToolCall{}}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		var event anthropicEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		assembler.consume(event, emit)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return assembler.response(), nil
}

func (p *anthropicProvider) buildPayload(req ChatRequest, stream bool) ([]byte, error) {
	var system strings.Builder
	messages := make([]map[string]any, 0, len(req.Messages))

	for _, m := range req.Messages {
		switch m.Role {
		case RoleSystem:
			if system.Len() > 0 {
				system.WriteString("\n\n")
			}
			system.WriteString(m.Content)
		case RoleUser:
			messages = appendMerged(messages, "user", map[string]any{"type": "text", "text": m.Content})
		case RoleAssistant:
			blocks := make([]any, 0, len(m.ToolCalls)+1)
			if m.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				input := json.RawMessage(tc.Arguments)
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Name,
					"input": input,
				})
			}
			if len(blocks) > 0 {
				messages = append(messages, map[string]any{"role": "assistant", "content": blocks})
			}
		case RoleTool:
			block := map[string]any{
				"type":        "tool_result",
				"tool_use_id": m.ToolCallID,
				"content":     m.Content,
			}
			messages = appendMerged(messages, "user", block)
		}
	}

	if len(messages) == 0 {
		messages = append(messages, map[string]any{
			"role":    "user",
			"content": []any{map[string]any{"type": "text", "text": "Continue."}},
		})
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	payload := map[string]any{
		"model":      req.Model,
		"messages":   messages,
		"max_tokens": maxTokens,
	}
	if system.Len() > 0 {
		payload["system"] = system.String()
	}
	if len(req.Tools) > 0 {
		tools := make([]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			schema := t.Parameters
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools = append(tools, map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": schema,
			})
		}
		payload["tools"] = tools
	}
	if req.Temperature > 0 {
		payload["temperature"] = req.Temperature
	}
	if stream {
		payload["stream"] = true
	}
	return json.Marshal(payload)
}

func (p *anthropicProvider) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	return httpReq, nil
}

func (p *anthropicProvider) do(ctx context.Context, body []byte) ([]byte, error) {
	httpReq, err := p.newRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	resp, err := p.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, httpError("claude", resp.StatusCode, data)
	}
	return data, nil
}

// appendMerged keeps the strict user/assistant alternation Anthropic expects by
// folding same-role content blocks into the previous message.
func appendMerged(messages []map[string]any, role string, block map[string]any) []map[string]any {
	if n := len(messages); n > 0 {
		if last, ok := messages[n-1]["role"].(string); ok && last == role {
			if content, ok := messages[n-1]["content"].([]any); ok {
				messages[n-1]["content"] = append(content, block)
				return messages
			}
		}
	}
	return append(messages, map[string]any{"role": role, "content": []any{block}})
}

type anthropicEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		Thinking    string `json:"thinking"`
	} `json:"delta"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Message struct {
		Usage struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

type anthropicAssembler struct {
	text  strings.Builder
	calls map[int]*ToolCall
	usage Usage
	err   string
}

func (a *anthropicAssembler) consume(event anthropicEvent, emit StreamFunc) {
	if event.Error != nil {
		a.err = event.Error.Message
		return
	}
	switch event.Type {
	case "message_start":
		a.usage = a.usage.Merge(Usage{PromptTokens: event.Message.Usage.InputTokens})
	case "message_delta":
		a.usage = a.usage.Merge(Usage{PromptTokens: event.Usage.InputTokens, CompletionTokens: event.Usage.OutputTokens})
	case "content_block_start":
		if event.ContentBlock.Type == "tool_use" {
			a.calls[event.Index] = &ToolCall{ID: event.ContentBlock.ID, Name: event.ContentBlock.Name}
			if emit != nil {
				emit(Delta{Kind: "tool_name", Text: event.ContentBlock.Name, Index: event.Index, ID: event.ContentBlock.ID})
			}
		}
	case "content_block_delta":
		switch event.Delta.Type {
		case "text_delta":
			a.text.WriteString(event.Delta.Text)
			if emit != nil {
				emit(Delta{Kind: "text", Text: event.Delta.Text})
			}
		case "thinking_delta":
			if emit != nil {
				emit(Delta{Kind: "reasoning", Text: event.Delta.Thinking})
			}
		case "input_json_delta":
			if call, ok := a.calls[event.Index]; ok {
				call.Arguments += event.Delta.PartialJSON
			}
			if emit != nil {
				emit(Delta{Kind: "tool_args", Text: event.Delta.PartialJSON, Index: event.Index})
			}
		}
	}
}

func (a *anthropicAssembler) response() *ChatResponse {
	message := Message{Role: RoleAssistant, Content: a.text.String()}
	indexes := make([]int, 0, len(a.calls))
	for index := range a.calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		call := *a.calls[index]
		if call.Arguments == "" {
			call.Arguments = "{}"
		}
		message.ToolCalls = append(message.ToolCalls, call)
	}
	a.usage.TotalTokens = a.usage.PromptTokens + a.usage.CompletionTokens
	return &ChatResponse{Message: message, Usage: a.usage}
}

func parseAnthropicResponse(data []byte) (*ChatResponse, error) {
	var parsed struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("claude: decode: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("claude: %s", parsed.Error.Message)
	}
	out := Message{Role: RoleAssistant}
	var text strings.Builder
	for _, block := range parsed.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			args := string(block.Input)
			if args == "" {
				args = "{}"
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: block.ID, Name: block.Name, Arguments: args})
		}
	}
	out.Content = text.String()
	return &ChatResponse{
		Message: out,
		Usage: Usage{
			PromptTokens:     parsed.Usage.InputTokens,
			CompletionTokens: parsed.Usage.OutputTokens,
			TotalTokens:      parsed.Usage.InputTokens + parsed.Usage.OutputTokens,
		},
	}, nil
}
