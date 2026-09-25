package main

import (
	"fmt"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/keys"
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
	routePan
	routeToggleFollowPTY
	routeQuit
	routeDetach
	// routeFocusColumn focuses a column by position. The client
	// resolves it, because the client's strip knows the order.
	routeFocusColumn
	// routeToggleLayout flips this client's layout. The client does it
	// alone; nothing is sent (#92).
	routeToggleLayout
	routeSearchStart
	routeSearchEdit
	routeSearchCommit
	routeSearchNavigate
	routeSearchCancel
	routeSearchAccept
	routeSearchLive
)

// route is what the router decided about one key event. It describes an
// action rather than performing one, so main's event loop stays a
// dispatch and the decision stays testable.
type route struct {
	Kind      routeKind
	Verb      protocol.VerbType
	Scroll    int
	Pan       int
	Column    int
	Text      string
	Backspace bool
	Direction int
}

type router struct {
	// prefix is an ultraviolet key name, already validated by
	// parsePrefix.
	prefix string
	// control is true while control mode is active. It is per-keystroke,
	// not sticky: holding Ctrl on a verb repeats it and keeps you here,
	// but the unmodified form fires once and leaves, exactly like an
	// unrecognised key, Escape, or the doubled prefix. So C-b l l l moves
	// one column and types "ll" into the pane; C-b C-l C-l l is what
	// moves three columns and then leaves.
	control bool
	// detachable mirrors the client's: true only when the session lives
	// in a separate server process that survives this client leaving.
	// When false, `d` is swallowed and leaves control mode, exactly like
	// any other key with no binding, because the only thing detaching
	// could mean in-process is killing every pane -- and a user who
	// learned the key elsewhere would lose their work to a keystroke the
	// bar no longer advertises.
	detachable bool
	// help is true while the overlay is up. It is a mode, not a route:
	// main mirrors it to the client after every key exactly as it does
	// with control, so the bar, the overlay and the router cannot
	// disagree about which mode is active.
	help   bool
	search int // 0: off, 1: query input, 2: match navigation
	// bindings is the active set of control mode bindings. If empty or nil,
	// defaults to keys.Bindings.
	bindings []keys.Binding
}

// route decides what to do with one key press, updating the mode as a
// side effect.
func (r *router) route(ev uv.KeyPressEvent) route {
	if r.search != 0 {
		if ev.MatchString("esc") {
			r.search = 0
			return route{Kind: routeSearchCancel}
		}
		if ev.MatchString("ctrl+g") {
			r.search = 0
			return route{Kind: routeSearchLive}
		}
		if ev.MatchString("enter") {
			if r.search == 1 {
				r.search = 2
				return route{Kind: routeSearchCommit}
			}
			r.search = 0
			return route{Kind: routeSearchAccept}
		}
		if r.search == 1 {
			if ev.MatchString("backspace") {
				return route{Kind: routeSearchEdit, Backspace: true}
			}
			if ev.Mod == 0 || ev.Mod == uv.ModShift {
				value := ev.Text
				if value == "" && ev.Code >= 32 && ev.Code < 127 {
					value = string(ev.Code)
				}
				if value != "" {
					return route{Kind: routeSearchEdit, Text: value}
				}
			}
			return route{Kind: routeIgnore}
		}
		if ev.Text == "N" || ev.Code == 'N' {
			return route{Kind: routeSearchNavigate, Direction: -1}
		}
		if ev.MatchString("n") {
			return route{Kind: routeSearchNavigate, Direction: 1}
		}
		return route{Kind: routeIgnore}
	}
	// The overlay swallows whatever dismisses it. Checked before the
	// table so that pressing `k` to close help cannot also scroll, and
	// before the prefix so the overlay is never a mode you can stack
	// things on top of.
	if r.help {
		r.help = false
		return route{Kind: routeIgnore}
	}

	if !r.control {
		if ev.MatchString(r.prefix) {
			r.control = true
			return route{Kind: routeIgnore}
		}
		return route{Kind: routeForward}
	}

	if ev.MatchString(r.prefix) {
		// Typed twice, the prefix means itself. Leaving the mode is the
		// right guess about intent: someone sending the literal byte is
		// talking to the pane, not to us.
		r.control = false
		return route{Kind: routeForward}
	}

	bindings := r.bindings
	if len(bindings) == 0 {
		bindings = keys.Bindings
	}
	for _, b := range bindings {
		if b.NeedsDetach && !r.detachable {
			continue
		}
		if forms := b.CtrlForms(); len(forms) > 0 && ev.MatchString(forms...) {
			return r.fire(b, true)
		}
		if ev.MatchString(b.MatchNames()...) {
			return r.fire(b, false)
		}
	}

	// Unknown keys are swallowed AND leave the mode, whatever modifier
	// they carried. Swallowed because forwarding would let a typo land
	// in a pane while the user still believes they are issuing commands;
	// exiting because a keystroke that did nothing must never strand you
	// in a mode that silently eats the next one. Holding Ctrl is not
	// evidence you meant a key that does not exist.
	r.control = false
	return route{Kind: routeIgnore}
}

// fire turns a matched binding into a route. sticky is true when the key
// carried Ctrl, which is the user saying "I have more of these coming".
func (r *router) fire(b keys.Binding, sticky bool) route {
	r.control = sticky

	switch b.Action {
	case keys.ActionVerb:
		return route{Kind: routeVerb, Verb: b.Verb}
	case keys.ActionScroll:
		return route{Kind: routeScroll, Scroll: b.Scroll}
	case keys.ActionPan:
		return route{Kind: routePan, Pan: b.Pan}
	case keys.ActionToggleFollowPTY:
		return route{Kind: routeToggleFollowPTY}
	case keys.ActionFocusColumn:
		// Digits have no ctrl form, so sticky is always false here.
		return route{Kind: routeFocusColumn, Column: b.Column}
	case keys.ActionToggleLayout:
		// NoRepeat, so sticky is always false: toggles once and leaves,
		// as it did when it was a verb.
		return route{Kind: routeToggleLayout}
	case keys.ActionSearch:
		r.search = 1
		r.control = false
		return route{Kind: routeSearchStart}
	case keys.ActionQuit:
		// ctrl+q is exactly q: "quit but stay in control mode" is not a
		// thing, because the client is leaving.
		r.control = false
		return route{Kind: routeQuit}
	case keys.ActionDetach:
		r.control = false
		return route{Kind: routeDetach}
	case keys.ActionHelp:
		// ? has no ctrl form, so sticky is always false here. Help
		// returns you to control mode on dismissal, which is what you
		// want right after looking up a binding.
		r.help = true
		r.control = true
		return route{Kind: routeIgnore}
	case keys.ActionExit:
		r.control = false
		return route{Kind: routeIgnore}
	}

	r.control = false
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
// can ever produce. Restricting to ctrl+<letter> and ctrl+space rules
// out most of that, but not all of it: ctrl+i decodes as tab and ctrl+m
// as enter (keys.Reserved; docs/LESSONS.md has the story), so a prefix
// on either would start a wideboi that can never enter control mode and
// has no way to quit but a signal. The shape of the allowlist does not
// imply those two are safe, so reject them explicitly.
func parsePrefix(name string) (prefix, label string, err error) {
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case name == "ctrl+space":
		return name, "C-space", nil
	case len(name) == len("ctrl+x") && strings.HasPrefix(name, "ctrl+") &&
		name[len(name)-1] >= 'a' && name[len(name)-1] <= 'z':
		letter := name[len(name)-1:]
		if why, bad := keys.Reserved[letter]; bad {
			return "", "", fmt.Errorf(
				"WIDEBOI_PREFIX=%q is not a usable prefix: ctrl+%s decodes as %s, not itself",
				name, letter, why)
		}
		return name, "C-" + letter, nil
	}
	return "", "", fmt.Errorf(
		"WIDEBOI_PREFIX=%q is not a usable prefix: want ctrl+<a-z> or ctrl+space", name)
}
