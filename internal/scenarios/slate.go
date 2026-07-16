package scenarios

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"time"
)

// DefineSlate chooses a deterministic, safe next fixture slate.
func DefineSlate(games []ScheduledGame) (Slate, error) {
	all := append([]ScheduledGame(nil), games...)
	seen := map[string]bool{}
	for _, g := range all {
		if g.ID == "" || seen[g.ID] || g.KickoffUTC.IsZero() || g.HomeTeamID == "" || g.AwayTeamID == "" || g.HomeTeamID == g.AwayTeamID {
			return Slate{}, fmt.Errorf("invalid scheduled game %q", g.ID)
		}
		seen[g.ID] = true
		switch g.Status {
		case "FullTime":
			if g.HomeScore == nil || g.AwayScore == nil {
				return Slate{}, fmt.Errorf("completed game %q lacks score", g.ID)
			}
		case "PreMatch":
			if g.HomeScore != nil || g.AwayScore != nil {
				return Slate{}, fmt.Errorf("prematch game %q has score", g.ID)
			}
		default:
			return Slate{}, fmt.Errorf("unsafe game status %q", g.Status)
		}
	}
	pending := []ScheduledGame{}
	for _, g := range all {
		if g.Status == "PreMatch" {
			pending = append(pending, g)
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].KickoffUTC.Equal(pending[j].KickoffUTC) {
			return pending[i].ID < pending[j].ID
		}
		return pending[i].KickoffUTC.Before(pending[j].KickoffUTC)
	})
	if len(pending) == 0 {
		return Slate{DefinitionVersion: DefinitionVersion, State: SlateNoUpcoming, FixtureIDs: []string{}}, nil
	}
	seed := pending[0]
	reliable := seed.Matchday != nil && *seed.Matchday > 0
	var selected []ScheduledGame
	if reliable {
		md := *seed.Matchday
		var first, last time.Time
		for _, g := range all {
			if g.Matchday == nil || *g.Matchday != md {
				continue
			}
			if g.KickoffUTC.IsZero() {
				reliable = false
				break
			}
			if first.IsZero() || g.KickoffUTC.Before(first) {
				first = g.KickoffUTC
			}
			if last.IsZero() || g.KickoffUTC.After(last) {
				last = g.KickoffUTC
			}
		}
		if reliable {
			for _, g := range pending {
				if g.Matchday != nil && *g.Matchday == md {
					selected = append(selected, g)
				}
			}
			for _, g := range pending {
				if (g.Matchday == nil || *g.Matchday != md) && !g.KickoffUTC.Before(first) && !g.KickoffUTC.After(last) {
					reliable = false
					break
				}
			}
		}
	}
	s := Slate{DefinitionVersion: DefinitionVersion, State: SlateReady, FixtureIDs: []string{}}
	if reliable {
		s.Source = SourceMatchday
		s.Matchday = *seed.Matchday
	} else {
		s.Source = SourceKickoffWindow
		cutoff := seed.KickoffUTC.UTC().Add(120 * time.Hour)
		for _, g := range pending {
			k := g.KickoffUTC.UTC()
			if !k.Before(seed.KickoffUTC.UTC()) && k.Before(cutoff) {
				selected = append(selected, g)
			}
		}
		s.CutoffUTC = cutoff
	}
	if len(selected) == 0 {
		return Slate{}, fmt.Errorf("empty ready slate")
	}
	sort.Slice(selected, func(i, j int) bool {
		if selected[i].KickoffUTC.Equal(selected[j].KickoffUTC) {
			return selected[i].ID < selected[j].ID
		}
		return selected[i].KickoffUTC.Before(selected[j].KickoffUTC)
	})
	s.StartsAtUTC = selected[0].KickoffUTC.UTC()
	s.LatestKickoffUTC = selected[len(selected)-1].KickoffUTC.UTC()
	if s.Source == SourceMatchday {
		s.CutoffUTC = s.LatestKickoffUTC
	}
	for _, g := range selected {
		s.FixtureIDs = append(s.FixtureIDs, g.ID)
	}
	s.ID = slateID(s)
	return s, nil
}
func slateID(s Slate) string {
	h := sha256.New()
	put := func(v string) {
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(len(v)))
		h.Write(b[:])
		h.Write([]byte(v))
	}
	put(s.DefinitionVersion)
	put(string(s.State))
	put(string(s.Source))
	put(fmt.Sprint(s.Matchday))
	put(s.StartsAtUTC.UTC().Format(time.RFC3339Nano))
	put(s.LatestKickoffUTC.UTC().Format(time.RFC3339Nano))
	put(s.CutoffUTC.UTC().Format(time.RFC3339Nano))
	for _, id := range s.FixtureIDs {
		put(id)
	}
	return strings.ToLower(fmt.Sprintf("%x", h.Sum(nil)))
}
