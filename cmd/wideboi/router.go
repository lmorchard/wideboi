package main

import (
	"fmt"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/protocol"
)

// The key router: every input decision wideboi makes, in one place that
// does not need a terminal to test.
//
// v1 bound every verb to alt, which requires the terminal to send Option
// as Meta. macOS terminals do not, by default, so every shortcut
// silently did nothing and the program looked broken. A prefix key is
// one byte that every terminal sends with no configuration, which
// removes the dependency instead of documenting it.
//
// The trade is that wideboi now claims a key the shell wants -- ctrl+b
// is readline's backward-char. That is acceptable only because it is
// exactly one key, it is escapable by typing it twice, and it is
// configurable. Those three are load-bearing, not niceties.

type routeKind int

const (
	// routeForward gives the key to the focused pane. The default, and
	// in normal mode the only outcome.
	routeForward routeKind = iota
	// routeIgnore swallows the key entirely.
	routeIgnore
	routeVerb
	routeScroll
	routeQuit
)

// route is what the router decided about one key event. It describes an
// action rather than performing one, so main's event loop stays a
// dispatch and the decision stays testable.
type route struct {
	Kind   routeKind
	Verb   protocol.VerbType
	Scroll int
}

type router struct {
	// prefix is an ultraviolet key name, already validated by
	// parsePrefix.
	prefix string
	// control is true while control mode is active. The mode is sticky:
	// only Escape, a doubled prefix, or quitting clears it, so C-b l l l
	// moves three columns.
	control bool
}

// route decides what to do with one key press, updating the mode as a
// side effect.
func (r *router) route(ev uv.KeyPressEvent) route {
	if !r.control {
		if ev.MatchString(r.prefix) {
			r.control = true
			return route{Kind: routeIgnore}
		}
		return route{Kind: routeForward}
	}

	switch {
	case ev.MatchString(r.prefix):
		// Typed twice, the prefix means itself. Leaving the mode is the
		// right guess about intent: someone sending the literal byte is
		// talking to the pane, not to us.
		r.control = false
		return route{Kind: routeForward}
	case ev.MatchString("esc"):
		r.control = false
		return route{Kind: routeIgnore}
	case ev.MatchString("q"):
		return route{Kind: routeQuit}
	case ev.MatchString("h") || ev.MatchString("left"):
		return route{Kind: routeVerb, Verb: protocol.VerbFocusLeft}
	case ev.MatchString("l") || ev.MatchString("right"):
		return route{Kind: routeVerb, Verb: protocol.VerbFocusRight}
	case ev.MatchString("n"):
		return route{Kind: routeVerb, Verb: protocol.VerbNewColumn}
	case ev.MatchString("w"):
		return route{Kind: routeVerb, Verb: protocol.VerbCycleWidth}
	case ev.MatchString("x"):
		return route{Kind: routeVerb, Verb: protocol.VerbKillPane}
	case ev.MatchString("j"):
		return route{Kind: routeVerb, Verb: protocol.VerbSmartJump}
	case ev.MatchString("u"):
		return route{Kind: routeScroll, Scroll: 10}
	case ev.MatchString("d"):
		return route{Kind: routeScroll, Scroll: -10}
	}

	// Unknown keys are swallowed rather than forwarded. The mode is
	// sticky, so forwarding would let a typo land in a pane while the
	// user still believes they are issuing commands.
	return route{Kind: routeIgnore}
}

// defaultPrefix is tmux's, deliberately. Users who run wideboi inside
// tmux will want to change it; see the README.
const defaultPrefix = "ctrl+b"

// parsePrefix validates a prefix key name, returning it alongside a
// short label for the status bar.
//
// The allowlist is narrow on purpose. An unmatchable prefix would leave
// a running wideboi with no verbs and no way to quit but a signal, and
// ultraviolet's MatchString gives no way to ask whether a name is one it
// can ever produce. Restricting to ctrl+<letter> and ctrl+space keeps
// the set to names that definitely work.
func parsePrefix(name string) (prefix, label string, err error) {
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case name == "ctrl+space":
		return name, "C-space", nil
	case len(name) == len("ctrl+x") && strings.HasPrefix(name, "ctrl+") &&
		name[len(name)-1] >= 'a' && name[len(name)-1] <= 'z':
		return name, "C-" + name[len(name)-1:], nil
	}
	return "", "", fmt.Errorf(
		"WIDEBOI_PREFIX=%q is not a usable prefix: want ctrl+<a-z> or ctrl+space", name)
}
