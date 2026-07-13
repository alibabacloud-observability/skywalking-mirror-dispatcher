// Package config loads and validates runtime settings.
package config

import (
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultListenAddr      = ":11800"
	defaultAdminAddr       = ":8080"
	defaultMaxMessageBytes = 50 * 1024 * 1024
	defaultMaxInflight     = 1024
	defaultARMSConcurrency = 64
	defaultARMSQueueSize   = 128
	defaultARMSFinish      = 5 * time.Second
	defaultDrainTimeout    = 30 * time.Second
)

// Config is intentionally flat because every setting maps to one environment
// variable and the first version has exactly one OAP and one ARMS target.
type Config struct {
	ListenAddr          string
	AdminAddr           string
	OAPEndpoint         string
	OAPTLS              bool
	OAPCAFile           string
	ARMSEndpoint        string
	ARMSAuthentication  string
	ListenerTLSCertFile string
	ListenerTLSKeyFile  string
	MaxMessageBytes     int
	MaxInflightRPCs     int
	ARMSMaxConcurrent   int
	ARMSStreamQueueSize int
	ARMSFinishTimeout   time.Duration
	DrainTimeout        time.Duration
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (Config, error) {
	cfg := Config{
		ListenAddr:          valueOr(getenv("LISTEN_ADDR"), defaultListenAddr),
		AdminAddr:           valueOr(getenv("ADMIN_ADDR"), defaultAdminAddr),
		OAPEndpoint:         strings.TrimSpace(getenv("OAP_ENDPOINT")),
		OAPCAFile:           strings.TrimSpace(getenv("OAP_CA_FILE")),
		ARMSEndpoint:        strings.TrimSpace(getenv("ARMS_ENDPOINT")),
		ARMSAuthentication:  strings.TrimSpace(getenv("ARMS_AUTHENTICATION")),
		ListenerTLSCertFile: strings.TrimSpace(getenv("LISTENER_TLS_CERT_FILE")),
		ListenerTLSKeyFile:  strings.TrimSpace(getenv("LISTENER_TLS_KEY_FILE")),
	}

	var err error
	if cfg.OAPTLS, err = parseBool(getenv, "OAP_TLS", false); err != nil {
		return Config{}, err
	}
	if cfg.MaxMessageBytes, err = parsePositiveInt(getenv, "GRPC_MAX_MESSAGE_BYTES", defaultMaxMessageBytes); err != nil {
		return Config{}, err
	}
	if cfg.MaxInflightRPCs, err = parsePositiveInt(getenv, "MAX_INFLIGHT_RPCS", defaultMaxInflight); err != nil {
		return Config{}, err
	}
	if cfg.ARMSMaxConcurrent, err = parsePositiveInt(getenv, "ARMS_MAX_CONCURRENT_RPCS", defaultARMSConcurrency); err != nil {
		return Config{}, err
	}
	if cfg.ARMSStreamQueueSize, err = parsePositiveInt(getenv, "ARMS_STREAM_QUEUE_SIZE", defaultARMSQueueSize); err != nil {
		return Config{}, err
	}
	if cfg.ARMSFinishTimeout, err = parsePositiveDuration(getenv, "ARMS_FINISH_TIMEOUT", defaultARMSFinish); err != nil {
		return Config{}, err
	}
	if cfg.DrainTimeout, err = parsePositiveDuration(getenv, "DRAIN_TIMEOUT", defaultDrainTimeout); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate rejects incomplete or contradictory startup settings before any
// listener is opened.
func (c Config) Validate() error {
	var errs []error
	for name, value := range map[string]string{
		"LISTEN_ADDR":         c.ListenAddr,
		"ADMIN_ADDR":          c.AdminAddr,
		"OAP_ENDPOINT":        c.OAPEndpoint,
		"ARMS_ENDPOINT":       c.ARMSEndpoint,
		"ARMS_AUTHENTICATION": c.ARMSAuthentication,
	} {
		if strings.TrimSpace(value) == "" {
			errs = append(errs, fmt.Errorf("%s is required", name))
		}
	}
	if (c.ListenerTLSCertFile == "") != (c.ListenerTLSKeyFile == "") {
		errs = append(errs, errors.New("LISTENER_TLS_CERT_FILE and LISTENER_TLS_KEY_FILE must be set together"))
	}
	if c.ListenerTLSCertFile != "" {
		if _, err := tls.LoadX509KeyPair(c.ListenerTLSCertFile, c.ListenerTLSKeyFile); err != nil {
			errs = append(errs, fmt.Errorf("load listener TLS certificate: %w", err))
		}
	}
	if c.OAPCAFile != "" && !c.OAPTLS {
		errs = append(errs, errors.New("OAP_CA_FILE requires OAP_TLS=true"))
	}
	for name, value := range map[string]int{
		"GRPC_MAX_MESSAGE_BYTES":   c.MaxMessageBytes,
		"MAX_INFLIGHT_RPCS":        c.MaxInflightRPCs,
		"ARMS_MAX_CONCURRENT_RPCS": c.ARMSMaxConcurrent,
		"ARMS_STREAM_QUEUE_SIZE":   c.ARMSStreamQueueSize,
	} {
		if value <= 0 {
			errs = append(errs, fmt.Errorf("%s must be greater than zero", name))
		}
	}
	if c.ARMSFinishTimeout <= 0 {
		errs = append(errs, errors.New("ARMS_FINISH_TIMEOUT must be greater than zero"))
	}
	if c.DrainTimeout <= 0 {
		errs = append(errs, errors.New("DRAIN_TIMEOUT must be greater than zero"))
	}
	return errors.Join(errs...)
}

// Summary contains no credentials and is safe to include in startup logs.
func (c Config) Summary() map[string]any {
	return map[string]any{
		"listen_addr":             c.ListenAddr,
		"admin_addr":              c.AdminAddr,
		"oap_endpoint":            c.OAPEndpoint,
		"oap_tls":                 c.OAPTLS,
		"oap_custom_ca":           c.OAPCAFile != "",
		"arms_endpoint":           c.ARMSEndpoint,
		"arms_authentication_set": c.ARMSAuthentication != "",
		"listener_tls":            c.ListenerTLSCertFile != "",
		"max_message_bytes":       c.MaxMessageBytes,
		"max_inflight_rpcs":       c.MaxInflightRPCs,
		"arms_max_concurrent":     c.ARMSMaxConcurrent,
		"arms_stream_queue_size":  c.ARMSStreamQueueSize,
		"arms_finish_timeout":     c.ARMSFinishTimeout.String(),
		"drain_timeout":           c.DrainTimeout.String(),
	}
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func parseBool(getenv func(string) string, name string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", name, err)
	}
	return value, nil
}

func parsePositiveInt(getenv func(string) string, name string, fallback int) (int, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}

func parsePositiveDuration(getenv func(string) string, name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration", name)
	}
	return value, nil
}
