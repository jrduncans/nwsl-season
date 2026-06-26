package config

import (
	"net"
	"os"
	"strings"
)

const (
	defaultHTTPHost = "127.0.0.1"
	defaultHTTPPort = "8080"
	defaultHTTPAddr = defaultHTTPHost + ":" + defaultHTTPPort
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
		HTTPAddr: httpAddrFromEnvironment(),
		DataDir:  valueOrDefault("NWSL_DATA_DIR", defaultDataDir),
	}
}

func httpAddrFromEnvironment() string {
	if value := os.Getenv("NWSL_HTTP_ADDR"); value != "" {
		return value
	}

	host := valueOrDefault("HOST", defaultHTTPHost)
	port := strings.TrimPrefix(valueOrDefault("PORT", defaultHTTPPort), ":")
	return net.JoinHostPort(host, port)
}

func valueOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
