package tests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"rocina/internal/bus"
	"rocina/internal/store"
	"rocina/internal/tools"
)

func newToolEnv(t *testing.T, kind bus.Kind) (*tools.Env, *bus.Bus) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	b := bus.New(st)
	chief := b.Register("chief", bus.KindChief, "chief", "", "m")
	sub := b.Register("sub-1", bus.KindSub, "worker", "chief", "m")
	agent := sub
	if kind == bus.KindChief {
		agent = chief
	}
	return &tools.Env{Agent: agent, Bus: b, Store: st, Workspace: t.TempDir()}, b
}

func runTool(t *testing.T, env *tools.Env, kind bus.Kind, name string, args any) (string, error) {
	t.Helper()
	registry := tools.NewRegistry(kind)
	tool, ok := registry.Get(name)
	if !ok {
		t.Fatalf("tool %q is not registered for %s", name, kind)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return tool.Run(context.Background(), env, raw)
}

func TestBashToolRunsInWorkspace(t *testing.T) {
	env, _ := newToolEnv(t, bus.KindChief)
	out, err := runTool(t, env, bus.KindChief, "bash_tool", map[string]any{"command": "echo rocina && pwd"})
	if err != nil {
		t.Fatalf("bash_tool: %v", err)
	}
	if !strings.Contains(out, "rocina") {
		t.Fatalf("unexpected output %q", out)
	}
}

func TestFileTools(t *testing.T) {
	env, _ := newToolEnv(t, bus.KindSub)
	if _, err := runTool(t, env, bus.KindSub, "write_file", map[string]any{"path": "notes/a.txt", "content": "hello world"}); err != nil {
		t.Fatalf("write_file: %v", err)
	}
	out, err := runTool(t, env, bus.KindSub, "read_file", map[string]any{"path": "notes/a.txt"})
	if err != nil || !strings.Contains(out, "hello world") {
		t.Fatalf("read_file: %q %v", out, err)
	}
	if _, err := runTool(t, env, bus.KindSub, "edit_file", map[string]any{"path": "notes/a.txt", "old": "world", "new": "rocina"}); err != nil {
		t.Fatalf("edit_file: %v", err)
	}
	out, _ = runTool(t, env, bus.KindSub, "read_file", map[string]any{"path": "notes/a.txt"})
	if !strings.Contains(out, "rocina") {
		t.Fatalf("edit_file did not apply: %q", out)
	}
	out, err = runTool(t, env, bus.KindSub, "list_dir", map[string]any{"path": "notes"})
	if err != nil || !strings.Contains(out, "a.txt") {
		t.Fatalf("list_dir: %q %v", out, err)
	}
	out, err = runTool(t, env, bus.KindSub, "grep_tool", map[string]any{"pattern": "rocina", "path": "."})
	if err != nil || !strings.Contains(out, "a.txt") {
		t.Fatalf("grep_tool: %q %v", out, err)
	}
}

func TestTodoToolsScopes(t *testing.T) {
	env, _ := newToolEnv(t, bus.KindChief)
	if _, err := runTool(t, env, bus.KindChief, "public_todo", map[string]any{"title": "plan", "priority": 2}); err != nil {
		t.Fatalf("public_todo: %v", err)
	}
	if _, err := runTool(t, env, bus.KindChief, "private_todo", map[string]any{"title": "scratch"}); err != nil {
		t.Fatalf("private_todo: %v", err)
	}
	public := env.Store.PublicTodos()
	private := env.Store.PrivateTodos("chief")
	if len(public) != 1 || len(private) != 1 {
		t.Fatalf("unexpected todo counts public=%d private=%d", len(public), len(private))
	}
	out, err := runTool(t, env, bus.KindChief, "update_public_todo", map[string]any{"id": public[0].ID, "status": "in_progress"})
	if err != nil || !strings.Contains(out, "in_progress") {
		t.Fatalf("update_public_todo: %q %v", out, err)
	}
	if _, err := runTool(t, env, bus.KindChief, "update_public_todo", map[string]any{"id": private[0].ID}); err == nil {
		t.Fatal("expected scope guard to reject private todo through update_public_todo")
	}
	if _, err := runTool(t, env, bus.KindChief, "update_private_todo", map[string]any{"id": public[0].ID}); err == nil {
		t.Fatal("expected scope guard to reject public todo through update_private_todo")
	}
}

func TestSharedMemoryTool(t *testing.T) {
	env, _ := newToolEnv(t, bus.KindChief)
	if _, err := runTool(t, env, bus.KindChief, "shared_memory", map[string]any{"action": "set", "key": "k", "value": "v"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	out, err := runTool(t, env, bus.KindChief, "shared_memory", map[string]any{"action": "get", "key": "k"})
	if err != nil || out != "v" {
		t.Fatalf("get: %q %v", out, err)
	}
	out, err = runTool(t, env, bus.KindChief, "shared_memory", map[string]any{"action": "list"})
	if err != nil || !strings.Contains(out, "k = v") {
		t.Fatalf("list: %q %v", out, err)
	}
}

func TestCoordinationToolPermissions(t *testing.T) {
	chief := tools.NewRegistry(bus.KindChief)
	sub := tools.NewRegistry(bus.KindSub)
	for _, name := range []string{"send_to_sub", "stop_wait", "spawn_subagent", "broadcast"} {
		if _, ok := chief.Get(name); !ok {
			t.Fatalf("chief is missing %q", name)
		}
		if _, ok := sub.Get(name); ok {
			t.Fatalf("subagent must not receive %q", name)
		}
	}
	for _, name := range []string{"wait_for_chief", "send_to_chief"} {
		if _, ok := sub.Get(name); !ok {
			t.Fatalf("subagent is missing %q", name)
		}
		if _, ok := chief.Get(name); ok {
			t.Fatalf("chief must not receive %q", name)
		}
	}
}

func TestSendToSubDelivers(t *testing.T) {
	env, b := newToolEnv(t, bus.KindChief)
	if _, err := runTool(t, env, bus.KindChief, "send_to_sub", map[string]any{"agent": "sub-1", "task": "build"}); err != nil {
		t.Fatalf("send_to_sub: %v", err)
	}
	msgs := b.Collect(b.Get("sub-1").ID)
	if len(msgs) != 1 || msgs[0].Text != "build" {
		t.Fatalf("unexpected delivery %+v", msgs)
	}
}

func TestSpawnRequiresHooks(t *testing.T) {
	env, _ := newToolEnv(t, bus.KindChief)
	if _, err := runTool(t, env, bus.KindChief, "spawn_subagent", map[string]any{"role": "worker"}); err == nil {
		t.Fatal("expected spawn to fail without hooks")
	}
}

func TestPolicyBlocksUserPaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	policy := tools.Policy{BlockUserPaths: true, Workspace: t.TempDir()}
	blocked := []string{
		filepath.Join(home, "Documents", "secret.txt"),
		filepath.Join(home, "Downloads", "x"),
		filepath.Join(home, ".ssh", "id_rsa"),
		filepath.Join(home, ".aws", "credentials"),
		filepath.Join(home, ".git-credentials"),
	}
	for _, path := range blocked {
		if err := policy.Check(path); err == nil {
			t.Fatalf("expected %s to be blocked", path)
		}
	}
	if err := policy.Check(filepath.Join(policy.Workspace, "notes.txt")); err != nil {
		t.Fatalf("workspace must stay allowed: %v", err)
	}
	relaxed := tools.Policy{Workspace: t.TempDir()}
	if err := relaxed.Check(filepath.Join(home, "Documents", "secret.txt")); err != nil {
		t.Fatalf("disabled policy must not block: %v", err)
	}
}

func TestPolicyBlocksSystemPaths(t *testing.T) {
	policy := tools.Policy{BlockSystemPaths: true, Workspace: t.TempDir()}
	blocked := []string{"/etc/passwd", "/usr/bin/env", "/bin/sh"}
	if runtime.GOOS == "darwin" {
		blocked = append(blocked, "/System", "/Library")
	}
	for _, path := range blocked {
		if err := policy.Check(path); err == nil {
			t.Fatalf("expected system path %s to be blocked", path)
		}
	}
	allowed := tools.Policy{
		BlockSystemPaths: true,
		AllowedRoots:     []string{"/etc/app"},
	}
	if err := allowed.Check("/etc/app/ok.conf"); err != nil {
		t.Fatalf("allowed root inside a system tree must stay reachable: %v", err)
	}
	relaxed := tools.Policy{}
	if err := relaxed.Check("/etc/passwd"); err != nil {
		t.Fatalf("disabled policy must not block: %v", err)
	}
	if err := policy.Check(policy.Workspace + "/file"); err != nil {
		t.Fatalf("workspace must stay allowed: %v", err)
	}
}

func TestBashRequiresApproval(t *testing.T) {
	env, _ := newToolEnv(t, bus.KindChief)
	env.Policy = tools.Policy{AskBeforeRun: true}
	if _, err := runTool(t, env, bus.KindChief, "bash_tool", map[string]any{"command": "echo hi"}); err == nil {
		t.Fatal("expected bash to require approval")
	}
	env.Approve = func(tools.Approval) bool { return false }
	if _, err := runTool(t, env, bus.KindChief, "bash_tool", map[string]any{"command": "echo hi"}); err == nil {
		t.Fatal("expected denied command to fail")
	}
	env.Approve = func(tools.Approval) bool { return true }
	out, err := runTool(t, env, bus.KindChief, "bash_tool", map[string]any{"command": "echo hi"})
	if err != nil || !strings.Contains(out, "hi") {
		t.Fatalf("approved command failed: %q %v", out, err)
	}
}

func TestTerminalTracksCwd(t *testing.T) {
	env, b := newToolEnv(t, bus.KindSub)
	target := t.TempDir()
	if _, err := runTool(t, env, bus.KindSub, "terminal_tool", map[string]any{"command": "cd " + target}); err != nil {
		t.Fatalf("terminal_tool: %v", err)
	}
	if env.Cwd() != target {
		t.Fatalf("cwd=%q want %q", env.Cwd(), target)
	}
	found := false
	for more := true; more; {
		select {
		case ev := <-b.Events():
			if ev.Type == "cwd" && ev.Text == target {
				found = true
			}
		default:
			more = false
		}
	}
	if !found {
		t.Fatal("expected a cwd event")
	}
}

func TestTerminalStreamsOutput(t *testing.T) {
	env, _ := newToolEnv(t, bus.KindSub)
	var live strings.Builder
	env.Output = func(text string) { live.WriteString(text) }
	if _, err := runTool(t, env, bus.KindSub, "terminal_tool", map[string]any{"command": "for i in 1 2 3; do echo line-$i; done"}); err != nil {
		t.Fatalf("terminal_tool: %v", err)
	}
	if !strings.Contains(live.String(), "line-1") || !strings.Contains(live.String(), "line-3") {
		t.Fatalf("terminal output was not streamed live: %q", live.String())
	}
}

func TestTerminalCapsOutput(t *testing.T) {
	env, _ := newToolEnv(t, bus.KindSub)
	out, err := runTool(t, env, bus.KindSub, "terminal_tool", map[string]any{"command": "printf '" + strings.Repeat("x", 20000) + "'"})
	if err != nil {
		t.Fatalf("terminal_tool: %v", err)
	}
	if len(out) > 5000 {
		t.Fatalf("terminal output was not capped: %d bytes", len(out))
	}
}

func TestThinkTool(t *testing.T) {
	env, _ := newToolEnv(t, bus.KindChief)
	out, err := runTool(t, env, bus.KindChief, "think", map[string]any{"thought": "this is a greeting"})
	if err != nil || out != "noted" {
		t.Fatalf("think: %q %v", out, err)
	}
	if _, err := runTool(t, env, bus.KindChief, "think", map[string]any{}); err == nil {
		t.Fatal("expected empty thought to fail")
	}
	if _, ok := tools.NewRegistry(bus.KindSub).Get("think"); !ok {
		t.Fatal("subagents should also have think")
	}
}
