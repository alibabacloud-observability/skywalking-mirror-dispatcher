package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/alibabacloud-observability/skywalking-mirror-dispatcher/internal/app"
	"github.com/alibabacloud-observability/skywalking-mirror-dispatcher/internal/config"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	logger.Info("configuration loaded", "config", cfg.Summary())
	service, err := app.New(cfg, logger)
	if err != nil {
		logger.Error("initialize service", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := service.Run(ctx); err != nil {
		logger.Error("service stopped with error", "error", err)
		os.Exit(1)
	}
}
