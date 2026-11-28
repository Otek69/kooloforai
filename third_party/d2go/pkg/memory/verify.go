package memory

import (
	"github.com/hectorgimenez/d2go/pkg/memory/scan"
)

// kooloforai patch: verified search for addresses whose pattern no longer matches. Near-matches of the old
// pattern give candidate addresses (see scan.FuzzyCandidates); each candidate is checked against the
// running game, and only a candidate that looks exactly like the expected structure is used and cached.

// DeepFound reports one address found by verified search.
type DeepFound struct {
	Name   string
	Offset uintptr
	Votes  int
	Note   string // why it was not found, if Offset == 0
}

func (gd *GameReader) isHeapPtr(p uintptr) bool {
	base, size := gd.Process.ModuleInfo()
	return p >= 0x10000 && p < 0x7FFFFFFF0000 && (p < base || p >= base+uintptr(size)) && p%8 == 0
}

func (gd *GameReader) u64(a uintptr) uintptr { return uintptr(gd.Process.ReadUInt(a, Uint64)) }

// verifiers check a candidate (module offset) against the live game.
func (gd *GameReader) verifier(name string) func(off int64) bool {
	base := gd.moduleBaseAddressPtr
	switch name {
	case "Roster":
		main := gd.GetRawPlayerUnits().GetMainPlayer()
		if main.Name == "" {
			return nil
		}
		return func(off int64) bool {
			p := gd.u64(base + uintptr(off))
			if !gd.isHeapPtr(p) || gd.Process.ReadStringFromMemory(p, 16) != main.Name {
				return false
			}
			next := gd.u64(p + 0x148)
			a := gd.Process.ReadUInt(p+0x5C, Uint32)
			return (next == 0 || gd.isHeapPtr(next)) && a >= 1 && a <= 136
		}
	case "Expansion":
		return func(off int64) bool {
			p := gd.u64(base + uintptr(off))
			if !gd.isHeapPtr(p) {
				return false
			}
			e := gd.Process.ReadUInt(p+0x5C, Uint16)
			return e == 0 || e == 1
		}
	case "WidgetStates":
		return func(off int64) bool {
			sf := gd.u64(base + uintptr(off))
			if !gd.isHeapPtr(sf) {
				return false
			}
			n := gd.u64(sf)
			return n >= 8 && n <= 1<<16 && n&(n-1) == 0 && gd.isHeapPtr(gd.u64(sf+8))
		}
	case "Ping":
		return func(off int64) bool {
			p := gd.u64(base + uintptr(off))
			return gd.isHeapPtr(p) && gd.Process.ReadUInt(p+36, Uint32) <= 5000
		}
	case "TerrorZones":
		// Only verifiable while terror zones are active (an all-zero struct proves nothing).
		return func(off int64) bool {
			s := base + uintptr(off)
			zones, n := gd.u64(s), int(gd.Process.ReadUInt(s+8, Uint8))
			if n < 1 || n > 16 || !gd.isHeapPtr(zones) {
				return false
			}
			for i := 0; i < n; i++ {
				if a := gd.Process.ReadUInt(zones+uintptr(i*4), Uint32); a < 1 || a > 136 {
					return false
				}
			}
			return true
		}
	}
	return nil
}

// minVotes: addresses with weak checks need several near-matches agreeing on them.
var minVotes = map[string]int{"Expansion": 2, "Ping": 2}

func offsetField(o *Offset, name string) *uintptr {
	switch name {
	case "Expansion":
		return &o.Expansion
	case "Roster":
		return &o.RosterOffset
	case "WidgetStates":
		return &o.WidgetStatesOffset
	case "TerrorZones":
		return &o.TZ
	case "Ping":
		return &o.Ping
	}
	return nil
}

// DeepFindOffsets runs the verified search for every supported address that is still 0, uses what it
// finds and remembers it for this D2R build. Needs a running game (it checks against live data).
func (gd *GameReader) DeepFindOffsets() []DeepFound {
	mem, err := gd.Process.getProcessMemory()
	if err != nil {
		return nil
	}
	set, _ := PatternSet()
	var out []DeepFound
	for _, name := range []string{"Expansion", "Roster", "WidgetStates", "TerrorZones", "Ping"} {
		field := offsetField(&gd.offset, name)
		if *field != 0 {
			continue
		}
		verify := gd.verifier(name)
		if verify == nil {
			out = append(out, DeepFound{Name: name, Note: "needs a running game"})
			continue
		}
		var cands []scan.Candidate
		for _, v := range set[name].Variants {
			cands = append(cands, scan.FuzzyCandidates(mem, v, 3)...)
		}
		if mv := minVotes[name]; mv > 1 {
			kept := cands[:0]
			for _, c := range cands {
				if c.Votes >= mv {
					kept = append(kept, c)
				}
			}
			cands = kept
		}
		if len(cands) == 0 {
			out = append(out, DeepFound{Name: name, Note: "no near-match in readable code"})
			continue
		}
		c, ok := scan.PickVerified(cands, verify)
		if !ok {
			note := "candidates found, but none matches the expected structure"
			if name == "TerrorZones" {
				note += " (only checkable while terror zones are active)"
			}
			out = append(out, DeepFound{Name: name, Note: note})
			continue
		}
		*field = uintptr(c.Offset)
		gd.Process.cachePut(mem, name, c.Offset)
		out = append(out, DeepFound{Name: name, Offset: uintptr(c.Offset), Votes: c.Votes})
	}
	return out
}
