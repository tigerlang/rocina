package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type openAIProvider struct {
	name    string
	baseURL string
	apiKey  string
	headers map[string]string
	http    *http.Client
}

func NewOpenAI(name, baseURL, apiKey string, headers map[string]string) Provider {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &openAIProvider{
		name:    name,
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		headers: headers,
		http:    &http.Client{Timeout: 10 * time.Minute},
	}
}

func (p *openAIProvider) Name() string { return p.name }

func (p *openAIProvider) Models(ctx context.Context) ([]string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	for k, v := range p.headers {
		httpReq.Header.Set(k, v)
	}
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
		return nil, httpError(p.name, resp.StatusCode, data)
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("%s: decode models: %w", p.name, err)
	}
	models := make([]string, 0, len(parsed.Data))
	for _, model := range parsed.Data {
		if model.ID != "" {
			models = append(models, model.ID)
		}
	}
	return models, nil
}

func (p *openAIProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	body, err := p.buildPayload(req, false)
	if err != nil {
		return nil, err
	}
	data, err := p.do(ctx, body)
	if err != nil {
		return nil, err
	}
	return parseOpenAIResponse(p.name, data)
}

func (p *openAIProvider) ChatStream(ctx context.Context, req ChatRequest, emit StreamFunc) (*ChatResponse, error) {
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
		return nil, httpError(p.name, resp.StatusCode, data)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if err != nil {
			return nil, err
		}
		out, err := parseOpenAIResponse(p.name, data)
		if err != nil {
			return nil, err
		}
		if emit != nil && out.Message.Content != "" {
			emit(Delta{Kind: "text", Text: out.Message.Content})
		}
		return out, nil
	}

	assembler := &openAIAssembler{calls: map[int]*ToolCall{}}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		chunk := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if chunk == "[DONE]" {
			break
		}
		if chunk == "" {
			continue
		}
		var parsed openAIChunk
		if err := json.Unmarshal([]byte(chunk), &parsed); err != nil {
			continue
		}
		assembler.consume(parsed, emit)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return assembler.response(), nil
}

func (p *openAIProvider) buildPayload(req ChatRequest, stream bool) ([]byte, error) {
	messages := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		item := map[string]any{"role": string(m.Role)}
		switch {
		case m.Role == RoleAssistant && len(m.ToolCalls) > 0:
			calls := make([]map[string]any, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				args := tc.Arguments
				if args == "" {
					args = "{}"
				}
				calls = append(calls, map[string]any{
					"id":   tc.ID,
					"type": "function",
					"function": map[string]any{
						"name":      tc.Name,
						"arguments": args,
					},
				})
			}
			item["tool_calls"] = calls
			if m.Content != "" {
				item["content"] = m.Content
			}
		case m.Role == RoleTool:
			item["content"] = m.Content
			item["tool_call_id"] = m.ToolCallID
			if m.Name != "" {
				item["name"] = m.Name
			}
		default:
			item["content"] = m.Content
		}
		messages = append(messages, item)
	}

	payload := map[string]any{"model": req.Model, "messages": messages}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			params := t.Parameters
			if len(params) == 0 {
				params = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  params,
				},
			})
		}
		payload["tools"] = tools
		payload["tool_choice"] = "auto"
	}
	if req.Temperature > 0 {
		payload["temperature"] = req.Temperature
	}
	if req.MaxTokens > 0 {
		payload["max_tokens"] = req.MaxTokens
	}
	if stream {
		payload["stream"] = true
		payload["stream_options"] = map[string]any{"include_usage": true}
	}
	if p.name == "openrouter" {
		payload["usage"] = map[string]any{"include": true}
	}
	return json.Marshal(payload)
}

func (p *openAIProvider) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	for k, v := range p.headers {
		httpReq.Header.Set(k, v)
	}
	return httpReq, nil
}

func (p *openAIProvider) do(ctx context.Context, body []byte) ([]byte, error) {
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
		return nil, httpError(p.name, resp.StatusCode, data)
	}
	return data, nil
}

type openAIChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int     `json:"prompt_tokens"`
		CompletionTokens int     `json:"completion_tokens"`
		TotalTokens      int     `json:"total_tokens"`
		Cost             float64 `json:"cost"`
	} `json:"usage"`
}

type openAIAssembler struct {
	content   strings.Builder
	reasoning strings.Builder
	calls     map[int]*ToolCall
	order     []int
	usage     Usage
}

func (a *openAIAssembler) consume(chunk openAIChunk, emit StreamFunc) {
	if chunk.Usage != nil {
		a.usage = a.usage.Merge(Usage{
			PromptTokens:     chunk.Usage.PromptTokens,
			CompletionTokens: chunk.Usage.CompletionTokens,
			TotalTokens:      chunk.Usage.TotalTokens,
			CostUSD:          chunk.Usage.Cost,
		})
	}
	if len(chunk.Choices) == 0 {
		return
	}
	delta := chunk.Choices[0].Delta
	if delta.Content != "" {
		a.content.WriteString(delta.Content)
		if emit != nil {
			emit(Delta{Kind: "text", Text: delta.Content})
		}
	}
	reasoning := delta.ReasoningContent
	if reasoning == "" {
		reasoning = delta.Reasoning
	}
	if reasoning != "" {
		a.reasoning.WriteString(reasoning)
		if emit != nil {
			emit(Delta{Kind: "reasoning", Text: reasoning})
		}
	}
	for _, tc := range delta.ToolCalls {
		call, ok := a.calls[tc.Index]
		if !ok {
			call = &ToolCall{}
			a.calls[tc.Index] = call
			a.order = append(a.order, tc.Index)
		}
		if tc.ID != "" {
			call.ID = tc.ID
		}
		if tc.Function.Name != "" {
			if call.Name == "" {
				call.Name = tc.Function.Name
				if emit != nil {
					emit(Delta{Kind: "tool_name", Text: call.Name, Index: tc.Index, ID: call.ID})
				}
			} else {
				call.Name += tc.Function.Name
			}
		}
		if tc.Function.Arguments != "" {
			call.Arguments += tc.Function.Arguments
			if emit != nil {
				emit(Delta{Kind: "tool_args", Text: tc.Function.Arguments, Index: tc.Index, ID: call.ID})
			}
		}
	}
}

func (a *openAIAssembler) response() *ChatResponse {
	message := Message{Role: RoleAssistant, Content: a.content.String()}
	for _, index := range a.order {
		if call, ok := a.calls[index]; ok {
			message.ToolCalls = append(message.ToolCalls, *call)
		}
	}
	return &ChatResponse{Message: message, Usage: a.usage}
}

func parseOpenAIResponse(name string, data []byte) (*ChatResponse, error) {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int     `json:"prompt_tokens"`
			CompletionTokens int     `json:"completion_tokens"`
			TotalTokens      int     `json:"total_tokens"`
			Cost             float64 `json:"cost"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("%s: decode: %w", name, err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("%s: %s", name, parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("%s: empty response", name)
	}
	out := Message{Role: RoleAssistant, Content: parsed.Choices[0].Message.Content}
	for _, tc := range parsed.Choices[0].Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	total := parsed.Usage.TotalTokens
	if total == 0 {
		total = parsed.Usage.PromptTokens + parsed.Usage.CompletionTokens
	}
	return &ChatResponse{
		Message: out,
		Usage: Usage{
			PromptTokens:     parsed.Usage.PromptTokens,
			CompletionTokens: parsed.Usage.CompletionTokens,
			TotalTokens:      total,
			CostUSD:          parsed.Usage.Cost,
		},
	}, nil
}
