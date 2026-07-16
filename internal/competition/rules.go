// Package competition owns immutable season-format configuration.
package competition

import "fmt"

type AchievementID string

const (
	AchievementShield      AchievementID = "shield"
	AchievementHomePlayoff AchievementID = "home_playoff"
	AchievementPlayoffs    AchievementID = "playoffs"
)

type Achievement struct {
	ID    AchievementID
	Label string
	TopK  int
}
type Rules struct {
	Season        string
	Stage         string
	Version       string
	ExpectedTeams int
	GamesPerTeam  int
	Achievements  []Achievement
}

var regular2026 = Rules{Season: "2026", Stage: "Regular Season", Version: "2026-regular-v1", ExpectedTeams: 16, GamesPerTeam: 30,
	Achievements: []Achievement{{AchievementShield, "Shield", 1}, {AchievementHomePlayoff, "Home playoff", 4}, {AchievementPlayoffs, "Playoffs", 8}}}

func (r Rules) Validate() error {
	if r.Season == "" || r.Stage == "" || r.Version == "" || r.ExpectedTeams < 1 || r.GamesPerTeam < 1 {
		return fmt.Errorf("invalid competition rules")
	}
	seenID, seenK := map[AchievementID]bool{}, map[int]bool{}
	last := 0
	for _, a := range r.Achievements {
		if a.ID == "" || a.Label == "" || a.TopK < 1 || a.TopK > r.ExpectedTeams || seenID[a.ID] || seenK[a.TopK] || a.TopK <= last {
			return fmt.Errorf("invalid achievement %q", a.ID)
		}
		seenID[a.ID], seenK[a.TopK], last = true, true, a.TopK
	}
	if len(r.Achievements) == 0 {
		return fmt.Errorf("competition rules have no achievements")
	}
	return nil
}

func (r Rules) Copy() Rules { r.Achievements = append([]Achievement(nil), r.Achievements...); return r }
func ForSeason(season, stage string) (Rules, bool) {
	if season == regular2026.Season && stage == regular2026.Stage {
		return regular2026.Copy(), true
	}
	return Rules{}, false
}
