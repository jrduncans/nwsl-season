package config

import (
	"net"
	"os"
	"path/filepath"
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
	DBPath   string
}

// FromEnvironment reads configuration, applying local-development defaults.
func FromEnvironment() Config {
	dataDir := valueOrDefault("NWSL_DATA_DIR", defaultDataDir)
	return Config{
		HTTPAddr: httpAddrFromEnvironment(),
		DataDir:  dataDir,
		DBPath:   filepath.Join(dataDir, "nwsl-season.sqlite"),
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
