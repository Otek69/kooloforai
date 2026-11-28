package scan

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

// Variant is one way to find an address: a byte pattern and how to turn a match into an offset.
//
//	kind "rip":   offset = match + add + int32 at (match + operandAt)   (RIP-relative references)
//	kind "value": offset = uint32 at (match + operandAt) + add          (offsets stored as immediates)
//
// All offsets are relative to the start of the D2R module.
type Variant struct {
	Pattern   string `yaml:"pattern" json:"pattern"` // IDA style: "48 8B 05 ?? ?? ?? ??"
	Kind      string `yaml:"kind" json:"kind"`
	OperandAt int    `yaml:"operandAt" json:"operandAt"`
	Add       int64  `yaml:"add" json:"add"`
}

// ParseIDA turns "48 8B ?? 1C" into bytes and a mask ('x' = must match, '?' = any).
func ParseIDA(p string) ([]byte, string, error) {
	fields := strings.Fields(p)
	if len(fields) == 0 {
		return nil, "", fmt.Errorf("empty pattern")
	}
	b := make([]byte, len(fields))
	var mask strings.Builder
	for i, f := range fields {
		if f == "?" || f == "??" {
			mask.WriteByte('?')
			continue
		}
		v, err := strconv.ParseUint(f, 16, 8)
		if err != nil {
			return nil, "", fmt.Errorf("bad byte %q in pattern %q", f, p)
		}
		b[i] = byte(v)
		mask.WriteByte('x')
	}
	return b, mask.String(), nil
}

// Find returns the first index where pattern/mask match in mem, or -1.
func Find(mem, pattern []byte, mask string) int {
	n := len(pattern)
	if n == 0 {
		return -1
	}
	first := -1
	for j := 0; j < n; j++ {
		if mask[j] == 'x' {
			first = j
			break
		}
	}
	for i := 0; i+n <= len(mem); i++ {
		if first >= 0 && mem[i+first] != pattern[first] {
			continue
		}
		ok := true
		for j := 0; j < n; j++ {
			if mask[j] == 'x' && mem[i+j] != pattern[j] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// Resolve finds the variant in mem and computes the module offset. ok=false if not found or invalid.
func Resolve(mem []byte, v Variant) (offset int64, match int, ok bool) {
	pat, mask, err := ParseIDA(v.Pattern)
	if err != nil {
		return 0, -1, false
	}
	match = Find(mem, pat, mask)
	if match < 0 {
		return 0, -1, false
	}
	at := match + v.OperandAt
	if at < 0 || at+4 > len(mem) {
		return 0, match, false
	}
	op := binary.LittleEndian.Uint32(mem[at : at+4])
	switch v.Kind {
	case "", "rip":
		offset = int64(match) + v.Add + int64(int32(op))
	case "value":
		offset = int64(op) + v.Add
	default:
		return 0, match, false
	}
	if offset <= 0 {
		return 0, match, false
	}
	return offset, match, true
}
