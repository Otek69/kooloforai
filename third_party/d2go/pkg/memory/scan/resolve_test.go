package scan

import (
	"encoding/binary"
	"path/filepath"
	"testing"
)

const builtin = `
GameData:
  essential: true
  variants:
    - {pattern: "44 88 25 ?? ?? ?? ??", kind: rip, operandAt: 3, add: -289}
Ping:
  variants:
    - {pattern: "48 8B 0D ?? ?? ?? ?? 49 2B C7", kind: rip, operandAt: 3, add: 7}
`

func TestLoadSetOverrideFirst(t *testing.T) {
	set, err := LoadSet([]byte(builtin), []byte(`
Ping:
  variants:
    - {pattern: "48 8B 0D ?? ?? ?? ?? 49 23 C7", kind: rip, operandAt: 3, add: 7}
`))
	if err != nil {
		t.Fatal(err)
	}
	if v := set["Ping"].Variants; len(v) != 2 || v[0].Pattern != "48 8B 0D ?? ?? ?? ?? 49 23 C7" {
		t.Fatalf("override not first: %+v", v)
	}
	if !set["GameData"].Essential {
		t.Fatal("essential flag lost")
	}
	if _, err := LoadSet([]byte(builtin), []byte("Ping: [broken")); err == nil {
		t.Fatal("broken override accepted")
	}
}

func TestResolveAllWithCache(t *testing.T) {
	set, _ := LoadSet([]byte(builtin), nil)
	cache := &Cache{Path: filepath.Join(t.TempDir(), "memory-cache.json")}
	mem := make([]byte, 0x200)
	copy(mem[0x100:], []byte{0x44, 0x88, 0x25})
	binary.LittleEndian.PutUint32(mem[0x103:], 0x5000)
	copy(mem[0x150:], []byte{0x48, 0x8B, 0x0D, 0x10, 0, 0, 0, 0x49, 0x2B, 0xC7})

	r := ResolveAll(mem, set, cache, "build1")
	if r["GameData"].Offset != 0x100-289+0x5000 || r["GameData"].Source != "pattern 1" {
		t.Fatalf("GameData %+v", r["GameData"])
	}
	if r["Ping"].Offset != 0x150+7+0x10 {
		t.Fatalf("Ping %+v", r["Ping"])
	}

	// Next run: the code holding Ping's pattern is locked (zeroed). The cache still knows it,
	// also after reloading from disk.
	for i := 0x150; i < 0x15A; i++ {
		mem[i] = 0
	}
	cache2 := &Cache{Path: cache.Path}
	r = ResolveAll(mem, set, cache2, "build1")
	if r["Ping"].Offset != 0x150+7+0x10 || r["Ping"].Source != "cache" {
		t.Fatalf("Ping from cache %+v", r["Ping"])
	}
	// A different D2R build must not use that cache entry.
	r = ResolveAll(mem, set, cache2, "build2")
	if r["Ping"].Offset != 0 {
		t.Fatalf("cache leaked across builds: %+v", r["Ping"])
	}
	cache2.Delete("build1", "Ping")
	if (&Cache{Path: cache.Path}).Get("build1", "Ping") != 0 {
		t.Fatal("delete not saved")
	}
}

func TestBuildKey(t *testing.T) {
	mem := make([]byte, 0x200)
	binary.LittleEndian.PutUint32(mem[0x3C:], 0x80)
	copy(mem[0x80:], "PE\x00\x00")
	binary.LittleEndian.PutUint32(mem[0x88:], 0x65A1B2C3)
	if k := BuildKey(mem, 1000); k != "1000-65a1b2c3" {
		t.Fatalf("key %s", k)
	}
	if k := BuildKey(make([]byte, 0x200), 1000); k != "1000" {
		t.Fatalf("fallback key %s", k)
	}
}
