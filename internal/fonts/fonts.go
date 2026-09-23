// Package fonts installs the monospace fonts Rocina recommends so terminals can
// use them. A TUI cannot change the terminal's font directly, so this downloads
// and registers the files with the OS font system. Monaspace Neon is preferred,
// JetBrains Mono is the fallback; when both fail nothing changes and the system
// default font is kept.
package fonts

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	monaspaceEntry = "Variable Fonts/Monaspace Neon/Monaspace Neon Var.ttf"
	monaspaceName  = "MonaspaceNeonVar.ttf"
	jetbrainsName  = "JetBrainsMonoVar.ttf"
)

// Sources are overridable so tests can exercise the fallback chain offline.
var (
	monaspaceURL = "https://github.com/githubnext/monaspace/releases/download/v1.400/monaspace-variable-v1.400.zip"
	jetbrainsURL = "https://github.com/google/fonts/raw/main/ofl/jetbrainsmono/JetBrainsMono%5Bwght%5D.ttf"
	userFontDir  = defaultUserFontDir
	scanRoots    = defaultFontRoots
)

type Result struct {
	Installed string
	Path      string
	Message   string
}

// SetSourcesForTest overrides download URLs, the target directory and the
// system font scan roots so the fallback chain can be tested offline.
func SetSourcesForTest(monaspace, jetbrains string, dir func() (string, error), roots func() []string) {
	monaspaceURL = monaspace
	jetbrainsURL = jetbrains
	userFontDir = dir
	scanRoots = roots
}

var httpClient = &http.Client{Timeout: 2 * time.Minute}

// Ensure installs Monaspace Neon, then JetBrains Mono, then reports the system
// default. It is safe to call repeatedly: already installed files are reused.
func Ensure() Result {
	dir, err := fontDir()
	if err != nil {
		return Result{Message: "could not locate a font directory: " + err.Error()}
	}
	if path, ok := installed(dir, monaspaceName, "Monaspace Neon"); ok {
		return Result{Installed: "Monaspace Neon", Path: path, Message: "already installed"}
	}
	if path, ok := installed(dir, jetbrainsName, "JetBrains Mono"); ok {
		return Result{Installed: "JetBrains Mono", Path: path, Message: "already installed"}
	}
	if path, err := installMonaspace(dir); err == nil {
		return Result{Installed: "Monaspace Neon", Path: path, Message: "installed"}
	}
	if path, err := installJetBrains(dir); err == nil {
		return Result{Installed: "JetBrains Mono", Path: path, Message: "installed; Monaspace Neon was unavailable"}
	}
	return Result{Message: "no preferred font could be installed; keeping the system default"}
}

func installed(dir, fileName, family string) (string, bool) {
	candidate := filepath.Join(dir, fileName)
	if _, err := os.Stat(candidate); err == nil {
		return candidate, true
	}
	if path, ok := installedByFamily(family); ok {
		return path, true
	}
	return "", false
}

// installedByFamily scans the OS font directories for a file whose name matches
// the family, so fonts installed by other tools are detected too.
func installedByFamily(family string) (string, bool) {
	key := strings.ToLower(strings.ReplaceAll(family, " ", ""))
	found := ""
	for _, root := range fontRoots() {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			name := strings.ToLower(strings.ReplaceAll(filepath.Base(path), " ", ""))
			if strings.Contains(name, key) {
				found = path
				return filepath.SkipAll
			}
			return nil
		})
		if found != "" {
			return found, true
		}
	}
	return "", false
}

func installMonaspace(dir string) (string, error) {
	data, err := download(monaspaceURL)
	if err != nil {
		return "", err
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	for _, file := range reader.File {
		if file.Name != monaspaceEntry {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		return writeFont(dir, monaspaceName, rc)
	}
	return "", fmt.Errorf("monaspace archive is missing %s", monaspaceEntry)
}

func installJetBrains(dir string) (string, error) {
	data, err := download(jetbrainsURL)
	if err != nil {
		return "", err
	}
	return writeFont(dir, jetbrainsName, bytes.NewReader(data))
}

func writeFont(dir, name string, src io.Reader) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	tmp := path + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(file, src); err != nil {
		file.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

func download(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

func fontRoots() []string {
	return scanRoots()
}

func defaultFontRoots() []string {
	var roots []string
	if home, err := os.UserHomeDir(); err == nil {
		switch runtime.GOOS {
		case "windows":
			roots = append(roots, filepath.Join(home, "AppData", "Local", "Microsoft", "Windows", "Fonts"))
		case "darwin":
			roots = append(roots, filepath.Join(home, "Library", "Fonts"))
		default:
			roots = append(roots, filepath.Join(home, ".local", "share", "fonts"), filepath.Join(home, ".fonts"))
		}
	}
	if runtime.GOOS == "linux" {
		roots = append(roots, "/usr/share/fonts", "/usr/local/share/fonts")
	}
	return roots
}
func fontDir() (string, error) {
	return userFontDir()
}

func defaultUserFontDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "Local", "Microsoft", "Windows", "Fonts"), nil
	case "darwin":
		return filepath.Join(home, "Library", "Fonts"), nil
	default:
		return filepath.Join(home, ".local", "share", "fonts"), nil
	}
}
