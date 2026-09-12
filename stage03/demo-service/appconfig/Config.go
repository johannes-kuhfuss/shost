package appconfig

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

type AppConfig struct {
	Server struct {
		Host                 string `envconfig:"SERVER_HOST"`
		Port                 string `envconfig:"SERVER_PORT" default:"8080"`
		TLSPort              string `envconfig:"SERVER_TLS_PORT" default:"8443"`
		GracefulShutdownTime int    `envconfig:"GRACEFUL_SHUTDOWN_TIME" default:"10"`
		UseTLS               bool   `envconfig:"USE_TLS" default:"false"`
		CertFile             string `envconfig:"CERT_FILE" default:"./cert/cert.pem"`
		KeyFile              string `envconfig:"KEY_FILE" default:"./cert/cert.key"`
	}
	Gin struct {
		Mode         string `envconfig:"GIN_MODE" default:"release"`
		TemplatePath string `envconfig:"TEMPLATE_PATH" default:"./templates/"`
		LogToLogger  bool   `envconfig:"LOG_TO_LOGGER" default:"false"`
	}
}

var (
	EnvFile = ".env"
)

// InitConfig initializes the configuration and sets the defaults
func InitConfig(file string, config *AppConfig) error {
	log.Printf("Initializing configuration from file %v...", file)
	if err := loadConfig(file); err != nil {
		log.Printf("Error while loading configuration from file. %v", err)
	}
	if err := envconfig.Process("", config); err != nil {
		return fmt.Errorf("could not initialize configuration: %v", err.Error())
	}
	setDefaults(config)
	if err := validateConfig(config); err != nil {
		return err
	}
	log.Print("Configuration initialized")
	return nil
}

func validateConfig(config *AppConfig) error {
	if config.Server.GracefulShutdownTime <= 0 {
		return fmt.Errorf("graceful shutdown time must be greater than 0")
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

// checkFilePath does sanity-checking on file paths
func checkFilePath(filePath *string) {
	if *filePath != "" {
		*filePath = filepath.Clean(*filePath)
		_, err := os.Stat(*filePath)
		if err == nil {
			*filePath, err = filepath.EvalSymlinks(*filePath)
			if err != nil {
				log.Printf("error checking file %v", *filePath)
			}
		}
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
