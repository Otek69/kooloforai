package memory

import (
	"sync/atomic"
	"time"

	"github.com/hectorgimenez/d2go/pkg/data/skill"
	"github.com/hectorgimenez/d2go/pkg/memory/scan"
)

// kooloforai patch: self-healing for addresses that moved after a D2R patch and that have no (working)
// code pattern. Candidates are found by shape and then verified against what the game shows.
// The pure logic lives in pkg/memory/scan (tested on any OS).

const oldSelectedCharacterNameOffset = 0x21D31A8

var selectedCharacterNameOffset atomic.Uint64

func init() { selectedCharacterNameOffset.Store(oldSelectedCharacterNameOffset) }

// SelectedCharacterNameOffset returns the offset currently used for the selected character's name.
func SelectedCharacterNameOffset() uintptr { return uintptr(selectedCharacterNameOffset.Load()) }

// DiscoverSelectedCharacterName searches the D2R module for the given character name (as currently
// selected on the character screen) and switches GetSelectedCharacterName to the hit closest to the
// old address. Returns the new offset, or 0 if the name wasn't found.
func (gd *GameReader) DiscoverSelectedCharacterName(name string) uintptr {
	mem, err := gd.Process.getProcessMemory()
	if err != nil {
		return 0
	}
	for _, h := range scan.StringHits(mem, name, oldSelectedCharacterNameOffset) {
		if gd.Process.ReadStringFromMemory(gd.moduleBaseAddressPtr+uintptr(h), 0) == name {
			selectedCharacterNameOffset.Store(uint64(h))
			gd.Process.cachePut(mem, cacheSelectedName, int64(h))
			return uintptr(h)
		}
	}
	return 0
}

// DiscoverKeyBindingsSkills picks the most plausible skill-hotkey table for the player's skills and
// uses it from now on. Returns the address found, or 0.
func (gd *GameReader) DiscoverKeyBindingsSkills(playerSkills map[skill.ID]skill.Points) uintptr {
	mem, err := gd.Process.getProcessMemory()
	if err != nil {
		return 0
	}
	known := map[uint32]bool{}
	for id, pts := range playerSkills {
		if pts.Level > 0 {
			known[uint32(id)] = true
		}
	}
	cands := scan.SkillTableRefs(mem, gd.moduleBaseAddressPtr)
	scores := make([]int, len(cands))
	for i, c := range cands {
		scores[i] = scan.ScoreSkillSlots(gd.ReadBytesFromMemory(c, scan.SkillSlots*scan.SkillSlotStride), known)
	}
	best := scan.Best(scores)
	if best < 0 {
		return 0
	}
	gd.offset.KeyBindingsSkillsOffset = cands[best]
	if scores[best] >= 10 { // at least one of the player's own skills is bound there: sure enough to remember
		gd.Process.cachePut(mem, cacheSkillKeys, int64(cands[best]-gd.moduleBaseAddressPtr))
	}
	return cands[best]
}

// KeyBindingsSkillsOffset returns the absolute address used for skill hotkeys (0 = unknown).
func (gd *GameReader) KeyBindingsSkillsOffset() uintptr { return gd.offset.KeyBindingsSkillsOffset }

// ensureKeyBindingsSkills retries discovery at most every 30 s while the pattern is missing.
func (gd *GameReader) ensureKeyBindingsSkills(playerSkills map[skill.ID]skill.Points) {
	if gd.offset.KeyBindingsSkillsOffset != 0 || len(playerSkills) == 0 || time.Since(gd.skillsDiscoveryAt) < 30*time.Second {
		return
	}
	gd.skillsDiscoveryAt = time.Now()
	gd.DiscoverKeyBindingsSkills(playerSkills)
}
