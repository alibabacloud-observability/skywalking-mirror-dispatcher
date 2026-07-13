package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

var environmentKeys = []string{
	"LISTEN_ADDR", "ADMIN_ADDR", "OAP_ENDPOINT", "OAP_TLS", "OAP_CA_FILE",
	"ARMS_ENDPOINT", "ARMS_AUTHENTICATION", "LISTENER_TLS_CERT_FILE", "LISTENER_TLS_KEY_FILE",
	"GRPC_MAX_MESSAGE_BYTES", "MAX_INFLIGHT_RPCS", "ARMS_MAX_CONCURRENT_RPCS",
	"ARMS_STREAM_QUEUE_SIZE", "ARMS_FINISH_TIMEOUT", "DRAIN_TIMEOUT",
}

func cleanEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range environmentKeys {
		t.Setenv(key, "")
	}
}

func TestLoadDefaultsAndRedactedSummary(t *testing.T) {
	cleanEnvironment(t)
	t.Setenv("OAP_ENDPOINT", "oap.internal:11800")
	t.Setenv("ARMS_ENDPOINT", "arms.example.com:443")
	t.Setenv("ARMS_AUTHENTICATION", "super-secret-token")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ListenAddr != defaultListenAddr || cfg.AdminAddr != defaultAdminAddr {
		t.Fatalf("unexpected listener defaults: %+v", cfg)
	}
	if cfg.ARMSFinishTimeout != 5*time.Second || cfg.DrainTimeout != 30*time.Second {
		t.Fatalf("unexpected duration defaults: %+v", cfg)
	}
	summary := fmt.Sprint(cfg.Summary())
	if strings.Contains(summary, cfg.ARMSAuthentication) {
		t.Fatalf("summary leaked ARMS authentication: %s", summary)
	}
}

func TestLoadRejectsMissingAndInvalidValues(t *testing.T) {
	cleanEnvironment(t)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "OAP_ENDPOINT") {
		t.Fatalf("missing required values error = %v", err)
	}

	cleanEnvironment(t)
	t.Setenv("OAP_ENDPOINT", "oap:11800")
	t.Setenv("ARMS_ENDPOINT", "arms:443")
	t.Setenv("ARMS_AUTHENTICATION", "token")
	t.Setenv("MAX_INFLIGHT_RPCS", "0")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "MAX_INFLIGHT_RPCS") {
		t.Fatalf("invalid positive integer error = %v", err)
	}
}

func TestLoadRejectsPartialListenerTLSAndCAWithoutOAPTLS(t *testing.T) {
	cleanEnvironment(t)
	t.Setenv("OAP_ENDPOINT", "oap:11800")
	t.Setenv("ARMS_ENDPOINT", "arms:443")
	t.Setenv("ARMS_AUTHENTICATION", "token")
	t.Setenv("LISTENER_TLS_CERT_FILE", "/missing/cert")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "must be set together") {
		t.Fatalf("partial listener TLS error = %v", err)
	}

	cleanEnvironment(t)
	t.Setenv("OAP_ENDPOINT", "oap:11800")
	t.Setenv("ARMS_ENDPOINT", "arms:443")
	t.Setenv("ARMS_AUTHENTICATION", "token")
	t.Setenv("OAP_CA_FILE", "/missing/ca")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "requires OAP_TLS") {
		t.Fatalf("CA without TLS error = %v", err)
	}
}
