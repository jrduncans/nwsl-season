package simulation

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"sort"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

// Seed returns a stable seed for the model, cached fixture snapshot, and fixed
// scenario. Display names and refresh timestamps are deliberately excluded.
func Seed(modelID string, teams []standings.Team, games []standings.Game, fixed map[string]Outcome) uint64 {
	h := sha256.New()
	writeText(h, modelID)

	orderedTeams := append([]standings.Team(nil), teams...)
	sort.Slice(orderedTeams, func(i, j int) bool { return orderedTeams[i].ID < orderedTeams[j].ID })
	writeUint(h, uint64(len(orderedTeams)))
	for _, team := range orderedTeams {
		writeText(h, team.ID)
	}

	orderedGames := append([]standings.Game(nil), games...)
	sort.Slice(orderedGames, func(i, j int) bool { return orderedGames[i].ID < orderedGames[j].ID })
	writeUint(h, uint64(len(orderedGames)))
	for _, game := range orderedGames {
		writeText(h, game.ID)
		writeText(h, game.Status)
		writeText(h, game.HomeTeamID)
		writeText(h, game.AwayTeamID)
		writeScore(h, game.HomeScore)
		writeScore(h, game.AwayScore)
	}

	ids := make([]string, 0, len(fixed))
	for id := range fixed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	writeUint(h, uint64(len(ids)))
	for _, id := range ids {
		writeText(h, id)
		writeText(h, string(fixed[id]))
	}

	return binary.BigEndian.Uint64(h.Sum(nil)[:8])
}

func writeText(h hash.Hash, value string) {
	writeUint(h, uint64(len(value)))
	_, _ = h.Write([]byte(value))
}

func writeUint(h hash.Hash, value uint64) {
	var buffer [8]byte
	binary.BigEndian.PutUint64(buffer[:], value)
	_, _ = h.Write(buffer[:])
}

func writeScore(h hash.Hash, value *int) {
	if value == nil {
		_, _ = h.Write([]byte{0})
		return
	}
	_, _ = h.Write([]byte{1})
	writeUint(h, uint64(*value))
}
