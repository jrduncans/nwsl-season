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
	ShortLabel      string
	Slug            string
	Kind            StageKind
	Public          bool
	Primary         bool
	SourceAvailable bool
	Inventory       *InventoryExpectation
	Rules           *Rules
	PlayoffPlaces   int
	Capabilities    []Capability
	BracketFormat   *BracketFormat
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

var catalog = append(append(historicalRegularSeasonEntries(), Entry{
	Season:          "2026",
	Stage:           "Regular Season",
	Label:           "2026 Regular Season",
	ShortLabel:      "Regular Season",
	Slug:            "regular-season",
	Kind:            StageKindLeagueTable,
	Public:          true,
	Primary:         true,
	SourceAvailable: true,
	Inventory:       &InventoryExpectation{Teams: 16, GamesPerTeam: 30, Games: 240},
	Rules:           &regular2026,
	PlayoffPlaces:   8,
	Capabilities: []Capability{
		CapabilityFixtures,
		CapabilityStandings,
		CapabilityXG,
		CapabilityScheduleDifficulty,
		CapabilityForecast,
		CapabilityQualification,
		CapabilityScenarios,
	},
}), publicKnockoutEntries()...)

func publicKnockoutEntries() []Entry {
	entries := make([]Entry, 0, 25)
	for _, season := range []string{"2016", "2017", "2018", "2019", "2021", "2022", "2023", "2024", "2025", "2026"} {
		entry := Entry{
			Season: season, Stage: "Playoffs", Label: season + " Playoffs", ShortLabel: "Playoffs", Slug: "playoffs",
			Kind: StageKindKnockout, Public: true, SourceAvailable: true,
			Capabilities: []Capability{CapabilityFixtures, CapabilityXG, CapabilityBracket}, BracketFormat: playoffBracket(season),
		}
		switch season {
		case "2016", "2017", "2018", "2019":
			entry.Inventory = &InventoryExpectation{Games: 3}
		case "2021", "2022", "2023":
			entry.Inventory = &InventoryExpectation{Games: 5}
		case "2024", "2025":
			entry.Inventory = &InventoryExpectation{Games: 7}
		}
		entries = append(entries, entry)
	}
	for _, season := range []string{"2020", "2021", "2022", "2023"} {
		games := map[string]int{"2020": 16, "2021": 20, "2022": 36, "2023": 36}[season]
		entries = append(entries, Entry{
			Season: season, Stage: "NWSL Challenge Cup Group Stage", Label: season + " NWSL Challenge Cup Group Stage", ShortLabel: "Challenge Cup Group Stage", Slug: "challenge-cup-group-stage",
			Kind: StageKindGroup, Public: true, Primary: season == "2020", SourceAvailable: true,
			Inventory: &InventoryExpectation{Games: games}, Capabilities: []Capability{CapabilityFixtures, CapabilityXG},
		})
	}
	for _, season := range []string{"2020", "2021", "2022", "2023"} {
		games := map[string]int{"2020": 7, "2021": 1, "2022": 3, "2023": 3}[season]
		entries = append(entries, Entry{
			Season: season, Stage: "NWSL Challenge Cup Knockout Round", Label: season + " NWSL Challenge Cup Knockout Round", ShortLabel: "Challenge Cup Knockout", Slug: "challenge-cup-knockout-round",
			Kind: StageKindKnockout, Public: true, SourceAvailable: true,
			Inventory: &InventoryExpectation{Games: games}, Capabilities: []Capability{CapabilityFixtures, CapabilityXG, CapabilityBracket}, BracketFormat: challengeCupKnockoutBracket(season),
		})
	}
	for _, season := range []string{"2024", "2025", "2026"} {
		entries = append(entries, Entry{
			Season: season, Stage: "NWSL Challenge Cup Final", Label: season + " NWSL Challenge Cup Final", ShortLabel: "Challenge Cup Final", Slug: "challenge-cup-final",
			Kind: StageKindKnockout, Public: true, SourceAvailable: true,
			Inventory: &InventoryExpectation{Games: 1}, Capabilities: []Capability{CapabilityFixtures, CapabilityXG, CapabilityBracket}, BracketFormat: singleFinalBracket(),
		})
	}
	return entries
}

func init() {
	if err := validateCatalog(catalog); err != nil {
		panic(err)
	}
}

func validateCatalog(entries []Entry) error {
	slugs := map[string]bool{}
	primaries := map[string]bool{}
	for _, entry := range entries {
		if err := entry.Validate(); err != nil {
			return err
		}
		key := entry.Season + "\x00" + entry.Slug
		if slugs[key] {
			return fmt.Errorf("duplicate stage slug %q for season %q", entry.Slug, entry.Season)
		}
		slugs[key] = true
		if entry.Public && entry.Primary {
			if primaries[entry.Season] {
				return fmt.Errorf("multiple public primary stages for season %q", entry.Season)
			}
			primaries[entry.Season] = true
		}
	}
	return nil
}

func historicalRegularSeasonEntries() []Entry {
	seasons := []string{"2016", "2017", "2018", "2019", "2021", "2022", "2023", "2024", "2025"}
	entries := make([]Entry, 0, len(seasons))
	for _, season := range seasons {
		entries = append(entries, Entry{
			Season:          season,
			Stage:           "Regular Season",
			Label:           season + " Regular Season",
			ShortLabel:      "Regular Season",
			Slug:            "regular-season",
			Kind:            StageKindLeagueTable,
			Public:          true,
			Primary:         true,
			SourceAvailable: true,
			PlayoffPlaces:   historicalPlayoffPlaces[season],
			Capabilities:    []Capability{CapabilityFixtures, CapabilityStandings, CapabilityXG},
		})
	}
	return entries
}

// historicalPlayoffPlaces records the regular-season postseason cut line
// documented by the NWSL: four places through 2019, six from 2021 through
// 2023, and eight from 2024 onward. It deliberately does not imply verified
// inventory, tiebreak, clinching, or playoff-bracket rules.
var historicalPlayoffPlaces = map[string]int{
	"2016": 4, "2017": 4, "2018": 4, "2019": 4,
	"2021": 6, "2022": 6, "2023": 6,
	"2024": 8, "2025": 8,
}

func (e Entry) Validate() error {
	for name, value := range map[string]string{
		"season":      e.Season,
		"stage":       e.Stage,
		"label":       e.Label,
		"short label": e.ShortLabel,
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
	if e.PlayoffPlaces < 0 {
		return fmt.Errorf("entry playoff places cannot be negative")
	}
	if e.PlayoffPlaces > 0 {
		if e.Kind != StageKindLeagueTable {
			return fmt.Errorf("entry playoff places require a league-table stage")
		}
		if e.Inventory != nil && e.Inventory.Teams > 0 && e.PlayoffPlaces > e.Inventory.Teams {
			return fmt.Errorf("entry playoff places exceed inventory teams")
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
	if e.BracketFormat != nil {
		if e.Kind != StageKindKnockout {
			return fmt.Errorf("bracket format requires a knockout stage")
		}
		if !seen[CapabilityBracket] {
			return fmt.Errorf("bracket format requires bracket capability")
		}
		if err := e.BracketFormat.Validate(); err != nil {
			return fmt.Errorf("entry bracket format is invalid: %w", err)
		}
	} else if seen[CapabilityBracket] {
		return fmt.Errorf("bracket capability requires a bracket format")
	}
	if e.PlayoffPlaces > 0 && !seen[CapabilityStandings] {
		return fmt.Errorf("entry playoff places require standings capability")
	}
	if e.Rules != nil {
		for _, achievement := range e.Rules.Achievements {
			if achievement.ID == AchievementPlayoffs && e.PlayoffPlaces != achievement.TopK {
				return fmt.Errorf("entry playoff places disagree with rules")
			}
		}
	}
	return nil
}

func (e Entry) Copy() Entry {
	clone := e
	if e.Inventory != nil {
		inventory := *e.Inventory
		clone.Inventory = &inventory
	}
	if e.Rules != nil {
		rules := e.Rules.Copy()
		clone.Rules = &rules
	}
	if e.BracketFormat != nil {
		format := e.BracketFormat.Copy()
		clone.BracketFormat = &format
	}
	clone.Capabilities = append([]Capability(nil), e.Capabilities...)
	return clone
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

// LookupSlug finds a public stage by its canonical URL slug.
func LookupSlug(season, slug string) (Entry, bool) {
	for _, entry := range catalog {
		if entry.Season == season && entry.Slug == slug && entry.Public {
			return entry.Copy(), true
		}
	}
	return Entry{}, false
}

// PrimaryEntry returns the one public primary stage for a season.
func PrimaryEntry(season string) (Entry, bool) {
	for _, entry := range catalog {
		if entry.Season == season && entry.Public && entry.Primary {
			return entry.Copy(), true
		}
	}
	return Entry{}, false
}

// PublicEntriesForSeason returns public stages in deterministic navigation order.
func PublicEntriesForSeason(season string) []Entry {
	entries := []Entry{}
	for _, entry := range catalog {
		if entry.Season == season && entry.Public {
			entries = append(entries, entry.Copy())
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Primary != entries[j].Primary {
			return entries[i].Primary
		}
		return stageFamilyRank(entries[i]) < stageFamilyRank(entries[j])
	})
	return entries
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

// SourceEntries returns defensive copies of catalog entries with an available
// ASA source. Source entries may be private because source availability and
// public product availability are intentionally separate concerns.
func SourceEntries() []Entry {
	return sourceEntries(catalog)
}

func sourceEntries(catalog []Entry) []Entry {
	entries := make([]Entry, 0, len(catalog))
	for _, entry := range catalog {
		if entry.SourceAvailable {
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
		left, right := stageFamilyRank(entries[i]), stageFamilyRank(entries[j])
		if left != right {
			return left < right
		}
		return entries[i].Label < entries[j].Label
	})
}

func stageFamilyRank(entry Entry) int {
	switch entry.Slug {
	case "regular-season":
		return 0
	case "playoffs":
		return 1
	case "challenge-cup-group-stage":
		return 2
	case "challenge-cup-knockout-round":
		return 3
	case "challenge-cup-final":
		return 4
	default:
		return 5
	}
}
