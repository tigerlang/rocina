package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Kind string

const (
	KindOpenAI     Kind = "openai"
	KindClaude     Kind = "claude"
	KindOpenRouter Kind = "openrouter"
	KindOther      Kind = "other"
)

type Security struct {
	BlockUserPaths bool     `json:"block_user_paths"`
	AskBeforeRun   bool     `json:"ask_before_run"`
	AllowedRoots   []string `json:"allowed_roots,omitempty"`
}

type Config struct {
	Provider     string   `json:"provider"`
	APIKey       string   `json:"api_key"`
	BaseURL      string   `json:"base_url"`
	Model        string   `json:"model"`
	ChiefModel   string   `json:"chief_model"`
	SubModel     string   `json:"sub_model"`
	DataDir      string   `json:"data_dir"`
	Workspace    string   `json:"workspace"`
	MaxSubagents int      `json:"max_subagents"`
	MaxSteps     int      `json:"max_steps"`
	Temperature  float64  `json:"temperature"`
	Goal         string   `json:"goal"`
	Headless     bool     `json:"headless"`
	Security     Security `json:"security"`
}

func Default() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Provider:     "openai",
		DataDir:      filepath.Join(home, ".rocina"),
		Workspace:    ".",
		MaxSubagents: 4,
		MaxSteps:     40,
		Temperature:  0.2,
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		if err := mergeFile(&cfg, path); err != nil {
			return cfg, err
		}
		applyEnv(&cfg)
		return cfg, nil
	}
	for _, candidate := range defaultPaths() {
		if err := mergeFile(&cfg, candidate); err == nil {
			break
		}
	}
	applyEnv(&cfg)
	return cfg, nil
}

func defaultPaths() []string {
	paths := []string{"rocina.json"}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "rocina", "config.json"))
	}
	return paths
}

func UserConfigPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "rocina", "config.json")
	}
	return "rocina.json"
}

func Save(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func mergeFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func (c Config) Kind() Kind {
	switch strings.ToLower(c.Provider) {
	case "claude", "anthropic":
		return KindClaude
	case "openrouter":
		return KindOpenRouter
	case "other":
		return KindOther
	default:
		return KindOpenAI
	}
}

func applyEnv(c *Config) {
	set := func(dst *string, key string) {
		if v := os.Getenv(key); v != "" {
			*dst = v
		}
	}
	set(&c.Provider, "ROCINA_PROVIDER")
	set(&c.APIKey, "ROCINA_API_KEY")
	set(&c.BaseURL, "ROCINA_BASE_URL")
	set(&c.Model, "ROCINA_MODEL")
	set(&c.ChiefModel, "ROCINA_CHIEF_MODEL")
	set(&c.SubModel, "ROCINA_SUB_MODEL")
	set(&c.DataDir, "ROCINA_DATA_DIR")
	set(&c.Workspace, "ROCINA_WORKSPACE")
	set(&c.Goal, "ROCINA_GOAL")
	if v := os.Getenv("ROCINA_HEADLESS"); v == "1" || strings.EqualFold(v, "true") {
		c.Headless = true
	}
}
