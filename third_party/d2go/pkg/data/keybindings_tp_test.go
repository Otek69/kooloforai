package data

import (
	"testing"

	"github.com/hectorgimenez/d2go/pkg/data/skill"
)

func TestTownPortalKB(t *testing.T) {
	var kb KeyBindings
	for i := range kb.Skills {
		kb.Skills[i].SkillID = -1
	}
	if _, ok := kb.TownPortalKB(); ok {
		t.Fatal("nothing bound")
	}
	kb.Skills[3] = SkillBinding{SkillID: skill.ScrollOfTownPortal, KeyBinding: KeyBinding{Key1: [2]byte{0x73}}}
	if k, ok := kb.TownPortalKB(); !ok || k.Key1[0] != 0x73 {
		t.Fatal("scroll should do")
	}
	kb.Skills[7] = SkillBinding{SkillID: skill.TomeOfTownPortal, KeyBinding: KeyBinding{Key1: [2]byte{0x77}}}
	if k, _ := kb.TownPortalKB(); k.Key1[0] != 0x77 {
		t.Fatal("tome should win")
	}
}
