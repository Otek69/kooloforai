package scan

import "sort"

// Candidate is an address that near-matches of a pattern point to, with how many near-matches agree.
type Candidate struct {
	Offset int64
	Votes  int
	Best   int // fewest differing bytes among its near-matches
}

// FuzzyCandidates finds near-matches of a variant (at most maxMismatch fixed bytes differ, never more than
// a third of them, and the operand bytes are not compared), computes the address each one points to the
// same way an exact match would, and returns the addresses ordered by votes (then by closeness).
// After a D2R patch the compiler often changes a register or a neighbouring instruction, but the code
// still points to the same variable, usually from several places.
func FuzzyCandidates(mem []byte, v Variant, maxMismatch int) []Candidate {
	pat, mask, err := ParseIDA(v.Pattern)
	if err != nil {
		return nil
	}
	n := len(pat)
	fixed := 0
	for j := 0; j < n; j++ {
		if mask[j] == 'x' {
			fixed++
		}
	}
	limit := min(maxMismatch, fixed/3)
	byOff := map[int64]*Candidate{}
	hits := 0
	for i := 0; i+n <= len(mem) && hits < 5000; i++ {
		mm := 0
		for j := 0; j < n && mm <= limit; j++ {
			if mask[j] == 'x' && mem[i+j] != pat[j] {
				mm++
			}
		}
		if mm > limit {
			continue
		}
		off, ok := OffsetAt(mem, v, i)
		if !ok || (v.Kind != "value" && off >= int64(len(mem))) { // rip targets lie inside the module
			continue
		}
		hits++
		c := byOff[off]
		if c == nil {
			c = &Candidate{Offset: off, Best: mm}
			byOff[off] = c
		}
		c.Votes++
		c.Best = min(c.Best, mm)
	}
	out := make([]Candidate, 0, len(byOff))
	for _, c := range byOff {
		out = append(out, *c)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Votes != out[b].Votes {
			return out[a].Votes > out[b].Votes
		}
		if out[a].Best != out[b].Best {
			return out[a].Best < out[b].Best
		}
		return out[a].Offset < out[b].Offset
	})
	return out
}

// PickVerified returns the best candidate that passes verify. It refuses to guess when two different
// verified candidates are equally good.
func PickVerified(cands []Candidate, verify func(off int64) bool) (Candidate, bool) {
	var found []Candidate
	for _, c := range cands {
		if verify(c.Offset) {
			found = append(found, c)
			if len(found) == 2 {
				break
			}
		}
	}
	switch {
	case len(found) == 0:
		return Candidate{}, false
	case len(found) == 2 && found[0].Votes == found[1].Votes && found[0].Best == found[1].Best:
		return Candidate{}, false
	}
	return found[0], true
}
