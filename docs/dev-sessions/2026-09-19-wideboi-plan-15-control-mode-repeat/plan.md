# Plan 15 Implementation Plan — Control-mode repeat, hjkl, help overlay

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make control-mode stickiness per-keystroke — Ctrl-modified verbs stay in the mode, unmodified verbs act and leave — while moving the verb table to `hjkl`, adding the scroll-down binding that does not exist today, and adding a `?` help overlay.

**Architecture:** A new `internal/keys` package holds the one verb table. `cmd/wideboi`'s router walks it to decide routing; `internal/client` reads it to render the status bar and the help overlay. Today that table exists twice and would become three copies; after this it exists once, which is also the seam a config file would later populate.

**Tech Stack:** Go, `charmbracelet/ultraviolet` (pinned pseudo-version), `charmbracelet/x/ansi`. Acceptance checks are Python driving a real pty (`scripts/smoke.py`, `scripts/ptylib.py`).

**Spec:** `docs/dev-sessions/2026-09-19-wideboi-plan-15-control-mode-repeat/spec.md`

## Global Constraints

- **Reserved letters: `i`, `m`, `[`.** Their control bytes are Tab (0x09), Enter (0x0D) and Escape (0x1B), so a verb on those letters can never have a working repeat form. No binding may use them.
- **Status bar budget is `cols - 1` cells.** Never write the terminal's last column: ultraviolet brackets a final-column write with autowrap toggles (`ESC[?7l` … `ESC[?7h`), which splits text across escape sequences on the wire.
- **Measure truncation in runes via `runeLen`, not bytes.** Existing helpers `runeLen` and `truncateRunes` in `internal/client/client.go` are the only correct ones.
- **`internal/client` must not import `internal/server`.** Enforced by `scripts/seam-check.sh`. `internal/keys` may import only `internal/protocol`.
- **Always run Go tests with `-count=1`.** A cached pass looks exactly like a real one.
- **Break every new test and watch it go red before moving on.** This repo has had three tests pass against the bug they were written to catch.
- **Never log to stderr while the alt screen is live.** Record failures on the object or use `slog` (file-backed), never `log.Printf`.
- Full gate is `make check`. It must pass before the final commit.

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/keys/keys.go` | **Create.** The single binding table: key, aliases, action, bar label, overlay description, detach requirement. |
| `internal/keys/keys_test.go` | **Create.** Table invariants: no duplicates, no reserved letters, every ctrl form decodes from its real byte. |
| `cmd/wideboi/router.go` | **Modify.** Replace the hardcoded `switch` with a table walk; add the ctrl/plain/exit rule and `help` mode. |
| `cmd/wideboi/router_test.go` | **Modify.** Enumerate the table × {plain, ctrl} × {detachable, not}. |
| `internal/client/client.go` | **Modify.** `controlHelp` reads the table; add `helpVisible` state and `SetHelpVisible`; `Draw` checks the overlay first. |
| `internal/client/help.go` | **Create.** Overlay geometry and rendering, isolated so `Draw` stays a dispatch. |
| `internal/client/help_overlay_test.go` | **Create.** Content comes from the table; geometry survives 0x0, 1x1, 4x2. |
| `internal/client/help_test.go` | **Modify.** Retire `TestControlHelpFitsEveryVerbAt80Columns`; replace with the new budget assertion. |
| `cmd/wideboi/main.go` | **Modify.** Mirror `rt.help` to the client after every key, in both event loops. |
| `scripts/smoke.py` | **Modify.** Wire-level cases for repeat, act-and-exit, unknown-exits, and the overlay. |

---

### Task 1: The `internal/keys` binding table

**Files:**
- Create: `internal/keys/keys.go`
- Test: `internal/keys/keys_test.go`

**Interfaces:**
- Consumes: `internal/protocol` (`VerbType` and its constants).
- Produces: `keys.Action` (`ActionVerb`, `ActionScroll`, `ActionQuit`, `ActionDetach`, `ActionHelp`, `ActionExit`); `keys.Binding` struct; `keys.Bindings []Binding`; `func (Binding) CtrlForm() (string, bool)`; `func (Binding) MatchNames() []string`; `func BarItems(detachable bool) (droppable, essential []string)`; `keys.Reserved map[string]string`.

- [ ] **Step 1: Write the failing test**

Create `internal/keys/keys_test.go`:

```go
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
	for _, b := range keys.Bindings {
		names := b.MatchNames()
		if len(names) == 0 {
			t.Errorf("%q produces no match names", b.Key)
		}
		if names[0] != b.Key {
			t.Errorf("%q: first match name is %q", b.Key, names[0])
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/keys/ -count=1`
Expected: FAIL — `no required module provides package github.com/lmorchard/wideboi/internal/keys` (the package does not exist yet).

- [ ] **Step 3: Write the implementation**

Create `internal/keys/keys.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/keys/ -count=1 -v`
Expected: PASS, all seven tests.

- [ ] **Step 5: Prove the reserved-letter guard can fail**

Temporarily add `{Key: "i", Action: ActionVerb, Verb: protocol.VerbNewColumn, BarGroup: "i x", Long: "x"}` to `Bindings`.

Run: `go test ./internal/keys/ -count=1`
Expected: FAIL in `TestNoBindingUsesAReservedLetter` with "binding \"i\" is reserved: ctrl+i is tab (0x09)".

Only that test fires, which is correct — `CtrlForm` returns false for reserved letters, so `TestEveryCtrlFormMatchesItsRealByte` skips the row rather than failing on it. The reserved guard is the one and only thing standing between that row and a binding that silently does nothing when Ctrl is held.

Remove the temporary row and re-run to confirm green.

- [ ] **Step 6: Commit**

```bash
git add internal/keys/
git commit -m "feat(keys): single source of truth for control-mode bindings"
```

---

### Task 2: Table-driven router with the ctrl/plain/exit rule

**Files:**
- Modify: `cmd/wideboi/router.go`
- Test: `cmd/wideboi/router_test.go`

**Interfaces:**
- Consumes: `keys.Bindings`, `keys.Binding.CtrlForm()`, `keys.Binding.MatchNames()`, the `keys.Action*` constants.
- Produces: `router` gains a `help bool` field; `route` gains no new fields; `routeKind` gains no new members (help is a mode flag, not a route).

- [ ] **Step 1: Write the failing test**

Replace `TestControlModeVerbTable` in `cmd/wideboi/router_test.go` with the block below, and add the three tests after it. Leave the other existing tests alone.

```go
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
			name, hasCtrl := b.CtrlForm()
			if !hasCtrl {
				continue
			}
			t.Run(b.Key+"/ctrl", func(t *testing.T) {
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
					// ctrl+q and ctrl+d are exactly q and d.
				default:
					if !r.control {
						t.Errorf("%s did not stay in control mode", name)
					}
				}
			})
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
		key('z'), key('5'),
		{Code: 'g', Mod: uv.ModCtrl},
		{Code: 'z', Mod: uv.ModCtrl},
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
```

Add this helper at the bottom of `router_test.go`, beside the existing `key` and `ctrl` helpers:

```go
// keyNamed builds a KeyPressEvent that MatchString will match against
// name, for the handful of names the table uses.
func keyNamed(t *testing.T, name string) uv.KeyPressEvent {
	t.Helper()
	switch name {
	case "esc":
		return uv.KeyPressEvent{Code: uv.KeyEscape}
	case "?":
		return uv.KeyPressEvent{Code: '?', Text: "?"}
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
```

Add `"strings"`, `"github.com/lmorchard/wideboi/internal/keys"` to the test file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/wideboi/ -count=1 -run 'TestControlMode|TestUnknown|TestCtrlRepeat|TestAnyKey'`
Expected: FAIL to compile — `r.help undefined`, `r.detachable` exists but `keys` is unused by `router.go`, and the old `j`-is-smart-jump behaviour contradicts the table.

- [ ] **Step 3: Write the implementation**

In `cmd/wideboi/router.go`, add `help` to the struct and replace the whole `switch` in `route` with a table walk.

Struct — add after `detachable`:

```go
	// help is true while the overlay is up. It is a mode, not a route:
	// main mirrors it to the client after every key exactly as it does
	// with control, so the bar, the overlay and the router cannot
	// disagree about which mode is active.
	help bool
```

Replace the body of `route` from the `switch` to the end of the function:

```go
func (r *router) route(ev uv.KeyPressEvent) route {
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

	for _, b := range keys.Bindings {
		if b.NeedsDetach && !r.detachable {
			continue
		}
		if name, ok := b.CtrlForm(); ok && ev.MatchString(name) {
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
```

Add `"github.com/lmorchard/wideboi/internal/keys"` to the imports and drop `"github.com/lmorchard/wideboi/internal/protocol"` if it is now unused (it is: `route.Verb` is still `protocol.VerbType`, so keep it).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/wideboi/ -count=1`
Expected: PASS.

- [ ] **Step 5: Prove the repeat test can fail**

In `fire`, temporarily change `r.control = sticky` to `r.control = false`.

Run: `go test ./cmd/wideboi/ -count=1 -run TestCtrlRepeatThenPlainExits`
Expected: FAIL — "repeat 0 left control mode". Restore and confirm green.

- [ ] **Step 6: Commit**

```bash
git add cmd/wideboi/router.go cmd/wideboi/router_test.go
git commit -m "feat(router): ctrl repeats control mode, unmodified keys act and exit"
```

---

### Task 3: Status bar renders from the table

**Files:**
- Modify: `internal/client/client.go` (delete `controlVerbs`, `controlTail`, `detachVerb`; rewrite `controlHelp`)
- Modify: `internal/client/help_test.go`

**Interfaces:**
- Consumes: `keys.BarItems(detachable bool) (droppable, essential []string)`.
- Produces: `controlHelp(budget int, detachable bool) string` keeps its signature; the package-level `controlVerbs`, `controlTail` and `detachVerb` identifiers are removed.

- [ ] **Step 1: Write the failing test**

In `internal/client/help_test.go`, delete `TestControlHelpFitsEveryVerbAt80Columns` and `TestControlHelpOffersDetachOnlyWhenDetachable` (they reference the removed identifiers), and add:

```go
// The whole menu must fit at 80 columns, which is the commonest width
// and cmd/wideboi's own fallback when the host reports no size.
//
// This assertion replaces one that pinned a smaller table. Adding
// scroll-down pushed the descriptive form to 81 cells against a 79-cell
// budget, so h/j/k/l collapse into one "hjkl move" entry and the help
// overlay does the teaching. If a verb is ever added that breaks this
// again, the overlay is already there and the bar should shed the entry
// rather than grow.
func TestControlHelpFitsEveryEntryAt80Columns(t *testing.T) {
	for _, detachable := range []bool{true, false} {
		at80 := controlHelp(79, detachable)
		droppable, essential := keys.BarItems(detachable)
		for _, want := range append(append([]string{}, droppable...), essential...) {
			if !strings.Contains(at80, want) {
				t.Errorf("detachable=%v: control help omits %q: %q", detachable, want, at80)
			}
		}
		if got := runeLen(at80); got > 79 {
			t.Errorf("detachable=%v: control help is %d cells at 80 columns, budget is 79: %q",
				detachable, got, at80)
		}
	}
}

// Detach is the one entry whose presence depends on how the client
// reached its server. An in-process wideboi owns its panes, so offering
// the verb would be advertising data loss.
func TestControlHelpOffersDetachOnlyWhenDetachable(t *testing.T) {
	if got := controlHelp(79, false); strings.Contains(got, "d detach") {
		t.Errorf("in-process control help offers detach: %q", got)
	}
	if got := controlHelp(79, true); !strings.Contains(got, "d detach") {
		t.Errorf("attached control help omits detach: %q", got)
	}
}

// The bar is built from the same table the router routes from, so a
// binding can never be advertised without existing or exist without
// being advertised.
func TestControlHelpNamesEveryBarGroup(t *testing.T) {
	at200 := controlHelp(199, true)
	for _, b := range keys.Bindings {
		if !strings.Contains(at200, b.BarGroup) {
			t.Errorf("control help omits %q (binding %q): %q", b.BarGroup, b.Key, at200)
		}
	}
}
```

Update the existing `TestControlHelpAlwaysNamesQuitAndExit` — it already calls `controlHelp(cols-1, true)`, so it needs no change. Add `"github.com/lmorchard/wideboi/internal/keys"` to the imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/client/ -count=1`
Expected: FAIL to compile — `undefined: keys` until the import lands, then FAIL on content because `controlHelp` still reads the old hardcoded `controlVerbs`.

- [ ] **Step 3: Write the implementation**

In `internal/client/client.go`, delete the `controlVerbs` var, the `detachVerb` const and the `controlTail` var, and replace `controlHelp` with:

```go
// controlHelp returns as much of the control-mode menu as fits in budget
// cells, always including the entries keys marks Essential.
//
// Both halves come from internal/keys, so the bar cannot advertise a
// binding the router does not have, or omit one it does. That used to be
// guaranteed by scripts/smoke.py comparing two hardcoded lists of the
// same strings, which only ever caught drift someone remembered to
// assert.
//
// Dropping starts from the end of the droppable list. The essential
// entries are never dropped: with no unprefixed escape hatch, a user who
// cannot read "q quit" and "esc exit" out of the bar has no way forward
// except a signal.
func controlHelp(budget int, detachable bool) string {
	droppable, essential := keys.BarItems(detachable)

	tail := strings.Join(essential, "  ")
	used := runeLen(tail)

	taken := make([]string, 0, len(droppable))
	for _, v := range droppable {
		if used+2+runeLen(v) > budget {
			break
		}
		used += 2 + runeLen(v)
		taken = append(taken, v)
	}

	return strings.Join(append(taken, tail), "  ")
}
```

Add `"github.com/lmorchard/wideboi/internal/keys"` to `client.go`'s imports.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/client/ -count=1 -v -run TestControlHelp`
Expected: PASS. Confirm the 80-column line is 77 cells with detach and 67 without.

- [ ] **Step 5: Prove the budget test can fail**

Temporarily change `"hjkl move"` to `"h/l focus and j/k scroll history"` on all four bindings in `internal/keys/keys.go`.

Run: `go test ./internal/client/ -count=1 -run TestControlHelpFitsEveryEntryAt80Columns`
Expected: FAIL — the menu omits an entry at 79 cells. Restore and confirm green.

- [ ] **Step 6: Commit**

```bash
git add internal/client/client.go internal/client/help_test.go
git commit -m "feat(client): build the control-mode bar from the keys table"
```

---

### Task 4: Help overlay rendering

**Files:**
- Create: `internal/client/help.go`
- Test: `internal/client/help_overlay_test.go`

**Interfaces:**
- Consumes: `keys.Bindings`, `compose.WriteStyled`, `runeLen`, `truncateRunes`.
- Produces: `func helpLines(prefixLabel string, detachable bool) []string`; `func drawHelpOverlay(scr uv.Screen, cols, rows int, prefixLabel string, detachable bool)`.

- [ ] **Step 1: Write the failing test**

Create `internal/client/help_overlay_test.go`:

```go
package client

import (
	"image"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/keys"
)

// The overlay documents the table, so it must document all of it. A
// binding missing from here is a binding a user cannot discover.
func TestHelpLinesNameEveryBinding(t *testing.T) {
	joined := strings.Join(helpLines("C-b", true), "\n")
	for _, b := range keys.Bindings {
		if !strings.Contains(joined, b.Long) {
			t.Errorf("overlay omits the description of %q: %q", b.Key, b.Long)
		}
		if !strings.Contains(joined, b.Key) {
			t.Errorf("overlay omits the key %q", b.Key)
		}
	}
}

// Detach is hidden in-process for the same reason it is hidden in the
// bar: offering it would be advertising data loss.
func TestHelpLinesHideDetachInProcess(t *testing.T) {
	joined := strings.Join(helpLines("C-b", false), "\n")
	if strings.Contains(joined, "detach") {
		t.Errorf("in-process overlay mentions detach:\n%s", joined)
	}
}

// The hint has to name the prefix the user actually configured. A
// hardcoded C-b is wrong for anyone who set WIDEBOI_PREFIX, and they are
// exactly the people most likely to open help.
func TestHelpLinesNameTheConfiguredPrefix(t *testing.T) {
	joined := strings.Join(helpLines("C-a", true), "\n")
	if !strings.Contains(joined, "C-a") {
		t.Errorf("overlay does not name the configured prefix:\n%s", joined)
	}
	if strings.Contains(joined, "C-b") {
		t.Errorf("overlay hardcodes C-b:\n%s", joined)
	}
}

// make verify-exit runs wideboi at 4x2, 1x1 and 0x0. The last two are
// exactly where an unguarded centred-box origin goes negative, and a
// panic there would take out the teardown contract ptycheck.py asserts.
func TestHelpOverlaySurvivesTinyViewports(t *testing.T) {
	for _, size := range []struct{ cols, rows int }{
		{0, 0}, {1, 1}, {2, 1}, {4, 2}, {10, 3}, {-1, -1}, {80, 24},
	} {
		t.Run("", func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%dx%d panicked: %v", size.cols, size.rows, r)
				}
			}()
			w, h := max(size.cols, 0), max(size.rows, 0)
			buf := compose.NewSurface(max(w, 1), max(h, 1))
			drawHelpOverlay(buf, size.cols, size.rows, "C-b", true)
		})
	}
}

// At a usable size the box must actually land inside the viewport, with
// the last column left alone -- ultraviolet brackets a final-column
// write with autowrap toggles, which splits text across escapes on the
// wire.
func TestHelpOverlayStaysInsideTheViewport(t *testing.T) {
	const cols, rows = 80, 24
	buf := compose.NewSurface(cols, rows)
	drawHelpOverlay(buf, cols, rows, "C-b", true)

	painted := false
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			c := buf.CellAt(x, y)
			if c == nil || c.Content == "" || c.Content == " " {
				continue
			}
			painted = true
			if x >= cols-1 {
				t.Errorf("overlay wrote the last column at row %d", y)
			}
		}
	}
	if !painted {
		t.Error("overlay drew nothing at 80x24")
	}
	_ = image.Rect(0, 0, cols, rows)
}

var _ uv.Screen = compose.NewSurface(1, 1)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/client/ -count=1 -run TestHelp`
Expected: FAIL to compile — `undefined: helpLines`, `undefined: drawHelpOverlay`.

- [ ] **Step 3: Write the implementation**

Create `internal/client/help.go`:

```go
package client

import (
	"fmt"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/keys"
)

// helpLines builds the overlay's contents from the binding table, so it
// cannot drift from the bindings it documents.
//
// The prefix is passed in rather than hardcoded: WIDEBOI_PREFIX exists,
// and someone who changed it is exactly the person most likely to open
// this.
func helpLines(prefixLabel string, detachable bool) []string {
	lines := []string{"control mode", ""}

	for _, b := range keys.Bindings {
		if b.NeedsDetach && !detachable {
			continue
		}
		lines = append(lines, fmt.Sprintf("%-4s  %s", b.Key, b.Long))
	}

	lines = append(lines,
		"",
		"hold ctrl to stay in control mode:",
		fmt.Sprintf("%s ctrl+k ctrl+k k  scrolls up three times", prefixLabel),
		"",
		"any key closes this",
	)
	return lines
}

// drawHelpOverlay paints a bordered box centred on scr.
//
// Every dimension is clamped before it is used. make verify-exit runs
// wideboi at 4x2, 1x1 and 0x0, and a centred-box origin is exactly the
// arithmetic that goes negative there -- a panic in this function would
// take out the teardown contract scripts/ptycheck.py asserts, in a code
// path nobody was even looking at.
//
// The box never touches the last column: ultraviolet brackets a write
// there with autowrap toggles, which splits the text across escape
// sequences on the wire. See docs/LESSONS.md.
func drawHelpOverlay(scr uv.Screen, cols, rows int, prefixLabel string, detachable bool) {
	if cols <= 0 || rows <= 0 {
		return
	}

	lines := helpLines(prefixLabel, detachable)

	inner := 0
	for _, l := range lines {
		if n := runeLen(l); n > inner {
			inner = n
		}
	}

	// Two cells of border plus one of padding on each side.
	boxW := inner + 4
	boxH := len(lines) + 2

	if maxW := cols - 1; boxW > maxW {
		boxW = maxW
	}
	if boxH > rows {
		boxH = rows
	}
	// Below this there is no room for a border and any content at all,
	// and a partial box reads as corruption rather than as help.
	if boxW < 4 || boxH < 3 {
		return
	}

	x0 := (cols - boxW) / 2
	y0 := (rows - boxH) / 2
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}

	style := uv.Style{Attrs: uv.AttrReverse}
	contentW := boxW - 4

	top := "┌" + strings.Repeat("─", boxW-2) + "┐"
	bottom := "└" + strings.Repeat("─", boxW-2) + "┘"
	compose.WriteStyled(scr, x0, y0, top, style)
	compose.WriteStyled(scr, x0, y0+boxH-1, bottom, style)

	for i := 0; i < boxH-2; i++ {
		text := ""
		if i < len(lines) {
			text = truncateRunes(lines[i], contentW)
		}
		if pad := contentW - runeLen(text); pad > 0 {
			text += strings.Repeat(" ", pad)
		}
		compose.WriteStyled(scr, x0, y0+1+i, "│ "+text+" │", style)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/client/ -count=1 -v -run TestHelp`
Expected: PASS, all five.

- [ ] **Step 5: Prove the tiny-viewport guard can fail**

Temporarily delete the `if boxW < 4 || boxH < 3 { return }` guard.

Run: `go test ./internal/client/ -count=1 -run TestHelpOverlaySurvivesTinyViewports`
Expected: FAIL — a panic or an out-of-range write at 1x1 or 2x1. Restore and confirm green.

- [ ] **Step 6: Commit**

```bash
git add internal/client/help.go internal/client/help_overlay_test.go
git commit -m "feat(client): render the control-mode help overlay from the keys table"
```

---

### Task 5: Wire help mode end to end, and assert it on the wire

**Files:**
- Modify: `internal/client/client.go` (add `helpVisible`, `SetHelpVisible`, the `Draw` branch)
- Modify: `cmd/wideboi/main.go` (mirror `rt.help` in both event loops)
- Modify: `scripts/smoke.py`

**Interfaces:**
- Consumes: `drawHelpOverlay`, `router.help`.
- Produces: `func (c *Client) SetHelpVisible(on bool)`.

- [ ] **Step 1: Write the failing test**

In `internal/client/help_overlay_test.go`, add:

```go
// The overlay is modal, so it wins over a wipe: a wipe is decorative and
// a modal is not. Draw returns early while a wipe runs, so the overlay
// check has to sit above that branch, not below it.
func TestHelpVisibleGatesTheOverlay(t *testing.T) {
	c := &Client{cols: 80, rows: 24, prefixLabel: "C-b"}
	if c.helpVisible {
		t.Fatal("help should start hidden")
	}
	c.SetHelpVisible(true)
	if !c.helpVisible {
		t.Error("SetHelpVisible(true) did not take")
	}
	c.SetHelpVisible(false)
	if c.helpVisible {
		t.Error("SetHelpVisible(false) did not take")
	}
}
```

In `scripts/smoke.py`, replace `case_control_mode_names_every_verb_at_80_columns` with the cases below and register them in `CASES`:

```python
def case_control_mode_names_every_entry_at_80_columns(fail):
    # 80 is the commonest terminal width and cmd/wideboi's own fallback
    # when the host reports no size. h/j/k/l collapse into one "hjkl
    # move" entry precisely so the whole menu still fits here: the
    # descriptive form needs 81 cells against a budget of 79.
    s = Session(cols=80, rows=24)
    s.type("\x02")
    out = s.output()
    for verb in (b"hjkl move", b"n new", b"w width", b"x kill",
                 b"a attn", b"? help", b"q quit", b"esc exit"):
        if verb not in out:
            fail(f"at 80 columns control mode never shows {verb.decode()!r}")
    # In-process owns its panes, so detaching would kill them.
    # scripts/attachcheck.py asserts the socket case.
    if b"d detach" in out:
        fail("in-process control mode offers 'd detach', which would kill the panes")
    s.type("\x1b")
    s.quit_and_reap()


def case_ctrl_repeats_control_mode(fail):
    # The feature, typed as real bytes. A unit test that synthesises
    # KeyPressEvent{Code:'l', Mod:ModCtrl} proves the router, not the
    # binding: MatchString returns false indistinguishably for "not
    # pressed" and "name I can never produce".
    #
    # 0x02 = C-b, 0x0c = C-l, 0x6c = plain l. Two sticky rights and one
    # that exits, so focus lands on pane 3 out of 4 and the mode ends.
    s = Session(cols=100, rows=30)
    s.type("\x02n", settle=1.2)   # a third column
    s.type("\x02n", settle=1.2)   # a fourth
    s.type("\x02")                # enter control mode
    s.type("\x0c")                # C-l: focus right, stay
    s.type("\x0c")                # C-l: focus right, stay
    if cursor_visible(s.output()) is not False:
        fail("cursor is visible mid-repeat; control mode should hide it")
    s.type("l")                   # plain l: focus right, exit
    out = s.output()
    if focus_pane_id(out, s.rows) is None:
        fail("no focus reported at all")
    # Typing must now reach the pane, which is the proof the mode ended.
    s.type("echo ctrl-repeat-done\r")
    if b"ctrl-repeat-done" not in s.output():
        fail("still in control mode after an unmodified verb -- the mode did not exit")
    s.quit_and_reap()


def case_unmodified_verb_exits_control_mode(fail):
    s = Session()
    s.type("\x02")
    s.type("l")
    s.type("echo one-shot-done\r")
    if b"one-shot-done" not in s.output():
        fail("an unmodified verb did not leave control mode")
    s.quit_and_reap()


def case_unknown_key_exits_control_mode(fail):
    # A keystroke that did nothing must never strand you in a mode that
    # eats the next one.
    s = Session()
    s.type("\x02")
    s.type("z")          # not a verb
    s.type("echo unknown-exits\r")
    if b"unknown-exits" not in s.output():
        fail("an unknown key left the session stuck in control mode")
    s.quit_and_reap()


def case_help_overlay_opens_and_any_key_dismisses(fail):
    s = Session(cols=100, rows=30)
    s.type("\x02?", settle=1.0)
    out = s.output()
    if b"hold ctrl to stay in control mode" not in out:
        fail("? did not raise the help overlay")
    if b"scroll this pane's history up" not in out:
        fail("the overlay does not describe the bindings")
    before = len(s.output())
    # k dismisses, and must NOT also scroll.
    s.type("k", settle=1.0)
    after = s.output()[before:]
    if b"hold ctrl to stay" in after:
        fail("the overlay redrew after the dismissing key")
    # Dismissal returns to control mode, so esc is still needed to leave.
    s.type("\x1b", settle=0.5)
    s.type("echo help-dismissed\r")
    if b"help-dismissed" not in s.output():
        fail("could not get back to typing after dismissing help")
    s.quit_and_reap()
```

Register in `CASES`, replacing the old entry:

```python
    ("control mode names every entry at 80 columns", case_control_mode_names_every_entry_at_80_columns),
    ("ctrl repeats control mode", case_ctrl_repeats_control_mode),
    ("unmodified verb exits control mode", case_unmodified_verb_exits_control_mode),
    ("unknown key exits control mode", case_unknown_key_exits_control_mode),
    ("help overlay opens and any key dismisses", case_help_overlay_opens_and_any_key_dismisses),
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/client/ -count=1 -run TestHelpVisible`
Expected: FAIL to compile — `c.helpVisible undefined`.

Run: `make build && python3 scripts/smoke.py --only "help overlay"`
Expected: FAIL — "? did not raise the help overlay".

- [ ] **Step 3: Write the implementation**

In `internal/client/client.go`, add the field to `Client` beside `controlMode`:

```go
	helpVisible  bool
```

Add the setter beside `SetControlMode`:

```go
// SetHelpVisible raises or clears the control-mode help overlay. Called
// by cmd/wideboi after every key from the router's own help flag, the
// same mirroring SetControlMode uses, so the overlay can never disagree
// with the router about whether it is up.
func (c *Client) SetHelpVisible(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.helpVisible = on
}
```

In `Draw`, insert this as the **first** thing after the mutex is taken, above the `activeWipe` branch:

```go
	// Above the wipe branch deliberately. A wipe is decorative and a
	// modal is not, so the overlay wins; below it, help raised during a
	// transition would not appear until the wipe finished.
	if c.helpVisible {
		drawHelpOverlay(scr, c.cols, c.rows, c.prefixLabel, c.detachable)
		scr.HideCursor()
		return
	}
```

In `cmd/wideboi/main.go`, in **both** event loops (`runAttach` around line 183 and `run` around line 317), add the help mirror immediately after the existing `cli.SetControlMode(rt.control)`:

```go
				cli.SetControlMode(rt.control)
				cli.SetHelpVisible(rt.help)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -count=1`
Expected: PASS.

Run: `make build && python3 scripts/smoke.py`
Expected: all cases OK, including the five new ones.

- [ ] **Step 5: Prove the overlay-wins-over-wipe ordering can fail**

Move the `c.helpVisible` block in `Draw` to just below the `activeWipe` branch.

Run: `python3 scripts/smoke.py --only "help overlay"`
Expected: this may still pass, because no wipe is active in that case. That is the point — **add a case that would catch it** by raising help immediately after a focus switch, which starts a wipe:

```python
def case_help_overlay_wins_over_a_wipe(fail):
    # A focus switch starts an 8-frame wipe. Help raised during it must
    # appear immediately, not after the transition finishes.
    s = Session(cols=100, rows=30)
    s.type("\x02")
    os.write(s.fd, b"l")     # focus right: starts a wipe
    os.write(s.fd, b"\x02?")  # and immediately raise help
    time.sleep(1.0)
    if b"hold ctrl to stay in control mode" not in s.output():
        fail("help raised during a wipe never appeared")
    s.type("k", settle=0.5)
    s.type("\x1b", settle=0.5)
    s.quit_and_reap()
```

Register it, run with the block in the wrong place and watch it fail, then restore the correct ordering and confirm green.

- [ ] **Step 6: Run the full gate**

Run: `make check`
Expected: exit 0. The golden snapshot captures startup, where the normal-mode status line is unchanged — if `scripts/golden.py` reports a diff, that is a finding to investigate, not a file to regenerate.

- [ ] **Step 7: Commit**

```bash
git add internal/client/ cmd/wideboi/main.go scripts/smoke.py
git commit -m "feat(client): wire the help overlay through control mode"
```

---

### Task 6: Update the docs the change invalidates

**Files:**
- Modify: `docs/LESSONS.md`
- Modify: `README.md`

- [ ] **Step 1: Check what the change invalidates**

Run: `grep -rn "u scroll\|j jump\|alt+\|d detach\|control mode" README.md docs/LESSONS.md`

Every hit naming an old binding is stale. `u` no longer does anything and `j` changed meaning.

- [ ] **Step 2: Update the README's key table**

Replace the control-mode verb list with the table from the spec's "New bindings" section, and add one sentence: hold Ctrl on a verb to stay in control mode, press it unmodified to act and leave, `?` for the full list.

- [ ] **Step 3: Add the LESSONS entry**

Append to `docs/LESSONS.md`, before the "Never write the terminal's last column" section:

```markdown
## `ctrl+h` is not Backspace, and three other letters are not themselves

Plan 15 nearly died on an assumption. Binding every verb to a ctrl form
looked impossible because `ctrl+h` is ASCII 0x08, which is BS — so the
focus-left key, the one you most want to repeat, would collide with
Backspace.

It does not. Ultraviolet maps 0x08 to `ctrl+h` unconditionally
(`decoder.go`'s `parseControl`), and the Backspace key sends 0x7F. All
eleven verb letters have working ctrl forms.

What *is* taken, and permanently: `ctrl+i` decodes as `tab`, `ctrl+m` as
`enter`, `ctrl+[` as `escape`. Those three letters can never carry a
binding with a repeat form. `internal/keys` enforces it, because the
failure mode is a key that silently does nothing when Ctrl is held.

The general lesson is the repo's existing one, applied again: **probe the
pinned dependency, do not reason from the ASCII table.** Two minutes with
`uv.EventDecoder` answered what an afternoon of arguing would not have.
```

- [ ] **Step 4: Verify and commit**

Run: `make check`
Expected: exit 0.

```bash
git add README.md docs/LESSONS.md
git commit -m "docs: update bindings for Plan 15 and record the ctrl+h finding"
```

---

## Self-review notes

**Spec coverage.** Input model → Task 2. Verb table and `internal/keys` → Task 1. Help overlay → Tasks 4 and 5. Bar relabeling → Task 3. Reserved letters → Task 1's `TestNoBindingUsesAReservedLetter`. Tiny-viewport safety → Task 4. Wire-level assertions → Task 5. Migration hazards (`j` changes meaning, `u` stops working) → Task 6's README update. Out-of-scope items (config remapping, smart-jump not cycling, scroll granularity) intentionally have no task.

**One thing the plan adds that the spec did not name:** Task 5 Step 5 discovered that the obvious smoke case cannot catch a wrong `Draw` ordering, so it adds `case_help_overlay_wins_over_a_wipe`. That is the repo's "prove every test can fail" rule doing its job at plan time rather than review time.

**Known interface consistency check.** `controlHelp(budget int, detachable bool)` keeps the signature Task 3 inherits from the current code. `keys.BarItems` returns `(droppable, essential []string)` and is called that way in Tasks 1, 3. `drawHelpOverlay(scr uv.Screen, cols, rows int, prefixLabel string, detachable bool)` is defined in Task 4 and called with exactly those arguments in Task 5. `Client.detachable` already exists from the previous branch; Task 5's `Draw` branch depends on it.
