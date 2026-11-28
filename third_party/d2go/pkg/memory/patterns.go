package memory

import (
	_ "embed"
	"os"
	"sync"

	"github.com/hectorgimenez/d2go/pkg/memory/scan"
)

// kooloforai patch: flexible memory reader. Address patterns live in patterns.yaml (built in) plus an
// optional override file; resolved addresses are cached per D2R build, so an address found once keeps
// working even when D2R locks the code holding its pattern on a later run.

//go:embed patterns.yaml
var builtinPatterns []byte

var (
	patMu        sync.Mutex
	patSet       scan.Set
	patLoadErr   error
	offsetCache  = &scan.Cache{}
	lastResolved map[string]scan.Result
	lastBuild    string
)

func init() { patSet, patLoadErr = scan.LoadSet(builtinPatterns, nil) }

// LoadPatternOverrides reads extra pattern variants (same format as patterns.yaml) that are tried
// before the built-in ones. A missing file is fine; a broken one is reported and ignored.
func LoadPatternOverrides(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	set, err := scan.LoadSet(builtinPatterns, raw)
	patMu.Lock()
	defer patMu.Unlock()
	if err != nil {
		patLoadErr = err
		return err
	}
	patSet, patLoadErr = set, nil
	return nil
}

// SetOffsetCachePath sets where resolved addresses are remembered (e.g. config/memory-cache.json).
func SetOffsetCachePath(path string) {
	patMu.Lock()
	offsetCache = &scan.Cache{Path: path}
	patMu.Unlock()
}

// PatternSet returns the address definitions in use (built-in + overrides).
func PatternSet() (scan.Set, error) {
	patMu.Lock()
	defer patMu.Unlock()
	return patSet, patLoadErr
}

// LastResolution reports, per address name, how it was found the last time offsets were calculated,
// and the D2R build key.
func LastResolution() (map[string]scan.Result, string) {
	patMu.Lock()
	defer patMu.Unlock()
	out := make(map[string]scan.Result, len(lastResolved))
	for k, v := range lastResolved {
		out[k] = v
	}
	return out, lastBuild
}

// ResetOffsetCache forgets everything cached for the running D2R build.
func (p *Process) ResetOffsetCache() {
	mem, err := p.getProcessMemory()
	if err != nil {
		return
	}
	build := scan.BuildKey(mem, p.moduleBaseSize)
	patMu.Lock()
	set, cache := patSet, offsetCache
	patMu.Unlock()
	for _, n := range set.Names() {
		cache.Delete(build, n)
	}
	cache.Delete(build, cacheSelectedName)
	cache.Delete(build, cacheSkillKeys)
}

const (
	cacheSelectedName = "_SelectedCharacterName" // found by DiscoverSelectedCharacterName
	cacheSkillKeys    = "_KeyBindingsSkills"     // found by DiscoverKeyBindingsSkills (module-relative)
)

func (p *Process) buildKey(mem []byte) string { return scan.BuildKey(mem, p.moduleBaseSize) }

func (p *Process) cacheGet(mem []byte, name string) int64 {
	patMu.Lock()
	c := offsetCache
	patMu.Unlock()
	return c.Get(p.buildKey(mem), name)
}

func (p *Process) cachePut(mem []byte, name string, off int64) {
	patMu.Lock()
	c := offsetCache
	patMu.Unlock()
	c.Put(p.buildKey(mem), name, off)
}
