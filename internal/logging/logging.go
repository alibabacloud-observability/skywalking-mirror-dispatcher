// Package logging creates the process-wide structured logger.
package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	FileName          = "skywalking-mirror.log"
	EnvStdout         = "LOG_STDOUT"
	defaultMaxSizeMB  = 100
	defaultMaxBackups = 5
	defaultMaxAgeDays = 30
)

// Config contains the fixed file rotation settings used by the process.
type Config struct {
	FilePath   string
	MaxSizeMB  int
	MaxBackups int
	MaxAgeDays int
	Compress   bool
	Stdout     bool
}

// LoadConfig places the log file beside the running executable and reads the
// optional stdout switch from the environment.
func LoadConfig() (Config, error) {
	executable, err := os.Executable()
	if err != nil {
		return Config{}, fmt.Errorf("resolve executable path: %w", err)
	}
	return loadConfig(executable, os.LookupEnv)
}

func loadConfig(executable string, lookupEnv func(string) (string, bool)) (Config, error) {
	cfg := Config{
		FilePath:   filepath.Join(filepath.Dir(executable), FileName),
		MaxSizeMB:  defaultMaxSizeMB,
		MaxBackups: defaultMaxBackups,
		MaxAgeDays: defaultMaxAgeDays,
		Compress:   true,
	}
	if value, ok := lookupEnv(EnvStdout); ok {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse %s: %w", EnvStdout, err)
		}
		cfg.Stdout = enabled
	}
	return cfg, nil
}

// New creates a production JSON logger backed by the rotating file and,
// optionally, stdout. The returned cleanup function closes the file sink.
func New(cfg Config) (*zap.Logger, func() error, error) {
	return newLogger(cfg, os.Stdout)
}

func newLogger(cfg Config, stdout io.Writer) (*zap.Logger, func() error, error) {
	if cfg.FilePath == "" {
		return nil, nil, fmt.Errorf("log file path is empty")
	}
	if cfg.MaxSizeMB <= 0 || cfg.MaxBackups < 0 || cfg.MaxAgeDays < 0 {
		return nil, nil, fmt.Errorf("invalid log rotation limits")
	}

	file, err := os.OpenFile(cfg.FilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file %q: %w", cfg.FilePath, err)
	}
	if err := file.Close(); err != nil {
		return nil, nil, fmt.Errorf("close log file %q after validation: %w", cfg.FilePath, err)
	}

	roller := &lumberjack.Logger{
		Filename:   cfg.FilePath,
		MaxSize:    cfg.MaxSizeMB,
		MaxBackups: cfg.MaxBackups,
		MaxAge:     cfg.MaxAgeDays,
		LocalTime:  true,
		Compress:   cfg.Compress,
	}
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeDuration = zapcore.StringDurationEncoder
	outputs := []zapcore.WriteSyncer{zapcore.AddSync(roller)}
	if cfg.Stdout {
		outputs = append(outputs, zapcore.Lock(zapcore.AddSync(stdout)))
	}
	output := zapcore.NewMultiWriteSyncer(outputs...)
	core := zapcore.NewCore(zapcore.NewJSONEncoder(encoderConfig), output, zapcore.InfoLevel)
	logger := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))

	cleanup := func() error {
		_ = logger.Sync()
		if err := roller.Close(); err != nil {
			return fmt.Errorf("close log file %q: %w", cfg.FilePath, err)
		}
		return nil
	}
	return logger, cleanup, nil
}
