package scan

import (
	"encoding/binary"
	"testing"
)

// Built from the doctor report: Expansion's pattern no longer matches, but two places with one changed
// register (48 8B CB instead of 48 8B D9) and a different displacement point to the same variable.
func TestFuzzyCandidatesVote(t *testing.T) {
	v := Variant{Pattern: "48 8B 05 ?? ?? ?? ?? 48 8B D9 F3 0F 10 50 ??", Kind: "rip", OperandAt: 3, Add: 7}
	mem := make([]byte, 0x3000)
	put := func(at int, target int, tail []byte) {
		copy(mem[at:], []byte{0x48, 0x8B, 0x05})
		binary.LittleEndian.PutUint32(mem[at+3:], uint32(int32(target-(at+7))))
		copy(mem[at+7:], tail)
	}
	put(0x100, 0x2800, []byte{0x48, 0x8B, 0xCB, 0xF3, 0x0F, 0x10, 0x48, 0x18}) // 2 bytes differ
	put(0x200, 0x2800, []byte{0x48, 0x8B, 0xCB, 0xF3, 0x0F, 0x10, 0x48, 0x18}) // same variable
	put(0x300, 0x2900, []byte{0x48, 0x8B, 0xD9, 0xF3, 0x0F, 0x10, 0x05, 0x78}) // 1 byte differs, once
	c := FuzzyCandidates(mem, v, 3)
	if len(c) < 2 || c[0].Offset != 0x2800 || c[0].Votes != 2 || c[1].Offset != 0x2900 {
		t.Fatalf("candidates %+v", c)
	}
	// Verification decides: if only 0x2900 holds a valid structure, it wins despite fewer votes.
	got, ok := PickVerified(c, func(off int64) bool { return off == 0x2900 })
	if !ok || got.Offset != 0x2900 {
		t.Fatalf("picked %+v %v", got, ok)
	}
	if _, ok := PickVerified(c, func(int64) bool { return false }); ok {
		t.Fatal("nothing verified, but picked")
	}
	tie := []Candidate{{Offset: 1, Votes: 1, Best: 1}, {Offset: 2, Votes: 1, Best: 1}}
	if _, ok := PickVerified(tie, func(int64) bool { return true }); ok {
		t.Fatal("guessed between equal candidates")
	}
}
