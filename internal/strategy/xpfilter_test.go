package strategy

import (
	"testing"

	"github.com/hectorgimenez/d2go/pkg/data"
)

func mon(id int, x, y int, t data.MonsterType) data.Monster {
	return data.Monster{UnitID: data.UnitID(id), Position: data.Position{X: x, Y: y}, Type: t}
}

func TestXPFilter(t *testing.T) {
	player := data.Position{X: 0, Y: 0}
	ms := data.Monsters{
		mon(1, 100, 100, data.MonsterTypeUnique),   // boss: keep
		mon(2, 105, 104, data.MonsterTypeNone),     // guarding the boss: keep
		mon(3, 300, 300, data.MonsterTypeNone),     // lone trash far away: drop
		mon(4, 5, 3, data.MonsterTypeNone),         // next to us: keep (self defense)
		mon(5, 200, 0, data.MonsterTypeNone),       // pack of 4: keep all
		mon(6, 202, 1, data.MonsterTypeNone),
		mon(7, 201, 3, data.MonsterTypeNone),
		mon(8, 204, 2, data.MonsterTypeNone),
		mon(9, 400, 0, data.MonsterTypeNone),       // pair: drop
		mon(10, 402, 0, data.MonsterTypeNone),
		mon(11, 500, 500, data.MonsterTypeChampion), // champion: keep
	}
	got := map[data.UnitID]bool{}
	for _, m := range XPFilter(func() data.Position { return player }, DefaultXP)(ms) {
		got[m.UnitID] = true
	}
	want := map[data.UnitID]bool{1: true, 2: true, 4: true, 5: true, 6: true, 7: true, 8: true, 11: true}
	for id := data.UnitID(1); id <= 11; id++ {
		if got[id] != want[id] {
			t.Errorf("monster %d: kept=%v want %v", id, got[id], want[id])
		}
	}
}
