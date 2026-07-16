// Package fixtures owns common fixture status and timestamp conventions.
package fixtures

import (
	"fmt"
	"time"
)

const (
	CompletedStatus = "FullTime"
	PreMatchStatus  = "PreMatch"
	AbandonedStatus = "Abandoned"
)

// ParseKickoff accepts the two UTC representations retained from ASA payloads.
func ParseKickoff(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05 MST"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("parse kickoff %q", value)
}
