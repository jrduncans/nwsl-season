package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	defaultEnvironmentFile  = "config.env"
	maxEnvironmentLineBytes = 1 << 20 // 1 MiB
)

// LoadEnvironmentFile loads a dotenv-style configuration file before the
// application reads its environment. The default path is config.env in the
// current working directory; NWSL_CONFIG_FILE selects another path.
//
// Existing process variables always win, so deployment-provided values cannot
// be accidentally replaced by a local file. A missing file is not an error,
// which keeps the normal environment-only deployment flow unchanged.
func LoadEnvironmentFile() error {
	path := strings.TrimSpace(os.Getenv("NWSL_CONFIG_FILE"))
	if path == "" {
		path = defaultEnvironmentFile
	}
	values, err := environmentFileValues(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read configuration environment file %s: %w", path, err)
	}
	for _, value := range values {
		if _, configured := os.LookupEnv(value.name); configured {
			continue
		}
		if err := os.Setenv(value.name, value.value); err != nil {
			return fmt.Errorf("set %s from %s: %w", value.name, path, err)
		}
	}
	return nil
}

type environmentValue struct {
	name  string
	value string
}

func environmentFileValues(path string) ([]environmentValue, error) {
	// #nosec G304 G703 -- NWSL_CONFIG_FILE intentionally selects the local configuration file.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxEnvironmentLineBytes)
	values := []environmentValue{}
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		name, value, present, err := parseEnvironmentLine(line)
		if err != nil {
			return nil, fmt.Errorf("read %s line %d: %w", path, lineNumber, err)
		}
		if present {
			values = append(values, environmentValue{name: name, value: value})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return values, nil
}

func parseEnvironmentLine(line string) (name, value string, present bool, err error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false, nil
	}
	if strings.HasPrefix(line, "export ") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	}
	separator := strings.IndexByte(line, '=')
	if separator < 1 {
		return "", "", false, fmt.Errorf("expected NAME=VALUE")
	}
	name = strings.TrimSpace(line[:separator])
	if !validEnvironmentName(name) {
		return "", "", false, fmt.Errorf("invalid variable name %q", name)
	}
	value = strings.TrimSpace(strings.TrimSuffix(line[separator+1:], "\r"))
	if strings.HasPrefix(value, "\"") {
		value, err = strconv.Unquote(value)
		if err != nil {
			return "", "", false, fmt.Errorf("invalid double-quoted value")
		}
	} else if strings.HasPrefix(value, "'") {
		if len(value) < 2 || !strings.HasSuffix(value, "'") {
			return "", "", false, fmt.Errorf("invalid single-quoted value")
		}
		value = value[1 : len(value)-1]
	}
	return name, value, true, nil
}

func validEnvironmentName(name string) bool {
	if name == "" {
		return false
	}
	for index := range len(name) {
		value := name[index]
		if (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z') || value == '_' {
			continue
		}
		if index > 0 && value >= '0' && value <= '9' {
			continue
		}
		return false
	}
	return true
}
