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

		// Aliases fire the same binding as Key, so they share Key's
		// namespace: an alias that collides with another binding's Key
		// (or another binding's alias) would shadow it in route()'s
		// first-match loop exactly as silently as a duplicate Key would.
		for _, a := range b.Aliases {
			if seen[a] {
				t.Errorf("%q's alias %q collides with another binding's key or alias", b.Key, a)
			}
			seen[a] = true
		}

		// BarGroup is optional; Long is not.
		//
		// It used to be required, on the reasoning that the status bar
		// should be able to name every binding. That stopped being
		// possible: the attached bar reached exactly 77 cells against
		// a 79-cell budget at 80 columns, so the next binding could
		// not fit however short its label, and shrinking existing
		// labels to make room only defers the problem one binding at
		// a time. TestControlHelpFitsEveryEntryAt80Columns already
		// recorded the resolution -- "the overlay is already there and
		// the bar should shed the entry rather than grow."
		//
		// So a binding may be overlay-only, but it may never be
		// undiscoverable: Long stays mandatory, and the overlay lists
		// every row in the table.
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
		case keys.ActionFocusColumn:
			if (b.Column < 1 || b.Column > 9) && b.Column != keys.LastColumn {
				t.Errorf("%q is ActionFocusColumn with Column %d; want 1-9 or LastColumn", b.Key, b.Column)
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
		case "tab":
			return []byte{0x09}, true
		case ".":
			return []byte{0x2e}, true
		case ",":
			return []byte{0x2c}, true
		default:
			// Single letter or digit
			if len(name) == 1 && (name[0] >= 'a' && name[0] <= 'z' || name[0] >= '0' && name[0] <= '9') {
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

// The card toggle is help-overlay-only and has no repeat form.
//
// No BarGroup: the attached bar is already at 77 cells against a
// 79-cell budget, and the rule recorded in
// TestControlHelpFitsEveryEntryAt80Columns is that the bar sheds
// rather than grows. NoRepeat: ctrl+c has to stay an unknown key that
// leaves control mode.
func TestToggleCardsIsOnC(t *testing.T) {
	for _, b := range keys.Bindings {
		if b.Action == keys.ActionVerb && b.Verb == protocol.VerbToggleCards {
			if b.Key != "c" {
				t.Errorf("card toggle is on %q, want %q", b.Key, "c")
			}
			if b.BarGroup != "" {
				t.Errorf("card toggle has BarGroup %q; it must stay out of the "+
					"status bar, which has no room for it", b.BarGroup)
			}
			if !b.NoRepeat {
				t.Error("card toggle allows a repeat form; ctrl+c must stay an " +
					"unknown key that leaves control mode")
			}
			if _, ok := b.CtrlForm(); ok {
				t.Error("card toggle still produces a ctrl form")
			}
			if b.Long == "" {
				t.Error("card toggle has no Long text, so the help overlay cannot name it")
			}
			return
		}
	}
	t.Error("no binding produces VerbToggleCards")
}

func TestBuildBindingsDefaults(t *testing.T) {
	b, err := keys.BuildBindings(nil)
	if err != nil {
		t.Fatalf("BuildBindings(nil) error: %v", err)
	}
	if len(b) != len(keys.Bindings) {
		t.Fatalf("got %d bindings, want %d", len(b), len(keys.Bindings))
	}
	for i := range b {
		if b[i].Key != keys.Bindings[i].Key {
			t.Errorf("binding[%d].Key = %q, want %q", i, b[i].Key, keys.Bindings[i].Key)
		}
		if b[i].BarGroup != keys.Bindings[i].BarGroup {
			t.Errorf("binding[%d].BarGroup = %q, want %q", i, b[i].BarGroup, keys.Bindings[i].BarGroup)
		}
	}
}

func TestBuildBindingsCustomValid(t *testing.T) {
	// Remap kill_pane to 'k' and scroll_up to 'e'
	custom := map[string]string{
		keys.ActionNameKillPane: "k",
		keys.ActionNameScrollUp: "e",
	}
	b, err := keys.BuildBindings(custom)
	if err != nil {
		t.Fatalf("BuildBindings failed: %v", err)
	}

	for _, item := range b {
		if item.ActionName == keys.ActionNameKillPane {
			if item.Key != "k" {
				t.Errorf("kill_pane key = %q, want %q", item.Key, "k")
			}
			if item.BarGroup != "k kill" {
				t.Errorf("kill_pane BarGroup = %q, want %q", item.BarGroup, "k kill")
			}
			ctrl, ok := item.CtrlForm()
			if !ok || ctrl != "ctrl+k" {
				t.Errorf("kill_pane CtrlForm() = (%q, %v), want (ctrl+k, true)", ctrl, ok)
			}
		}
		if item.ActionName == keys.ActionNameScrollUp {
			if item.Key != "e" {
				t.Errorf("scroll_up key = %q, want %q", item.Key, "e")
			}
			if item.BarGroup != "hjel move" {
				t.Errorf("movement BarGroup = %q, want %q", item.BarGroup, "hjel move")
			}
		}
	}
}

// The reorder verbs are remappable like any other.
func TestBuildBindingsRemapsMoveVerbs(t *testing.T) {
	b, err := keys.BuildBindings(map[string]string{keys.ActionNameMoveLeft: "e"})
	if err != nil {
		t.Fatalf("BuildBindings failed: %v", err)
	}
	for _, item := range b {
		if item.ActionName == keys.ActionNameMoveLeft && item.Key != "e" {
			t.Errorf("move_left key = %q, want %q", item.Key, "e")
		}
	}
}

func TestBuildBindingsReservedKeyRejected(t *testing.T) {
	for reserved := range keys.Reserved {
		_, err := keys.BuildBindings(map[string]string{
			keys.ActionNameKillPane: reserved,
		})
		if err == nil {
			t.Errorf("expected error for reserved key %q, got nil", reserved)
		} else if !strings.Contains(err.Error(), "reserved") {
			t.Errorf("error for reserved key %q should mention 'reserved', got %v", reserved, err)
		}
	}
}

func TestBuildBindingsDuplicateKeyRejected(t *testing.T) {
	// 'x' is default for kill_pane, assigning cycle_width to 'x' without moving kill_pane should fail
	_, err := keys.BuildBindings(map[string]string{
		keys.ActionNameCycleWidth: "x",
	})
	if err == nil {
		t.Error("expected error for duplicate key 'x', got nil")
	} else if !strings.Contains(err.Error(), "duplicate") && !strings.Contains(err.Error(), "collision") {
		t.Errorf("expected duplicate/collision error, got %v", err)
	}
}

// Digits are fixed, but they still hold their keys: remapping another
// action onto one is a collision, not a silent shadow.
func TestBuildBindingsRejectsRemapOntoADigit(t *testing.T) {
	_, err := keys.BuildBindings(map[string]string{keys.ActionNameKillPane: "1"})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("remapping kill_pane onto 1: err = %v, want a duplicate-key error", err)
	}
}

// Ten digit rows, one per key, 1-9 by position and 0 for the last.
func TestDigitsFocusColumnsByPosition(t *testing.T) {
	want := map[string]int{"0": keys.LastColumn}
	for n := 1; n <= 9; n++ {
		want[string(rune('0'+n))] = n
	}
	got := map[string]int{}
	for _, b := range keys.Bindings {
		if b.Action == keys.ActionFocusColumn {
			got[b.Key] = b.Column
		}
	}
	if len(got) != len(want) {
		t.Fatalf("digit bindings = %v, want %v", got, want)
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("key %q focuses column %d, want %d", k, got[k], n)
		}
	}
}

func TestBuildBindingsUnknownActionRejected(t *testing.T) {
	_, err := keys.BuildBindings(map[string]string{
		"nonexistent_action": "z",
	})
	if err == nil {
		t.Error("expected error for unknown action, got nil")
	} else if !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("expected error to mention 'unknown action', got %v", err)
	}
}

func TestBuildBindingsInvalidKeyNames(t *testing.T) {
	cases := []struct {
		key     string
		wantErr string
	}{
		{"pgdn", "did you mean \"pgdown\""},
		{"not-a-key", "not a recognized key name"},
		{"ctrl", "not a recognized key name"},
		{"", "cannot be empty"},
	}

	for _, tc := range cases {
		_, err := keys.BuildBindings(map[string]string{
			keys.ActionNameKillPane: tc.key,
		})
		if err == nil {
			t.Errorf("expected error for key %q, got nil", tc.key)
		} else if !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("key %q error = %v, want to contain %q", tc.key, err, tc.wantErr)
		}
	}
}

func TestBuildBindingsValidNamedKeysAccepted(t *testing.T) {
	b, err := keys.BuildBindings(map[string]string{
		keys.ActionNameScrollDown: "pgdown",
		keys.ActionNameScrollUp:   "pgup",
	})
	if err != nil {
		t.Fatalf("BuildBindings with valid named keys failed: %v", err)
	}
	for _, item := range b {
		if item.ActionName == keys.ActionNameScrollDown && item.Key != "pgdown" {
			t.Errorf("scroll_down key = %q, want pgdown", item.Key)
		}
	}
}

func TestBarItemsFor(t *testing.T) {
	b, err := keys.BuildBindings(map[string]string{
		keys.ActionNameKillPane: "k",
		keys.ActionNameScrollUp: "e",
	})
	if err != nil {
		t.Fatalf("BuildBindings failed: %v", err)
	}
	droppable, essential := keys.BarItemsFor(b, true)
	joined := strings.Join(droppable, " ") + " " + strings.Join(essential, " ")
	if !strings.Contains(joined, "k kill") {
		t.Errorf("bar items should contain 'k kill', got %q", joined)
	}
	if !strings.Contains(joined, "hjel move") {
		t.Errorf("bar items should contain 'hjel move', got %q", joined)
	}
}
