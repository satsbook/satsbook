package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration for the application.
type Config struct {
	// LND connection settings
	LNDHost         string
	LNDPort         int
	LNDMacaroonPath string
	LNDTLSCertPath  string

	// Database settings
	DatabasePath string

	// Syncer settings
	SyncInterval   time.Duration
	MaxHistoryDays int

	// Application settings
	AppPort     int
	LogLevel    string
	PriceAPIURL string
}

// Load reads configuration from environment variables.
// It prioritizes SATSBOOK_* prefixed variables, but falls back to
// Umbrel-injected variables (LND_IP, LND_GRPC_PORT, etc.) as defaults.
func Load() (*Config, error) {
	cfg := &Config{
		// LND defaults - prefer SATSBOOK_ prefix, fall back to Umbrel vars
		LNDHost:         getEnv("SATSBOOK_LND_HOST", getEnv("LND_IP", "localhost")),
		LNDPort:         getEnvAsInt("SATSBOOK_LND_PORT", getEnvAsInt("LND_GRPC_PORT", 10009)),
		LNDMacaroonPath: getEnv("SATSBOOK_LND_MACAROON_PATH", getEnv("LND_MACAROON_PATH", "")),
		LNDTLSCertPath:  getEnv("SATSBOOK_LND_TLS_CERT_PATH", getEnv("LND_TLS_CERT_PATH", "")),

		// Database defaults
		DatabasePath: getEnv("SATSBOOK_DATABASE_PATH", "./satsbook.db"),

		// Syncer defaults
		SyncInterval:   getEnvAsDuration("SATSBOOK_SYNC_INTERVAL", 5*time.Minute),
		MaxHistoryDays: getEnvAsInt("SATSBOOK_MAX_HISTORY_DAYS", 90),

		// Application defaults
		AppPort:     getEnvAsInt("SATSBOOK_APP_PORT", 8080),
		LogLevel:    getEnv("SATSBOOK_LOG_LEVEL", "info"),
		PriceAPIURL: getEnv("SATSBOOK_PRICE_API_URL", "https://mempool.space/api/v1/prices"),
	}

	// Validate required fields
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return cfg, nil
}

// validate checks that required configuration values are present.
func (c *Config) validate() error {
	if c.LNDHost == "" {
		return fmt.Errorf("LND host is required (set SATSBOOK_LND_HOST or LND_IP)")
	}

	if c.LNDPort <= 0 || c.LNDPort > 65535 {
		return fmt.Errorf("LND port must be between 1 and 65535, got %d", c.LNDPort)
	}

	if c.LNDMacaroonPath == "" {
		return fmt.Errorf("LND macaroon path is required (set SATSBOOK_LND_MACAROON_PATH or LND_MACAROON_PATH)")
	}

	if c.LNDTLSCertPath == "" {
		return fmt.Errorf("LND TLS cert path is required (set SATSBOOK_LND_TLS_CERT_PATH or LND_TLS_CERT_PATH)")
	}

	if c.DatabasePath == "" {
		return fmt.Errorf("database path is required (set SATSBOOK_DATABASE_PATH)")
	}

	if c.AppPort <= 0 || c.AppPort > 65535 {
		return fmt.Errorf("app port must be between 1 and 65535, got %d", c.AppPort)
	}

	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLogLevels[c.LogLevel] {
		return fmt.Errorf("log level must be one of [debug, info, warn, error], got %q", c.LogLevel)
	}

	if c.SyncInterval < 1*time.Minute {
		return fmt.Errorf("sync interval must be at least 1 minute, got %v", c.SyncInterval)
	}

	if c.MaxHistoryDays < 1 || c.MaxHistoryDays > 3650 {
		return fmt.Errorf("max history days must be between 1 and 3650, got %d", c.MaxHistoryDays)
	}

	return nil
}

// getEnv retrieves an environment variable or returns a default value.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvAsInt retrieves an environment variable as an integer or returns a default value.
func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

// getEnvAsDuration retrieves an environment variable as a duration or returns a default value.
func getEnvAsDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return defaultValue
}
