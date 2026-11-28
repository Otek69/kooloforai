package scan

import (
	"encoding/binary"
	"testing"
)

func TestParseIDA(t *testing.T) {
	b, m, err := ParseIDA("48 8b ?? 1C ?")
	if err != nil || len(b) != 5 || m != "xx?x?" || b[1] != 0x8B || b[3] != 0x1C {
		t.Fatalf("%x %q %v", b, m, err)
	}
	if _, _, err := ParseIDA("48 ZZ"); err == nil {
		t.Fatal("bad byte accepted")
	}
}

func TestResolveRipAndValue(t *testing.T) {
	mem := make([]byte, 0x100)
	// rip: at 0x10 "48 8B 05 <rel>" with rel=0x40 -> offset = 0x10 + 7 + 0x40
	copy(mem[0x10:], []byte{0x48, 0x8B, 0x05})
	binary.LittleEndian.PutUint32(mem[0x13:], 0x40)
	off, match, ok := Resolve(mem, Variant{Pattern: "48 8B 05 ?? ?? ?? ??", Kind: "rip", OperandAt: 3, Add: 7})
	if !ok || match != 0x10 || off != 0x10+7+0x40 {
		t.Fatalf("rip: off %#x match %#x ok %v", off, match, ok)
	}
	// value: "48 03 C7 49 8B 8C C6 <u32>" -> offset = u32
	copy(mem[0x30:], []byte{0x48, 0x03, 0xC7, 0x49, 0x8B, 0x8C, 0xC6})
	binary.LittleEndian.PutUint32(mem[0x37:], 0x2224BA0)
	off, _, ok = Resolve(mem, Variant{Pattern: "48 03 C7 49 8B 8C C6", Kind: "value", OperandAt: 7})
	if !ok || off != 0x2224BA0 {
		t.Fatalf("value: off %#x ok %v", off, ok)
	}
	// negative operandAt (Roster style): the operand's top byte IS the pattern's first byte (0x02),
	// exactly like the original d2go code reads it.
	binary.LittleEndian.PutUint32(mem[0x5D:], 0x02000100)
	copy(mem[0x61:], []byte{0x45, 0x33, 0xD2})
	off, _, ok = Resolve(mem, Variant{Pattern: "02 45 33 D2", Kind: "rip", OperandAt: -3, Add: 1})
	if !ok || off != 0x60+1+0x02000100 {
		t.Fatalf("roster style: off %#x ok %v", off, ok)
	}
	if _, _, ok := Resolve(mem, Variant{Pattern: "AA BB CC DD", Kind: "rip"}); ok {
		t.Fatal("missing pattern resolved")
	}
}
