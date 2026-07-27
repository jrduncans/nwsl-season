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
	if got := Default().Model.Info().ID; got != "xg-poisson-v1" {
		t.Fatalf("default model = %q, want xg-poisson-v1", got)
	}
}

func TestEvaluationCatalogAddsOnlyTheStraightLineBaseline(t *testing.T) {
	for _, entry := range Catalog() {
		if entry.Model.Info().ID == straightLinePaceID {
			t.Fatal("Forecast Lab catalog includes the evaluation-only baseline")
		}
	}
	entries := EvaluationCatalog()
	if got := entries[0].Model.Info().ID; got != straightLinePaceID {
		t.Fatalf("first evaluation model = %q, want %q", got, straightLinePaceID)
	}
}
