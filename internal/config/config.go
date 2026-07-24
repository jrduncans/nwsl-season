package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
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
	defaultQualificationBudget    = 5 * time.Second
	defaultScenarioBudget         = 30 * time.Second
	defaultHistoryRetention       = 90 * 24 * time.Hour
	defaultForecastConcurrency    = 2
	defaultForecastTimeout        = 15 * time.Second
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
	QualificationBudget    time.Duration
	ScenarioBudget         time.Duration
	HistoryRetention       time.Duration
	ForecastConcurrency    int
	ForecastTimeout        time.Duration
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
	qualificationBudget, err := durationFromEnvironment("NWSL_QUALIFICATION_BUDGET", defaultQualificationBudget)
	if err != nil {
		return Config{}, err
	}
	scenarioBudget, err := durationFromEnvironment("NWSL_SCENARIO_BUDGET", defaultScenarioBudget)
	if err != nil {
		return Config{}, err
	}
	historyRetention, err := durationFromEnvironment("NWSL_HISTORY_RETENTION", defaultHistoryRetention)
	if err != nil {
		return Config{}, err
	}
	forecastConcurrency, err := positiveIntFromEnvironment("NWSL_FORECAST_CONCURRENCY", defaultForecastConcurrency)
	if err != nil {
		return Config{}, err
	}
	forecastTimeout, err := durationFromEnvironment("NWSL_FORECAST_TIMEOUT", defaultForecastTimeout)
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
		QualificationBudget:    qualificationBudget,
		ScenarioBudget:         scenarioBudget,
		HistoryRetention:       historyRetention,
		ForecastConcurrency:    forecastConcurrency,
		ForecastTimeout:        forecastTimeout,
	}, nil
}

func positiveIntFromEnvironment(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", name, value)
	}
	return parsed, nil
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
