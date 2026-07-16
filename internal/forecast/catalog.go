package forecast

type CatalogEntry struct {
	Model   Model
	Default bool
}

func Catalog() []CatalogEntry {
	return []CatalogEntry{{Model: NewCurrentPaceV1()}, {Model: NewResultsPoissonV1(), Default: true}, {Model: NewXGPoissonV1()}}
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
