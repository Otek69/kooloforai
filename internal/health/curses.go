package health

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hectorgimenez/d2go/pkg/data/state"
)

// ErrCurseChicken is returned when a configured dangerous curse or aura is on the character.
// It wraps ErrChicken so the rest of Koolo treats it like a normal chicken.
var ErrCurseChicken = fmt.Errorf("%w: dangerous curse or aura", ErrChicken)

// CurseStates maps the names used in config (chickenOnCurses) to game states.
var CurseStates = map[string]state.State{
	"amplify_damage": state.Amplifydamage,
	"decrepify":      state.Decrepify,
	"lower_resist":   state.Lowerresist,
	"iron_maiden":    state.Ironmaiden,
	"conviction":     state.Conviction,
	"fanaticism":     state.Fanaticism,
	"might":          state.Might,
	"weaken":         state.Weaken,
	"terror":         state.Terror,
	"attract":        state.Attract,
	"dim_vision":     state.Dimvision,
}

func dangerousCurse(states state.States, names []string) (string, bool) {
	for _, n := range names {
		st, ok := CurseStates[strings.ToLower(strings.TrimSpace(n))]
		if ok && states.HasState(st) {
			return n, true
		}
	}
	return "", false
}

// IsCurseChicken reports whether err was caused by a curse/aura chicken.
func IsCurseChicken(err error) bool { return errors.Is(err, ErrCurseChicken) }
