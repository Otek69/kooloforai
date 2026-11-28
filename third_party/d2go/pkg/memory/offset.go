package memory

import (
	"github.com/hectorgimenez/d2go/pkg/memory/scan"
)

type Offset struct {
	GameData                    uintptr
	UnitTable                   uintptr
	UI                          uintptr
	Hover                       uintptr
	Expansion                   uintptr
	RosterOffset                uintptr
	PanelManagerContainerOffset uintptr
	WidgetStatesOffset          uintptr
	WaypointsOffset             uintptr
	FPS                         uintptr
	KeyBindingsOffset           uintptr
	KeyBindingsSkillsOffset     uintptr
	TZ                          uintptr
	Quests                      uintptr
	Ping                        uintptr
	LegacyGraphics              uintptr
}

// calculateOffsets resolves every address from the pattern table (patterns.yaml + overrides), falling
// back to the per-build cache (kooloforai patch: was a fixed list of hard-coded patterns).
func calculateOffsets(process *Process) Offset {
	memory, _ := process.getProcessMemory()
	patMu.Lock()
	set, cache := patSet, offsetCache
	patMu.Unlock()
	build := process.buildKey(memory)
	res := scan.ResolveAll(memory, set, cache, build)

	// Restore what self-healing found earlier for this build.
	if off := cache.Get(build, cacheSelectedName); off > 0 {
		selectedCharacterNameOffset.Store(uint64(off))
	}
	if res["KeyBindingsSkills"].Offset == 0 {
		if off := cache.Get(build, cacheSkillKeys); off > 0 {
			res["KeyBindingsSkills"] = scan.Result{Offset: off, Source: "cache (search)", Match: -1}
		}
	}
	patMu.Lock()
	lastResolved, lastBuild = res, build
	patMu.Unlock()

	off := func(name string) uintptr { return uintptr(res[name].Offset) }
	o := Offset{
		GameData:                    off("GameData"),
		UnitTable:                   off("UnitTable"),
		UI:                          off("UI"),
		Hover:                       off("Hover"),
		Expansion:                   off("Expansion"),
		RosterOffset:                off("Roster"),
		PanelManagerContainerOffset: off("PanelManager"),
		WidgetStatesOffset:          off("WidgetStates"),
		WaypointsOffset:             off("Waypoints"),
		FPS:                         off("FPS"),
		KeyBindingsOffset:           off("KeyBindings"),
		TZ:                          off("TerrorZones"),
		Quests:                      off("Quests"),
		Ping:                        off("Ping"),
		LegacyGraphics:              off("LegacyGraphics"),
	}
	// The skill hotkey table is used as an absolute address; 0 = unknown (self-healing searches for it).
	if k := off("KeyBindingsSkills"); k != 0 {
		o.KeyBindingsSkillsOffset = process.moduleBaseAddressPtr + k
	}
	return o
}
