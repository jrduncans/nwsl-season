package forecast

type CatalogEntry struct {
	Model   Model
	Default bool
}

func Catalog() []CatalogEntry {
	return []CatalogEntry{{Model: NewCurrentPaceV1()}, {Model: NewResultsPoissonV1()}, {Model: NewXGPoissonV1(), Default: true}}
}

// EvaluationCatalog adds a deliberately simple reference model to the models
// available in Forecast Lab. It is useful for interpreting evaluation results,
// but is intentionally not offered as a user-facing forecast choice.
func EvaluationCatalog() []CatalogEntry {
	return append([]CatalogEntry{{Model: NewStraightLinePaceV1()}}, Catalog()...)
}
func Lookup(id string) (CatalogEntry, bool) {
	for _, entry := range Catalog() {
		if entry.Model.Info().ID == id {
			return entry, true
		}
	}
	return CatalogEntry{}, false
}
func Default() CatalogEntry {
	for _, entry := range Catalog() {
		if entry.Default {
			return entry
		}
	}
	panic("forecast catalog has no default model")
}
