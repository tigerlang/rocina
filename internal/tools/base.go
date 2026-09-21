package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

func registerBase(r *Registry) {
	r.add(Tool{
		Def: def("bash_tool", "Run a shell command in the workspace and return combined stdout/stderr plus the exit status.", obj(map[string]any{
			"command":         str("shell command to execute"),
			"timeout_seconds": integer("optional timeout, default 60"),
		}, "command")),
		Run: runBash,
	})
	r.add(Tool{
		Def: def("terminal_tool", "Run a command in a persistent shell session so working directory and exported variables survive between calls.", obj(map[string]any{
			"command": str("shell command to execute in the persistent session"),
		}, "command")),
		Run: runTerminal,
	})
	r.add(Tool{
		Def: def("web_tool", "Fetch a URL over HTTP and return the status code and body.", obj(map[string]any{
			"url":       str("absolute URL"),
			"method":    str("HTTP method, default GET"),
			"body":      str("optional request body"),
			"max_bytes": integer("optional response size cap, default 24000"),
		}, "url")),
		Run: runWeb,
	})
}

func runBash(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Command) == "" {
		return "", fmt.Errorf("command is required")
	}
	if err := env.authorize("bash_tool", in.Command); err != nil {
		return "", err
	}
	timeout := in.TimeoutSeconds
	if timeout <= 0 {
		timeout = 60
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, "bash", "-lc", in.Command)
	cmd.Dir = env.Workspace
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	combined := truncateOutput(out.String())
	if cctx.Err() == context.DeadlineExceeded {
		return combined, fmt.Errorf("command timed out after %ds", timeout)
	}
	if err != nil {
		return fmt.Sprintf("%s\n[exit: %v]", combined, err), nil
	}
	return combined, nil
}

func runTerminal(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Command) == "" {
		return "", fmt.Errorf("command is required")
	}
	if err := env.authorize("terminal_tool", in.Command); err != nil {
		return "", err
	}
	term, err := env.terminal()
	if err != nil {
		return "", err
	}
	out, err := term.Run(ctx, in.Command)
	if cwd := term.Cwd(); cwd != "" {
		env.SetCwd(cwd)
	}
	if err != nil {
		return out, err
	}
	return out, nil
}

func runWeb(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		URL      string `json:"url"`
		Method   string `json:"method"`
		Body     string `json:"body"`
		MaxBytes int    `json:"max_bytes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.URL) == "" {
		return "", fmt.Errorf("url is required")
	}
	method := strings.ToUpper(strings.TrimSpace(in.Method))
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, in.URL, strings.NewReader(in.Body))
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Rocina/0.1")
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	limit := in.MaxBytes
	if limit <= 0 {
		limit = 24000
	}
	if limit > 24000 {
		limit = 24000
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("status: %d\n%s", resp.StatusCode, truncateOutput(string(body))), nil
}

const outputLimit = 16000

func truncateOutput(s string) string {
	if len(s) <= outputLimit {
		return s
	}
	return s[:outputLimit] + "\n...[truncated]"
}
