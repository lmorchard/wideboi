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

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/lmorchard/wideboi/internal/protocol"
)

// Canonical action names used in configuration files.
const (
	ActionNameFocusLeft    = "focus_left"
	ActionNameFocusRight   = "focus_right"
	ActionNameScrollDown   = "scroll_down"
	ActionNameScrollUp     = "scroll_up"
	ActionNameNewColumn    = "new_column"
	ActionNameCycleWidth   = "cycle_width"
	ActionNameGrowWidth    = "grow_width"
	ActionNameShrinkWidth  = "shrink_width"
	ActionNameMoveLeft     = "move_left"
	ActionNameMoveRight    = "move_right"
	ActionNameKillPane     = "kill_pane"
	ActionNameSmartJump    = "smart_jump"
	ActionNameFocusLast    = "focus_last"
	ActionNameToggleCards  = "toggle_cards"
	ActionNameToggleStatus = "toggle_status"
	ActionNameHelp         = "help"
	ActionNameSearch       = "search"
	ActionNameDetach       = "detach"
	ActionNameQuit         = "quit"
	ActionNameExit         = "exit"
)

// Action is what a binding does when it fires. It is deliberately not
// protocol.VerbType: scrolling, quitting, detaching, help, the layout
// toggle and leaving the mode are not verbs the server knows about.
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
	// ActionFocusColumn focuses the column at position Column, counting
	// from 1 at the left.
	ActionFocusColumn
	// ActionToggleLayout flips this client between the card fan and the
	// scrolling strip. Client-local: layout is presentation (#92).
	ActionToggleLayout
	ActionSearch
)

// LastColumn is Column's value for "the rightmost column, however many
// there are".
const LastColumn = -1

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
	// ActionName is the canonical name for this action used in configuration.
	ActionName string

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
	// Column is the 1-based position ActionFocusColumn focuses, or
	// LastColumn.
	Column int

	// BarGroup is the status-bar label. Bindings sharing a group collapse
	// into one entry, which is how h/j/k/l occupy nine cells rather than
	// thirty-six.
	BarGroup string
	// Long is the help overlay's one-line description.
	Long string
	// HelpGroup collapses bindings into one help-overlay line, the way
	// BarGroup does for the status bar, and is that line's text.
	HelpGroup string
	// HelpKey overrides the key label on a HelpGroup's line. Without it
	// the label is the members' keys joined with "/", which stays right
	// when a user remaps one of them.
	HelpKey string

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

// Help-overlay lines shared by pairs of bindings. The overlay has to fit
// at 80x24 (TestHelpOverlayFitsAt80x24), and one line per pair is what
// keeps it there. Each group's order matches its label: h/l, j/k, o/p, y/u.
const (
	helpFocus     = "focus the column left / right"
	helpScroll    = "scroll this pane's history down / up"
	helpWidth     = "shrink / grow this column's width"
	helpMove      = "move this column left / right"
	helpAttention = "jump to attention / status dashboard"
)

// Bindings is the table, in status-bar display order.
var Bindings = slices.Concat([]Binding{
	{ActionName: ActionNameFocusLeft, Key: "h", Aliases: []string{"left"}, Action: ActionVerb, Verb: protocol.VerbFocusLeft,
		BarGroup: "hjkl move", Long: "focus the column to the left", HelpGroup: helpFocus},
	{ActionName: ActionNameFocusRight, Key: "l", Aliases: []string{"right"}, Action: ActionVerb, Verb: protocol.VerbFocusRight,
		BarGroup: "hjkl move", Long: "focus the column to the right", HelpGroup: helpFocus},
	{ActionName: ActionNameScrollDown, Key: "j", Action: ActionScroll, Scroll: -10,
		BarGroup: "hjkl move", Long: "scroll this pane's history down", HelpGroup: helpScroll},
	{ActionName: ActionNameScrollUp, Key: "k", Action: ActionScroll, Scroll: 10,
		BarGroup: "hjkl move", Long: "scroll this pane's history up", HelpGroup: helpScroll},
	{ActionName: ActionNameNewColumn, Key: "n", Action: ActionVerb, Verb: protocol.VerbNewColumn,
		BarGroup: "n new", Long: "open a new column"},
	{ActionName: ActionNameCycleWidth, Key: "w", Action: ActionVerb, Verb: protocol.VerbCycleWidth,
		BarGroup: "w width", Long: "cycle this column's width"},
	{ActionName: ActionNameShrinkWidth, Key: "o", Action: ActionVerb, Verb: protocol.VerbShrinkWidth,
		Long: "shrink this column's width", HelpGroup: helpWidth},
	{ActionName: ActionNameGrowWidth, Key: "p", Action: ActionVerb, Verb: protocol.VerbGrowWidth,
		Long: "grow this column's width", HelpGroup: helpWidth},
	{ActionName: ActionNameMoveLeft, Key: "y", Action: ActionVerb, Verb: protocol.VerbMoveLeft,
		Long: "move this column left", HelpGroup: helpMove},
	{ActionName: ActionNameMoveRight, Key: "u", Action: ActionVerb, Verb: protocol.VerbMoveRight,
		Long: "move this column right", HelpGroup: helpMove},
	{ActionName: ActionNameKillPane, Key: "x", Action: ActionVerb, Verb: protocol.VerbKillPane,
		BarGroup: "x kill", Long: "kill the focused pane"},
	{ActionName: ActionNameSmartJump, Key: "a", Action: ActionVerb, Verb: protocol.VerbSmartJump,
		BarGroup: "a attn", Long: "jump to a pane wanting attention", HelpGroup: helpAttention},
	{ActionName: ActionNameToggleStatus, Key: "s", Action: ActionVerb, Verb: protocol.VerbToggleStatus,
		Long: "open or focus pane status dashboard", HelpGroup: helpAttention},
	// tab has no ctrl form -- CtrlForm wants a single letter, and ctrl+i
	// decodes as tab anyway -- and repeating a toggle only bounces.
	{ActionName: ActionNameFocusLast, Key: "tab", Action: ActionVerb, Verb: protocol.VerbFocusLast,
		Long: "focus the previously focused pane"},
}, digitBindings(), []Binding{
	{ActionName: ActionNameSearch, Key: "/", Action: ActionSearch, NoRepeat: true,
		Long: "search the focused pane's history"},
	{ActionName: ActionNameHelp, Key: "?", Action: ActionHelp,
		BarGroup: "? help", Long: "show this help"},
	{ActionName: ActionNameDetach, Key: "d", Action: ActionDetach, NeedsDetach: true,
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
	{ActionName: ActionNameToggleCards, Key: "c", Action: ActionToggleLayout,
		NoRepeat: true, Long: "toggle the card layout"},
	{ActionName: ActionNameQuit, Key: "q", Action: ActionQuit, Essential: true,
		BarGroup: "q quit", Long: "quit wideboi and close every pane"},
	{ActionName: ActionNameExit, Key: "esc", Action: ActionExit, Essential: true,
		BarGroup: "esc exit", Long: "leave control mode"},
})

// digitHelp is the one overlay line all ten digit bindings share.
const digitHelp = "focus a column by position, 0 the last"

// digitBindings returns 1-9, focusing the column at that position from
// the left, and 0 for the last one. Positions rather than pane IDs: IDs
// are never reused, so after a few kills they no longer fit in a digit.
//
// These have no ActionName in validActions, so they cannot be remapped;
// BuildBindings' collision check still stops another action landing on
// one.
func digitBindings() []Binding {
	out := make([]Binding, 0, 10)
	for n := 1; n <= 9; n++ {
		out = append(out, Binding{ActionName: fmt.Sprintf("focus_column_%d", n),
			Key: strconv.Itoa(n), Action: ActionFocusColumn, Column: n,
			Long: fmt.Sprintf("focus column %d", n), HelpGroup: digitHelp, HelpKey: "0-9"})
	}
	return append(out, Binding{ActionName: "focus_column_last",
		Key: "0", Action: ActionFocusColumn, Column: LastColumn,
		Long: "focus the last column", HelpGroup: digitHelp, HelpKey: "0-9"})
}

// CtrlForms returns the ultraviolet key name of the repeat form for
// every key on this binding that has one.
//
// Only single lowercase letters outside Reserved do. "esc" is a named
// key with no ctrl encoding, and "?" needs a shift on every layout, so
// ctrl+? is not a combination terminals reliably produce.
func (b Binding) CtrlForms() []string {
	if b.NoRepeat {
		return nil
	}
	var out []string
	for _, k := range b.MatchNames() {
		if len(k) != 1 || k[0] < 'a' || k[0] > 'z' {
			continue
		}
		if _, bad := Reserved[k]; bad {
			continue
		}
		out = append(out, "ctrl+"+k)
	}
	return out
}

// MatchNames returns every name the unmodified form answers to, ready to
// hand to uv.KeyPressEvent.MatchString, which ORs its arguments.
func (b Binding) MatchNames() []string {
	out := make([]string, 0, 1+len(b.Aliases))
	out = append(out, b.Key)
	out = append(out, b.Aliases...)
	return out
}

// BarItemsFor returns the distinct status-bar labels for the given bindings,
// split into the ones that may be dropped when the terminal is narrow and the
// ones that may not, both in table order.
func BarItemsFor(bindings []Binding, detachable bool) (droppable, essential []string) {
	seen := map[string]bool{}
	for _, b := range bindings {
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

// BarItems returns the distinct status-bar labels for the default Bindings table.
func BarItems(detachable bool) (droppable, essential []string) {
	return BarItemsFor(Bindings, detachable)
}

// validActions is the set of canonical action names and recognized aliases.
var validActions = map[string]string{
	ActionNameFocusLeft:    ActionNameFocusLeft,
	ActionNameFocusRight:   ActionNameFocusRight,
	ActionNameScrollDown:   ActionNameScrollDown,
	ActionNameScrollUp:     ActionNameScrollUp,
	ActionNameNewColumn:    ActionNameNewColumn,
	ActionNameCycleWidth:   ActionNameCycleWidth,
	ActionNameGrowWidth:    ActionNameGrowWidth,
	ActionNameShrinkWidth:  ActionNameShrinkWidth,
	ActionNameMoveLeft:     ActionNameMoveLeft,
	ActionNameMoveRight:    ActionNameMoveRight,
	ActionNameKillPane:     ActionNameKillPane,
	ActionNameSmartJump:    ActionNameSmartJump,
	"attn":                 ActionNameSmartJump,
	ActionNameToggleStatus: ActionNameToggleStatus,
	"status":               ActionNameToggleStatus,
	ActionNameFocusLast:    ActionNameFocusLast,
	ActionNameToggleCards:  ActionNameToggleCards,
	ActionNameHelp:         ActionNameHelp,
	ActionNameSearch:       ActionNameSearch,
	ActionNameDetach:       ActionNameDetach,
	ActionNameQuit:         ActionNameQuit,
	ActionNameExit:         ActionNameExit,
}

// validNamedKeys is the set of non-single-character key names produced by ultraviolet.
var validNamedKeys = map[string]bool{
	"esc":       true,
	"enter":     true,
	"tab":       true,
	"space":     true,
	"backspace": true,
	"delete":    true,
	"up":        true,
	"down":      true,
	"left":      true,
	"right":     true,
	"pgup":      true,
	"pgdown":    true,
	"home":      true,
	"end":       true,
	"insert":    true,
	"f1":        true,
	"f2":        true,
	"f3":        true,
	"f4":        true,
	"f5":        true,
	"f6":        true,
	"f7":        true,
	"f8":        true,
	"f9":        true,
	"f10":       true,
	"f11":       true,
	"f12":       true,
}

// validateKey normalizes one configured key for act and checks it can be bound.
func validateKey(act, key string) (string, error) {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return "", fmt.Errorf("key for action %q cannot be empty", act)
	}
	if why, bad := Reserved[k]; bad {
		return "", fmt.Errorf("key %q for action %q is reserved: %s", k, act, why)
	}
	if !isValidKeyName(k) {
		if k == "pgdn" {
			return "", fmt.Errorf("key %q for action %q is not a recognized key name; did you mean \"pgdown\"?", k, act)
		}
		return "", fmt.Errorf("key %q for action %q is not a recognized key name; must be a single printable character or valid named key (e.g. esc, space, left, right, up, down, pgup, pgdown)", k, act)
	}
	return k, nil
}

// collisionError explains a key held by two bindings. At least one of
// them was configured -- the default table has no collisions
// (TestTableIsWellFormed) -- and the hint names the one that was not.
func collisionError(key, a, b string, configured map[string][]string) error {
	_, aSet := configured[a]
	_, bSet := configured[b]
	if aSet == bSet {
		return fmt.Errorf("duplicate key %q assigned to both %q and %q", key, a, b)
	}
	mine, other := a, b
	if bSet {
		mine, other = b, a
	}
	if _, remappable := validActions[other]; !remappable {
		return fmt.Errorf("duplicate key %q for %q: %q holds it and cannot be remapped", key, mine, other)
	}
	return fmt.Errorf("duplicate key %q for %q: %q holds it by default; remap %s too", key, mine, other, other)
}

// isValidKeyName reports whether k is a valid key name or character in ultraviolet.
func isValidKeyName(k string) bool {
	if validNamedKeys[k] {
		return true
	}
	runes := []rune(k)
	if len(runes) == 1 {
		r := runes[0]
		return unicode.IsPrint(r) && !unicode.IsSpace(r)
	}
	return false
}

// BuildBindings builds and validates a set of control-mode bindings with custom
// key mappings applied over the default Bindings table.
//
// Each entry replaces every default key of its action, aliases included.
// The first key is the primary -- the one the bar and help show -- and
// the rest are aliases. An empty list unbinds the action.
func BuildBindings(custom map[string][]string) ([]Binding, error) {
	if len(custom) == 0 {
		out := make([]Binding, len(Bindings))
		copy(out, Bindings)
		return out, nil
	}

	// 1. Validate custom action names and keys
	normalized := make(map[string][]string, len(custom))
	for act, ks := range custom {
		canonical, ok := validActions[act]
		if !ok {
			return nil, fmt.Errorf("unknown action %q; valid actions are: focus_left, focus_right, scroll_down, scroll_up, new_column, cycle_width, grow_width, shrink_width, move_left, move_right, kill_pane, smart_jump, toggle_status, focus_last, toggle_cards, help, detach, quit, exit", act)
		}
		if canonical == ActionNameQuit && len(ks) == 0 {
			return nil, fmt.Errorf("action %q cannot be unbound: it is the only way to end the session", act)
		}
		norm := make([]string, 0, len(ks))
		for _, key := range ks {
			k, err := validateKey(act, key)
			if err != nil {
				return nil, err
			}
			if slices.Contains(norm, k) {
				return nil, fmt.Errorf("key %q is listed twice for action %q", k, act)
			}
			norm = append(norm, k)
		}
		normalized[canonical] = norm
	}

	// 2. Clone default bindings, replacing the keys of configured ones
	out := make([]Binding, 0, len(Bindings))
	for _, b := range Bindings {
		if ks, ok := normalized[b.ActionName]; ok {
			if len(ks) == 0 {
				continue // unbound
			}
			b.Key = ks[0]
			b.Aliases = nil
			if len(ks) > 1 {
				b.Aliases = slices.Clone(ks[1:])
			}
		}
		out = append(out, b)
	}

	// A help group's line describes all of its members ("left / right"),
	// so once one is unbound the survivors fall back to their own Long.
	for _, ub := range Bindings {
		if ks, ok := normalized[ub.ActionName]; !ok || len(ks) > 0 || ub.HelpGroup == "" {
			continue
		}
		for i := range out {
			if out[i].HelpGroup == ub.HelpGroup {
				out[i].HelpGroup = ""
				out[i].HelpKey = ""
			}
		}
	}

	// 3. Collision check across every key of every binding
	seen := make(map[string]string) // key -> actionName
	for _, b := range out {
		for _, k := range b.MatchNames() {
			if prev, exists := seen[k]; exists {
				return nil, collisionError(k, prev, b.ActionName, normalized)
			}
			seen[k] = b.ActionName
		}
	}

	// 4. Update BarGroup labels dynamically
	var hKey, jKey, kKey, lKey string
	for _, b := range out {
		switch b.ActionName {
		case ActionNameFocusLeft:
			hKey = b.Key
		case ActionNameScrollDown:
			jKey = b.Key
		case ActionNameScrollUp:
			kKey = b.Key
		case ActionNameFocusRight:
			lKey = b.Key
		}
	}
	moveGroup := fmt.Sprintf("%s%s%s%s move", hKey, jKey, kKey, lKey)

	for i := range out {
		b := &out[i]
		switch b.ActionName {
		case ActionNameFocusLeft, ActionNameScrollDown, ActionNameScrollUp, ActionNameFocusRight:
			b.BarGroup = moveGroup
		case ActionNameNewColumn:
			b.BarGroup = fmt.Sprintf("%s new", b.Key)
		case ActionNameCycleWidth:
			b.BarGroup = fmt.Sprintf("%s width", b.Key)
		case ActionNameGrowWidth, ActionNameShrinkWidth, ActionNameToggleCards:
			b.BarGroup = ""
		case ActionNameKillPane:
			b.BarGroup = fmt.Sprintf("%s kill", b.Key)
		case ActionNameSmartJump:
			b.BarGroup = fmt.Sprintf("%s attn", b.Key)
		case ActionNameHelp:
			b.BarGroup = fmt.Sprintf("%s help", b.Key)
		case ActionNameDetach:
			b.BarGroup = fmt.Sprintf("%s detach", b.Key)
		case ActionNameQuit:
			b.BarGroup = fmt.Sprintf("%s quit", b.Key)
		case ActionNameExit:
			b.BarGroup = fmt.Sprintf("%s exit", b.Key)
		}
	}

	return out, nil
}
