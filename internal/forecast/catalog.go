package forecast

type CatalogEntry struct {
	Model   Model
	Default bool
}

func Catalog() []CatalogEntry {
	return []CatalogEntry{
		{Model: NewCurrentPaceV1()},
		{Model: NewResultsPoissonHomeTwoSeasonsV1()},
		{Model: NewXGPoissonHomeTwoSeasonsV1()},
		{Model: NewXGPoissonRecentFormV1()},
		{Model: NewXGPoissonScheduleLoadV1(), Default: true},
	}
}

// EvaluationCatalog prepends historical-evaluation-only reference models to
// the user-facing Forecast Lab catalog.
func EvaluationCatalog() []CatalogEntry {
	return append([]CatalogEntry{
		{Model: NewStraightLinePaceV1()},
	}, Catalog()...)
}
func Lookup(id string) (CatalogEntry, bool) {
	for _, entry := range Catalog() {
		if entry.Model.Info().ID == id {
			return entry, true
		}
	}
	// Accept old shared Forecast Lab URLs during parsing; CanonicalID upgrades
	// them before a model is fitted.
	switch id {
	case resultsPoissonID:
		return CatalogEntry{Model: NewResultsPoissonHomeTwoSeasonsV1()}, true
	case xgPoissonID:
		return CatalogEntry{Model: NewXGPoissonHomeTwoSeasonsV1()}, true
	}
	return CatalogEntry{}, false
}

// CanonicalID upgrades legacy season-only model IDs to the selected
// two-season venue implementations used by Forecast Lab.
func CanonicalID(id string) string {
	switch id {
	case resultsPoissonID:
		return resultsPoissonHomeTwoSeasonsID
	case xgPoissonID:
		return xgPoissonHomeTwoSeasonsID
	default:
		return id
	}
}
func Default() CatalogEntry {
	for _, entry := range Catalog() {
		if entry.Default {
			return entry
		}
	}
	panic("forecast catalog has no default model")
}
