package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"rocina/internal/bus"
	"rocina/internal/llm"
	"rocina/internal/store"
	"rocina/internal/tools"
)

type Runtime struct {
	Agent       *bus.Agent
	Provider    llm.Provider
	Model       string
	System      string
	MaxSteps    int
	Temperature float64

	Bus      *bus.Bus
	Store    *store.Store
	Registry *tools.Registry
	Env      *tools.Env

	HistoryPath string
	IdleTimeout time.Duration

	mu             sync.Mutex
	history        []llm.Message
	sentSinceInput bool
	totalTokens    int
	contextTokens  int
	requests       int
	cost           float64
	costKnown      bool
	requestTimes   []time.Time
}

type Usage struct {
	ContextTokens int
	TotalTokens   int
	Requests      int
	Cost          float64
	CostKnown     bool
	RPM           float64
}

func (r *Runtime) History() []llm.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]llm.Message, len(r.history))
	copy(out, r.history)
	return out
}

func (r *Runtime) Restore(messages []llm.Message) {
	r.mu.Lock()
	r.history = append([]llm.Message(nil), messages...)
	r.mu.Unlock()
}

func (r *Runtime) LoadHistory() {
	if r.HistoryPath == "" {
		return
	}
	data, err := os.ReadFile(r.HistoryPath)
	if err != nil {
		return
	}
	var messages []llm.Message
	if err := json.Unmarshal(data, &messages); err != nil {
		return
	}
	r.Restore(messages)
}

func (r *Runtime) persistHistory() {
	if r.HistoryPath == "" {
		return
	}
	data, err := json.MarshalIndent(r.History(), "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(r.HistoryPath), 0o755); err != nil {
		return
	}
	tmp := r.HistoryPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, r.HistoryPath)
}

func (r *Runtime) recordUsage(usage llm.Usage) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	total := usage.TotalTokens
	if total == 0 {
		total = usage.PromptTokens + usage.CompletionTokens
	}
	r.totalTokens += total
	r.contextTokens = usage.PromptTokens
	r.requests++
	r.cost += usage.CostUSD
	if usage.CostUSD > 0 {
		r.costKnown = true
	}
	r.requestTimes = append(r.requestTimes, now)
	cut := now.Add(-time.Minute)
	trimmed := r.requestTimes[:0]
	for _, at := range r.requestTimes {
		if at.After(cut) {
			trimmed = append(trimmed, at)
		}
	}
	r.requestTimes = trimmed
}

func (r *Runtime) Usage() Usage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Usage{
		ContextTokens: r.contextTokens,
		TotalTokens:   r.totalTokens,
		Requests:      r.requests,
		Cost:          r.cost,
		CostKnown:     r.costKnown,
		RPM:           float64(len(r.requestTimes)),
	}
}

func (r *Runtime) appendUser(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	r.mu.Lock()
	r.history = append(r.history, llm.Message{Role: llm.RoleUser, Content: text})
	r.mu.Unlock()
}

func (r *Runtime) drainInbox() {
	for _, env := range r.Bus.Collect(r.Agent.ID) {
		r.appendUser(fmt.Sprintf("[%s -> %s] %s", env.From, r.Agent.Name, env.Text))
	}
}

func (r *Runtime) RunChief(ctx context.Context, goal string) error {
	r.appendUser(goal)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.drainInbox()
		done, err := r.converse(ctx)
		if err != nil {
			return err
		}
		if !done {
			continue
		}
		if r.Bus.ActiveSubs() == 0 {
			r.Bus.SetState(r.Agent, bus.StateIdle, "")
			return nil
		}
		// Wait for subagent activity without spending a model request per timeout.
		r.Bus.SetState(r.Agent, bus.StateWaiting, "")
		timeout := r.IdleTimeout
		if timeout <= 0 {
			timeout = 120 * time.Second
		}
		for {
			msg, ok := r.Bus.IdleWait(ctx, r.Agent.ID, timeout)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !ok {
				if r.Bus.ActiveSubs() == 0 {
					r.Bus.SetState(r.Agent, bus.StateIdle, "")
					return nil
				}
				continue
			}
			r.appendUser(fmt.Sprintf("[%s -> chief] %s", msg.From, msg.Text))
			break
		}
	}
}

func (r *Runtime) RunSub(ctx context.Context, initial string) error {
	pending := initial
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.TrimSpace(pending) != "" {
			r.appendUser(pending)
			pending = ""
		} else {
			msg, err := r.Bus.Wait(ctx, r.Agent.ID)
			if err != nil {
				return err
			}
			r.appendUser(fmt.Sprintf("[%s -> %s] %s", msg.From, r.Agent.Name, msg.Text))
		}
		r.drainInbox()
		r.mu.Lock()
		r.sentSinceInput = false
		r.mu.Unlock()
		if _, err := r.converse(ctx); err != nil {
			return err
		}
		r.finishSubTurn()
	}
}

func (r *Runtime) converse(ctx context.Context) (bool, error) {
	for step := 0; ; step++ {
		allowTools := step < r.MaxSteps
		done, err := r.step(ctx, allowTools)
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			r.Bus.SetState(r.Agent, bus.StateError, err.Error())
			return false, err
		}
		if done {
			return true, nil
		}
	}
}

func (r *Runtime) step(ctx context.Context, allowTools bool) (bool, error) {
	r.Bus.SetState(r.Agent, bus.StateRunning, "")

	system := r.System
	if board := r.Store.Board(r.Agent.Name); board != "" {
		system += "\n\n" + board
	}
	r.mu.Lock()
	messages := make([]llm.Message, 0, len(r.history)+1)
	messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: system})
	messages = append(messages, r.history...)
	r.mu.Unlock()

	req := llm.ChatRequest{
		Model:       r.Model,
		Messages:    messages,
		Temperature: r.Temperature,
	}
	if allowTools {
		req.Tools = r.Registry.Defs()
	}

	reasoned := false
	resp, err := r.Provider.ChatStream(ctx, req, func(d llm.Delta) {
		switch d.Kind {
		case "text":
			r.Bus.Publish(bus.Event{Type: "assistant.delta", Agent: r.Agent.Name, Text: d.Text})
		case "reasoning":
			reasoned = true
			r.Bus.Publish(bus.Event{Type: "reasoning.delta", Agent: r.Agent.Name, Text: d.Text})
		case "tool_name":
			r.Bus.Publish(bus.Event{Type: "tool.call", Agent: r.Agent.Name, Tool: d.Text, CallID: d.ID})
		case "tool_args":
			r.Bus.Publish(bus.Event{Type: "tool.args", Agent: r.Agent.Name, Text: d.Text, CallID: d.ID})
		}
	})
	if err != nil {
		return false, err
	}
	r.Bus.Publish(bus.Event{Type: "assistant.done", Agent: r.Agent.Name})
	if reasoned {
		r.Bus.Publish(bus.Event{Type: "reasoning.done", Agent: r.Agent.Name})
	}
	r.recordUsage(resp.Usage)

	r.mu.Lock()
	r.history = append(r.history, resp.Message)
	calls := resp.Message.ToolCalls
	r.mu.Unlock()

	if len(calls) == 0 {
		r.persistHistory()
		return true, nil
	}
	for _, call := range calls {
		result := r.executeTool(ctx, call)
		r.mu.Lock()
		r.history = append(r.history, llm.Message{
			Role:       llm.RoleTool,
			Content:    result,
			ToolCallID: call.ID,
			Name:       call.Name,
		})
		r.mu.Unlock()
	}
	r.persistHistory()
	return false, nil
}

func (r *Runtime) executeTool(ctx context.Context, call llm.ToolCall) string {
	r.Store.RecordTool(r.Agent.Name, call.Name, summarise(call.Arguments))
	var result string
	tool, ok := r.Registry.Get(call.Name)
	switch {
	case !ok:
		result = fmt.Sprintf("error: unknown tool %q", call.Name)
	default:
		if call.Name == "send_to_chief" {
			r.mu.Lock()
			r.sentSinceInput = true
			r.mu.Unlock()
		}
		out, err := tool.Run(ctx, r.Env, jsonRaw(call.Arguments))
		switch {
		case err != nil && out != "":
			result = fmt.Sprintf("error: %v\n%s", err, out)
		case err != nil:
			result = fmt.Sprintf("error: %v", err)
		default:
			result = out
		}
	}
	r.Bus.Publish(bus.Event{Type: "tool.result", Agent: r.Agent.Name, Tool: call.Name, Text: result, CallID: call.ID})
	return result
}

func (r *Runtime) finishSubTurn() {
	r.mu.Lock()
	sent := r.sentSinceInput
	text := lastAssistantText(r.history)
	r.mu.Unlock()
	if sent || strings.TrimSpace(text) == "" {
		return
	}
	target := r.Agent.Parent
	if target == "" {
		if chief := r.Bus.Chief(); chief != nil {
			target = chief.Name
		}
	}
	if target == "" {
		return
	}
	_ = r.Bus.Send(bus.Envelope{From: r.Agent.Name, To: target, Kind: "result", Text: text})
}

func lastAssistantText(history []llm.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == llm.RoleAssistant {
			return history[i].Content
		}
	}
	return ""
}

func summarise(args string) string {
	args = strings.TrimSpace(args)
	if len(args) > 160 {
		return args[:160] + "..."
	}
	return args
}

func jsonRaw(s string) []byte {
	if strings.TrimSpace(s) == "" {
		return []byte("{}")
	}
	return []byte(s)
}
