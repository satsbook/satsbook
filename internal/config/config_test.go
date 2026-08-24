package config

import (
	"os"
	"testing"
	"time"
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

func TestLoad_MissingLNDHost(t *testing.T) {
	cfg := &Config{
		LNDHost:         "",
		LNDPort:         10009,
		LNDMacaroonPath: "/test/macaroon",
		LNDTLSCertPath:  "/test/tls.cert",
		DatabasePath:    "./satsbook.db",
		AppPort:         8080,
		LogLevel:        "info",
	}
	if err := cfg.validate(); err == nil {
		t.Fatal("validate() should fail when LNDHost is empty")
	}
}

func TestLoad_MissingDatabasePath(t *testing.T) {
	cfg := &Config{
		LNDHost:         "localhost",
		LNDPort:         10009,
		LNDMacaroonPath: "/test/macaroon",
		LNDTLSCertPath:  "/test/tls.cert",
		DatabasePath:    "",
		AppPort:         8080,
		LogLevel:        "info",
	}
	if err := cfg.validate(); err == nil {
		t.Fatal("validate() should fail when DatabasePath is empty")
	}
}

func TestLoad_InvalidAppPort(t *testing.T) {
	cases := []int{0, -1, 65536, 99999}
	for _, port := range cases {
		cfg := &Config{
			LNDHost:         "localhost",
			LNDPort:         10009,
			LNDMacaroonPath: "/test/macaroon",
			LNDTLSCertPath:  "/test/tls.cert",
			DatabasePath:    "./satsbook.db",
			AppPort:         port,
			LogLevel:        "info",
		}
		if err := cfg.validate(); err == nil {
			t.Errorf("validate() should fail for AppPort=%d", port)
		}
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

func TestLoad_NoLND_IsValid(t *testing.T) {
	// When no LND vars are set, config should load successfully (dashboard-only mode)
	clearEnv()
	os.Setenv("SATSBOOK_DATABASE_PATH", "/test/db.sqlite")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() should succeed without LND config, got: %v", err)
	}
	if cfg.LNDConfigured() {
		t.Error("LNDConfigured() should be false when no LND vars are set")
	}
}

func TestLNDConfigured(t *testing.T) {
	tests := []struct {
		name     string
		macaroon string
		tls      string
		want     bool
	}{
		{"both set", "/mac", "/tls", true},
		{"macaroon only", "/mac", "", false},
		{"tls only", "", "/tls", false},
		{"neither set", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{LNDMacaroonPath: tt.macaroon, LNDTLSCertPath: tt.tls}
			if got := c.LNDConfigured(); got != tt.want {
				t.Errorf("LNDConfigured() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidate_LNDPartialConfig_MissingHost(t *testing.T) {
	cfg := &Config{
		LNDMacaroonPath: "/test/macaroon",
		LNDTLSCertPath:  "/test/tls.cert",
		LNDHost:         "",
		LNDPort:         10009,
		DatabasePath:    "./satsbook.db",
		AppPort:         8080,
		LogLevel:        "info",
	}
	if err := cfg.validate(); err == nil {
		t.Fatal("validate() should fail when LND creds are set but host is empty")
	}
}

// --- Telegram config tests (issue #31) ---

func TestLoad_TelegramEnvVars(t *testing.T) {
	clearEnv()
	os.Setenv("SATSBOOK_DATABASE_PATH", "/test/db.sqlite")
	os.Setenv("SATSBOOK_TELEGRAM_BOT_TOKEN", "bot123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11")
	os.Setenv("SATSBOOK_TELEGRAM_CHAT_ID", "-100987654321")
	defer func() {
		os.Unsetenv("SATSBOOK_TELEGRAM_BOT_TOKEN")
		os.Unsetenv("SATSBOOK_TELEGRAM_CHAT_ID")
		clearEnv()
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.TelegramBotToken != "bot123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11" {
		t.Errorf("TelegramBotToken = %q, want the configured token", cfg.TelegramBotToken)
	}
	if cfg.TelegramChatID != "-100987654321" {
		t.Errorf("TelegramChatID = %q, want -100987654321", cfg.TelegramChatID)
	}
}

func TestLoad_TelegramDefaults_Empty(t *testing.T) {
	clearEnv()
	os.Setenv("SATSBOOK_DATABASE_PATH", "/test/db.sqlite")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	// Without env vars, Telegram must default to empty strings — not crash.
	if cfg.TelegramBotToken != "" {
		t.Errorf("expected empty TelegramBotToken by default, got %q", cfg.TelegramBotToken)
	}
	if cfg.TelegramChatID != "" {
		t.Errorf("expected empty TelegramChatID by default, got %q", cfg.TelegramChatID)
	}
}

// --- BitcoinRPCConfigured tests ---

func TestBitcoinRPCConfigured_WithHostAndCookie(t *testing.T) {
	c := &Config{
		BitcoinRPCHost:       "localhost",
		BitcoinRPCCookiePath: "/run/bitcoin/cookie",
	}
	if !c.BitcoinRPCConfigured() {
		t.Error("expected BitcoinRPCConfigured to be true with host + cookie")
	}
}

func TestBitcoinRPCConfigured_WithHostAndCredentials(t *testing.T) {
	c := &Config{
		BitcoinRPCHost:     "localhost",
		BitcoinRPCUser:     "rpcuser",
		BitcoinRPCPassword: "rpcpass",
	}
	if !c.BitcoinRPCConfigured() {
		t.Error("expected BitcoinRPCConfigured to be true with host + user + password")
	}
}

func TestBitcoinRPCConfigured_NoHost(t *testing.T) {
	c := &Config{
		BitcoinRPCCookiePath: "/run/bitcoin/cookie",
	}
	if c.BitcoinRPCConfigured() {
		t.Error("expected BitcoinRPCConfigured to be false with no host")
	}
}

func TestBitcoinRPCConfigured_HostButNoCredentials(t *testing.T) {
	c := &Config{
		BitcoinRPCHost: "localhost",
	}
	if c.BitcoinRPCConfigured() {
		t.Error("expected BitcoinRPCConfigured to be false with host but no credentials")
	}
}

func TestBitcoinRPCConfigured_PartialCredentials_UserOnly(t *testing.T) {
	c := &Config{
		BitcoinRPCHost: "localhost",
		BitcoinRPCUser: "user",
		// no password, no cookie
	}
	if c.BitcoinRPCConfigured() {
		t.Error("expected BitcoinRPCConfigured to be false with user but no password or cookie")
	}
}

// --- getEnvAsDuration tests ---

func TestGetEnvAsDuration_ValidDuration(t *testing.T) {
	os.Setenv("TEST_DURATION_VALID", "30s")
	defer os.Unsetenv("TEST_DURATION_VALID")

	got := getEnvAsDuration("TEST_DURATION_VALID", 5*time.Minute)
	if got != 30*time.Second {
		t.Errorf("getEnvAsDuration = %v, want 30s", got)
	}
}

func TestGetEnvAsDuration_InvalidFallsBackToDefault(t *testing.T) {
	os.Setenv("TEST_DURATION_INVALID", "not-a-duration")
	defer os.Unsetenv("TEST_DURATION_INVALID")

	defaultVal := 5 * time.Minute
	got := getEnvAsDuration("TEST_DURATION_INVALID", defaultVal)
	if got != defaultVal {
		t.Errorf("getEnvAsDuration with invalid value should return default %v, got %v", defaultVal, got)
	}
}

func TestGetEnvAsDuration_MissingFallsBackToDefault(t *testing.T) {
	os.Unsetenv("TEST_DURATION_MISSING_XYZ")
	defaultVal := 10 * time.Minute
	got := getEnvAsDuration("TEST_DURATION_MISSING_XYZ", defaultVal)
	if got != defaultVal {
		t.Errorf("getEnvAsDuration with missing env should return default %v, got %v", defaultVal, got)
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
