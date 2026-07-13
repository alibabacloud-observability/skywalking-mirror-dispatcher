package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/alibabacloud-observability/skywalking-mirror-dispatcher/internal/app"
	"github.com/alibabacloud-observability/skywalking-mirror-dispatcher/internal/config"
	"github.com/alibabacloud-observability/skywalking-mirror-dispatcher/internal/logging"
	"go.uber.org/zap"
)

func main() {
	os.Exit(run())
}

func run() int {
	logConfig, err := logging.LoadConfig()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "initialize logging: %v\n", err)
		return 1
	}
	logger, closeLogger, err := logging.New(logConfig)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "initialize logging: %v\n", err)
		return 1
	}
	defer func() { _ = closeLogger() }()
	logger.Info("logging initialized", zap.String("file", logConfig.FilePath), zap.Bool("stdout", logConfig.Stdout))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", zap.Error(err))
		return 1
	}
	logger.Info("configuration loaded", zap.Any("config", cfg.Summary()))
	service, err := app.New(cfg, logger)
	if err != nil {
		logger.Error("initialize service", zap.Error(err))
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := service.Run(ctx); err != nil {
		logger.Error("service stopped with error", zap.Error(err))
		return 1
	}
	return 0
}
