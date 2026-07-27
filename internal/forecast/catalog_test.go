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
