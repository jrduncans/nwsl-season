package simulation

import "encoding/json"

// Postseason is certified final seeding plus source-backed bracket progress.
// A nonempty Unavailable withholds all championship odds, preserving the table.
// Missing slots are unpublished matches, never completed results.
type Postseason struct {
	Seeds          map[int]string
	Slots          map[string]PostseasonMatch
	Unavailable    string
	SourceIdentity string
}

// PostseasonMatch records an identified source match. Winner is empty only
// for a validated, unplayed fixture. Kickoffs do not adjust playoff strengths.
type PostseasonMatch struct {
	Home, Away, Winner string
}

// Material is a canonical representation for cache identity and random seeds.
func (p *Postseason) Material() []byte {
	if p == nil {
		return nil
	}
	value, _ := json.Marshal(p) // String-keyed maps and strings cannot fail.
	return value
}
