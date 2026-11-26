// Package strategy holds game knowledge distilled from speedrunners
// (MrLlamaSC's tutorial series, speedrun.com/d2lod) as pure, testable logic.
package strategy

import (
	"github.com/hectorgimenez/d2go/pkg/data"
)

// XP settings for experience-efficient clearing.
type XPSettings struct {
	// Normal monsters closer than this to the player are always fought (they would hit us anyway).
	SelfDefenseRadius int
	// Normal monsters near an elite are part of the boss fight (minions, guards).
	EliteRadius int
	// A normal monster with at least PackSize-1 other enemies within PackRadius is a pack worth killing.
	PackSize   int
	PackRadius int
}

// DefaultXP matches the speedrun rule of thumb: boss groups and big packs are worth stopping for,
// lone trash is not ("kill boss monsters and their minions: they give several times the experience").
var DefaultXP = XPSettings{SelfDefenseRadius: 8, EliteRadius: 12, PackSize: 4, PackRadius: 8}

func dist2(a, b data.Position) int {
	dx, dy := a.X-b.X, a.Y-b.Y
	return dx*dx + dy*dy
}

// XPFilter keeps elites (champions, uniques, super uniques, minions), monsters fighting alongside them,
// big packs, and anything close enough to be a threat. It drops lone weak monsters, which cost time
// for almost no experience. player is read at filter time, so the filter can be reused while moving.
func XPFilter(player func() data.Position, s XPSettings) data.MonsterFilter {
	return func(ms data.Monsters) []data.Monster {
		p := player()
		var elites []data.Monster
		for _, m := range ms {
			if m.IsElite() {
				elites = append(elites, m)
			}
		}
		self, elite, pack := s.SelfDefenseRadius*s.SelfDefenseRadius, s.EliteRadius*s.EliteRadius, s.PackRadius*s.PackRadius

		out := make([]data.Monster, 0, len(ms))
		for _, m := range ms {
			if keepForXP(m, ms, elites, p, self, elite, pack, s.PackSize) {
				out = append(out, m)
			}
		}
		return out
	}
}

func keepForXP(m data.Monster, all data.Monsters, elites []data.Monster, p data.Position, self, elite, pack, packSize int) bool {
	if m.IsElite() || m.IsMonsterRaiser() {
		return true // bosses give the experience; shamans/raisers must die or the fight never ends
	}
	if dist2(m.Position, p) <= self {
		return true
	}
	for _, e := range elites {
		if dist2(m.Position, e.Position) <= elite {
			return true
		}
	}
	near := 1
	for _, o := range all {
		if o.UnitID != m.UnitID && dist2(m.Position, o.Position) <= pack {
			near++
			if near >= packSize {
				return true
			}
		}
	}
	return false
}
