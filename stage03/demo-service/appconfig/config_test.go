package appconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var configEnvironment = []string{
	"SERVER_HOST",
	"SERVER_PORT",
	"SERVER_TLS_PORT",
	"GRACEFUL_SHUTDOWN_TIME",
	"DRAIN_REQUESTS_TIME",
	"USE_TLS",
	"CERT_FILE",
	"KEY_FILE",
	"GIN_MODE",
	"TEMPLATE_PATH",
	"LOG_TO_LOGGER",
	"LOG_FORMAT",
	"LOG_LEVEL",
}

func TestInitConfigDefaultsWithoutEnvFile(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("USE_TLS", "false")

	var config AppConfig
	err := InitConfig(filepath.Join(t.TempDir(), "missing.env"), &config)
	if err != nil {
		t.Fatalf("InitConfig() error = %v", err)
	}
	if config.Server.Port != "8080" || config.Server.TLSPort != "8443" {
		t.Fatalf("ports = (%q, %q), want (8080, 8443)", config.Server.Port, config.Server.TLSPort)
	}
	if config.Logging.Format != "text" || config.Logging.Level != "info" {
		t.Fatalf("logging defaults = %v, want text/info", config.Logging)
	}
	if config.Server.GracefulShutdownTime != 10 || config.Server.DrainRequestsTime != 12 {
		t.Fatalf("shutdown durations = (%d, %d), want (10, 12)", config.Server.GracefulShutdownTime, config.Server.DrainRequestsTime)
	}
}

func TestEnvironmentTakesPrecedenceOverEnvFile(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("SERVER_PORT", "7777")
	t.Setenv("USE_TLS", "false")
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("SERVER_PORT=9999\nUSE_TLS=true\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	var config AppConfig
	if err := InitConfig(envFile, &config); err != nil {
		t.Fatalf("InitConfig() error = %v", err)
	}
	if config.Server.Port != "7777" || config.Server.UseTLS {
		t.Fatalf("environment precedence not respected: port=%q useTLS=%v", config.Server.Port, config.Server.UseTLS)
	}
}

func TestInitConfigRejectsMalformedEnvironment(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("USE_TLS", "false")
	t.Setenv("DRAIN_REQUESTS_TIME", "not-a-number")

	var config AppConfig
	err := InitConfig(filepath.Join(t.TempDir(), "missing.env"), &config)
	if err == nil || !strings.Contains(err.Error(), "DRAIN_REQUESTS_TIME") {
		t.Fatalf("InitConfig() error = %v, want malformed environment error", err)
	}
}

func TestValidateConfig(t *testing.T) {
	valid := AppConfig{}
	valid.Server.Port = "8080"
	valid.Server.TLSPort = "8443"
	valid.Server.GracefulShutdownTime = 10

	tests := []struct {
		name   string
		mutate func(*AppConfig)
		want   string
	}{
		{name: "zero shutdown", mutate: func(c *AppConfig) { c.Server.GracefulShutdownTime = 0 }, want: "graceful shutdown"},
		{name: "negative drain", mutate: func(c *AppConfig) { c.Server.DrainRequestsTime = -1 }, want: "drain requests"},
		{name: "invalid HTTP port", mutate: func(c *AppConfig) { c.Server.Port = "nope" }, want: "server port"},
		{name: "invalid TLS port", mutate: func(c *AppConfig) { c.Server.TLSPort = "65536" }, want: "server TLS port"},
		{name: "invalid host", mutate: func(c *AppConfig) { c.Server.Host = "bad:host:name" }, want: "server host"},
		{name: "missing certificate", mutate: func(c *AppConfig) {
			c.Server.UseTLS = true
			c.Server.CertFile = "missing.crt"
			c.Server.KeyFile = "missing.key"
		}, want: "certificate file"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			err := validateConfig(&config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateConfig() error = %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestTLSDisabledDoesNotRequireCertificateFiles(t *testing.T) {
	config := AppConfig{}
	config.Server.Port = "8080"
	config.Server.TLSPort = "8443"
	config.Server.GracefulShutdownTime = 1
	config.Server.CertFile = "missing.crt"
	config.Server.KeyFile = "missing.key"
	if err := validateConfig(&config); err != nil {
		t.Fatalf("validateConfig() error with TLS disabled = %v", err)
	}
}

func TestValidateConfigRejectsMissingKey(t *testing.T) {
	certificateFile := filepath.Join(t.TempDir(), "tls.crt")
	if err := os.WriteFile(certificateFile, []byte("present"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := AppConfig{}
	config.Server.Port = "8080"
	config.Server.TLSPort = "8443"
	config.Server.GracefulShutdownTime = 1
	config.Server.UseTLS = true
	config.Server.CertFile = certificateFile
	config.Server.KeyFile = filepath.Join(t.TempDir(), "missing.key")
	if err := validateConfig(&config); err == nil || !strings.Contains(err.Error(), "key file") {
		t.Fatalf("validateConfig() error = %v, want missing key error", err)
	}
}

func TestSetDefaultsCleansCertificatePaths(t *testing.T) {
	config := AppConfig{}
	config.Server.CertFile = filepath.Join("directory", "..", "tls.crt")
	config.Server.KeyFile = filepath.Join("directory", "..", "tls.key")
	setDefaults(&config)
	if config.Server.CertFile != "tls.crt" || config.Server.KeyFile != "tls.key" {
		t.Fatalf("cleaned paths = (%q, %q), want tls.crt and tls.key", config.Server.CertFile, config.Server.KeyFile)
	}
}

func clearConfigEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range configEnvironment {
		value, exists := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
		name := name
		t.Cleanup(func() {
			if exists {
				_ = os.Setenv(name, value)
			} else {
				_ = os.Unsetenv(name)
			}
		})
	}
}
