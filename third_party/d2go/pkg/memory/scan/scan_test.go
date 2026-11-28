package scan

import (
	"encoding/binary"
	"testing"
)

func table(ids ...uint32) []byte {
	b := make([]byte, SkillSlots*SkillSlotStride)
	for i := 0; i < SkillSlots; i++ {
		v := uint32(0xFFFF)
		if i < len(ids) {
			v = ids[i]
		}
		binary.LittleEndian.PutUint32(b[i*SkillSlotStride:], v)
	}
	return b
}

func TestScoreSkillSlots(t *testing.T) {
	known := map[uint32]bool{38: true, 40: true} // charged bolt, frozen armor
	if s := ScoreSkillSlots(table(38, 40), known); s != 20 {
		t.Fatalf("real table scored %d", s)
	}
	if s := ScoreSkillSlots(table(), known); s != 1 {
		t.Fatalf("empty table scored %d", s)
	}
	garbage := table(38)
	binary.LittleEndian.PutUint32(garbage[3*SkillSlotStride:], 0x41414141)
	if s := ScoreSkillSlots(garbage, known); s != -1 {
		t.Fatalf("garbage scored %d", s)
	}
	if s := ScoreSkillSlots(table(54), known); s != 1 { // a skill id we don't have: plausible but weak
		t.Fatalf("unknown skill scored %d", s)
	}
}

func TestBest(t *testing.T) {
	if Best([]int{-1, 3, 20, 1}) != 2 {
		t.Fatal("highest score should win")
	}
	if Best([]int{1, 1, -1}) != -1 {
		t.Fatal("two empty tables: can't tell")
	}
	if Best([]int{-1, 1}) != 1 {
		t.Fatal("a single empty table is fine")
	}
	if Best([]int{-1, -1}) != -1 {
		t.Fatal("nothing plausible")
	}
}

func TestSkillTableRefs(t *testing.T) {
	mem := make([]byte, 64)
	// at 10: imul rcx,rax,0x1C ; lea rax,[rip+0x20]
	copy(mem[10:], []byte{0x48, 0x6B, 0xC8, 0x1C, 0x48, 0x8D, 0x05, 0x20, 0, 0, 0})
	// at 30: imul rdx,r8,0x1C ; lea r9,[rip-0x10]  (other registers: still matches)
	copy(mem[30:], []byte{0x49, 0x6B, 0xD0, 0x1C, 0x4C, 0x8D, 0x0D, 0xF0, 0xFF, 0xFF, 0xFF})
	// at 45: imul with stride 0x20: not a skill table
	copy(mem[45:], []byte{0x48, 0x6B, 0xC8, 0x20, 0x48, 0x8D, 0x05, 0, 0, 0, 0})
	refs := SkillTableRefs(mem, 0x1000)
	if len(refs) != 2 || refs[0] != 0x1000+10+11+0x20 || refs[1] != 0x1000+30+11-0x10 {
		t.Fatalf("refs %#x", refs)
	}
}

func TestStringHits(t *testing.T) {
	mem := []byte("\x00xSorcaa\x00....\x00Sorcaa\x00..........\x00Sorcaa\x00")
	hits := StringHits(mem, "Sorcaa", 30)
	if len(hits) != 2 || hits[0] != 32 || hits[1] != 14 {
		t.Fatalf("hits %v (the tail of xSorcaa must not count)", hits)
	}
}
