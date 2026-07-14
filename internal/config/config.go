package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultHTTPHost = "127.0.0.1"
	defaultHTTPPort = "8080"
	defaultHTTPAddr = defaultHTTPHost + ":" + defaultHTTPPort
	defaultDataDir  = "data"

	defaultSyncSeason             = "2026"
	defaultSyncStage              = "Regular Season"
	defaultSyncCheckInterval      = 5 * time.Minute
	defaultSyncCompletionGrace    = 3 * time.Hour
	defaultSyncMinAttemptInterval = 30 * time.Minute
	defaultSyncTimeout            = 20 * time.Second
)

// Config holds values that vary between local development and deployment.
type Config struct {
	HTTPAddr string
	DataDir  string
	DBPath   string

	SyncSeason             string
	SyncStage              string
	SyncCheckInterval      time.Duration
	SyncCompletionGrace    time.Duration
	SyncMinAttemptInterval time.Duration
	SyncTimeout            time.Duration
}

// FromEnvironment reads configuration, applying local-development defaults.
func FromEnvironment() (Config, error) {
	dataDir := valueOrDefault("NWSL_DATA_DIR", defaultDataDir)
	checkInterval, err := durationFromEnvironment("NWSL_SYNC_CHECK_INTERVAL", defaultSyncCheckInterval)
	if err != nil {
		return Config{}, err
	}
	completionGrace, err := durationFromEnvironment("NWSL_SYNC_COMPLETION_GRACE", defaultSyncCompletionGrace)
	if err != nil {
		return Config{}, err
	}
	minimumAttemptInterval, err := durationFromEnvironment("NWSL_SYNC_MIN_ATTEMPT_INTERVAL", defaultSyncMinAttemptInterval)
	if err != nil {
		return Config{}, err
	}
	timeout, err := durationFromEnvironment("NWSL_SYNC_TIMEOUT", defaultSyncTimeout)
	if err != nil {
		return Config{}, err
	}

	return Config{
		HTTPAddr: httpAddrFromEnvironment(),
		DataDir:  dataDir,
		DBPath:   filepath.Join(dataDir, "nwsl-season.sqlite"),

		SyncSeason:             valueOrDefault("NWSL_SYNC_SEASON", defaultSyncSeason),
		SyncStage:              valueOrDefault("NWSL_SYNC_STAGE", defaultSyncStage),
		SyncCheckInterval:      checkInterval,
		SyncCompletionGrace:    completionGrace,
		SyncMinAttemptInterval: minimumAttemptInterval,
		SyncTimeout:            timeout,
	}, nil
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

func durationFromEnvironment(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration, got %q", name, value)
	}
	return duration, nil
}
