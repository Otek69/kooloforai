package scan

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"
)

// Def is how to find one address: tried variant by variant, first hit wins.
type Def struct {
	Essential bool      `yaml:"essential" json:"essential"`
	Variants  []Variant `yaml:"variants" json:"variants"`
}

// Set maps address names (GameData, UnitTable, ...) to their definitions.
type Set map[string]Def

// LoadSet parses the built-in definitions and merges optional overrides: an override's variants are
// tried BEFORE the built-in ones, so a fix for a new D2R patch can be added without rebuilding.
func LoadSet(builtin, override []byte) (Set, error) {
	set := Set{}
	if err := yaml.Unmarshal(builtin, &set); err != nil {
		return nil, fmt.Errorf("built-in patterns: %w", err)
	}
	if len(override) == 0 {
		return set, nil
	}
	var ov Set
	if err := yaml.Unmarshal(override, &ov); err != nil {
		return set, fmt.Errorf("memory-patterns.yaml: %w", err)
	}
	for name, d := range ov {
		cur := set[name]
		cur.Variants = append(append([]Variant{}, d.Variants...), cur.Variants...)
		cur.Essential = cur.Essential || d.Essential
		set[name] = cur
	}
	return set, nil
}

// Names returns the address names, sorted.
func (s Set) Names() []string {
	out := make([]string, 0, len(s))
	for n := range s {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Result says where an address came from.
type Result struct {
	Offset int64  `json:"offset"` // module-relative, 0 = not found
	Source string `json:"source"` // "pattern 1", "pattern 2", "cache", "" (missing)
	Match  int    `json:"match"`  // where the pattern matched (-1 if not by pattern)
}

// ResolveAll resolves every address: by pattern variants first, then from the cache for this build.
// Everything found by pattern is written back to the cache.
func ResolveAll(mem []byte, set Set, cache *Cache, build string) map[string]Result {
	out := map[string]Result{}
	for _, name := range set.Names() {
		r := Result{Match: -1}
		for i, v := range set[name].Variants {
			if off, match, ok := Resolve(mem, v); ok {
				r = Result{Offset: off, Source: fmt.Sprintf("pattern %d", i+1), Match: match}
				break
			}
		}
		if r.Offset != 0 {
			cache.Put(build, name, r.Offset)
		} else if off := cache.Get(build, name); off != 0 {
			r = Result{Offset: off, Source: "cache", Match: -1}
		}
		out[name] = r
	}
	return out
}

// BuildKey identifies the exact D2R build: module size plus the PE header's link timestamp.
func BuildKey(mem []byte, moduleSize uint32) string {
	if len(mem) >= 0x40 {
		peOff := int(binary.LittleEndian.Uint32(mem[0x3C:0x40]))
		if peOff > 0 && peOff+12 <= len(mem) && string(mem[peOff:peOff+4]) == "PE\x00\x00" {
			return fmt.Sprintf("%d-%08x", moduleSize, binary.LittleEndian.Uint32(mem[peOff+8:peOff+12]))
		}
	}
	return fmt.Sprintf("%d", moduleSize)
}

// Cache remembers addresses per D2R build on disk, so addresses found once (even if the code holding
// their pattern is locked on later runs) keep working, and later starts are instant.
type Cache struct {
	Path string
	mu   sync.Mutex
	data map[string]map[string]int64
}

func (c *Cache) load() {
	if c.data != nil {
		return
	}
	c.data = map[string]map[string]int64{}
	if c.Path == "" {
		return
	}
	if raw, err := os.ReadFile(c.Path); err == nil {
		_ = json.Unmarshal(raw, &c.data)
	}
}

func (c *Cache) Get(build, name string) int64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	return c.data[build][name]
}

// Put stores an address and saves the file if it changed.
func (c *Cache) Put(build, name string, off int64) {
	if c == nil || off == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	if c.data[build] == nil {
		c.data[build] = map[string]int64{}
	}
	if c.data[build][name] == off {
		return
	}
	c.data[build][name] = off
	if c.Path == "" {
		return
	}
	if b, err := json.MarshalIndent(c.data, "", "  "); err == nil {
		_ = os.MkdirAll(filepath.Dir(c.Path), 0o755)
		tmp := c.Path + ".tmp"
		if os.WriteFile(tmp, b, 0o644) == nil {
			_ = os.Rename(tmp, c.Path)
		}
	}
}

// Delete forgets an address for a build (e.g. when it turned out to be wrong).
func (c *Cache) Delete(build, name string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.load()
	delete(c.data[build], name)
	c.mu.Unlock()
	c.Put(build, "_", 1) // forces a save; "_" is ignored
}
