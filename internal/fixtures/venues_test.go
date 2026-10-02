package fixtures

import "testing"

func TestVenueCatalogLocations(t *testing.T) {
	for stadiumID, zone := range venueTimeZones {
		location, err := VenueLocation(stadiumID, "")
		if err != nil || location.String() != zone {
			t.Fatalf("stadium %s: %v, %v", stadiumID, location, err)
		}
	}
	if _, err := VenueLocation("unknown", ""); err == nil {
		t.Fatal("unknown stadium must not default to UTC")
	}
}
