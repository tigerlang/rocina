package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Policy struct {
	BlockUserPaths   bool
	BlockSystemPaths bool
	AskBeforeRun     bool
	AllowedRoots     []string
	Workspace        string
}

type Approval struct {
	Action string
	Detail string
}

type Approver func(Approval) bool

func (p Policy) Check(path string) error {
	if (!p.BlockUserPaths && !p.BlockSystemPaths) || strings.TrimSpace(path) == "" {
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
	if p.BlockUserPaths {
		for _, blocked := range blockedUserRoots() {
			if within(abs, blocked) {
				return fmt.Errorf("access to protected user path %s is blocked by security settings", abs)
			}
		}
	}
	if p.BlockSystemPaths {
		for _, blocked := range blockedSystemRoots() {
			if within(abs, blocked) {
				return fmt.Errorf("access to protected system path %s is blocked by security settings", abs)
			}
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
	absRoot = filepath.Clean(absRoot)
	rel, err := filepath.Rel(absRoot, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// blockedUserRoots lists personal directories that must stay private. Extra
// roots are read from the XDG user dirs file on Linux so localised or relocated
// folders are covered too.
func blockedUserRoots() []string {
	var roots []string
	home, err := os.UserHomeDir()
	if err != nil {
		return roots
	}
	roots = append(roots, home)
	names := []string{
		"Documents", "Desktop", "Downloads", "Pictures", "Music", "Videos",
		"Public", "Templates", "Library", "Applications",
		".ssh", ".gnupg", ".aws", ".kube", ".docker", ".mozilla", ".thunderbird",
		".bash_history", ".zsh_history", ".netrc", ".gitconfig", ".git-credentials",
		"AppData", "OneDrive",
	}
	for _, name := range names {
		roots = append(roots, filepath.Join(home, name))
	}
	roots = append(roots, xdgUserDirs(home)...)
	switch runtime.GOOS {
	case "windows":
		roots = append(roots, filepath.Dir(home))
		roots = append(roots, filepath.Join(home, "AppData", "Roaming"),
			filepath.Join(home, "AppData", "Local"))
	case "darwin":
		roots = append(roots, "/Users")
	default:
		roots = append(roots, "/home")
	}
	return roots
}

// xdgUserDirs reads ~/.config/user-dirs.dirs, the source of truth for the real
// Documents/Downloads/etc locations on Linux.
func xdgUserDirs(home string) []string {
	data, err := os.ReadFile(filepath.Join(home, ".config", "user-dirs.dirs"))
	if err != nil {
		return nil
	}
	var roots []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "XDG_") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		value := strings.Trim(strings.TrimSpace(line[eq+1:]), `"`)
		value = strings.ReplaceAll(value, "$HOME", home)
		if strings.HasPrefix(value, "/") {
			roots = append(roots, value)
		}
	}
	return roots
}

// blockedSystemRoots lists OS-owned locations. Workspace and allowed_roots still
// take precedence, so projects inside them remain reachable.
func blockedSystemRoots() []string {
	switch runtime.GOOS {
	case "windows":
		var roots []string
		for _, key := range []string{"SystemRoot", "windir", "ProgramFiles", "ProgramFiles(x86)", "ProgramData"} {
			if value := os.Getenv(key); value != "" {
				roots = append(roots, value)
			}
		}
		if systemDrive := os.Getenv("SystemDrive"); systemDrive != "" {
			roots = append(roots, filepath.Join(systemDrive+string(filepath.Separator), "Windows"))
		}
		return roots
	case "darwin":
		return []string{"/System", "/Library", "/bin", "/sbin", "/usr", "/private", "/Applications", "/cores", "/dev", "/Volumes"}
	default:
		return []string{"/etc", "/bin", "/sbin", "/usr", "/lib", "/lib64", "/lib32", "/boot", "/dev", "/proc", "/sys", "/root", "/var", "/opt", "/srv", "/run"}
	}
}
