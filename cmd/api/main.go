package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/CarambaG/taskflow/internal/app"
	"github.com/CarambaG/taskflow/internal/config"
)

func main() {
	if err := run(); err != nil {
		slog.Error("application stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := app.NewLogger(cfg.LogLevel)
	slog.SetDefault(logger)
	application, err := app.New(context.Background(), cfg, logger)
	if err != nil {
		return fmt.Errorf("initialize application: %w", err)
	}
	if err := application.Run(); err != nil {
		return fmt.Errorf("run application: %w", err)
	}
	return nil
}
