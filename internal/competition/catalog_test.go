package competition

import (
	"reflect"
	"strings"
	"testing"
)

func TestLookup2026RegularSeason(t *testing.T) {
	entry, ok := Lookup("2026", "Regular Season")
	if !ok {
		t.Fatal("expected verified 2026 regular season")
	}
	if err := entry.Validate(); err != nil {
		t.Fatal(err)
	}
	if entry.Season != "2026" || entry.Stage != "Regular Season" || entry.Label != "2026 Regular Season" || entry.Slug != "regular-season" || entry.Kind != StageKindLeagueTable || !entry.Public || !entry.Primary || !entry.SourceAvailable {
		t.Fatalf("entry = %+v", entry)
	}
	if *entry.Inventory != (InventoryExpectation{Teams: 16, GamesPerTeam: 30, Games: 240}) {
		t.Fatalf("inventory = %+v", entry.Inventory)
	}
	if entry.PlayoffPlaces != 8 {
		t.Fatalf("playoff places = %d, want 8", entry.PlayoffPlaces)
	}
	want := []Capability{CapabilityFixtures, CapabilityStandings, CapabilityXG, CapabilityScheduleDifficulty, CapabilityForecast, CapabilityQualification, CapabilityScenarios}
	if len(entry.Capabilities) != len(want) {
		t.Fatalf("capabilities = %v, want %v", entry.Capabilities, want)
	}
	for i, capability := range want {
		if entry.Capabilities[i] != capability || !entry.Supports(capability) {
			t.Fatalf("capabilities = %v, want %v", entry.Capabilities, want)
		}
	}
	if entry.Supports(CapabilityBracket) {
		t.Fatal("regular season unexpectedly supports bracket")
	}
}

func TestLookupUnknownScopes(t *testing.T) {
	for _, scope := range [][2]string{{"2027", "Regular Season"}, {"2020", "Regular Season"}} {
		if _, ok := Lookup(scope[0], scope[1]); ok {
			t.Fatalf("Lookup(%q, %q) unexpectedly succeeded", scope[0], scope[1])
		}
	}
	if entry, ok := Lookup("2026", "Playoffs"); !ok || entry.Kind != StageKindKnockout || entry.Primary || entry.Inventory != nil || entry.Rules != nil || !entry.Supports(CapabilityFixtures) || !entry.Supports(CapabilityXG) || !entry.Supports(CapabilityBracket) || entry.BracketFormat == nil {
		t.Fatalf("playoff entry=%+v,%t", entry, ok)
	}
}

func TestCatalogValidationRejectsAmbiguousPublicRouting(t *testing.T) {
	base := Entry{Season: "2040", Stage: "Regular", Label: "2040 Regular", ShortLabel: "Regular", Slug: "regular", Kind: StageKindLeagueTable, Public: true, Primary: true, SourceAvailable: true}
	if err := validateCatalog([]Entry{base, {Season: "2040", Stage: "Playoffs", Label: "2040 Playoffs", ShortLabel: "Playoffs", Slug: "regular", Kind: StageKindKnockout, Public: true, SourceAvailable: true}}); err == nil {
		t.Fatal("duplicate slug succeeded")
	}
	other := base
	other.Stage, other.Label, other.ShortLabel, other.Slug = "Other", "2040 Other", "Other", "other"
	if err := validateCatalog([]Entry{base, other}); err == nil {
		t.Fatal("multiple public primaries succeeded")
	}
	if entry, ok := PrimaryEntry("2026"); !ok || entry.Stage != "Regular Season" {
		t.Fatalf("primary=%+v,%t", entry, ok)
	}
	entries := PublicEntriesForSeason("2026")
	if len(entries) != 3 || entries[0].Stage != "Regular Season" || entries[1].Stage != "Playoffs" || entries[2].Stage != "NWSL Challenge Cup Final" {
		t.Fatalf("stages=%+v", entries)
	}
}

func TestHistoricalRegularSeasonCatalogIsSourceOnlyAndFactual(t *testing.T) {
	wantSeasons := []string{"2025", "2024", "2023", "2022", "2021", "2019", "2018", "2017", "2016"}
	wantPlayoffPlaces := map[string]int{"2016": 4, "2017": 4, "2018": 4, "2019": 4, "2021": 6, "2022": 6, "2023": 6, "2024": 8, "2025": 8}
	entries := PublicEntries()
	if len(entries) != 31 {
		t.Fatalf("public entries = %d, want 31", len(entries))
	}
	for _, season := range wantSeasons {
		entry, ok := Lookup(season, "Regular Season")
		if !ok {
			t.Fatalf("missing historical entry %s", season)
		}
		if entry.Season != season || entry.Stage != "Regular Season" || !entry.Public || !entry.Primary || !entry.SourceAvailable || entry.Inventory != nil || entry.Rules != nil {
			t.Fatalf("historical entry %s = %+v", season, entry)
		}
		if entry.PlayoffPlaces != wantPlayoffPlaces[season] {
			t.Fatalf("%s playoff places = %d, want %d", season, entry.PlayoffPlaces, wantPlayoffPlaces[season])
		}
		wantCapabilities := []Capability{CapabilityFixtures, CapabilityStandings, CapabilityXG}
		if len(entry.Capabilities) != len(wantCapabilities) {
			t.Fatalf("%s capabilities = %v", season, entry.Capabilities)
		}
		for j, capability := range wantCapabilities {
			if entry.Capabilities[j] != capability {
				t.Fatalf("%s capabilities = %v, want %v", season, entry.Capabilities, wantCapabilities)
			}
		}
	}
}

func TestCatalogCopiesAreDefensive(t *testing.T) {
	first, _ := Lookup("2026", "Regular Season")
	first.Inventory.Teams = 1
	first.Capabilities[0] = CapabilityBracket
	first.Rules.Achievements[0].TopK = 99
	second, _ := Lookup("2026", "Regular Season")
	if second.Inventory.Teams != 16 || second.Capabilities[0] != CapabilityFixtures || second.Rules.Achievements[0].TopK != 1 {
		t.Fatalf("lookup exposed mutation: %+v", second)
	}

	public := PublicEntries()
	public[0].Inventory.Games = 1
	public[0].Capabilities[0] = CapabilityBracket
	public[0].Rules.Achievements[0].Label = "changed"
	again := PublicEntries()[0]
	if again.Inventory.Games != 240 || again.Capabilities[0] != CapabilityFixtures || again.Rules.Achievements[0].Label != "Shield" {
		t.Fatalf("public entries exposed mutation: %+v", again)
	}
}

func TestPublicEntryOrderingHelper(t *testing.T) {
	entries := []Entry{
		{Season: "2025", Label: "2025 B", Primary: true},
		{Season: "2026", Label: "2026 Secondary", Primary: false},
		{Season: "2026", Label: "2026 Primary", Primary: true},
		{Season: "2026", Label: "2026 Alpha", Primary: false},
	}
	sortEntries(entries)
	got := []string{entries[0].Label, entries[1].Label, entries[2].Label, entries[3].Label}
	want := []string{"2026 Primary", "2026 Alpha", "2026 Secondary", "2025 B"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ordering = %v, want %v", got, want)
		}
	}
}

func TestSourceEntriesFilteringOrderingAndCopies(t *testing.T) {
	entries := []Entry{
		{Season: "2025", Stage: "Regular Season", Label: "2025", SourceAvailable: true},
		{Season: "2026", Stage: "Private", Label: "Private", Primary: false, SourceAvailable: true, Inventory: &InventoryExpectation{Teams: 2}, Capabilities: []Capability{CapabilityFixtures}},
		{Season: "2026", Stage: "Primary", Label: "Primary", Primary: true, SourceAvailable: true, Inventory: &InventoryExpectation{Teams: 4}, Capabilities: []Capability{CapabilityFixtures}},
		{Season: "2027", Stage: "Unavailable", Label: "Unavailable", SourceAvailable: false},
	}

	got := sourceEntries(entries)
	if len(got) != 3 {
		t.Fatalf("source entries = %d, want 3", len(got))
	}
	labels := []string{got[0].Label, got[1].Label, got[2].Label}
	want := []string{"Primary", "Private", "2025"}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("source entry ordering = %v, want %v", labels, want)
		}
	}
	got[0].Inventory.Teams = 99
	got[0].Capabilities[0] = CapabilityBracket
	if entries[2].Inventory.Teams != 4 || entries[2].Capabilities[0] != CapabilityFixtures {
		t.Fatalf("source entries exposed catalog mutation: %+v", entries[2])
	}
}

func validEntry() Entry {
	return Entry{Season: "2026", Stage: "Test", Label: "Test", ShortLabel: "Test", Slug: "test", Kind: StageKindLeagueTable, SourceAvailable: true}
}

func TestEntryValidationRejections(t *testing.T) {
	base := validEntry()
	cases := []struct {
		name string
		edit func(*Entry)
		want string
	}{
		{"blank season", func(e *Entry) { e.Season = " " }, "season"},
		{"blank stage", func(e *Entry) { e.Stage = "" }, "stage"},
		{"blank label", func(e *Entry) { e.Label = "" }, "label"},
		{"blank short label", func(e *Entry) { e.ShortLabel = " " }, "short label"},
		{"blank slug", func(e *Entry) { e.Slug = " " }, "slug"},
		{"invalid slug", func(e *Entry) { e.Slug = "Bad_slug" }, "slug"},
		{"unknown kind", func(e *Entry) { e.Kind = "other" }, "kind"},
		{"private primary", func(e *Entry) { e.Primary = true }, "primary"},
		{"unavailable public source", func(e *Entry) { e.Public = true; e.SourceAvailable = false }, "source"},
		{"negative inventory", func(e *Entry) { e.Inventory = &InventoryExpectation{Teams: -1} }, "negative"},
		{"all zero inventory", func(e *Entry) { e.Inventory = &InventoryExpectation{} }, "all zero"},
		{"only teams", func(e *Entry) { e.Inventory = &InventoryExpectation{Teams: 2} }, "together"},
		{"odd inventory product", func(e *Entry) { e.Inventory = &InventoryExpectation{Teams: 3, GamesPerTeam: 1} }, "even"},
		{"wrong games", func(e *Entry) { e.Inventory = &InventoryExpectation{Teams: 2, GamesPerTeam: 2, Games: 3} }, "disagrees"},
		{"negative playoff places", func(e *Entry) { e.PlayoffPlaces = -1 }, "negative"},
		{"playoff places non league", func(e *Entry) { e.Kind = StageKindKnockout; e.PlayoffPlaces = 1 }, "league-table"},
		{"playoff places exceed teams", func(e *Entry) { e.Inventory = &InventoryExpectation{Teams: 2, GamesPerTeam: 2}; e.PlayoffPlaces = 3 }, "exceed"},
		{"playoff places without standings", func(e *Entry) { e.PlayoffPlaces = 1 }, "standings"},
		{"invalid rules", func(e *Entry) { e.Rules = &Rules{} }, "rules"},
		{"rules mismatch", func(e *Entry) { r := regular2026.Copy(); r.Stage = "Other"; e.Rules = &r; e.Season = r.Season }, "match"},
		{"playoff places disagree with rules", func(e *Entry) {
			r := regular2026.Copy()
			e.Season, e.Stage, e.Rules, e.PlayoffPlaces = r.Season, r.Stage, &r, 7
			e.Capabilities = []Capability{CapabilityStandings}
		}, "disagree"},
		{"unknown capability", func(e *Entry) { e.Capabilities = []Capability{"other"} }, "unknown"},
		{"duplicate capability", func(e *Entry) { e.Capabilities = []Capability{CapabilityFixtures, CapabilityFixtures} }, "duplicated"},
		{"standings non league", func(e *Entry) { e.Kind = StageKindGroup; e.Capabilities = []Capability{CapabilityStandings} }, "league-table"},
		{"schedule difficulty non league", func(e *Entry) { e.Kind = StageKindGroup; e.Capabilities = []Capability{CapabilityScheduleDifficulty} }, "league-table"},
		{"forecast non league", func(e *Entry) { e.Kind = StageKindGroup; e.Capabilities = []Capability{CapabilityForecast} }, "league-table"},
		{"qualification non league", func(e *Entry) {
			e.Kind = StageKindGroup
			e.Rules = &regular2026
			e.Stage = regular2026.Stage
			e.Capabilities = []Capability{CapabilityQualification}
		}, "league-table"},
		{"scenarios non league", func(e *Entry) {
			e.Kind = StageKindGroup
			e.Rules = &regular2026
			e.Stage = regular2026.Stage
			e.Capabilities = []Capability{CapabilityScenarios}
		}, "league-table"},
		{"qualification without rules", func(e *Entry) { e.Capabilities = []Capability{CapabilityQualification} }, "verified rules"},
		{"scenarios without rules", func(e *Entry) { e.Capabilities = []Capability{CapabilityScenarios} }, "verified rules"},
		{"bracket non knockout", func(e *Entry) { e.Capabilities = []Capability{CapabilityBracket} }, "knockout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := base
			tc.edit(&entry)
			err := entry.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestValidNilAndStageSpecificEntries(t *testing.T) {
	for _, entry := range []Entry{
		validEntry(),
		{Season: "2026", Stage: "Exhibition", Label: "Exhibition", ShortLabel: "Exhibition", Slug: "exhibition", Kind: StageKindLeagueTable, Capabilities: []Capability{CapabilityFixtures, CapabilityXG}},
		{Season: "2026", Stage: "Final", Label: "Final", ShortLabel: "Final", Slug: "final", Kind: StageKindKnockout, Capabilities: []Capability{CapabilityBracket}, BracketFormat: singleFinalBracket()},
	} {
		if err := entry.Validate(); err != nil {
			t.Fatalf("valid entry rejected: %v", err)
		}
	}
}

func TestPublicCompetitionCatalogMetadata(t *testing.T) {
	tests := []struct {
		season, stage, slug, short string
		kind                       StageKind
		primary                    bool
		games, rounds, slots       int
		policy                     AdvancementPolicy
	}{
		{"2016", "Playoffs", "playoffs", "Playoffs", StageKindKnockout, false, 3, 2, 3, AdvancementPolicyFixed},
		{"2021", "Playoffs", "playoffs", "Playoffs", StageKindKnockout, false, 5, 3, 5, AdvancementPolicyHistoricallyObservedReseeded},
		{"2024", "Playoffs", "playoffs", "Playoffs", StageKindKnockout, false, 7, 3, 7, AdvancementPolicyFixed},
		{"2026", "Playoffs", "playoffs", "Playoffs", StageKindKnockout, false, 0, 3, 7, AdvancementPolicyFixed},
		{"2020", "NWSL Challenge Cup Group Stage", "challenge-cup-group-stage", "Challenge Cup Group Stage", StageKindGroup, true, 16, 0, 0, ""},
		{"2023", "NWSL Challenge Cup Group Stage", "challenge-cup-group-stage", "Challenge Cup Group Stage", StageKindGroup, false, 36, 0, 0, ""},
		{"2020", "NWSL Challenge Cup Knockout Round", "challenge-cup-knockout-round", "Challenge Cup Knockout", StageKindKnockout, false, 7, 3, 7, AdvancementPolicyFixed},
		{"2021", "NWSL Challenge Cup Knockout Round", "challenge-cup-knockout-round", "Challenge Cup Knockout", StageKindKnockout, false, 1, 1, 1, AdvancementPolicySingleFinal},
		{"2023", "NWSL Challenge Cup Knockout Round", "challenge-cup-knockout-round", "Challenge Cup Knockout", StageKindKnockout, false, 3, 2, 3, AdvancementPolicyFixed},
		{"2026", "NWSL Challenge Cup Final", "challenge-cup-final", "Challenge Cup Final", StageKindKnockout, false, 1, 1, 1, AdvancementPolicySingleFinal},
	}
	for _, tc := range tests {
		t.Run(tc.season+"/"+tc.stage, func(t *testing.T) {
			entry, ok := Lookup(tc.season, tc.stage)
			if !ok || entry.Slug != tc.slug || entry.ShortLabel != tc.short || entry.Kind != tc.kind || entry.Primary != tc.primary {
				t.Fatalf("entry=%+v, ok=%v", entry, ok)
			}
			if (tc.games > 0 && (entry.Inventory == nil || entry.Inventory.Games != tc.games)) || (tc.games == 0 && entry.Inventory != nil) {
				t.Fatalf("inventory=%+v, want games %d", entry.Inventory, tc.games)
			}
			if tc.rounds == 0 {
				if entry.BracketFormat != nil || entry.Supports(CapabilityBracket) || len(entry.Capabilities) != 2 {
					t.Fatalf("group metadata=%+v", entry)
				}
				return
			}
			if entry.BracketFormat == nil || entry.BracketFormat.Version != 1 || len(entry.BracketFormat.Rounds) != tc.rounds || len(entry.BracketFormat.Slots) != tc.slots || entry.BracketFormat.AdvancementPolicy != tc.policy {
				t.Fatalf("format=%+v", entry.BracketFormat)
			}
			if len(entry.Capabilities) != 3 || !entry.Supports(CapabilityFixtures) || !entry.Supports(CapabilityXG) || !entry.Supports(CapabilityBracket) {
				t.Fatalf("capabilities=%v", entry.Capabilities)
			}
		})
	}
}

func TestEveryPhaseEightCatalogEntry(t *testing.T) {
	playoffGames := map[string]int{
		"2016": 3, "2017": 3, "2018": 3, "2019": 3,
		"2021": 5, "2022": 5, "2023": 5,
		"2024": 7, "2025": 7, "2026": 0,
	}
	for season, games := range playoffGames {
		entry, ok := Lookup(season, "Playoffs")
		assertPhaseEightEntry(t, entry, ok, season, "playoffs", "Playoffs", StageKindKnockout, games, true, false)
	}
	for season, games := range map[string]int{"2020": 16, "2021": 20, "2022": 36, "2023": 36} {
		entry, ok := Lookup(season, "NWSL Challenge Cup Group Stage")
		assertPhaseEightEntry(t, entry, ok, season, "challenge-cup-group-stage", "Challenge Cup Group Stage", StageKindGroup, games, false, season == "2020")
		if entry.Primary != (season == "2020") {
			t.Fatalf("%s group primary = %t", season, entry.Primary)
		}
	}
	for season, games := range map[string]int{"2020": 7, "2021": 1, "2022": 3, "2023": 3} {
		entry, ok := Lookup(season, "NWSL Challenge Cup Knockout Round")
		assertPhaseEightEntry(t, entry, ok, season, "challenge-cup-knockout-round", "Challenge Cup Knockout", StageKindKnockout, games, true, false)
	}
	for _, season := range []string{"2024", "2025", "2026"} {
		entry, ok := Lookup(season, "NWSL Challenge Cup Final")
		assertPhaseEightEntry(t, entry, ok, season, "challenge-cup-final", "Challenge Cup Final", StageKindKnockout, 1, true, false)
	}
}

func assertPhaseEightEntry(t *testing.T, entry Entry, ok bool, season, slug, short string, kind StageKind, games int, bracket, primary bool) {
	t.Helper()
	if !ok || entry.Season != season || entry.Slug != slug || entry.ShortLabel != short || entry.Kind != kind || !entry.Public || !entry.SourceAvailable {
		t.Fatalf("entry = %+v, found=%t", entry, ok)
	}
	if entry.Primary != primary {
		t.Fatalf("%s/%s primary = %t, want %t", season, slug, entry.Primary, primary)
	}
	if games == 0 {
		if entry.Inventory != nil {
			t.Fatalf("%s/%s inventory = %+v, want nil", season, slug, entry.Inventory)
		}
	} else if entry.Inventory == nil || entry.Inventory.Games != games || entry.Inventory.Teams != 0 || entry.Inventory.GamesPerTeam != 0 {
		t.Fatalf("%s/%s inventory = %+v, want %d games", season, slug, entry.Inventory, games)
	}
	wantCapabilities := []Capability{CapabilityFixtures, CapabilityXG}
	if bracket {
		wantCapabilities = append(wantCapabilities, CapabilityBracket)
	}
	if !reflect.DeepEqual(entry.Capabilities, wantCapabilities) || (entry.BracketFormat != nil) != bracket {
		t.Fatalf("%s/%s capabilities/format = %v / %+v", season, slug, entry.Capabilities, entry.BracketFormat)
	}
}

func TestVerifiedBracketTopology(t *testing.T) {
	classicPairs := []*[2]int{seedPair(1, 4), seedPair(2, 3), nil}
	classicConnections := []BracketConnection{{SourceSlotIDs: []string{"semifinal-1", "semifinal-2"}, DestinationSlotID: "final"}}
	reseededPairs := []*[2]int{seedPair(3, 6), seedPair(4, 5), nil, nil, nil}
	fixedPairs := []*[2]int{seedPair(1, 8), seedPair(4, 5), seedPair(2, 7), seedPair(3, 6), nil, nil, nil}
	fixedConnections := []BracketConnection{
		{SourceSlotIDs: []string{"quarterfinal-1", "quarterfinal-2"}, DestinationSlotID: "semifinal-1"},
		{SourceSlotIDs: []string{"quarterfinal-3", "quarterfinal-4"}, DestinationSlotID: "semifinal-2"},
		{SourceSlotIDs: []string{"semifinal-1", "semifinal-2"}, DestinationSlotID: "final"},
	}
	semifinalPairs := []*[2]int{nil, nil, nil}
	singleFinalPairs := []*[2]int{nil}
	noConnections := []BracketConnection{}
	tests := []struct {
		season, stage string
		rounds        int
		policy        AdvancementPolicy
		pairs         []*[2]int
		connections   []BracketConnection
	}{
		{"2016", "Playoffs", 2, AdvancementPolicyFixed, classicPairs, classicConnections},
		{"2017", "Playoffs", 2, AdvancementPolicyFixed, classicPairs, classicConnections},
		{"2018", "Playoffs", 2, AdvancementPolicyFixed, classicPairs, classicConnections},
		{"2019", "Playoffs", 2, AdvancementPolicyFixed, classicPairs, classicConnections},
		{"2021", "Playoffs", 3, AdvancementPolicyHistoricallyObservedReseeded, reseededPairs, classicConnections},
		{"2022", "Playoffs", 3, AdvancementPolicyHistoricallyObservedReseeded, reseededPairs, classicConnections},
		{"2023", "Playoffs", 3, AdvancementPolicyHistoricallyObservedReseeded, reseededPairs, classicConnections},
		{"2024", "Playoffs", 3, AdvancementPolicyFixed, fixedPairs, fixedConnections},
		{"2025", "Playoffs", 3, AdvancementPolicyFixed, fixedPairs, fixedConnections},
		{"2026", "Playoffs", 3, AdvancementPolicyFixed, fixedPairs, fixedConnections},
		{"2020", "NWSL Challenge Cup Knockout Round", 3, AdvancementPolicyFixed, fixedPairs, fixedConnections},
		{"2021", "NWSL Challenge Cup Knockout Round", 1, AdvancementPolicySingleFinal, singleFinalPairs, noConnections},
		{"2022", "NWSL Challenge Cup Knockout Round", 2, AdvancementPolicyFixed, semifinalPairs, classicConnections},
		{"2023", "NWSL Challenge Cup Knockout Round", 2, AdvancementPolicyFixed, semifinalPairs, classicConnections},
		{"2024", "NWSL Challenge Cup Final", 1, AdvancementPolicySingleFinal, singleFinalPairs, noConnections},
		{"2025", "NWSL Challenge Cup Final", 1, AdvancementPolicySingleFinal, singleFinalPairs, noConnections},
		{"2026", "NWSL Challenge Cup Final", 1, AdvancementPolicySingleFinal, singleFinalPairs, noConnections},
	}
	for _, tc := range tests {
		t.Run(tc.season+"/"+tc.stage, func(t *testing.T) {
			entry, _ := Lookup(tc.season, tc.stage)
			format := entry.BracketFormat
			if format == nil || format.Version != 1 || len(format.Rounds) != tc.rounds || format.AdvancementPolicy != tc.policy || len(format.Slots) != len(tc.pairs) {
				t.Fatalf("format = %+v", format)
			}
			for i, pair := range tc.pairs {
				if !reflect.DeepEqual(format.Slots[i].SeedPair, pair) {
					t.Fatalf("slot %d pair = %v, want %v", i, format.Slots[i].SeedPair, pair)
				}
			}
			if !reflect.DeepEqual(format.Connections, tc.connections) {
				t.Fatalf("connections = %+v, want %+v", format.Connections, tc.connections)
			}
		})
	}
}

func TestChallengeCup2020IsPrimary(t *testing.T) {
	entry, ok := PrimaryEntry("2020")
	if !ok || entry.Stage != "NWSL Challenge Cup Group Stage" || entry.Slug != "challenge-cup-group-stage" {
		t.Fatalf("primary=%+v, ok=%v", entry, ok)
	}
}

func TestCatalogOrderingUsesCompetitionFamily(t *testing.T) {
	entries := PublicEntries()
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Season+":"+entry.Slug)
	}
	want := []string{
		"2026:regular-season", "2026:playoffs", "2026:challenge-cup-final",
		"2025:regular-season", "2025:playoffs", "2025:challenge-cup-final",
		"2024:regular-season", "2024:playoffs", "2024:challenge-cup-final",
		"2023:regular-season", "2023:playoffs", "2023:challenge-cup-group-stage", "2023:challenge-cup-knockout-round",
		"2022:regular-season", "2022:playoffs", "2022:challenge-cup-group-stage", "2022:challenge-cup-knockout-round",
		"2021:regular-season", "2021:playoffs", "2021:challenge-cup-group-stage", "2021:challenge-cup-knockout-round",
		"2020:challenge-cup-group-stage", "2020:challenge-cup-knockout-round",
		"2019:regular-season", "2019:playoffs", "2018:regular-season", "2018:playoffs",
		"2017:regular-season", "2017:playoffs", "2016:regular-season", "2016:playoffs",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ordering = %v, want %v", got, want)
	}
}

func TestBracketCopiesAreDeep(t *testing.T) {
	entry, _ := Lookup("2024", "Playoffs")
	entry.BracketFormat.Rounds[0].Label = "changed"
	entry.BracketFormat.Slots[0].SeedPair[0] = 99
	entry.BracketFormat.Connections[0].SourceSlotIDs[0] = "changed"
	again, _ := Lookup("2024", "Playoffs")
	if again.BracketFormat.Rounds[0].Label != "Quarterfinals" || again.BracketFormat.Slots[0].SeedPair[0] != 1 || again.BracketFormat.Connections[0].SourceSlotIDs[0] != "quarterfinal-1" {
		t.Fatalf("bracket copy leaked mutation: %+v", again.BracketFormat)
	}
}

func TestBracketValidationRejectsMalformedFormats(t *testing.T) {
	valid := singleFinalBracket()
	cases := []struct {
		name string
		edit func(*BracketFormat)
	}{
		{"unknown policy", func(f *BracketFormat) { f.AdvancementPolicy = "unknown" }},
		{"unsupported version", func(f *BracketFormat) { f.Version = 2 }},
		{"blank round id", func(f *BracketFormat) { f.Rounds[0].ID = " " }},
		{"blank round label", func(f *BracketFormat) { f.Rounds[0].Label = " " }},
		{"blank slot id", func(f *BracketFormat) { f.Slots[0].ID = " " }},
		{"duplicate round", func(f *BracketFormat) { f.Rounds = append(f.Rounds, f.Rounds[0]) }},
		{"duplicate slot", func(f *BracketFormat) { f.Slots = append(f.Slots, f.Slots[0]) }},
		{"missing slot round", func(f *BracketFormat) { f.Slots[0].RoundID = "missing" }},
		{"invalid seed pair", func(f *BracketFormat) { f.Slots[0].SeedPair = seedPair(0, 1) }},
		{"missing source", func(f *BracketFormat) {
			f.Connections = []BracketConnection{{SourceSlotIDs: []string{"missing"}, DestinationSlotID: "final"}}
		}},
		{"same round connection", func(f *BracketFormat) {
			f.Connections = []BracketConnection{{SourceSlotIDs: []string{"final"}, DestinationSlotID: "final"}}
		}},
		{"duplicate destination", func(f *BracketFormat) {
			f.Rounds = append([]BracketRound{{ID: "semifinals", Label: "Semifinals"}}, f.Rounds...)
			f.Slots = append([]BracketSlot{{ID: "semi-1", RoundID: "semifinals"}, {ID: "semi-2", RoundID: "semifinals"}}, f.Slots...)
			f.Connections = []BracketConnection{
				{SourceSlotIDs: []string{"semi-1"}, DestinationSlotID: "final"},
				{SourceSlotIDs: []string{"semi-2"}, DestinationSlotID: "final"},
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			format := valid.Copy()
			tc.edit(&format)
			if err := format.Validate(); err == nil {
				t.Fatal("Validate unexpectedly succeeded")
			}
		})
	}
	entry := validEntry()
	entry.Kind = StageKindKnockout
	entry.Capabilities = []Capability{CapabilityFixtures, CapabilityXG, CapabilityBracket}
	entry.BracketFormat = nil
	if err := entry.Validate(); err == nil {
		t.Fatal("bracket capability without format unexpectedly succeeded")
	}
	entry.BracketFormat = valid
	entry.Capabilities = []Capability{CapabilityFixtures, CapabilityXG}
	if err := entry.Validate(); err == nil {
		t.Fatal("format without bracket capability unexpectedly succeeded")
	}
}

func TestBracketValidationRestrictsSourceOrderToCompleteOpeningPermutation(t *testing.T) {
	entry, _ := Lookup("2024", "Playoffs")
	for index, edit := range []func(*BracketFormat){
		func(f *BracketFormat) { f.Slots[0].SourceOrder = 0 },
		func(f *BracketFormat) {
			f.Slots[0].SourceOrder, f.Slots[1].SourceOrder, f.Slots[2].SourceOrder, f.Slots[3].SourceOrder = 1, 2, 4, 5
		},
		func(f *BracketFormat) { f.Slots[4].SourceOrder = 1 },
	} {
		format := entry.BracketFormat.Copy()
		edit(&format)
		if err := format.Validate(); err == nil {
			t.Fatalf("invalid source order %d accepted", index)
		}
	}
}
