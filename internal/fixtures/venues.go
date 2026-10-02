package fixtures

import (
	"fmt"
	"time"
	_ "time/tzdata" // Venue dates also work in deployments without system zoneinfo.
)

// VenueLocation resolves cached ASA stadium IDs without contacting the source.
// Unknown venues stay unknown; a home team's usual venue is not a safe fallback.
// Catalog provenance and the two fixture-specific corrections are documented in
// docs/history-logic-guide.md. IANA rules account for historical daylight saving.
func VenueLocation(stadiumID, gameID string) (*time.Location, error) {
	if stadiumID == "" {
		switch gameID {
		case "Xj5YPveRMb", "KXMeXv6vQ6":
			stadiumID = "vzqoJrj5ap" // Independently verified Inter&Co Stadium fixtures.
		}
	}
	zone, ok := venueTimeZones[stadiumID]
	if !ok {
		return nil, fmt.Errorf("unknown venue %q", stadiumID)
	}
	return time.LoadLocation(zone)
}

// ASA NWSL stadia catalog, verified 2026-10-01. IDs survive sponsor/name changes.
var venueTimeZones = map[string]string{
	"0Oq62v7q6D": "America/Los_Angeles",         // Torero Stadium
	"0x5g6ojM7O": "America/Chicago",             // Shell Energy Stadium
	"0x5gJ7mM7O": "America/Chicago",             // Field of Legends
	"2lqRXGLMr0": "America/New_York",            // Centreville Bank Stadium
	"2vQ1eawQrA": "America/New_York",            // Lower.com Field
	"4wM4ZmdqjB": "America/New_York",            // Rochester Rinos Stadium
	"7VqG1OWMvW": "America/Chicago",             // Wrigley Field
	"7vQ7xbOMD1": "America/Los_Angeles",         // BMO Stadium
	"9Yqda07QvJ": "America/Los_Angeles",         // Lumen Field
	"9YqdkYR5vJ": "America/Chicago",             // SeatGeek Stadium
	"9YqdwY75vJ": "America/New_York",            // Jordan Field
	"9z5kRwJMA3": "America/Los_Angeles",         // AT&T Park
	"9z5ka6gQA3": "America/Denver",              // Dick's Sporting Goods Park
	"9z5ka8PQA3": "America/Denver",              // Empower Field at Mile High
	"BLMvra8Mxe": "America/Kentucky/Louisville", // Lynn Family Stadium
	"KAqBVWBqbg": "America/New_York",            // Citi Field
	"KPqjw8PQ6v": "America/Denver",              // Zions Bank Stadium
	"KXMe8lXQ64": "America/Chicago",             // Northwestern Stadium
	"NPqxAnWQ9d": "America/New_York",            // Frontier Field
	"NPqxnokM9d": "America/Los_Angeles",         // Titan Stadium
	"NPqxy6XQ9d": "America/Chicago",             // Children's Mercy Park
	"NWMW84L5lz": "America/New_York",            // Red Bull Arena
	"NWMW8ZN5lz": "America/New_York",            // Subaru Park
	"NWMWoE7Mlz": "America/New_York",            // MSU Soccer Park
	"Oa5wKXY514": "America/Los_Angeles",         // Snapdragon Stadium
	"Oa5wdz9q14": "America/Los_Angeles",         // Cheney Stadium
	"Pk5L6mmQOW": "America/Chicago",             // Children's Mercy Victory Field
	"Vj58W84M8n": "America/Los_Angeles",         // PayPal Park
	"a35rBro5L6": "America/Los_Angeles",         // UW Medicine Pitch at Memorial Stadium
	"e7MzlRjqr0": "America/Denver",              // America First Field
	"eVq3ZzV5WO": "America/New_York",            // Icahn Stadium
	"eVq3alGMWO": "America/Chicago",             // Toyota Stadium
	"gOMna3xQwN": "America/New_York",            // Osceola Heritage Park
	"gOMnlReqwN": "America/Los_Angeles",         // Moda Field
	"gjMN7lK5Kp": "America/New_York",            // Segra Field
	"gpMOoayMzy": "America/New_York",            // Yurcak Field
	"gpMOrLOQzy": "America/New_York",            // WakeMed Soccer Park
	"gpMOzPrQzy": "America/New_York",            // Camping World Stadium
	"gpMOzdEQzy": "America/Chicago",             // Soldier Field
	"jYQJXZd5GR": "America/Los_Angeles",         // One Spokane Stadium
	"p6qb18650G": "America/Denver",              // Centennial Stadium
	"p6qbX06M0G": "America/Los_Angeles",         // Providence Park
	"raMyKkeqd2": "America/New_York",            // Daytona International Speedway Stadium
	"vzqoGO7qap": "America/New_York",            // Maryland SoccerPlex
	"vzqoJrj5ap": "America/New_York",            // Inter&Co Stadium
	"wvq9p1Y5Wn": "America/Los_Angeles",         // Dignity Health Sports Park
	"wvq9p775Wn": "America/New_York",            // Gillette Stadium
	"xW5p3L0Mg1": "America/Chicago",             // CPKC Stadium
	"xW5pwORMg1": "America/New_York",            // Audi Field
}
