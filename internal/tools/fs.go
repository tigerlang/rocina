package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func registerFS(r *Registry) {
	r.add(Tool{
		Def: def("read_file", "Read a UTF-8 text file.", obj(map[string]any{
			"path": str("file path, absolute or relative to the workspace"),
		}, "path")),
		Run: runReadFile,
	})
	r.add(Tool{
		Def: def("write_file", "Create or overwrite a text file, creating parent directories.", obj(map[string]any{
			"path":    str("file path"),
			"content": str("full file content"),
		}, "path", "content")),
		Run: runWriteFile,
	})
	r.add(Tool{
		Def: def("edit_file", "Replace the first occurrence of a string in a file.", obj(map[string]any{
			"path": str("file path"),
			"old":  str("text to replace"),
			"new":  str("replacement text"),
		}, "path", "old", "new")),
		Run: runEditFile,
	})
	r.add(Tool{
		Def: def("list_dir", "List directory entries with sizes.", obj(map[string]any{
			"path": str("directory path, default is the workspace"),
		})),
		Run: runListDir,
	})
	r.add(Tool{
		Def: def("grep_tool", "Search files for a regular expression.", obj(map[string]any{
			"pattern":     str("regular expression"),
			"path":        str("file or directory, default is the workspace"),
			"max_results": integer("maximum matches, default 200"),
		}, "pattern")),
		Run: runGrep,
	})
}

func resolvePath(env *Env, p string) string {
	if p == "" {
		return env.Workspace
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(env.Workspace, p)
}

func runReadFile(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	path := resolvePath(env, in.Path)
	if err := env.Policy.Check(path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return truncateOutput(string(data)), nil
}

func runWriteFile(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	path := resolvePath(env, in.Path)
	if err := env.Policy.Check(path); err != nil {
		return "", err
	}
	if err := env.authorize("write_file", path); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(in.Content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(in.Content), path), nil
}

func runEditFile(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Path string `json:"path"`
		Old  string `json:"old"`
		New  string `json:"new"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if in.Old == "" {
		return "", fmt.Errorf("old is required")
	}
	path := resolvePath(env, in.Path)
	if err := env.Policy.Check(path); err != nil {
		return "", err
	}
	if err := env.authorize("edit_file", path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := string(data)
	idx := strings.Index(content, in.Old)
	if idx < 0 {
		return "", fmt.Errorf("old text not found in %s", path)
	}
	updated := content[:idx] + in.New + content[idx+len(in.Old):]
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s", path), nil
}

func runListDir(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(args, &in)
	dir := resolvePath(env, in.Path)
	if err := env.Policy.Check(dir); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		kind := "-"
		if e.IsDir() {
			kind = "d"
		}
		names = append(names, fmt.Sprintf("%s %10d %s", kind, info.Size(), e.Name()))
	}
	sort.Strings(names)
	return strings.Join(names, "\n"), nil
}

func runGrep(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	re, err := regexp.Compile(in.Pattern)
	if err != nil {
		return "", err
	}
	root := resolvePath(env, in.Path)
	if err := env.Policy.Check(root); err != nil {
		return "", err
	}
	max := in.MaxResults
	if max <= 0 {
		max = 200
	}
	var matches []string
	walk := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if len(matches) >= max {
			return filepath.SkipAll
		}
		info, err := d.Info()
		if err != nil || info.Size() > 2<<20 {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if re.MatchString(line) {
				matches = append(matches, fmt.Sprintf("%s:%d: %s", path, lineNo, line))
				if len(matches) >= max {
					return filepath.SkipAll
				}
			}
		}
		return nil
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		_ = filepath.WalkDir(root, walk)
	} else {
		_ = walk(root, fs.FileInfoToDirEntry(info), nil)
	}
	if len(matches) == 0 {
		return "no matches", nil
	}
	return strings.Join(matches, "\n"), nil
}
