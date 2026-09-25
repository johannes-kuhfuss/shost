package appconfig

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

type AppConfig struct {
	Kubernetes struct {
		PodName      string `envconfig:"POD_NAME"`
		PodIP        string `envconfig:"POD_IP"`
		PodNamespace string `envconfig:"POD_NAMESPACE"`
		NodeName     string `envconfig:"NODE_NAME"`
	}
	Logging struct {
		Format string `envconfig:"LOG_FORMAT" default:"text"`
		Level  string `envconfig:"LOG_LEVEL" default:"info"`
	}
	Server struct {
		Host                 string `envconfig:"SERVER_HOST"`
		Port                 string `envconfig:"SERVER_PORT" default:"8080"`
		TLSPort              string `envconfig:"SERVER_TLS_PORT" default:"8443"`
		GracefulShutdownTime int    `envconfig:"GRACEFUL_SHUTDOWN_TIME" default:"10"`
		DrainRequestsTime    int    `envconfig:"DRAIN_REQUESTS_TIME" default:"12"`
		UseTLS               bool   `envconfig:"USE_TLS" default:"false"`
		CertFile             string `envconfig:"CERT_FILE" default:"/var/run/demo-service/tls/tls.crt"`
		KeyFile              string `envconfig:"KEY_FILE" default:"/var/run/demo-service/tls/tls.key"`
	}
	Gin struct {
		Mode         string `envconfig:"GIN_MODE" default:"release"`
		TemplatePath string `envconfig:"TEMPLATE_PATH" default:"./templates/"`
	}
}

var (
	EnvFile = ".env"
)

// InitConfig initializes the configuration and sets the defaults
func InitConfig(file string, config *AppConfig) error {
	if err := loadConfig(file); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("Could not load configuration file", "file", file, "error", err)
	}
	if err := envconfig.Process("", config); err != nil {
		return fmt.Errorf("could not initialize configuration: %v", err.Error())
	}
	setDefaults(config)
	if err := validateConfig(config); err != nil {
		return err
	}
	return nil
}

func validateConfig(config *AppConfig) error {
	if config.Server.GracefulShutdownTime <= 0 {
		return fmt.Errorf("graceful shutdown time must be greater than 0")
	}
	if config.Server.DrainRequestsTime < 0 {
		return fmt.Errorf("drain requests time must not be negative")
	}
	if err := validatePort("server port", config.Server.Port); err != nil {
		return err
	}
	if err := validatePort("server TLS port", config.Server.TLSPort); err != nil {
		return err
	}
	if _, err := net.ResolveTCPAddr("tcp", net.JoinHostPort(config.Server.Host, config.Server.Port)); err != nil {
		return fmt.Errorf("invalid server host %q: %w", config.Server.Host, err)
	}
	if config.Server.UseTLS {
		if _, err := os.Stat(config.Server.CertFile); err != nil {
			return fmt.Errorf("TLS certificate file is not accessible: %w", err)
		}
		if _, err := os.Stat(config.Server.KeyFile); err != nil {
			return fmt.Errorf("TLS key file is not accessible: %w", err)
		}
	}
	return nil
}

func validatePort(name, value string) error {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%s must be a number between 1 and 65535", name)
	}
	return nil
}

// checkFilePath normalizes a configured file path. Accessibility is validated
// separately when the corresponding feature is enabled.
func checkFilePath(filePath *string) {
	if *filePath != "" {
		*filePath = filepath.Clean(*filePath)
	}
}

// setDefaults sets defaults for some configurations items
func setDefaults(config *AppConfig) {
	checkFilePath(&config.Server.CertFile)
	checkFilePath(&config.Server.KeyFile)
}

// loadConfig loads the configuration from file. Returns an error if loading fails
func loadConfig(file string) error {
	if err := godotenv.Load(file); err != nil {
		return err
	}
	return nil
}
