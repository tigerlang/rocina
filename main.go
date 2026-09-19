package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"rocina/internal/config"
	"rocina/internal/orchestrator"
	"rocina/internal/session"
	"rocina/internal/tui"
	"rocina/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "rocina:", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("rocina", flag.ContinueOnError)
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.BoolVar(showVersion, "v", false, "print version and exit (shorthand)")
	configPath := fs.String("config", "", "path to a json config file")
	provider := fs.String("provider", "", "provider: openai|claude|openrouter|other")
	apiKey := fs.String("api-key", "", "api key")
	baseURL := fs.String("base-url", "", "base url override")
	model := fs.String("model", "", "default model id")
	chiefModel := fs.String("chief-model", "", "chief model id")
	subModel := fs.String("sub-model", "", "subagent model id")
	dataDir := fs.String("data-dir", "", "data directory")
	workspace := fs.String("workspace", "", "workspace directory")
	goal := fs.String("goal", "", "goal for the chief agent")
	headless := fs.Bool("headless", false, "run without the TUI")
	maxSubs := fs.Int("max-subagents", 0, "maximum number of subagents")
	maxSteps := fs.Int("max-steps", 0, "tool steps per turn")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}

	if *showVersion {
		fmt.Printf("rocina %s\n", version.Version)
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	applyString := func(dst *string, value string) {
		if value != "" {
			*dst = value
		}
	}
	applyString(&cfg.Provider, *provider)
	applyString(&cfg.APIKey, *apiKey)
	applyString(&cfg.BaseURL, *baseURL)
	applyString(&cfg.Model, *model)
	applyString(&cfg.ChiefModel, *chiefModel)
	applyString(&cfg.SubModel, *subModel)
	applyString(&cfg.DataDir, *dataDir)
	applyString(&cfg.Workspace, *workspace)
	applyString(&cfg.Goal, *goal)
	if *headless {
		cfg.Headless = true
	}
	if *maxSubs > 0 {
		cfg.MaxSubagents = *maxSubs
	}
	if *maxSteps > 0 {
		cfg.MaxSteps = *maxSteps
	}

	orch, err := orchestrator.New(cfg)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if cfg.Headless {
		if cfg.Goal == "" {
			return fmt.Errorf("--goal is required in headless mode")
		}
		if err := orch.Start(ctx); err != nil {
			return err
		}
		if err := orch.Submit(cfg.Goal); err != nil {
			return err
		}
		orch.Wait()
		if ctx.Err() == nil {
			fmt.Println("rocina: chief finished")
		}
		return nil
	}

	manager, err := session.NewManager(ctx, cfg)
	if err != nil {
		return err
	}
	if cfg.Goal != "" {
		if active := manager.Active(); active != nil {
			if err := active.Orch.Submit(cfg.Goal); err != nil {
				return err
			}
		}
	}
	program := tea.NewProgram(tui.New(manager), tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err = program.Run()
	cancel()
	for _, s := range manager.List() {
		s.Orch.Wait()
	}
	return err
}
