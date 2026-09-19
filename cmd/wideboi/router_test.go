package main

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
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

// Control mode is sticky: verbs do not exit it, so C-b l l l moves three
// columns. Enumerate the whole table rather than sampling it -- the
// input space is finite and small.
func TestControlModeVerbTable(t *testing.T) {
	cases := []struct {
		ev   uv.KeyPressEvent
		want route
	}{
		{key('h'), route{Kind: routeVerb, Verb: protocol.VerbFocusLeft}},
		{key('l'), route{Kind: routeVerb, Verb: protocol.VerbFocusRight}},
		{key(uv.KeyLeft), route{Kind: routeVerb, Verb: protocol.VerbFocusLeft}},
		{key(uv.KeyRight), route{Kind: routeVerb, Verb: protocol.VerbFocusRight}},
		{key('n'), route{Kind: routeVerb, Verb: protocol.VerbNewColumn}},
		{key('w'), route{Kind: routeVerb, Verb: protocol.VerbCycleWidth}},
		{key('x'), route{Kind: routeVerb, Verb: protocol.VerbKillPane}},
		{key('j'), route{Kind: routeVerb, Verb: protocol.VerbSmartJump}},
		{key('u'), route{Kind: routeScroll, Scroll: 10}},
		{key('d'), route{Kind: routeScroll, Scroll: -10}},
		{key('q'), route{Kind: routeQuit}},
	}
	for _, tc := range cases {
		r := &router{prefix: "ctrl+b", control: true}
		if got := r.route(tc.ev); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.ev.String(), got, tc.want)
		}
		if tc.want.Kind != routeQuit && !r.control {
			t.Errorf("%s: left control mode; the mode is sticky", tc.ev.String())
		}
	}
}

func TestControlModeSwallowsUnknownKeys(t *testing.T) {
	// Forwarding would let a typo land in a pane while the user still
	// believes they are issuing commands. Shift+Q is in here because
	// ultraviolet does not match a shifted letter against its lowercase
	// name, so it is genuinely unknown rather than a quit.
	r := &router{prefix: "ctrl+b", control: true}
	for _, ev := range []uv.KeyPressEvent{
		key('z'), key('1'), ctrl('c'),
		{Code: 'q', Mod: uv.ModShift, Text: "Q"},
	} {
		if got := r.route(ev); got.Kind != routeIgnore {
			t.Errorf("%s: kind %v, want routeIgnore", ev.String(), got.Kind)
		}
		if !r.control {
			t.Fatalf("%s: left control mode", ev.String())
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
