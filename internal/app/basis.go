package app

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/history"
)

// penaltiesParam names the query parameter that selects the scoring basis on
// Explore and the league history page. Absent or "include" counts penalty
// kicks; "exclude" uses non-penalty goals and non-penalty xG.
const (
	penaltiesParam   = "penalties"
	penaltiesExclude = "exclude"
	penaltiesInclude = "include"
)

// pointsBasisNote explains that official points and ASA xPts keep penalties.
const pointsBasisNote = "Points include penalties; ASA publishes no non-penalty expected points."

var errPenaltiesSelection = fmt.Errorf("penalties must be include or exclude")

// penaltiesBasis reads the basis from already-decoded query values.
func penaltiesBasis(query url.Values) (history.ScoringBasis, error) {
	values, present := query[penaltiesParam]
	if !present {
		return history.AllShots, nil
	}
	if len(values) != 1 {
		return history.AllShots, errPenaltiesSelection
	}
	return penaltiesValue(values[0])
}

// penaltiesBasisRaw reads the basis from a raw query string, ignoring
// malformed unrelated fields the way the other History state does.
func penaltiesBasisRaw(rawQuery string) (history.ScoringBasis, error) {
	var values []string
	for _, part := range strings.Split(rawQuery, "&") {
		key, value, _ := strings.Cut(part, "=")
		decoded, err := url.QueryUnescape(key)
		if err != nil || decoded != penaltiesParam {
			continue
		}
		decodedValue, err := url.QueryUnescape(value)
		if err != nil {
			return history.AllShots, errPenaltiesSelection
		}
		values = append(values, decodedValue)
	}
	if len(values) == 0 {
		return history.AllShots, nil
	}
	if len(values) != 1 {
		return history.AllShots, errPenaltiesSelection
	}
	return penaltiesValue(values[0])
}

func penaltiesValue(value string) (history.ScoringBasis, error) {
	switch value {
	case penaltiesInclude:
		return history.AllShots, nil
	case penaltiesExclude:
		return history.NonPenalty, nil
	default:
		return history.AllShots, errPenaltiesSelection
	}
}

// withPenalties returns a copy of values with the basis applied. The default
// basis is omitted so existing links and canonical URLs do not change.
func withPenalties(values url.Values, basis history.ScoringBasis) url.Values {
	copied := make(url.Values, len(values)+1)
	for key, value := range values {
		copied[key] = value
	}
	copied.Del(penaltiesParam)
	if basis.ExcludesPenalties() {
		copied.Set(penaltiesParam, penaltiesExclude)
	}
	return copied
}

// basisPattern matches every label that changes with the basis in one pass, so
// replacement text is never rewritten again. The browser script uses the same
// alternatives.
var basisPattern = regexp.MustCompile(`\b(?:\d\+? goals?|Expected goals|expected goals|xGD|xG|Goal differential|Goal margin|Total goals|Goals)\b`)

func basisReplacement(match string) string {
	switch match {
	case "Expected goals":
		return "Non-penalty expected goals"
	case "expected goals":
		return "non-penalty expected goals"
	case "xGD":
		return "npxGD"
	case "xG":
		return "npxG"
	case "Goal margin":
		return "Non-penalty goal margin"
	case "Goal differential":
		return "Non-penalty goal differential"
	case "Total goals":
		return "Total non-penalty goals"
	case "Goals":
		return "Non-penalty goals"
	}
	// "4+ goals", "1 goal": a count of goals.
	count, word, _ := strings.Cut(match, " ")
	return count + " non-penalty " + word
}

// basisText rewrites a user-visible label for the scoring basis. It is applied
// once to source text and leaves the default basis unchanged. The browser
// script mirrors these rules.
func basisText(basis history.ScoringBasis, text string) string {
	if !basis.ExcludesPenalties() || strings.Contains(text, "npxG") || strings.Contains(text, "on-penalty") {
		return text
	}
	return basisPattern.ReplaceAllStringFunc(text, basisReplacement)
}
