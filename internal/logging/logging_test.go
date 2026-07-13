package logging

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestLoadConfigUsesExecutableDirectoryAndDisablesStdoutByDefault(t *testing.T) {
	cfg, err := loadConfig(filepath.Join("opt", "mirror", "skywalking-mirror"), func(string) (string, bool) {
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("opt", "mirror", FileName)
	if cfg.FilePath != want {
		t.Fatalf("FilePath=%q, want %q", cfg.FilePath, want)
	}
	if cfg.MaxSizeMB != 100 || cfg.MaxBackups != 5 || cfg.MaxAgeDays != 30 || !cfg.Compress || cfg.Stdout {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadConfigReadsStdoutSwitch(t *testing.T) {
	cfg, err := loadConfig("skywalking-mirror", func(name string) (string, bool) {
		if name == EnvStdout {
			return "true", true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Stdout {
		t.Fatal("Stdout=false, want true")
	}

	if _, err := loadConfig("skywalking-mirror", func(string) (string, bool) {
		return "sometimes", true
	}); err == nil {
		t.Fatal("expected invalid LOG_STDOUT to fail")
	}
}

func TestLoggerWritesStructuredEventToFileByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	var stdout bytes.Buffer
	logger, closeLogger, err := newLogger(Config{
		FilePath:   path,
		MaxSizeMB:  1,
		MaxBackups: 1,
		MaxAgeDays: 1,
	}, &stdout)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("servers started", zap.String("grpc_addr", ":11800"))
	if err := closeLogger(); err != nil {
		t.Fatal(err)
	}

	fileData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertEvent(t, "file", fileData)
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q, want no output", stdout.String())
	}
}

func TestLoggerOptionallyCopiesEventToStdout(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	var stdout bytes.Buffer
	logger, closeLogger, err := newLogger(Config{
		FilePath:   path,
		MaxSizeMB:  1,
		MaxBackups: 1,
		MaxAgeDays: 1,
		Stdout:     true,
	}, &stdout)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("servers started", zap.String("grpc_addr", ":11800"))
	if err := closeLogger(); err != nil {
		t.Fatal(err)
	}
	fileData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertEvent(t, "file", fileData)
	assertEvent(t, "stdout", stdout.Bytes())
}

func assertEvent(t *testing.T, name string, data []byte) {
	t.Helper()
	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &event); err != nil {
		t.Fatalf("%s contains invalid JSON: %v", name, err)
	}
	if event["msg"] != "servers started" || event["grpc_addr"] != ":11800" || event["level"] != "info" {
		t.Fatalf("unexpected %s event: %#v", name, event)
	}
}

func TestLoggerRotatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	logger, closeLogger, err := newLogger(Config{
		FilePath:   path,
		MaxSizeMB:  1,
		MaxBackups: 2,
		MaxAgeDays: 1,
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", 600*1024)
	logger.Info("rotation test", zap.String("payload", payload))
	logger.Info("rotation test", zap.String("payload", payload))
	if err := closeLogger(); err != nil {
		t.Fatal(err)
	}

	backups, err := filepath.Glob(filepath.Join(dir, "skywalking-mirror-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("backup count=%d, want 1; files=%v", len(backups), backups)
	}
}

func TestLoggerRejectsUnwritablePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", FileName)
	logger, closeLogger, err := newLogger(Config{
		FilePath:   path,
		MaxSizeMB:  1,
		MaxBackups: 1,
		MaxAgeDays: 1,
	}, io.Discard)
	if err == nil {
		if closeLogger != nil {
			_ = closeLogger()
		}
		t.Fatal("expected logger initialization to fail")
	}
	if logger != nil || closeLogger != nil {
		t.Fatalf("logger or cleanup returned after initialization failure")
	}
}
