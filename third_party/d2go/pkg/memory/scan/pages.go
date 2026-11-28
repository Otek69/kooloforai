package scan

// PageUnion keeps every page of the game module that was ever read. D2R's code doesn't change while
// it runs, but which pages are readable does, so over several scans the picture gets complete.
type PageUnion struct {
	PageSize int
	mem      []byte
	ok       []bool
}

// Merge updates the union with a fresh read (done[pg] = page pg was read now) and fills pages that
// are unreadable now but were read earlier into data. Returns how many pages are still unknown.
func (u *PageUnion) Merge(data []byte, done []bool) int {
	if u.PageSize == 0 {
		u.PageSize = 0x1000
	}
	if len(u.mem) != len(data) { // first call, or the module changed: start over
		u.mem = make([]byte, len(data))
		u.ok = make([]bool, len(done))
	}
	unknown := 0
	for pg, d := range done {
		a := pg * u.PageSize
		b := min(a+u.PageSize, len(data))
		switch {
		case d:
			copy(u.mem[a:b], data[a:b])
			u.ok[pg] = true
		case u.ok[pg]:
			copy(data[a:b], u.mem[a:b])
		default:
			unknown++
		}
	}
	return unknown
}
