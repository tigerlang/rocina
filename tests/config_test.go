package tests

import (
	"os"
	"path/filepath"
	"testing"

	"rocina/internal/config"
)

func TestConfigDefaultsAndKinds(t *testing.T) {
	cfg := config.Default()
	if cfg.Provider != "openai" || cfg.MaxSubagents == 0 || cfg.MaxSteps == 0 {
		t.Fatalf("unexpected defaults %+v", cfg)
	}
	cases := map[string]config.Kind{
		"openai":     config.KindOpenAI,
		"anthropic":  config.KindClaude,
		"claude":     config.KindClaude,
		"openrouter": config.KindOpenRouter,
		"other":      config.KindOther,
	}
	for provider, want := range cases {
		cfg.Provider = provider
		if got := cfg.Kind(); got != want {
			t.Fatalf("provider %q resolved to %q, want %q", provider, got, want)
		}
	}
}

func TestConfigLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rocina.json")
	body := `{"provider":"other","base_url":"https://api.example.com/v1","model":"test-model"}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Kind() != config.KindOther || cfg.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("file not applied: %+v", cfg)
	}
	if _, err := config.Load(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("expected explicit missing config to fail")
	}
}

func TestConfigEnvOverride(t *testing.T) {
	t.Setenv("ROCINA_PROVIDER", "claude")
	t.Setenv("ROCINA_API_KEY", "secret")
	t.Setenv("ROCINA_MODEL", "claude-sonnet")
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Provider != "claude" || cfg.APIKey != "secret" || cfg.Model != "claude-sonnet" {
		t.Fatalf("env override failed: %+v", cfg)
	}
}
