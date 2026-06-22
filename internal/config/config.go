package config

import "os"

const (
	defaultHTTPAddr = ":8080"
	defaultDataDir  = "data"
)

// Config holds values that vary between local development and deployment.
type Config struct {
	HTTPAddr string
	DataDir  string
}

// FromEnvironment reads configuration, applying local-development defaults.
func FromEnvironment() Config {
	return Config{
		HTTPAddr: valueOrDefault("NWSL_HTTP_ADDR", defaultHTTPAddr),
		DataDir:  valueOrDefault("NWSL_DATA_DIR", defaultDataDir),
	}
}

func valueOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
