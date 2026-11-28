package scan

import "testing"

func TestPageUnion(t *testing.T) {
	u := &PageUnion{PageSize: 4}
	// scan 1: page 0 readable, page 1 locked
	d1 := []byte{1, 1, 1, 1, 0, 0, 0, 0}
	if n := u.Merge(d1, []bool{true, false}); n != 1 {
		t.Fatalf("unknown %d", n)
	}
	// scan 2: page 0 locked now, page 1 readable: both known afterwards
	d2 := []byte{0, 0, 0, 0, 2, 2, 2, 2}
	if n := u.Merge(d2, []bool{false, true}); n != 0 || d2[0] != 1 || d2[4] != 2 {
		t.Fatalf("unknown %d data %v", n, d2)
	}
	// scan 3: page 0 readable again with new content: fresh bytes win
	d3 := []byte{9, 9, 9, 9, 0, 0, 0, 0}
	if n := u.Merge(d3, []bool{true, false}); n != 0 || d3[0] != 9 || d3[4] != 2 {
		t.Fatalf("unknown %d data %v", n, d3)
	}
}
