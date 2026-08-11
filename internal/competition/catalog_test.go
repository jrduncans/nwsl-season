package competition

import (
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
	for _, scope := range [][2]string{{"2027", "Regular Season"}, {"2026", "Playoffs"}} {
		if _, ok := Lookup(scope[0], scope[1]); ok {
			t.Fatalf("Lookup(%q, %q) unexpectedly succeeded", scope[0], scope[1])
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
	return Entry{Season: "2026", Stage: "Test", Label: "Test", Slug: "test", Kind: StageKindLeagueTable, SourceAvailable: true}
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
		{"invalid rules", func(e *Entry) { e.Rules = &Rules{} }, "rules"},
		{"rules mismatch", func(e *Entry) { r := regular2026.Copy(); r.Stage = "Other"; e.Rules = &r; e.Season = r.Season }, "match"},
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
		{Season: "2026", Stage: "Exhibition", Label: "Exhibition", Slug: "exhibition", Kind: StageKindLeagueTable, Capabilities: []Capability{CapabilityFixtures, CapabilityXG}},
		{Season: "2026", Stage: "Final", Label: "Final", Slug: "final", Kind: StageKindKnockout, Capabilities: []Capability{CapabilityBracket}},
	} {
		if err := entry.Validate(); err != nil {
			t.Fatalf("valid entry rejected: %v", err)
		}
	}
}
