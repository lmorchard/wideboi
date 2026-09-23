package main

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/keys"
	"github.com/lmorchard/wideboi/internal/protocol"
)

// Special keys are ordinary Code values in ultraviolet -- uv.KeyPgUp and
// friends are runes -- so one constructor covers both.
func key(code rune) uv.KeyPressEvent  { return uv.KeyPressEvent{Code: code} }
func ctrl(code rune) uv.KeyPressEvent { return uv.KeyPressEvent{Code: code, Mod: uv.ModCtrl} }

// Normal mode forwards everything. This is the point of the plan: v1
// claimed alt+*, ctrl+q, ctrl+o, pgup and pgdn, and every one of those
// made some pane worse.
func TestNormalModeForwardsEveryKeyIncludingTheOnesV1Stole(t *testing.T) {
	r := &router{prefix: "ctrl+b"}
	for _, ev := range []uv.KeyPressEvent{
		key('a'), key('q'), key('h'),
		ctrl('q'), ctrl('o'), ctrl('c'), ctrl('w'), ctrl('l'), ctrl('n'),
		key(uv.KeyPgUp), key(uv.KeyPgDown),
		{Code: 'n', Mod: uv.ModAlt}, {Code: 'q', Mod: uv.ModAlt},
	} {
		got := r.route(ev)
		if got.Kind != routeForward {
			t.Errorf("%s: kind %v, want routeForward -- the pane needs this key", ev.String(), got.Kind)
		}
		if r.control {
			t.Fatalf("%s: entered control mode", ev.String())
		}
	}
}

func TestPrefixEntersControlModeAndIsSwallowed(t *testing.T) {
	r := &router{prefix: "ctrl+b"}
	got := r.route(ctrl('b'))
	if got.Kind != routeIgnore {
		t.Errorf("kind %v, want routeIgnore -- the prefix must not reach the pane", got.Kind)
	}
	if !r.control {
		t.Error("prefix did not enter control mode")
	}
}

func TestDoubledPrefixSendsTheLiteralKeyAndLeavesControlMode(t *testing.T) {
	// Without this there is no way to type the prefix byte into a pane,
	// and any program that needs it -- readline's backward-char, for one
	// -- becomes unreachable.
	r := &router{prefix: "ctrl+b", control: true}
	got := r.route(ctrl('b'))
	if got.Kind != routeForward {
		t.Errorf("kind %v, want routeForward", got.Kind)
	}
	if r.control {
		t.Error("still in control mode after a doubled prefix")
	}
}

func TestEscapeLeavesControlModeWithoutReachingThePane(t *testing.T) {
	r := &router{prefix: "ctrl+b", control: true}
	got := r.route(uv.KeyPressEvent{Code: uv.KeyEscape})
	if got.Kind != routeIgnore {
		t.Errorf("kind %v, want routeIgnore", got.Kind)
	}
	if r.control {
		t.Error("escape did not leave control mode")
	}
}

// Enumerate the table rather than sampling it, in every combination that
// changes the answer: the input space is finite and small, and
// MatchString cannot tell a key nobody pressed from a name it can never
// produce.
func TestControlModeTable(t *testing.T) {
	for _, b := range keys.Bindings {
		for _, detachable := range []bool{true, false} {
			hidden := b.NeedsDetach && !detachable

			// Unmodified: act, then leave the mode.
			t.Run(b.Key+"/plain", func(t *testing.T) {
				r := &router{prefix: "ctrl+b", control: true, detachable: detachable}
				got := r.route(keyNamed(t, b.Key))
				if hidden {
					if got.Kind != routeIgnore || r.control {
						t.Errorf("hidden binding %q: got %+v control=%v, want ignore and exit",
							b.Key, got, r.control)
					}
					return
				}
				assertAction(t, b, got)
				if b.Action == keys.ActionHelp {
					if !r.control || !r.help {
						t.Errorf("? should raise help and stay in control mode: control=%v help=%v",
							r.control, r.help)
					}
					return
				}
				if r.control {
					t.Errorf("%q unmodified did not leave control mode", b.Key)
				}
			})

			// Ctrl-modified: act, and stay -- except the terminal verbs,
			// where staying is meaningless because the client is leaving.
			for _, name := range b.CtrlForms() {
				t.Run(name, func(t *testing.T) {
					r := &router{prefix: "ctrl+b", control: true, detachable: detachable}
					got := r.route(keyNamed(t, name))
					if hidden {
						if got.Kind != routeIgnore || r.control {
							t.Errorf("hidden binding %s: got %+v control=%v", name, got, r.control)
						}
						return
					}
					assertAction(t, b, got)
					switch b.Action {
					case keys.ActionQuit, keys.ActionDetach:
						// ctrl+q and ctrl+d are exactly q and d: staying is
						// meaningless when the client is leaving, so the
						// modifier must not make the mode sticky here the
						// way it does for every other verb.
						if r.control {
							t.Errorf("%s stayed in control mode; ctrl+%s must behave exactly as %s",
								name, b.Key, b.Key)
						}
					default:
						if !r.control {
							t.Errorf("%s did not stay in control mode", name)
						}
					}
				})
			}
		}
	}
}

func assertAction(t *testing.T, b keys.Binding, got route) {
	t.Helper()
	switch b.Action {
	case keys.ActionVerb:
		if got.Kind != routeVerb || got.Verb != b.Verb {
			t.Errorf("%q: got %+v, want verb %v", b.Key, got, b.Verb)
		}
	case keys.ActionScroll:
		if got.Kind != routeScroll || got.Scroll != b.Scroll {
			t.Errorf("%q: got %+v, want scroll %d", b.Key, got, b.Scroll)
		}
	case keys.ActionQuit:
		if got.Kind != routeQuit {
			t.Errorf("%q: got %+v, want routeQuit", b.Key, got)
		}
	case keys.ActionDetach:
		if got.Kind != routeDetach {
			t.Errorf("%q: got %+v, want routeDetach", b.Key, got)
		}
	case keys.ActionFocusColumn:
		if got.Kind != routeFocusColumn || got.Column != b.Column {
			t.Errorf("%q: got %+v, want focus column %d", b.Key, got, b.Column)
		}
	case keys.ActionToggleLayout:
		if got.Kind != routeToggleLayout {
			t.Errorf("%q: got %+v, want routeToggleLayout", b.Key, got)
		}
	case keys.ActionHelp, keys.ActionExit:
		if got.Kind != routeIgnore {
			t.Errorf("%q: got %+v, want routeIgnore", b.Key, got)
		}
	}
}

// A keystroke that did nothing must never leave you in a mode that eats
// the next one -- regardless of whether Ctrl was held. Holding Ctrl is
// not evidence you meant a key that does not exist.
func TestUnknownKeysExitControlModeWithAnyModifier(t *testing.T) {
	cases := []uv.KeyPressEvent{
		key('z'), key('g'), // 5 was here until the digits were bound
		{Code: 'g', Mod: uv.ModCtrl},
		{Code: 'z', Mod: uv.ModCtrl},
		ctrl('c'),
		{Code: 'q', Mod: uv.ModShift, Text: "Q"},
	}
	for _, ev := range cases {
		r := &router{prefix: "ctrl+b", control: true, detachable: true}
		got := r.route(ev)
		if got.Kind != routeIgnore {
			t.Errorf("%s: kind %v, want routeIgnore", ev.String(), got.Kind)
		}
		if r.control {
			t.Errorf("%s: stayed in control mode", ev.String())
		}
	}
}

// Three lefts and out, which is the whole point of the feature.
func TestCtrlRepeatThenPlainExits(t *testing.T) {
	r := &router{prefix: "ctrl+b", detachable: true}
	if got := r.route(uv.KeyPressEvent{Code: 'b', Mod: uv.ModCtrl}); got.Kind != routeIgnore {
		t.Fatalf("prefix: %+v", got)
	}
	for i := 0; i < 2; i++ {
		got := r.route(uv.KeyPressEvent{Code: 'h', Mod: uv.ModCtrl})
		if got.Kind != routeVerb || got.Verb != protocol.VerbFocusLeft {
			t.Fatalf("repeat %d: %+v", i, got)
		}
		if !r.control {
			t.Fatalf("repeat %d left control mode", i)
		}
	}
	got := r.route(key('h'))
	if got.Kind != routeVerb || got.Verb != protocol.VerbFocusLeft {
		t.Fatalf("final: %+v", got)
	}
	if r.control {
		t.Error("the unmodified final press did not leave control mode")
	}
}

// The overlay eats the key that dismisses it. Pressing k to close help
// must not also scroll.
func TestAnyKeyDismissesHelpAndIsSwallowed(t *testing.T) {
	for _, ev := range []uv.KeyPressEvent{key('k'), key('q'), key('z'), {Code: uv.KeyEscape}} {
		r := &router{prefix: "ctrl+b", control: true, help: true, detachable: true}
		got := r.route(ev)
		if got.Kind != routeIgnore {
			t.Errorf("%s dismissing help: kind %v, want routeIgnore", ev.String(), got.Kind)
		}
		if r.help {
			t.Errorf("%s did not dismiss the overlay", ev.String())
		}
		if !r.control {
			t.Errorf("%s: dismissing help should return to control mode", ev.String())
		}
	}
}

func TestParsePrefixAcceptsUsableKeys(t *testing.T) {
	for _, tc := range []struct{ in, prefix, label string }{
		{"ctrl+b", "ctrl+b", "C-b"},
		{"ctrl+a", "ctrl+a", "C-a"},
		{"CTRL+A", "ctrl+a", "C-a"},
		{"  ctrl+z  ", "ctrl+z", "C-z"},
		{"ctrl+space", "ctrl+space", "C-space"},
	} {
		prefix, label, err := parsePrefix(tc.in)
		if err != nil {
			t.Errorf("parsePrefix(%q): %v", tc.in, err)
			continue
		}
		if prefix != tc.prefix || label != tc.label {
			t.Errorf("parsePrefix(%q) = (%q, %q), want (%q, %q)", tc.in, prefix, label, tc.prefix, tc.label)
		}
	}
}

func TestParsePrefixRejectsUnusableKeys(t *testing.T) {
	// An unmatchable prefix is not a cosmetic problem: with no
	// unprefixed bindings left, it would leave a running wideboi with no
	// verbs and no way to quit but a signal. Refuse at startup instead.
	for _, in := range []string{"", "b", "alt+b", "ctrl+", "ctrl+bb", "ctrl+1", "ctrl+f5", "nonsense"} {
		if _, _, err := parsePrefix(in); err == nil {
			t.Errorf("parsePrefix(%q) accepted an unusable prefix", in)
		}
	}
}

// ctrl+i decodes as tab and ctrl+m as enter, so a prefix on either would
// start a wideboi that can never enter control mode -- there would be no
// way to quit but a signal. parsePrefix's own allowlist shape does not
// rule these out (they are ctrl+<a-z> like any other), so this has to be
// an explicit check against keys.Reserved, and the error has to name
// what the letter actually decodes as.
func TestParsePrefixRejectsReservedLetters(t *testing.T) {
	for letter, why := range keys.Reserved {
		if len(letter) != 1 || letter[0] < 'a' || letter[0] > 'z' {
			continue // "[" is not reachable here: the allowlist is ctrl+<a-z>.
		}
		in := "ctrl+" + letter
		_, _, err := parsePrefix(in)
		if err == nil {
			t.Errorf("parsePrefix(%q) accepted a prefix that decodes as %s", in, why)
			continue
		}
		if !strings.Contains(err.Error(), why) {
			t.Errorf("parsePrefix(%q) error %q does not name what it decodes as (%s)", in, err, why)
		}
	}
}

// The doubled-prefix escape hatch has to win even when the configured
// prefix collides with a binding's own ctrl repeat form.
// WIDEBOI_PREFIX=ctrl+l means C-l C-l forwards one literal ctrl+l and
// leaves control mode, rather than repeating focus-right twice -- route
// checks the prefix before it walks the binding table, and that
// ordering is deliberate, not emergent. See the README's "Changing the
// prefix" section.
func TestDoubledPrefixWinsOverAColludingCtrlForm(t *testing.T) {
	r := &router{prefix: "ctrl+l"}

	got := r.route(ctrl('l'))
	if got.Kind != routeIgnore || !r.control {
		t.Fatalf("first ctrl+l: kind %v control=%v, want routeIgnore and control mode entered",
			got.Kind, r.control)
	}

	got = r.route(ctrl('l'))
	if got.Kind != routeForward || r.control {
		t.Errorf("second ctrl+l: kind %v control=%v, want routeForward and control mode left -- "+
			"the doubled prefix must win over focus-right's repeat form", got.Kind, r.control)
	}
}

// A configured prefix has to be the one that works, and the default has
// to be the one that works when nothing is configured.
func TestConfiguredPrefixReplacesTheDefault(t *testing.T) {
	prefix, label, err := parsePrefix("ctrl+a")
	if err != nil {
		t.Fatal(err)
	}
	r := &router{prefix: prefix}
	if got := r.route(ctrl('b')); got.Kind != routeForward {
		t.Errorf("ctrl+b: kind %v, want routeForward -- it is not the prefix any more", got.Kind)
	}
	if got := r.route(ctrl('a')); got.Kind != routeIgnore || !r.control {
		t.Errorf("ctrl+a: kind %v control=%v, want routeIgnore and control mode", got.Kind, r.control)
	}
	if label != "C-a" {
		t.Errorf("label %q, want C-a", label)
	}
}

// keyNamed builds a KeyPressEvent that MatchString will match against
// name, for the handful of names the table uses.
func keyNamed(t *testing.T, name string) uv.KeyPressEvent {
	t.Helper()
	switch name {
	case "esc":
		return uv.KeyPressEvent{Code: uv.KeyEscape}
	case "?":
		return uv.KeyPressEvent{Code: '?', Text: "?"}
	case "tab":
		return uv.KeyPressEvent{Code: uv.KeyTab}
	}
	if rest, ok := strings.CutPrefix(name, "ctrl+"); ok && len(rest) == 1 {
		return uv.KeyPressEvent{Code: rune(rest[0]), Mod: uv.ModCtrl}
	}
	if len(name) == 1 {
		return uv.KeyPressEvent{Code: rune(name[0]), Text: name}
	}
	t.Fatalf("keyNamed does not know how to build %q", name)
	return uv.KeyPressEvent{}
}

func customRouter(t *testing.T, custom map[string][]string) *router {
	t.Helper()
	b, err := keys.BuildBindings(custom)
	if err != nil {
		t.Fatalf("BuildBindings: %v", err)
	}
	return &router{prefix: "ctrl+b", control: true, bindings: b}
}

func TestCustomAliasAndItsCtrlFormFire(t *testing.T) {
	custom := map[string][]string{keys.ActionNameFocusLeft: {"h", "g"}}

	r := customRouter(t, custom)
	if got := r.route(key('g')); got.Kind != routeVerb || got.Verb != protocol.VerbFocusLeft || r.control {
		t.Errorf("g: got %+v control=%v, want FocusLeft and out of control mode", got, r.control)
	}

	r = customRouter(t, custom)
	if got := r.route(ctrl('g')); got.Kind != routeVerb || got.Verb != protocol.VerbFocusLeft || !r.control {
		t.Errorf("ctrl+g: got %+v control=%v, want FocusLeft and still in control mode", got, r.control)
	}
}

func TestReplacedDefaultAliasIsUnknown(t *testing.T) {
	r := customRouter(t, map[string][]string{keys.ActionNameFocusLeft: {"h"}})
	if got := r.route(uv.KeyPressEvent{Code: uv.KeyLeft}); got.Kind != routeIgnore || r.control {
		t.Errorf("left: got %+v control=%v, want an unknown key that leaves control mode", got, r.control)
	}
}
