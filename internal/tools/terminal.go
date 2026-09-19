package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"rocina/internal/id"
)

type Terminal struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *bufio.Reader
	closed bool
}

func newTerminal() (*Terminal, error) {
	cmd := exec.Command("bash", "--norc")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Terminal{cmd: cmd, stdin: stdin, out: bufio.NewReader(stdout)}, nil
}

func (t *Terminal) Run(ctx context.Context, command string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return "", fmt.Errorf("terminal session is closed")
	}
	token := id.New("term")
	script := fmt.Sprintf("%s\nprintf '\\n__ROCINA_END_%s__\\n'\n", command, token)
	if _, err := io.WriteString(t.stdin, script); err != nil {
		t.shutdown()
		return "", err
	}

	type result struct {
		data string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		var buf strings.Builder
		for {
			line, err := t.out.ReadString('\n')
			if err != nil {
				ch <- result{buf.String(), err}
				return
			}
			if strings.Contains(line, "__ROCINA_END_"+token+"__") {
				ch <- result{buf.String(), nil}
				return
			}
			buf.WriteString(line)
		}
	}()

	select {
	case res := <-ch:
		if res.err != nil {
			t.shutdown()
			return res.data, res.err
		}
		return strings.TrimRight(res.data, "\n"), nil
	case <-ctx.Done():
		t.shutdown()
		return "", ctx.Err()
	}
}

func (t *Terminal) shutdown() {
	if t.closed {
		return
	}
	t.closed = true
	_ = t.stdin.Close()
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	_ = t.cmd.Wait()
}
