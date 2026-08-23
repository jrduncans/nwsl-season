package forecast

import "testing"

func TestCatalogDefaultMatchesModelEvaluationV1(t *testing.T) {
	entries := Catalog()
	defaults := 0
	for _, entry := range entries {
		if entry.Default {
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("default models = %d, want 1", defaults)
	}
	if got := Default().Model.Info().ID; got != xgPoissonHomeTwoSeasonsID {
		t.Fatalf("default model = %q, want %s", got, xgPoissonHomeTwoSeasonsID)
	}
}

func TestEvaluationCatalogAddsEvaluationOnlyCandidates(t *testing.T) {
	evaluationOnly := map[string]bool{straightLinePaceID: true, xgPoissonScheduleLoadID: true}
	for _, entry := range Catalog() {
		if evaluationOnly[entry.Model.Info().ID] {
			t.Fatal("Forecast Lab catalog includes an evaluation-only candidate")
		}
	}
	entries := EvaluationCatalog()
	for index, want := range []string{
		straightLinePaceID,
		currentPaceID,
		resultsPoissonHomeTwoSeasonsID,
		xgPoissonHomeTwoSeasonsID,
		xgPoissonRecentFormID,
		xgPoissonScheduleLoadID,
	} {
		if got := entries[index].Model.Info().ID; got != want {
			t.Fatalf("evaluation model %d = %q, want %q", index, got, want)
		}
	}
}

func TestCanonicalIDUpgradesLegacyVenueModels(t *testing.T) {
	for legacy, want := range map[string]string{
		resultsPoissonID: resultsPoissonHomeTwoSeasonsID,
		xgPoissonID:      xgPoissonHomeTwoSeasonsID,
		currentPaceID:    currentPaceID,
	} {
		if got := CanonicalID(legacy); got != want {
			t.Errorf("CanonicalID(%q) = %q, want %q", legacy, got, want)
		}
	}
}
