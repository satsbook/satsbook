package config

import (
	"os"
	"testing"
)

func TestLoad_WithSatsbookPrefix(t *testing.T) {
	// Set up environment variables with SATSBOOK_ prefix
	os.Setenv("SATSBOOK_LND_HOST", "testhost")
	os.Setenv("SATSBOOK_LND_PORT", "10010")
	os.Setenv("SATSBOOK_LND_MACAROON_PATH", "/test/macaroon.path")
	os.Setenv("SATSBOOK_LND_TLS_CERT_PATH", "/test/tls.cert")
	os.Setenv("SATSBOOK_DATABASE_PATH", "/test/db.sqlite")
	os.Setenv("SATSBOOK_APP_PORT", "9000")
	os.Setenv("SATSBOOK_LOG_LEVEL", "debug")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.LNDHost != "testhost" {
		t.Errorf("LNDHost = %q, want %q", cfg.LNDHost, "testhost")
	}
	if cfg.LNDPort != 10010 {
		t.Errorf("LNDPort = %d, want %d", cfg.LNDPort, 10010)
	}
	if cfg.LNDMacaroonPath != "/test/macaroon.path" {
		t.Errorf("LNDMacaroonPath = %q, want %q", cfg.LNDMacaroonPath, "/test/macaroon.path")
	}
	if cfg.LNDTLSCertPath != "/test/tls.cert" {
		t.Errorf("LNDTLSCertPath = %q, want %q", cfg.LNDTLSCertPath, "/test/tls.cert")
	}
	if cfg.DatabasePath != "/test/db.sqlite" {
		t.Errorf("DatabasePath = %q, want %q", cfg.DatabasePath, "/test/db.sqlite")
	}
	if cfg.AppPort != 9000 {
		t.Errorf("AppPort = %d, want %d", cfg.AppPort, 9000)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
}

func TestLoad_WithUmbrelVariables(t *testing.T) {
	// Set up Umbrel-injected variables (without SATSBOOK_ prefix)
	os.Setenv("LND_IP", "umbrel-host")
	os.Setenv("LND_GRPC_PORT", "10011")
	os.Setenv("LND_MACAROON_PATH", "/umbrel/macaroon.path")
	os.Setenv("LND_TLS_CERT_PATH", "/umbrel/tls.cert")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.LNDHost != "umbrel-host" {
		t.Errorf("LNDHost = %q, want %q", cfg.LNDHost, "umbrel-host")
	}
	if cfg.LNDPort != 10011 {
		t.Errorf("LNDPort = %d, want %d", cfg.LNDPort, 10011)
	}
	if cfg.LNDMacaroonPath != "/umbrel/macaroon.path" {
		t.Errorf("LNDMacaroonPath = %q, want %q", cfg.LNDMacaroonPath, "/umbrel/macaroon.path")
	}
	if cfg.LNDTLSCertPath != "/umbrel/tls.cert" {
		t.Errorf("LNDTLSCertPath = %q, want %q", cfg.LNDTLSCertPath, "/umbrel/tls.cert")
	}

	// Check defaults for non-LND settings
	if cfg.DatabasePath != "./satsbook.db" {
		t.Errorf("DatabasePath = %q, want %q", cfg.DatabasePath, "./satsbook.db")
	}
	if cfg.AppPort != 8080 {
		t.Errorf("AppPort = %d, want %d", cfg.AppPort, 8080)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
}

func TestLoad_SatsbookPrefixTakesPrecedence(t *testing.T) {
	// Set both SATSBOOK_ and Umbrel variables
	// SATSBOOK_ should take precedence
	os.Setenv("SATSBOOK_LND_HOST", "satsbook-host")
	os.Setenv("LND_IP", "umbrel-host")
	os.Setenv("SATSBOOK_LND_PORT", "10012")
	os.Setenv("LND_GRPC_PORT", "10011")
	os.Setenv("SATSBOOK_LND_MACAROON_PATH", "/satsbook/macaroon")
	os.Setenv("LND_MACAROON_PATH", "/umbrel/macaroon")
	os.Setenv("SATSBOOK_LND_TLS_CERT_PATH", "/satsbook/tls.cert")
	os.Setenv("LND_TLS_CERT_PATH", "/umbrel/tls.cert")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.LNDHost != "satsbook-host" {
		t.Errorf("LNDHost = %q, want %q (SATSBOOK_ prefix should take precedence)", cfg.LNDHost, "satsbook-host")
	}
	if cfg.LNDPort != 10012 {
		t.Errorf("LNDPort = %d, want %d (SATSBOOK_ prefix should take precedence)", cfg.LNDPort, 10012)
	}
	if cfg.LNDMacaroonPath != "/satsbook/macaroon" {
		t.Errorf("LNDMacaroonPath = %q, want %q (SATSBOOK_ prefix should take precedence)", cfg.LNDMacaroonPath, "/satsbook/macaroon")
	}
	if cfg.LNDTLSCertPath != "/satsbook/tls.cert" {
		t.Errorf("LNDTLSCertPath = %q, want %q (SATSBOOK_ prefix should take precedence)", cfg.LNDTLSCertPath, "/satsbook/tls.cert")
	}
}

func TestLoad_MissingRequiredMacaroon(t *testing.T) {
	// Set all required vars except macaroon
	os.Setenv("SATSBOOK_LND_HOST", "testhost")
	os.Setenv("SATSBOOK_LND_TLS_CERT_PATH", "/test/tls.cert")
	defer clearEnv()

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail when macaroon path is missing")
	}

	expectedMsg := "config validation failed: LND macaroon path is required"
	if err.Error()[:len(expectedMsg)] != expectedMsg {
		t.Errorf("Error message = %q, want to start with %q", err.Error(), expectedMsg)
	}
}

func TestLoad_MissingRequiredTLSCert(t *testing.T) {
	// Set all required vars except TLS cert
	os.Setenv("SATSBOOK_LND_HOST", "testhost")
	os.Setenv("SATSBOOK_LND_MACAROON_PATH", "/test/macaroon")
	defer clearEnv()

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail when TLS cert path is missing")
	}

	expectedMsg := "config validation failed: LND TLS cert path is required"
	if err.Error()[:len(expectedMsg)] != expectedMsg {
		t.Errorf("Error message = %q, want to start with %q", err.Error(), expectedMsg)
	}
}

func TestLoad_InvalidPort(t *testing.T) {
	os.Setenv("SATSBOOK_LND_HOST", "testhost")
	os.Setenv("SATSBOOK_LND_PORT", "99999") // Invalid port
	os.Setenv("SATSBOOK_LND_MACAROON_PATH", "/test/macaroon")
	os.Setenv("SATSBOOK_LND_TLS_CERT_PATH", "/test/tls.cert")
	defer clearEnv()

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail with invalid port")
	}

	expectedMsg := "config validation failed: LND port must be between 1 and 65535"
	if err.Error()[:len(expectedMsg)] != expectedMsg {
		t.Errorf("Error message = %q, want to start with %q", err.Error(), expectedMsg)
	}
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	os.Setenv("SATSBOOK_LND_HOST", "testhost")
	os.Setenv("SATSBOOK_LND_MACAROON_PATH", "/test/macaroon")
	os.Setenv("SATSBOOK_LND_TLS_CERT_PATH", "/test/tls.cert")
	os.Setenv("SATSBOOK_LOG_LEVEL", "invalid")
	defer clearEnv()

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail with invalid log level")
	}

	expectedMsg := "config validation failed: log level must be one of"
	if err.Error()[:len(expectedMsg)] != expectedMsg {
		t.Errorf("Error message = %q, want to start with %q", err.Error(), expectedMsg)
	}
}

func TestLoad_ValidLogLevels(t *testing.T) {
	validLevels := []string{"debug", "info", "warn", "error"}

	for _, level := range validLevels {
		t.Run(level, func(t *testing.T) {
			os.Setenv("SATSBOOK_LND_HOST", "testhost")
			os.Setenv("SATSBOOK_LND_MACAROON_PATH", "/test/macaroon")
			os.Setenv("SATSBOOK_LND_TLS_CERT_PATH", "/test/tls.cert")
			os.Setenv("SATSBOOK_LOG_LEVEL", level)
			defer clearEnv()

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() failed with valid log level %q: %v", level, err)
			}

			if cfg.LogLevel != level {
				t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, level)
			}
		})
	}
}

func TestGetEnvAsInt_InvalidValue(t *testing.T) {
	os.Setenv("TEST_INT", "not-a-number")
	defer os.Unsetenv("TEST_INT")

	result := getEnvAsInt("TEST_INT", 42)
	if result != 42 {
		t.Errorf("getEnvAsInt with invalid value should return default, got %d, want %d", result, 42)
	}
}

func TestGetEnvAsInt_ValidValue(t *testing.T) {
	os.Setenv("TEST_INT", "123")
	defer os.Unsetenv("TEST_INT")

	result := getEnvAsInt("TEST_INT", 42)
	if result != 123 {
		t.Errorf("getEnvAsInt = %d, want %d", result, 123)
	}
}

// clearEnv clears all test environment variables
func clearEnv() {
	testVars := []string{
		"SATSBOOK_LND_HOST", "SATSBOOK_LND_PORT", "SATSBOOK_LND_MACAROON_PATH",
		"SATSBOOK_LND_TLS_CERT_PATH", "SATSBOOK_DATABASE_PATH",
		"SATSBOOK_APP_PORT", "SATSBOOK_LOG_LEVEL",
		"LND_IP", "LND_GRPC_PORT", "LND_MACAROON_PATH", "LND_TLS_CERT_PATH",
	}
	for _, v := range testVars {
		os.Unsetenv(v)
	}
}
