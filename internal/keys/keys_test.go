package keys_test

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/keys"
	"github.com/lmorchard/wideboi/internal/protocol"
)

// Every binding must be reachable, labelled, and internally consistent.
// The table is the single source of truth for the router, the status bar
// and the help overlay, so a malformed row is three bugs.
func TestTableIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, b := range keys.Bindings {
		if b.Key == "" {
			t.Fatalf("binding with empty Key: %+v", b)
		}
		if seen[b.Key] {
			t.Errorf("duplicate binding for %q", b.Key)
		}
		seen[b.Key] = true

		if b.BarGroup == "" {
			t.Errorf("%q has no BarGroup; the status bar cannot name it", b.Key)
		}
		if b.Long == "" {
			t.Errorf("%q has no Long description; the help overlay cannot explain it", b.Key)
		}
		switch b.Action {
		case keys.ActionVerb:
			if b.Verb == 0 {
				t.Errorf("%q is ActionVerb with no Verb set", b.Key)
			}
		case keys.ActionScroll:
			if b.Scroll == 0 {
				t.Errorf("%q is ActionScroll with a zero delta, which does nothing", b.Key)
			}
		case keys.ActionQuit, keys.ActionDetach, keys.ActionHelp, keys.ActionExit:
		default:
			t.Errorf("%q has unknown Action %v", b.Key, b.Action)
		}
	}
}

// i, m and [ send Tab, Enter and Escape as control bytes, so a verb on
// any of them could never have a working repeat form. Enforced rather
// than commented: the failure would be a binding that silently does
// nothing when Ctrl is held.
func TestNoBindingUsesAReservedLetter(t *testing.T) {
	for _, b := range keys.Bindings {
		if why, bad := keys.Reserved[b.Key]; bad {
			t.Errorf("binding %q is reserved: ctrl+%s is %s, so it has no repeat form",
				b.Key, b.Key, why)
		}
	}
}

// The load-bearing check. MatchString returns false indistinguishably
// for "key not pressed" and "name I can never produce" -- that is how
// the pgdn binding shipped dead through two plans. So decode the byte a
// terminal actually sends for ctrl+<letter> and assert the name the
// router will match against agrees with it.
func TestEveryCtrlFormMatchesItsRealByte(t *testing.T) {
	var d uv.EventDecoder
	for _, b := range keys.Bindings {
		name, ok := b.CtrlForm()
		if !ok {
			continue
		}
		raw := []byte{b.Key[0] - 0x60} // ctrl+<letter> is the letter minus 0x60
		n, ev := d.Decode(raw)
		if n == 0 {
			t.Errorf("%s: decoder consumed nothing from %#v", name, raw)
			continue
		}
		kp, isKey := ev.(uv.KeyPressEvent)
		if !isKey {
			t.Errorf("%s: %#v decoded to %T, not a key press", name, raw, ev)
			continue
		}
		if !kp.MatchString(name) {
			t.Errorf("%s: the byte %#v decodes as %q, which does not match %q -- "+
				"this binding would silently never fire", name, raw, kp.String(), name)
		}
	}
}

// Plain forms too: a binding nobody typed is a binding nobody verified.
func TestEveryPlainFormMatchesItself(t *testing.T) {
	// Helper to map a name to its raw byte sequence
	nameToBytes := func(name string) ([]byte, bool) {
		switch name {
		case "esc":
			return []byte{0x1b}, true
		case "left":
			return []byte("\x1b[D"), true
		case "right":
			return []byte("\x1b[C"), true
		case "?":
			return []byte{0x3f}, true
		default:
			// Single letter
			if len(name) == 1 && name[0] >= 'a' && name[0] <= 'z' {
				return []byte{name[0]}, true
			}
			return nil, false
		}
	}

	var d uv.EventDecoder
	for _, b := range keys.Bindings {
		names := b.MatchNames()
		if len(names) == 0 {
			t.Errorf("%q produces no match names", b.Key)
			continue
		}

		// For each match name, decode the real byte sequence and verify MatchString accepts it
		for _, name := range names {
			raw, ok := nameToBytes(name)
			if !ok {
				t.Errorf("%q: match name %q has no known byte sequence", b.Key, name)
				continue
			}

			n, ev := d.Decode(raw)
			if n == 0 {
				t.Errorf("%q: decoder consumed nothing from %q (%#v)", b.Key, name, raw)
				continue
			}

			kp, isKey := ev.(uv.KeyPressEvent)
			if !isKey {
				t.Errorf("%q: %q (%#v) decoded to %T, not a key press", b.Key, name, raw, ev)
				continue
			}

			if !kp.MatchString(name) {
				t.Errorf("%q: match name %q decoded as %q, which does not match -- "+
					"this binding would silently never fire", b.Key, name, kp.String())
			}
		}
	}
}

// The bar splits into what may be dropped when narrow and what never
// may. Quit and escape are the only way out of a sticky mode, so they
// are always present.
func TestBarItemsKeepQuitAndExitEssential(t *testing.T) {
	for _, detachable := range []bool{true, false} {
		droppable, essential := keys.BarItems(detachable)
		joined := strings.Join(essential, " ")
		for _, want := range []string{"q quit", "esc exit"} {
			if !strings.Contains(joined, want) {
				t.Errorf("detachable=%v: essential items %q omit %q", detachable, essential, want)
			}
		}
		hasDetach := strings.Contains(strings.Join(droppable, " ")+joined, "d detach")
		if hasDetach != detachable {
			t.Errorf("detachable=%v: bar shows detach = %v", detachable, hasDetach)
		}
	}
}

// Focus and scroll collapse to one bar entry, but remain four bindings.
func TestHjklShareOneBarGroup(t *testing.T) {
	groups := map[string]string{}
	for _, b := range keys.Bindings {
		groups[b.Key] = b.BarGroup
	}
	for _, k := range []string{"h", "j", "k", "l"} {
		if groups[k] != "hjkl move" {
			t.Errorf("%q has BarGroup %q, want %q", k, groups[k], "hjkl move")
		}
	}
	droppable, _ := keys.BarItems(true)
	n := 0
	for _, g := range droppable {
		if g == "hjkl move" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("hjkl appears %d times in the bar, want exactly 1: %q", n, droppable)
	}
}

// j and k must move history in opposite directions, and k must be the
// one that goes up -- SetScrollOffset treats a positive delta as "back
// into history".
func TestScrollDirections(t *testing.T) {
	deltas := map[string]int{}
	for _, b := range keys.Bindings {
		if b.Action == keys.ActionScroll {
			deltas[b.Key] = b.Scroll
		}
	}
	if deltas["k"] <= 0 {
		t.Errorf("k should scroll up (positive delta), got %d", deltas["k"])
	}
	if deltas["j"] >= 0 {
		t.Errorf("j should scroll down (negative delta), got %d", deltas["j"])
	}
	if deltas["k"] != -deltas["j"] {
		t.Errorf("j and k should be symmetric: j=%d k=%d", deltas["j"], deltas["k"])
	}
}

// Guard the remap itself, so a later edit cannot quietly put smart-jump
// back on j and take scroll-down away again.
func TestSmartJumpIsOnA(t *testing.T) {
	for _, b := range keys.Bindings {
		if b.Action == keys.ActionVerb && b.Verb == protocol.VerbSmartJump {
			if b.Key != "a" {
				t.Errorf("smart jump is on %q, want %q", b.Key, "a")
			}
			return
		}
	}
	t.Error("no binding produces VerbSmartJump")
}
