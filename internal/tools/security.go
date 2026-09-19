package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Policy struct {
	BlockUserPaths bool
	AskBeforeRun   bool
	AllowedRoots   []string
	Workspace      string
}

type Approval struct {
	Action string
	Detail string
}

type Approver func(Approval) bool

func (p Policy) Check(path string) error {
	if !p.BlockUserPaths || strings.TrimSpace(path) == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	abs = filepath.Clean(abs)
	if p.Workspace != "" && within(abs, p.Workspace) {
		return nil
	}
	for _, root := range p.AllowedRoots {
		if within(abs, root) {
			return nil
		}
	}
	for _, blocked := range blockedUserRoots() {
		if within(abs, blocked) {
			return fmt.Errorf("access to protected user path %s is blocked by security settings", abs)
		}
	}
	return nil
}

func (e *Env) authorize(action, detail string) error {
	if !e.Policy.AskBeforeRun {
		return nil
	}
	if e.Approve == nil {
		return fmt.Errorf("approval required but no interactive approver is connected")
	}
	if !e.Approve(Approval{Action: action, Detail: detail}) {
		return fmt.Errorf("%s denied by user", action)
	}
	return nil
}

func within(path, root string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(absRoot), path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func blockedUserRoots() []string {
	var roots []string
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, home)
		for _, name := range []string{"Documents", "Desktop", "Downloads", "Pictures", "Music", "Videos", "Library", ".ssh", ".gnupg"} {
			roots = append(roots, filepath.Join(home, name))
		}
		switch runtime.GOOS {
		case "windows":
			roots = append(roots, filepath.Dir(home))
		case "darwin":
			roots = append(roots, "/Users")
		}
	}
	return roots
}
