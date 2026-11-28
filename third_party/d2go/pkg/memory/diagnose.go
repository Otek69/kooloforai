package memory

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// kooloforai patch: lets Koolo Doctor check, pattern by pattern, whether this memory reader still
// understands the running D2R build. Patterns are the same ones calculateOffsets uses.

type PatternCheck struct {
	Name    string
	Found   bool
	Address uintptr // offset inside the D2R module
}

type patternDef struct {
	name, pattern, mask string
	byOperand           bool
}

var offsetPatterns = []patternDef{
	{"GameData", "\x44\x88\x25\x00\x00\x00\x00\x66\x44\x89\x25\x00\x00\x00\x00", "xxx????xxxx????", false},
	{"UnitTable", "\x48\x03\xC7\x49\x8B\x8C\xC6", "xxxxxxx", false},
	{"UI", "\x40\x84\xed\x0f\x94\x05", "xxxxxx", false},
	{"Hover", "\xc6\x84\xc2\x00\x00\x00\x00\x00\x48\x8b\x74", "xxx?????xxx", false},
	{"Expansion", "\x48\x8B\x05\x00\x00\x00\x00\x48\x8B\xD9\xF3\x0F\x10\x50\x00", "xxx????xxxxxxx?", false},
	{"Roster", "\x02\x45\x33\xD2\x4D\x8B", "xxxxxx", false},
	{"PanelManager", "\x48\x89\x05\x00\x00\x00\x00\x48\x85\xDB\x74\x1E", "xxx????xxxxx", true},
	{"WidgetStates", "\x48\x8B\x0D\x00\x00\x00\x00\x4C\x8D\x44\x24\x00\x48\x03\xC2", "xxx????xxxx?xxx", false},
	{"Waypoints", "\x48\x89\x05\x00\x00\x00\x00\x0F\x11\x00", "xxx????xxx", false},
	{"FPS", "\x8B\x1D\x00\x00\x00\x00\x48\x8D\x05\x00\x00\x00\x00\x48\x8D\x4C\x24\x40", "xx????xxx????xxxxx", false},
	{"KeyBindings", "\x48\x8D\x05\xAF\xEE", "xxxxx", false},
	{"KeyBindingsSkills", "\x0F\x10\x04\x24\x48\x6B\xC8\x1C\x48\x8D\x05", "xxxxxxxxxxx", false},
	{"TerrorZones", "\x48\x89\x05\xCC\xCC\xCC\xCC\x48\x8D\x05\xCC\xCC\xCC\xCC\x48\x89\x05\xCC\xCC\xCC\xCC\x48\x8D\x05\xCC\xCC\xCC\xCC\x48\x89\x15\xCC\xCC\xCC\xCC\x48\x89\x15", "xxx????xxx????xxx????xxx????xxx????xxx", false},
	{"Quests", "\x42\xc6\x84\x28\x00\x00\x00\x00\x00\x49\xff\xc5\x49\x83\xfd\x29", "xxxx?????xxxxxxx", false},
	{"Ping", "\x48\x8B\x0D\xCC\xCC\xCC\xCC\x49\x2B\xC7", "xxx????xxx", false},
	{"LegacyGraphics", "\x80\x3D\x00\x00\x00\x00\x00\x48\x8D\x54\x24\x30", "xx?????xxxxx", false},
}

// DiagnosePatterns scans the game module and reports which offset patterns are found.
// The first 5 (GameData, UnitTable, UI, Hover, Expansion) are essential for Koolo.
func (p *Process) DiagnosePatterns() ([]PatternCheck, error) {
	memory, err := p.getProcessMemory()
	if err != nil {
		return nil, err
	}
	out := make([]PatternCheck, 0, len(offsetPatterns))
	for _, d := range offsetPatterns {
		off := p.findPattern(memory, d.pattern, d.mask)
		out = append(out, PatternCheck{Name: d.name, Found: off != 0, Address: uintptr(off)})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Deep scan (kooloforai patch): helps find updated patterns after a D2R patch.
// ---------------------------------------------------------------------------

// FuzzyMatch is a place where a pattern almost matches.
type FuzzyMatch struct {
	Offset     int    // offset inside the module
	Mismatches int    // fixed (non-?) bytes that differ
	Bytes      []byte // the actual bytes there
}

// FuzzyPatterns returns, for each pattern NOT found exactly, the best near-matches with at most
// maxMismatch differing fixed bytes (closest first, up to 3 per pattern).
func (p *Process) FuzzyPatterns(maxMismatch int) (map[string][]FuzzyMatch, []byte, error) {
	memory, err := p.getProcessMemory()
	if err != nil {
		return nil, nil, err
	}
	out := map[string][]FuzzyMatch{}
	for _, d := range offsetPatterns {
		if p.findPattern(memory, d.pattern, d.mask) != 0 {
			continue
		}
		var best []FuzzyMatch
		n := len(d.pattern)
		fixed := 0
		for j := 0; j < n; j++ {
			if d.mask[j] != '?' {
				fixed++
			}
		}
		limit := min(maxMismatch, fixed/3) // never accept matches that are mostly different
		for i := 0; i+n <= len(memory); i++ {
			if memory[i] != d.pattern[0] && d.mask[0] != '?' && limit == 0 {
				continue
			}
			mm := 0
			for j := 0; j < n && mm <= limit; j++ {
				if d.mask[j] != '?' && memory[i+j] != d.pattern[j] {
					mm++
				}
			}
			if mm > limit {
				continue
			}
			best = append(best, FuzzyMatch{Offset: i, Mismatches: mm, Bytes: append([]byte(nil), memory[i:i+n]...)})
			if len(best) > 200 { // pattern too generic at this distance
				break
			}
		}
		// keep the closest 3
		for a := 0; a < len(best); a++ {
			for b := a + 1; b < len(best); b++ {
				if best[b].Mismatches < best[a].Mismatches {
					best[a], best[b] = best[b], best[a]
				}
			}
		}
		if len(best) > 3 {
			best = best[:3]
		}
		out[d.name] = best
	}
	return out, memory, nil
}

// PatternBytes returns the pattern and mask for a named offset pattern (for reports).
func PatternBytes(name string) (pattern []byte, mask string) {
	for _, d := range offsetPatterns {
		if d.name == name {
			return []byte(d.pattern), d.mask
		}
	}
	return nil, ""
}

// Region describes a memory region of the D2R module.
type Region struct {
	Offset   int
	Size     int
	State    uint32
	Protect  uint32
	Readable bool
}

// ModuleRegions lists the module's memory regions with their protection (VirtualQueryEx).
func (p *Process) ModuleRegions() []Region {
	var out []Region
	addr := p.moduleBaseAddressPtr
	end := p.moduleBaseAddressPtr + uintptr(p.moduleBaseSize)
	for addr < end {
		var mbi windows.MemoryBasicInformation
		if err := windows.VirtualQueryEx(p.handler, addr, &mbi, unsafe.Sizeof(mbi)); err != nil || mbi.RegionSize == 0 {
			break
		}
		prot := mbi.Protect
		readable := mbi.State == windows.MEM_COMMIT && prot&(windows.PAGE_NOACCESS|windows.PAGE_GUARD) == 0 && prot != 0
		out = append(out, Region{Offset: int(addr - p.moduleBaseAddressPtr), Size: int(mbi.RegionSize), State: mbi.State, Protect: prot, Readable: readable})
		addr = mbi.BaseAddress + mbi.RegionSize
	}
	return out
}
