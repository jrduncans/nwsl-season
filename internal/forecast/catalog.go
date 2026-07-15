package forecast

type CatalogEntry struct {
	Model       Model
	Recommended bool
	EvidenceID  string
}

func Catalog() []CatalogEntry {
	return []CatalogEntry{{Model: NewCurrentPaceV1(), EvidenceID: "model-evaluation-v1"}, {Model: NewResultsPoissonV1(), Recommended: true, EvidenceID: "model-evaluation-v1"}, {Model: NewXGPoissonV1(), EvidenceID: "model-evaluation-v1"}}
}
func Lookup(id string) (CatalogEntry, bool) {
	for _, entry := range Catalog() {
		if entry.Model.Info().ID == id {
			return entry, true
		}
	}
	return CatalogEntry{}, false
}
func Recommended() CatalogEntry {
	for _, entry := range Catalog() {
		if entry.Recommended {
			return entry
		}
	}
	panic("forecast catalog has no recommended model")
}
