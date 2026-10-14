// Package httpapi provides the shared bounded JSON HTTP runtime.
package httpapi

import (
	"fmt"
	"net"
	"strconv"
	"time"
)

// Config contains server settings only, never database credentials or tokens.
type Config struct {
	Addr                                                      string
	TLSCertFile, TLSKeyFile                                   string
	MaxBodyBytes                                              int64
	MaxHeaderBytes                                            int
	ReadHeaderTimeout, ReadTimeout, WriteTimeout, IdleTimeout time.Duration
	RequestTimeout, ShutdownTimeout                           time.Duration
}

// DefaultConfig supplies conservative initial limits, not throughput targets.
func DefaultConfig() Config {
	return Config{Addr: ":8443", MaxBodyBytes: 64 << 10, MaxHeaderBytes: 16 << 10,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: time.Minute,
		RequestTimeout: 10 * time.Second, ShutdownTimeout: 10 * time.Second}
}

// LoadConfig reads HTTP_* environment settings without including values in errors.
func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := DefaultConfig()
	if value := getenv("HTTP_ADDR"); value != "" {
		cfg.Addr = value
	}
	cfg.TLSCertFile = getenv("HTTP_TLS_CERT_FILE")
	cfg.TLSKeyFile = getenv("HTTP_TLS_KEY_FILE")
	for key, target := range map[string]*time.Duration{
		"HTTP_READ_HEADER_TIMEOUT": &cfg.ReadHeaderTimeout, "HTTP_READ_TIMEOUT": &cfg.ReadTimeout,
		"HTTP_WRITE_TIMEOUT": &cfg.WriteTimeout, "HTTP_IDLE_TIMEOUT": &cfg.IdleTimeout,
		"HTTP_REQUEST_TIMEOUT": &cfg.RequestTimeout, "HTTP_SHUTDOWN_TIMEOUT": &cfg.ShutdownTimeout} {
		if value := getenv(key); value != "" {
			parsed, err := time.ParseDuration(value)
			if err != nil {
				return Config{}, fmt.Errorf("invalid %s", key)
			}
			*target = parsed
		}
	}
	if value := getenv("HTTP_MAX_BODY_BYTES"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("invalid HTTP_MAX_BODY_BYTES")
		}
		cfg.MaxBodyBytes = parsed
	}
	if value := getenv("HTTP_MAX_HEADER_BYTES"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return Config{}, fmt.Errorf("invalid HTTP_MAX_HEADER_BYTES")
		}
		cfg.MaxHeaderBytes = parsed
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	_, port, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return fmt.Errorf("invalid HTTP_ADDR")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 0 || number > 65535 {
		return fmt.Errorf("invalid HTTP_ADDR")
	}
	if c.TLSCertFile == "" || c.TLSKeyFile == "" {
		return fmt.Errorf("HTTP_TLS_CERT_FILE and HTTP_TLS_KEY_FILE are required")
	}
	if c.MaxBodyBytes <= 0 || c.MaxHeaderBytes <= 0 {
		return fmt.Errorf("HTTP request limits must be positive")
	}
	for _, duration := range []time.Duration{c.ReadHeaderTimeout, c.ReadTimeout, c.WriteTimeout, c.IdleTimeout, c.RequestTimeout, c.ShutdownTimeout} {
		if duration <= 0 {
			return fmt.Errorf("HTTP timeouts must be positive")
		}
	}
	if c.WriteTimeout <= c.RequestTimeout {
		return fmt.Errorf("HTTP_WRITE_TIMEOUT must exceed HTTP_REQUEST_TIMEOUT")
	}
	return nil
}
