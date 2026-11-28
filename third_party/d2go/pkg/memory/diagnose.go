package memory

import (
	"unsafe"

	"github.com/hectorgimenez/d2go/pkg/memory/scan"

	"golang.org/x/sys/windows"
)

// kooloforai patch: lets Koolo Doctor check, pattern by pattern, whether this memory reader still
// understands the running D2R build. Patterns come from the same table calculateOffsets uses (patterns.yaml + overrides).

type PatternCheck struct {
	Name      string
	Found     bool    // the address is known (by a pattern or from the cache)
	Address   uintptr // the address's offset inside the D2R module
	Source    string  // "pattern 1", "pattern 2", "cache", "" = missing
	Essential bool    // Koolo cannot play without it
}

type patternDef struct {
	name, pattern, mask string
}

// offsetPatterns lists, per address, every pattern variant (overrides first), in name order.
func offsetPatterns() []patternDef {
	set, _ := PatternSet()
	var out []patternDef
	for _, n := range set.Names() {
		for _, v := range set[n].Variants {
			if b, mask, err := scan.ParseIDA(v.Pattern); err == nil {
				out = append(out, patternDef{n, string(b), mask})
			}
		}
	}
	return out
}

// DiagnosePatterns resolves every address the way Koolo does and reports how each was found.
func (p *Process) DiagnosePatterns() ([]PatternCheck, error) {
	memory, err := p.getProcessMemory()
	if err != nil {
		return nil, err
	}
	set, _ := PatternSet()
	patMu.Lock()
	cache := offsetCache
	patMu.Unlock()
	res := scan.ResolveAll(memory, set, cache, p.buildKey(memory))
	out := make([]PatternCheck, 0, len(res))
	for _, n := range set.Names() {
		r := res[n]
		out = append(out, PatternCheck{Name: n, Found: r.Offset != 0, Address: uintptr(r.Offset), Source: r.Source, Essential: set[n].Essential})
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
	found := map[string]bool{}
	defs := offsetPatterns()
	for _, d := range defs {
		if scan.Find(memory, []byte(d.pattern), d.mask) >= 0 {
			found[d.name] = true
		}
	}
	for _, d := range defs {
		if found[d.name] || out[d.name] != nil {
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
		if best == nil {
			best = []FuzzyMatch{}
		}
		out[d.name] = best
	}
	return out, memory, nil
}

// PatternBytes returns the pattern and mask for a named offset pattern (for reports).
func PatternBytes(name string) (pattern []byte, mask string) {
	for _, d := range offsetPatterns() {
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
