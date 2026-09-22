package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"rocina/internal/bus"
	"rocina/internal/store"
)

func registerShared(r *Registry) {
	r.add(Tool{
		Def: def("public_todo", "Create a shared todo visible to the chief and every subagent.", obj(map[string]any{
			"title":    str("concise todo title"),
			"detail":   str("optional detail"),
			"priority": integer("higher runs first"),
		}, "title")),
		Run: createPublicTodo,
	})
	r.add(Tool{
		Def: def("private_todo", "Create a private todo visible only to the calling agent.", obj(map[string]any{
			"title":    str("concise todo title"),
			"detail":   str("optional detail"),
			"priority": integer("higher runs first"),
		}, "title")),
		Run: createPrivateTodo,
	})
	r.add(Tool{
		Def: def("update_public_todo", "Update a shared todo.", obj(map[string]any{
			"id":       str("todo id"),
			"title":    str("new title"),
			"detail":   str("new detail"),
			"status":   str("pending|in_progress|done|blocked"),
			"priority": integer("new priority"),
		}, "id")),
		Run: updatePublicTodo,
	})
	r.add(Tool{
		Def: def("update_private_todo", "Update a private todo owned by the calling agent.", obj(map[string]any{
			"id":       str("todo id"),
			"title":    str("new title"),
			"detail":   str("new detail"),
			"status":   str("pending|in_progress|done|blocked"),
			"priority": integer("new priority"),
		}, "id")),
		Run: updatePrivateTodo,
	})
	r.add(Tool{
		Def: def("list_agents", "List every agent with its kind, state and active todo counts.", obj(map[string]any{})),
		Run: listAgents,
	})
	r.add(Tool{
		Def: def("shared_memory", "Read or write the shared key/value blackboard.", obj(map[string]any{
			"action": str("get|set|delete|list"),
			"key":    str("memory key"),
			"value":  str("value for set"),
		}, "action")),
		Run: sharedMemory,
	})
	r.add(Tool{
		Def: def("checkpoint", "Save, load or list this agent's conversation checkpoints.", obj(map[string]any{
			"action": str("save|load|list"),
			"name":   str("checkpoint name"),
		}, "action")),
		Run: checkpoint,
	})
	r.add(Tool{
		Def: def("think", "Record a short reasoning step before acting. Has no side effects.", obj(map[string]any{
			"thought": str("reasoning to keep for this turn"),
		}, "thought")),
		Run: think,
	})
}

func registerChief(r *Registry) {
	r.add(Tool{
		Def: def("send_to_sub", "Assign a task to a subagent by name or id.", obj(map[string]any{
			"agent": str("target subagent name or id"),
			"task":  str("task description"),
		}, "agent", "task")),
		Run: sendToSub,
	})
	r.add(Tool{
		Def: def("stop_wait", "Clear a subagent's wait state and deliver a task to it.", obj(map[string]any{
			"agent": str("target subagent name or id"),
			"task":  str("task to deliver"),
		}, "agent", "task")),
		Run: stopWait,
	})
	r.add(Tool{
		Def: def("spawn_subagent", "Create a new subagent worker.", obj(map[string]any{
			"name":  str("optional unique name"),
			"role":  str("specialisation and instructions"),
			"model": str("optional model override"),
			"task":  str("optional first task"),
		}, "role")),
		Run: spawnSubagent,
	})
	r.add(Tool{
		Def: def("broadcast", "Send a message to every other agent.", obj(map[string]any{
			"text": str("message text"),
		}, "text")),
		Run: broadcast,
	})
	r.add(Tool{
		Def: def("wake_up", "Revive a stuck or failed agent. Use it when a subagent is in error, loops forever or a terminal command never returns. Optionally give it a fresh instruction.", obj(map[string]any{
			"agent": str("subagent name or id"),
			"task":  str("optional instruction to resume with"),
		}, "agent")),
		Run: wakeUp,
	})
	r.add(Tool{
		Def: def("wake_up_all", "Revive every inactive or failed subagent at once.", obj(map[string]any{
			"task": str("optional instruction to resume with"),
		})),
		Run: wakeUpAll,
	})
}

func registerSub(r *Registry) {
	r.add(Tool{
		Def: def("wait_for_chief", "Block until the chief sends data. Subagents cannot write while waiting; they only receive.", obj(map[string]any{})),
		Run: waitForChief,
	})
	r.add(Tool{
		Def: def("send_to_chief", "Send a result, question or any payload to the chief.", obj(map[string]any{
			"text": str("payload"),
		}, "text")),
		Run: sendToChief,
	})
}

func createPublicTodo(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Title    string `json:"title"`
		Detail   string `json:"detail"`
		Priority int    `json:"priority"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	todo, err := env.Store.CreateTodo(true, "", in.Title, in.Detail, env.Agent.Name, in.Priority)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("public todo %s created and broadcast to all agents", todo.ID), nil
}

func createPrivateTodo(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Title    string `json:"title"`
		Detail   string `json:"detail"`
		Priority int    `json:"priority"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	todo, err := env.Store.CreateTodo(false, env.Agent.Name, in.Title, in.Detail, env.Agent.Name, in.Priority)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("private todo %s created for %s", todo.ID, env.Agent.Name), nil
}

func updatePublicTodo(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	return updateTodo(ctx, env, args, true)
}

func updatePrivateTodo(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	return updateTodo(ctx, env, args, false)
}

func updateTodo(ctx context.Context, env *Env, args json.RawMessage, public bool) (string, error) {
	var in struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Detail   string `json:"detail"`
		Status   string `json:"status"`
		Priority *int   `json:"priority"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	todo, ok := env.Store.GetTodo(in.ID)
	if !ok {
		return "", fmt.Errorf("todo %q not found", in.ID)
	}
	if todo.Public != public {
		scope := "private"
		if public {
			scope = "public"
		}
		return "", fmt.Errorf("todo %q is not a %s todo", in.ID, scope)
	}
	if !public && todo.Owner != env.Agent.Name {
		return "", fmt.Errorf("private todo %q belongs to %s", in.ID, todo.Owner)
	}
	patch := store.TodoPatch{Priority: in.Priority}
	if in.Title != "" {
		patch.Title = &in.Title
	}
	if in.Detail != "" {
		patch.Detail = &in.Detail
	}
	if in.Status != "" {
		status := store.Status(in.Status)
		patch.Status = &status
	}
	updated, err := env.Store.UpdateTodo(env.Agent.Name, in.ID, patch)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("todo %s updated: status=%s", updated.ID, updated.Status), nil
}

func listAgents(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "store revision %d\n", env.Store.Revision())
	for _, a := range env.Bus.List() {
		fmt.Fprintf(&b, "- %s kind=%s state=%s model=%s public=%d private=%d\n",
			a.Name, a.Kind, a.State(), a.Model,
			len(env.Store.PublicTodos()), len(env.Store.PrivateTodos(a.Name)))
	}
	return b.String(), nil
}

func sharedMemory(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Action string `json:"action"`
		Key    string `json:"key"`
		Value  string `json:"value"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	switch strings.ToLower(in.Action) {
	case "set":
		if in.Key == "" {
			return "", fmt.Errorf("key is required")
		}
		env.Store.SharedSet(in.Key, in.Value, env.Agent.Name)
		return fmt.Sprintf("shared memory %q updated", in.Key), nil
	case "get":
		value, ok := env.Store.SharedGet(in.Key)
		if !ok {
			return "", fmt.Errorf("shared memory %q not found", in.Key)
		}
		return value, nil
	case "delete":
		env.Store.SharedDelete(in.Key, env.Agent.Name)
		return fmt.Sprintf("shared memory %q deleted", in.Key), nil
	case "list":
		all := env.Store.SharedAll()
		if len(all) == 0 {
			return "shared memory is empty", nil
		}
		keys := make([]string, 0, len(all))
		for k := range all {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		for _, k := range keys {
			fmt.Fprintf(&b, "%s = %s\n", k, all[k])
		}
		return b.String(), nil
	default:
		return "", fmt.Errorf("unknown action %q", in.Action)
	}
}

func checkpoint(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Action string `json:"action"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if env.Hooks.Checkpoint == nil {
		return "", fmt.Errorf("checkpointing is not available")
	}
	return env.Hooks.Checkpoint(env.Agent.Name, strings.ToLower(in.Action), in.Name)
}

func think(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Thought string `json:"thought"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Thought) == "" {
		return "", fmt.Errorf("thought is required")
	}
	return "noted", nil
}

func waitForChief(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	if env.Agent.Kind != bus.KindSub {
		return "", fmt.Errorf("wait_for_chief is available to subagents only")
	}
	env.Bus.Publish(bus.Event{Type: "tool", Agent: env.Agent.Name, Text: "waiting for chief"})
	msg, err := env.Bus.Wait(ctx, env.Agent.ID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("message from %s: %s", msg.From, msg.Text), nil
}

func sendToChief(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	if env.Agent.Kind != bus.KindSub {
		return "", fmt.Errorf("send_to_chief is available to subagents only")
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Text) == "" {
		return "", fmt.Errorf("text is required")
	}
	target := env.Agent.Parent
	if target == "" {
		if chief := env.Bus.Chief(); chief != nil {
			target = chief.Name
		}
	}
	if target == "" {
		return "", fmt.Errorf("no chief agent registered")
	}
	if err := env.Bus.Send(bus.Envelope{From: env.Agent.Name, To: target, Kind: "result", Text: in.Text}); err != nil {
		return "", err
	}
	return "delivered to " + target, nil
}

func sendToSub(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	if env.Agent.Kind != bus.KindChief {
		return "", fmt.Errorf("send_to_sub is available to the chief only")
	}
	var in struct {
		Agent string `json:"agent"`
		Task  string `json:"task"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	target := env.Bus.Get(in.Agent)
	if target == nil {
		return "", fmt.Errorf("unknown subagent %q", in.Agent)
	}
	if target.Kind != bus.KindSub {
		return "", fmt.Errorf("%q is not a subagent", in.Agent)
	}
	if err := env.Bus.Send(bus.Envelope{From: env.Agent.Name, To: target.Name, Kind: "task", Text: in.Task}); err != nil {
		return "", err
	}
	return "task queued for " + target.Name, nil
}

func stopWait(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	if env.Agent.Kind != bus.KindChief {
		return "", fmt.Errorf("stop_wait is available to the chief only")
	}
	var in struct {
		Agent string `json:"agent"`
		Task  string `json:"task"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	target := env.Bus.Get(in.Agent)
	if target == nil {
		return "", fmt.Errorf("unknown subagent %q", in.Agent)
	}
	if err := env.Bus.StopWait(in.Agent, in.Task); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s released from wait with a new task", target.Name), nil
}

func spawnSubagent(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	if env.Agent.Kind != bus.KindChief {
		return "", fmt.Errorf("spawn_subagent is available to the chief only")
	}
	var in struct {
		Name  string `json:"name"`
		Role  string `json:"role"`
		Model string `json:"model"`
		Task  string `json:"task"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Role) == "" {
		return "", fmt.Errorf("role is required")
	}
	if env.Hooks.Spawn == nil {
		return "", fmt.Errorf("spawning is not available")
	}
	agentID, err := env.Hooks.Spawn(SpawnSpec{Name: in.Name, Role: in.Role, Model: in.Model, Task: in.Task})
	if err != nil {
		return "", err
	}
	return "subagent spawned: " + agentID, nil
}

func broadcast(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	if env.Agent.Kind != bus.KindChief {
		return "", fmt.Errorf("broadcast is available to the chief only")
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Text) == "" {
		return "", fmt.Errorf("text is required")
	}
	count := env.Bus.Broadcast(env.Agent.Name, in.Text)
	return fmt.Sprintf("broadcast delivered to %d agents", count), nil
}

func wakeUp(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	if env.Agent.Kind != bus.KindChief {
		return "", fmt.Errorf("wake_up is available to the chief only")
	}
	var in struct {
		Agent string `json:"agent"`
		Task  string `json:"task"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Agent) == "" {
		return "", fmt.Errorf("agent is required")
	}
	if env.Hooks.Wake == nil {
		return "", fmt.Errorf("waking is not available")
	}
	name, err := env.Hooks.Wake(in.Agent, in.Task)
	if err != nil {
		return "", err
	}
	return "woke " + name, nil
}

func wakeUpAll(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	if env.Agent.Kind != bus.KindChief {
		return "", fmt.Errorf("wake_up_all is available to the chief only")
	}
	var in struct {
		Task string `json:"task"`
	}
	_ = json.Unmarshal(args, &in)
	if env.Hooks.WakeAll == nil {
		return "", fmt.Errorf("waking is not available")
	}
	woken, err := env.Hooks.WakeAll(in.Task)
	if err != nil {
		return "", err
	}
	if len(woken) == 0 {
		return "no subagents needed waking", nil
	}
	return "woke " + strings.Join(woken, ", "), nil
}
