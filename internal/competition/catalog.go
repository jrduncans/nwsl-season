package competition

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type StageKind string

const (
	StageKindLeagueTable StageKind = "league_table"
	StageKindKnockout    StageKind = "knockout"
	StageKindGroup       StageKind = "group"
)

type Capability string

const (
	CapabilityFixtures           Capability = "fixtures"
	CapabilityStandings          Capability = "standings"
	CapabilityXG                 Capability = "xg"
	CapabilityScheduleDifficulty Capability = "schedule_difficulty"
	CapabilityForecast           Capability = "forecast"
	CapabilityQualification      Capability = "qualification"
	CapabilityScenarios          Capability = "scenarios"
	CapabilityBracket            Capability = "bracket"
)

type InventoryExpectation struct {
	Teams        int
	GamesPerTeam int
	Games        int
}

type Entry struct {
	Season          string
	Stage           string
	Label           string
	Slug            string
	Kind            StageKind
	Public          bool
	Primary         bool
	SourceAvailable bool
	Inventory       *InventoryExpectation
	Rules           *Rules
	Capabilities    []Capability
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

var catalog = []Entry{{
	Season:          "2026",
	Stage:           "Regular Season",
	Label:           "2026 Regular Season",
	Slug:            "regular-season",
	Kind:            StageKindLeagueTable,
	Public:          true,
	Primary:         true,
	SourceAvailable: true,
	Inventory:       &InventoryExpectation{Teams: 16, GamesPerTeam: 30, Games: 240},
	Rules:           &regular2026,
	Capabilities: []Capability{
		CapabilityFixtures,
		CapabilityStandings,
		CapabilityXG,
		CapabilityScheduleDifficulty,
		CapabilityForecast,
		CapabilityQualification,
		CapabilityScenarios,
	},
}}

func (e Entry) Validate() error {
	for name, value := range map[string]string{
		"season": e.Season,
		"stage":  e.Stage,
		"label":  e.Label,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("entry %s is blank", name)
		}
	}
	if !slugPattern.MatchString(e.Slug) {
		return fmt.Errorf("entry slug %q is not lowercase kebab case", e.Slug)
	}
	switch e.Kind {
	case StageKindLeagueTable, StageKindKnockout, StageKindGroup:
	default:
		return fmt.Errorf("entry kind %q is unknown", e.Kind)
	}
	if e.Primary && !e.Public {
		return fmt.Errorf("primary entry must be public")
	}
	if e.Public && !e.SourceAvailable {
		return fmt.Errorf("public entry requires an available ASA source")
	}
	if e.Inventory != nil {
		i := e.Inventory
		if i.Teams < 0 || i.GamesPerTeam < 0 || i.Games < 0 {
			return fmt.Errorf("entry inventory cannot contain negative values")
		}
		if i.Teams == 0 && i.GamesPerTeam == 0 && i.Games == 0 {
			return fmt.Errorf("entry inventory expectation cannot be all zero")
		}
		if (i.Teams == 0) != (i.GamesPerTeam == 0) {
			return fmt.Errorf("entry inventory must set Teams and GamesPerTeam together")
		}
		if i.Teams != 0 {
			product := i.Teams * i.GamesPerTeam
			if product%2 != 0 {
				return fmt.Errorf("entry inventory Teams * GamesPerTeam must be even")
			}
			if i.Games != 0 && i.Games != product/2 {
				return fmt.Errorf("entry inventory Games disagrees with team fields")
			}
		}
	}
	if e.Rules != nil {
		if err := e.Rules.Validate(); err != nil {
			return fmt.Errorf("entry rules are invalid: %w", err)
		}
		if e.Rules.Season != e.Season || e.Rules.Stage != e.Stage {
			return fmt.Errorf("entry rules season/stage does not match entry")
		}
	}
	seen := make(map[Capability]bool, len(e.Capabilities))
	known := map[Capability]bool{
		CapabilityFixtures: true, CapabilityStandings: true, CapabilityXG: true,
		CapabilityScheduleDifficulty: true, CapabilityForecast: true,
		CapabilityQualification: true, CapabilityScenarios: true, CapabilityBracket: true,
	}
	for _, capability := range e.Capabilities {
		if !known[capability] {
			return fmt.Errorf("entry capability %q is unknown", capability)
		}
		if seen[capability] {
			return fmt.Errorf("entry capability %q is duplicated", capability)
		}
		seen[capability] = true
		switch capability {
		case CapabilityStandings, CapabilityScheduleDifficulty, CapabilityForecast, CapabilityQualification, CapabilityScenarios:
			if e.Kind != StageKindLeagueTable {
				return fmt.Errorf("entry capability %q requires a league-table stage", capability)
			}
		case CapabilityBracket:
			if e.Kind != StageKindKnockout {
				return fmt.Errorf("entry capability %q requires a knockout stage", capability)
			}
		}
		if (capability == CapabilityQualification || capability == CapabilityScenarios) && e.Rules == nil {
			return fmt.Errorf("entry capability %q requires verified rules", capability)
		}
	}
	return nil
}

func (e Entry) Copy() Entry {
	copy := e
	if e.Inventory != nil {
		inventory := *e.Inventory
		copy.Inventory = &inventory
	}
	if e.Rules != nil {
		rules := e.Rules.Copy()
		copy.Rules = &rules
	}
	copy.Capabilities = append([]Capability(nil), e.Capabilities...)
	return copy
}

func (e Entry) Supports(capability Capability) bool {
	for _, candidate := range e.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

func Lookup(season, stage string) (Entry, bool) {
	for _, entry := range catalog {
		if entry.Season == season && entry.Stage == stage {
			return entry.Copy(), true
		}
	}
	return Entry{}, false
}

func PublicEntries() []Entry {
	entries := make([]Entry, 0, len(catalog))
	for _, entry := range catalog {
		if entry.Public {
			entries = append(entries, entry.Copy())
		}
	}
	sortEntries(entries)
	return entries
}

func sortEntries(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Season != entries[j].Season {
			return entries[i].Season > entries[j].Season
		}
		if entries[i].Primary != entries[j].Primary {
			return entries[i].Primary
		}
		return entries[i].Label < entries[j].Label
	})
}
