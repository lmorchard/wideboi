// Package keys is the single source of truth for wideboi's control-mode
// bindings.
//
// The table used to exist twice -- a switch in cmd/wideboi/router.go and
// a []string in internal/client/client.go -- kept in agreement by
// scripts/smoke.py asserting the strings matched. Adding scroll-down and
// a help overlay would have made it three copies of the same facts, and
// docs/LESSONS.md already records what drift in this area costs.
//
// It imports only internal/protocol, which keeps it on the client side
// of the seam scripts/seam-check.sh enforces.
package keys

import "github.com/lmorchard/wideboi/internal/protocol"

// Action is what a binding does when it fires. It is deliberately not
// protocol.VerbType: scrolling, quitting, detaching, help and leaving
// the mode are not verbs the server knows about.
type Action int

const (
	// ActionVerb sends Verb to the server.
	ActionVerb Action = iota + 1
	// ActionScroll asks the server to move the focused pane's scrollback
	// offset by Scroll.
	ActionScroll
	// ActionQuit tears down the session.
	ActionQuit
	// ActionDetach leaves a socket-attached session running.
	ActionDetach
	// ActionHelp raises the overlay.
	ActionHelp
	// ActionExit leaves control mode without doing anything else.
	ActionExit
)

// Reserved names the letters that can never carry a binding, and why.
// Their control bytes are already spoken for by keys every terminal
// sends, so ctrl+<letter> decodes as something else entirely and the
// binding's repeat form would silently never fire.
var Reserved = map[string]string{
	"i": "tab (0x09)",
	"m": "enter (0x0d)",
	"[": "escape (0x1b)",
}

// Binding is one row of the control-mode table.
type Binding struct {
	// Key is the ultraviolet key name for the unmodified form.
	Key string
	// Aliases are additional names that fire the same binding, for keys
	// with no meaningful repeat form of their own (the arrows).
	Aliases []string

	Action Action
	// Verb is set when Action is ActionVerb.
	Verb protocol.VerbType
	// Scroll is the offset delta when Action is ActionScroll. Positive
	// moves back into history; internal/server/term clamps both ends.
	Scroll int

	// BarGroup is the status-bar label. Bindings sharing a group collapse
	// into one entry, which is how h/j/k/l occupy nine cells rather than
	// thirty-six.
	BarGroup string
	// Long is the help overlay's one-line description.
	Long string

	// NeedsDetach hides the binding unless the client reached its server
	// over a socket. An in-process wideboi owns its panes, so detaching
	// there could only mean killing them.
	NeedsDetach bool
	// Essential keeps the entry in the bar at every width. With no
	// unprefixed escape hatch, a user who cannot read these has no way
	// forward except a signal.
	Essential bool

	// NoRepeat suppresses the ctrl+<letter> repeat form.
	//
	// Repeat exists so "move two columns left" is one chord rather
	// than two, which is meaningless for a toggle -- pressing it
	// twice returns you to where you started. Suppressing it also
	// keeps the letter's control byte available for whatever it
	// already meant: ctrl+c stays an unknown key that leaves control
	// mode, which is the escape hatch a user reaches for by reflex.
	NoRepeat bool
}

// Bindings is the table, in status-bar display order.
var Bindings = []Binding{
	{Key: "h", Aliases: []string{"left"}, Action: ActionVerb, Verb: protocol.VerbFocusLeft,
		BarGroup: "hjkl move", Long: "focus the column to the left"},
	{Key: "l", Aliases: []string{"right"}, Action: ActionVerb, Verb: protocol.VerbFocusRight,
		BarGroup: "hjkl move", Long: "focus the column to the right"},
	{Key: "j", Action: ActionScroll, Scroll: -10,
		BarGroup: "hjkl move", Long: "scroll this pane's history down"},
	{Key: "k", Action: ActionScroll, Scroll: 10,
		BarGroup: "hjkl move", Long: "scroll this pane's history up"},
	{Key: "n", Action: ActionVerb, Verb: protocol.VerbNewColumn,
		BarGroup: "n new", Long: "open a new column"},
	{Key: "w", Action: ActionVerb, Verb: protocol.VerbCycleWidth,
		BarGroup: "w width", Long: "cycle this column's width"},
	{Key: "x", Action: ActionVerb, Verb: protocol.VerbKillPane,
		BarGroup: "x kill", Long: "kill the focused pane"},
	{Key: "a", Action: ActionVerb, Verb: protocol.VerbSmartJump,
		BarGroup: "a attn", Long: "jump to a pane wanting attention"},
	{Key: "?", Action: ActionHelp,
		BarGroup: "? help", Long: "show this help"},
	{Key: "d", Action: ActionDetach, NeedsDetach: true,
		BarGroup: "d detach", Long: "detach, leaving the session running"},
	// No BarGroup on purpose: this verb lives in the help overlay
	// only. The attached bar already totals exactly 77 cells against a
	// 79-cell budget at 80 columns, so there is no room for another
	// entry, and TestControlHelpFitsEveryEntryAt80Columns says what to
	// do about it -- "the overlay is already there and the bar should
	// shed the entry rather than grow." Growing the bar would push
	// "d detach" off, which scripts/attachcheck.py asserts is present.
	//
	// NoRepeat because ctrl+c must stay an unknown key that leaves
	// control mode; see the field's comment.
	{Key: "c", Action: ActionVerb, Verb: protocol.VerbToggleCards,
		NoRepeat: true, Long: "toggle the card layout"},
	{Key: "q", Action: ActionQuit, Essential: true,
		BarGroup: "q quit", Long: "quit wideboi and close every pane"},
	{Key: "esc", Action: ActionExit, Essential: true,
		BarGroup: "esc exit", Long: "leave control mode"},
}

// CtrlForm returns the ultraviolet key name for this binding's repeat
// form and whether it has one.
//
// Only single lowercase letters outside Reserved do. "esc" is a named
// key with no ctrl encoding, and "?" needs a shift on every layout, so
// ctrl+? is not a combination terminals reliably produce.
func (b Binding) CtrlForm() (string, bool) {
	if b.NoRepeat {
		return "", false
	}
	if len(b.Key) != 1 {
		return "", false
	}
	c := b.Key[0]
	if c < 'a' || c > 'z' {
		return "", false
	}
	if _, bad := Reserved[b.Key]; bad {
		return "", false
	}
	return "ctrl+" + b.Key, true
}

// MatchNames returns every name the unmodified form answers to, ready to
// hand to uv.KeyPressEvent.MatchString, which ORs its arguments.
func (b Binding) MatchNames() []string {
	out := make([]string, 0, 1+len(b.Aliases))
	out = append(out, b.Key)
	out = append(out, b.Aliases...)
	return out
}

// BarItems returns the distinct status-bar labels, split into the ones
// that may be dropped when the terminal is narrow and the ones that may
// not, both in table order.
func BarItems(detachable bool) (droppable, essential []string) {
	seen := map[string]bool{}
	for _, b := range Bindings {
		if b.NeedsDetach && !detachable {
			continue
		}
		if b.BarGroup == "" || seen[b.BarGroup] {
			continue
		}
		seen[b.BarGroup] = true
		if b.Essential {
			essential = append(essential, b.BarGroup)
		} else {
			droppable = append(droppable, b.BarGroup)
		}
	}
	return droppable, essential
}
