// Package scan holds the pure (OS-independent, testable) parts of the kooloforai self-healing memory
// discovery: finding candidate addresses by code shape and scoring candidate data.
package scan

import (
	"bytes"
	"encoding/binary"
	"sort"
)

const (
	SkillSlotStride = 0x1c
	SkillSlots      = 16
)

// SkillTableRefs finds every "imul r64,r64,0x1C" directly followed by a RIP-relative "lea r64,[rip+X]"
// in mem (the module image starting at base) and returns the absolute addresses the leas point to.
func SkillTableRefs(mem []byte, base uintptr) []uintptr {
	var out []uintptr
	seen := map[uintptr]bool{}
	for i := 0; i+11 <= len(mem); i++ {
		if mem[i]&0xF0 != 0x40 || mem[i+1] != 0x6B || mem[i+3] != SkillSlotStride {
			continue
		}
		if (mem[i+4] != 0x48 && mem[i+4] != 0x4C) || mem[i+5] != 0x8D || mem[i+6]&0xC7 != 0x05 {
			continue
		}
		rel := int32(binary.LittleEndian.Uint32(mem[i+7 : i+11]))
		target := uintptr(int64(base) + int64(i) + 11 + int64(rel))
		if !seen[target] {
			seen[target] = true
			out = append(out, target)
		}
	}
	return out
}

// ScoreSkillSlots rates a candidate skill-hotkey table: -1 = impossible, otherwise higher is more likely.
// known holds the skill ids the player actually has. Strict (kooloforai patch): D2R binds every skill
// to at most one hotkey, so a repeated id means it is not the table; and when the player's skills are
// known, every bound id must be one of them.
func ScoreSkillSlots(blob []byte, known map[uint32]bool) int {
	if len(blob) < SkillSlots*SkillSlotStride {
		return -1
	}
	score, unassigned := 0, 0
	seen := map[uint32]bool{}
	for i := 0; i < SkillSlots; i++ {
		v := binary.LittleEndian.Uint32(blob[i*SkillSlotStride:])
		switch {
		case v == 0xFFFF || v == 0xFFFFFFFF:
			unassigned++
			continue
		case v > 400:
			return -1 // not a skill id
		case v == 0:
			continue
		case seen[v]:
			return -1 // same skill on two hotkeys: not the table
		case known[v]:
			score += 10
		case len(known) > 0:
			return -1 // a skill the player doesn't have
		default:
			score++
		}
		seen[v] = true
	}
	if score == 0 && unassigned == SkillSlots {
		return 1 // valid but empty (no skills bound yet)
	}
	return score
}

// Best returns the index of the highest score > 0, or -1 if there is none or the best is only an
// empty table that appears more than once (can't tell which one is real).
func Best(scores []int) int {
	best, bestScore, ties := -1, 0, 0
	for i, s := range scores {
		switch {
		case s > bestScore:
			best, bestScore, ties = i, s, 0
		case s == bestScore && s > 0:
			ties++
		}
	}
	if best < 0 || (ties > 0 && bestScore <= 1) {
		return -1
	}
	return best
}

// StringHits returns offsets in mem where name occurs as a whole zero-terminated string,
// ordered by distance to near (the old known offset).
func StringHits(mem []byte, name string, near int) []int {
	if name == "" {
		return nil
	}
	needle := append([]byte(name), 0)
	var hits []int
	for i := 0; i < len(mem) && len(hits) <= 64; {
		j := bytes.Index(mem[i:], needle)
		if j < 0 {
			break
		}
		at := i + j
		if at == 0 || mem[at-1] == 0 {
			hits = append(hits, at)
		}
		i = at + 1
	}
	sort.Slice(hits, func(a, b int) bool { return abs(hits[a]-near) < abs(hits[b]-near) })
	return hits
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// SkillTablesByContent searches the module's data directly for the skill hotkey table (kooloforai patch),
// for when the code that references it is locked: every 4-byte aligned spot is scored like a table, and
// only spots with at least one of the player's skills bound (score >= 10) count. Returns the offsets with
// the highest score (several if the game keeps copies).
func SkillTablesByContent(mem []byte, known map[uint32]bool) (offsets []int, score int) {
	n := SkillSlots * SkillSlotStride
	for i := 0; i+n <= len(mem); i += 4 {
		v := binary.LittleEndian.Uint32(mem[i:])
		if v != 0xFFFF && v != 0xFFFFFFFF && !known[v] { // fast reject: first slot must be plausible
			continue
		}
		s := ScoreSkillSlots(mem[i:i+n], known)
		switch {
		case s < 10 || s < score:
		case s > score:
			offsets, score = []int{i}, s
		default:
			offsets = append(offsets, i)
		}
	}
	return offsets, score
}
