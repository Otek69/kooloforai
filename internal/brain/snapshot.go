package brain

import (
	"fmt"
	"strings"

	"github.com/hectorgimenez/d2go/pkg/data"
	"github.com/hectorgimenez/d2go/pkg/data/item"
	"github.com/hectorgimenez/d2go/pkg/data/stat"
)

// Snapshot is the exact game state (read from memory by Koolo) that the
// model gets as text. It is far cheaper and more accurate than a screenshot.
type Snapshot struct {
	Character  string
	Class      string
	Level      int
	Area       string
	LifePct    int
	ManaPct    int
	Gold       int
	StatPoints int
	SkillPoint int
	Resists    string
	Equipped   []string
	Inventory  int
	HasMerc    bool
}

func TakeSnapshot(d data.Data, characterName, class string) Snapshot {
	pu := d.PlayerUnit
	get := func(id stat.ID) int {
		s, _ := pu.FindStat(id, 0)
		return s.Value
	}

	s := Snapshot{
		Character:  characterName,
		Class:      class,
		Level:      get(stat.Level),
		Area:       pu.Area.Area().Name,
		Gold:       pu.TotalPlayerGold(),
		StatPoints: get(stat.StatPoints),
		SkillPoint: get(stat.SkillPoints),
		Resists: fmt.Sprintf("fire %d, cold %d, lightning %d, poison %d",
			get(stat.FireResist), get(stat.ColdResist), get(stat.LightningResist), get(stat.PoisonResist)),
		HasMerc: d.HasMerc,
	}
	if mx := get(stat.MaxLife); mx > 0 {
		s.LifePct = pu.HPPercent()
	}
	if mx := get(stat.MaxMana); mx > 0 {
		s.ManaPct = pu.MPPercent()
	}
	for _, it := range d.Inventory.ByLocation(item.LocationEquipped) {
		s.Equipped = append(s.Equipped, describeItem(it))
	}
	s.Inventory = len(d.Inventory.ByLocation(item.LocationInventory))
	return s
}

func describeItem(it data.Item) string {
	name := string(it.Name)
	if it.IsRuneword && it.RunewordName != "" {
		name = fmt.Sprintf("%s (runeword in %s)", it.RunewordName, it.Name)
	} else if it.Identified && it.IdentifiedName != "" && it.IdentifiedName != string(it.Name) {
		name = fmt.Sprintf("%s (%s)", it.IdentifiedName, it.Name)
	}
	q := it.Quality.ToString()
	if it.Ethereal {
		q += ", ethereal"
	}
	return fmt.Sprintf("%s [%s]", name, q)
}

func (s Snapshot) String() string {
	if s.Class == "" && s.Level == 0 {
		return "(no game state read yet)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Character: %s, %s, level %d\n", s.Character, s.Class, s.Level)
	fmt.Fprintf(&b, "Location: %s\n", s.Area)
	fmt.Fprintf(&b, "Life %d%%, mana %d%%, gold %d\n", s.LifePct, s.ManaPct, s.Gold)
	fmt.Fprintf(&b, "Unspent stat points %d, skill points %d\n", s.StatPoints, s.SkillPoint)
	fmt.Fprintf(&b, "Resistances from gear (before difficulty penalty): %s\n", s.Resists)
	fmt.Fprintf(&b, "Mercenary: %v\n", s.HasMerc)
	if len(s.Equipped) > 0 {
		fmt.Fprintf(&b, "Equipped: %s\n", strings.Join(s.Equipped, "; "))
	}
	fmt.Fprintf(&b, "Items in inventory: %d\n", s.Inventory)
	return b.String()
}
