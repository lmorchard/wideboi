# Jump-to-column, last-pane toggle, and card reordering Implementation Plan

**Goal:** `y`/`u` reorder the focused column, `tab` flips to the previous pane,
`1`–`9`/`0` focus a column by position, headers show positions.

**Approach:** Reorder and last-pane are new `layout.Strip` primitives behind three
appended `VerbType`s. Digit jumps resolve on the client and reuse
`MsgFocusPane`. All bindings live in `internal/keys`; the help overlay gains a
`HelpGroup` so related keys share a line.

**Tech stack:** Go, ultraviolet key events, pty smoke harness (`scripts/smoke.py`).

**Spec amendment (Phase 4, approved by Les 2026-09-22):** the help overlay is exactly 24 rows
at 80x24 today (2 header + 15 bindings + 5 footer lines + 2 border). This work
adds 4 lines, so it would clip. Phase 4 uses the new `HelpGroup` to also collapse
`h/l`, `j/k` and `o/p` (and `y/u`), which brings it back to 24. It also pins
"the overlay fits at 80x24" with a test. The spec's "not fixing overlay overflow"
was written before the count showed we would *cause* the overflow.

Commit per phase: `Phase N: <name>`. Git signing needs
`SSH_AUTH_SOCK=/Users/lmorchard/.bitwarden-ssh-agent.sock`.

---

## Phase 1: Reorder the focused column (`y` / `u`) — #74

End to end: a strip primitive, two verbs, two bindings with ctrl repeat, and a
smoke case.

**Files:**
- Modify: `internal/layout/layout.go`: add `MoveLeft`, `MoveRight`
- Test: `internal/layout/layout_test.go`
- Modify: `internal/protocol/messages.go`: append `VerbMoveLeft`, `VerbMoveRight` (after `VerbShrinkWidth`; wire values, append only)
- Modify: `internal/server/server.go`: two verb cases, **no** `resizePanesLocked`
- Test: `internal/server/reorder_test.go` (new, package `server`)
- Modify: `internal/keys/keys.go`: constants `ActionNameMoveLeft = "move_left"`, `ActionNameMoveRight = "move_right"`; two `Bindings` rows after `p`; add both to `validActions` and to the unknown-action error text
- Modify: `scripts/smoke.py`: `case_move_column_reorders` + a `CASES` entry
- Modify: `config.example.toml`: two lines under "Pane & Column Management"

**Key changes:**

```go
// MoveLeft swaps the focused column with its left neighbour. Focus
// follows the column, and widths travel with it: nothing is resized,
// because a pane's logical width is its column's width (the no-shrink
// premise), and a move changes neither.
func (s *Strip) MoveLeft() {
	i := s.focusIndex
	if i <= 0 || i >= len(s.columns) {
		return
	}
	s.columns[i-1], s.columns[i] = s.columns[i], s.columns[i-1]
	s.focusIndex = i - 1
}

// MoveRight is MoveLeft's mirror.
func (s *Strip) MoveRight() {
	i := s.focusIndex
	if i < 0 || i >= len(s.columns)-1 {
		return
	}
	s.columns[i+1], s.columns[i] = s.columns[i], s.columns[i+1]
	s.focusIndex = i + 1
}
```

Server:
```go
case protocol.VerbMoveLeft:
	s.strip.MoveLeft()
case protocol.VerbMoveRight:
	s.strip.MoveRight()
	// No resizePanesLocked: see VerbToggleCards. Order is presentation.
```

Bindings (help-overlay only, no `BarGroup`):
```go
{ActionName: ActionNameMoveLeft, Key: "y", Action: ActionVerb, Verb: protocol.VerbMoveLeft,
	Long: "move this column left"},
{ActionName: ActionNameMoveRight, Key: "u", Action: ActionVerb, Verb: protocol.VerbMoveRight,
	Long: "move this column right"},
```

Tests (write first, watch fail):
- `TestMoveLeftAndRightSwapWithNeighbourAndKeepFocus` (layout): columns 1,2,3 with widths 40,50,60, focus on 3. `MoveLeft` gives `PaneIDs()==[1,3,2]`, focus 3, and `ColumnWidth(3)==60`. `MoveLeft` again gives `[3,1,2]`. A third `MoveLeft` is a no-op at the edge. `MoveRight` ×3 gives `[1,2,3]` and then a no-op. Empty strip: both are no-ops, no panic.
- `TestMoveVerbsReorderWithoutResizing` (server, via `serverWithStatuses` with 3 idle panes). Take `order := s.strip.PaneIDs()` and focus `order[2]`. Send `MsgVerb{VerbMoveLeft}` and assert `PaneIDs()` is `[order[0], order[2], order[1]]`, focus unchanged, and every pane's `cols` still 40. Send `VerbMoveRight` and assert the original order.
- Router and keys coverage is automatic: `TestControlModeTable`, `TestEveryCtrlFormMatchesItsRealByte` and `TestEveryPlainFormMatchesItself` enumerate `Bindings`. Confirm the new rows show up in `-v` output.
- `TestBuildBindingsCustomValid`-style check: `BuildBindings(map[string]string{"move_left": "e"})` succeeds and the row's Key is `e`.

Smoke (`case_move_column_reorders`), asserted through `focus_pane_id`, never echoed text:
```python
# Startup is columns [1, 2], focus 1. Two new columns make [1, 3, 4, 2]
# with focus on 4. C-y y moves 4 left twice -> [4, 1, 3, 2], focus
# still 4 (focus follows the card). The focus check alone passes after
# zero, one or two moves, which is why phase 3 extends this case with a
# position jump that tells all three apart.
s = Session(cols=100, rows=30)
s.type("\x02n", settle=1.2)
s.type("\x02n", settle=1.2)
s.type("\x02\x19y")               # C-b C-y y
if focus_pane_id(s.output(), s.rows) != 4:
    fail(...)
s.close()
```
For this phase, the smoke case proves only that focus follows the card. Phase 3
appends `\x02` + `2` and asserts pane 1 sits at position 2, which proves the
order changed.

**Verification — automated:**
- [x] New layout and server tests fail before the implementation — **no-op stubs: `step 0: PaneIDs() = [1 2 3], want [1 3 2]`; server `order = [1 2 3], want [1 3 2]`**
- [x] `make quick` passes — **green**
- [x] `go test -count=1 ./internal/layout ./internal/server ./internal/keys ./cmd/wideboi` passes — **green; TestControlModeTable has y/u plain+ctrl subtests**
- [x] `make smoke` passes, including the new case — **34 passed, 0 failed; golden matches**
- [x] Adaptation: `u` collided with the `scroll_up = "u"` remap in README, smoke config case, and keys/client/config tests; all moved to `e` (compat note for PR)

**Verification — manual:**
- [ ] In `./bin/wideboi`, open 3 columns, then `C-b C-y y` moves the focused card two places left; widths unchanged; works in both scroll and card layout

---

## Phase 2: Flip to the previous pane (`tab`) — #80

**Files:**
- Modify: `internal/layout/layout.go`: `lastFocusPaneID` field, `noteFocusFrom`, `FocusLast`, `LastFocusPaneID`; hook FocusLeft/FocusRight/AddColumn/FocusPaneID; KillPane clears
- Test: `internal/layout/layout_test.go`
- Modify: `internal/protocol/messages.go`: append `VerbFocusLast` after `VerbMoveRight`
- Modify: `internal/server/server.go`: `case protocol.VerbFocusLast: s.strip.FocusLast()`
- Test: `internal/server/reorder_test.go` (same new file; rename to `verbs_test.go` if it reads better)
- Modify: `internal/keys/keys.go`: `ActionNameFocusLast = "focus_last"`, binding row, `validActions`, error text
- Modify: `internal/keys/keys_test.go`: `nameToBytes` learns `"tab"` → `0x09`
- Modify: `cmd/wideboi/router_test.go`: `keyNamed` learns `"tab"` → `uv.KeyPressEvent{Code: uv.KeyTab}`
- Modify: `scripts/smoke.py`: `case_digit_and_last_pane_jumps` (the tab part now; phase 3 adds digits)
- Modify: `config.example.toml`

**Key changes:**

```go
// noteFocusFrom records prev as the last-focused pane if focus has
// actually moved off it. Every focus mutation calls it, so tab covers
// keys, clicks, attention jumps and digit jumps alike -- all of them
// end in one of these methods.
func (s *Strip) noteFocusFrom(prev int) {
	if prev != 0 && prev != s.FocusedPaneID() {
		s.lastFocusPaneID = prev
	}
}

// FocusLast focuses the previously focused pane, which makes the pane
// being left the new previous one: pressing it twice returns you.
func (s *Strip) FocusLast() {
	if s.lastFocusPaneID != 0 {
		s.FocusPaneID(s.lastFocusPaneID)
	}
}

func (s *Strip) LastFocusPaneID() int { return s.lastFocusPaneID }
```
Each of `FocusLeft`, `FocusRight`, `AddColumn` and `FocusPaneID` begins with
`prev := s.FocusedPaneID()` and ends with `s.noteFocusFrom(prev)`. `FocusPaneID`
with an unknown ID changes nothing, so it records nothing. `KillPane` adds
`if s.lastFocusPaneID == paneID { s.lastFocusPaneID = 0 }` and does not record the
focus shift that the removal causes. `MoveLeft`/`MoveRight` do not record,
because the focused pane is the same.

Binding (help-only; `tab` has no ctrl form, since `CtrlForm` needs a single letter):
```go
{ActionName: ActionNameFocusLast, Key: "tab", Action: ActionVerb, Verb: protocol.VerbFocusLast,
	Long: "focus the previously focused pane"},
```
`tab` is already in `validNamedKeys`.

Tests (first, and watch them fail):
- `TestFocusLastTogglesBetweenTwoPanes` (layout): add 1,2,3 (focus 3). `FocusPaneID(1)`, then `FocusLast` gives 3, and `FocusLast` again gives 1.
- `TestFocusLastTracksEveryFocusMove` (layout): table over the mutators. Each subtest starts from 1,2,3 focused on 2 and applies one of FocusLeft, FocusRight, FocusPaneID(3) or AddColumn(4). Afterwards `LastFocusPaneID()==2`.
- `TestFocusLastIgnoresNoOpsAndMoves` (layout): FocusLeft at the left edge, FocusPaneID(same) and FocusPaneID(99) leave `LastFocusPaneID` unchanged. `MoveLeft` too.
- `TestKillingLastFocusedPaneForgetsIt` (layout): focus 1→3, kill 1, then `LastFocusPaneID()==0` and `FocusLast` is a no-op.
- `TestFocusLastVerbFlipsBack` (server): `MsgFocusPane` to `order[0]` and then to `order[2]`, send `VerbFocusLast`, and focus is `order[0]`.

Smoke (`case_digit_and_last_pane_jumps`, tab part):
```python
# Startup: [1, 2], focus 1. C-b l -> 2. C-b tab -> 1: flips back, and
# only wideboi's status line can say so.
s = Session()
s.type("\x02l")
if focus_pane_id(s.output(), s.rows) != 2: fail(...)
s.type("\x02\t")
if focus_pane_id(s.output(), s.rows) != 1: fail(...)
s.close()
```

**Verification — automated:**
- [x] New tests fail first for the stated reason — **stubbed: `after FocusLast: focus = 1, want 3`, `LastFocusPaneID() = 0, want 2`, server `= 3, want 1`. Kill test passes vs stub by nature; proved by deleting the clear: `= 1, want 0`**
- [x] `make quick` passes — **green**
- [x] `make smoke` passes — **35 passed; sabotaged FocusLast → `FAIL digit and last-pane jumps`, restored+rebuilt**

**Verification — manual:**
- [ ] `C-b tab` bounces between two panes; clicking a pane then `C-b tab` returns to the pane you clicked away from

---

## Phase 3: Digit jumps and header positions — #80

**Files:**
- Modify: `internal/keys/keys.go`: `ActionFocusColumn` (appended to `Action`), `Column int` field, `LastColumn = -1`, `HelpGroup`/`HelpKey` fields, ten digit rows (not in `validActions`, so they cannot be remapped)
- Modify: `internal/keys/keys_test.go`: `TestTableIsWellFormed` accepts `ActionFocusColumn` with `Column` in 1..9 or `LastColumn`; `nameToBytes` learns digits
- Modify: `cmd/wideboi/router.go`: `routeFocusColumn`, a `Column int` on `route`, a `fire` case
- Modify: `cmd/wideboi/router_test.go`: `assertAction` case
- Modify: `cmd/wideboi/main.go`: `case routeFocusColumn: cli.FocusColumn(ctx, act.Column)`
- Modify: `internal/client/client.go`: `FocusColumn`; a `positions map[int]int` in `frameState`, filled in `frameStateLocked`; the header prints the position
- Modify: `internal/client/help.go`: `helpLines` collapses by `HelpGroup`
- Test: `internal/client/focus_column_test.go` (new), `internal/client/help_test.go`, and a header test in `internal/client/cards_test.go` or a new file
- Modify: `scripts/smoke.py`: extend `case_digit_and_last_pane_jumps` and `case_move_column_reorders`

**Key changes:**

keys.go:
```go
// ActionFocusColumn focuses the Column'th column from the left.
ActionFocusColumn

// LastColumn is Column's value for "the rightmost column, however many".
const LastColumn = -1

// in Binding:
// Column is the 1-based position ActionFocusColumn focuses, or LastColumn.
Column int
// HelpGroup collapses bindings into one help-overlay line, the way
// BarGroup does for the status bar. It is also that line's text.
HelpGroup string
// HelpKey overrides the key label on a HelpGroup's line. Without it the
// label is the members' keys joined with "/", which stays right when
// a user remaps one of them.
HelpKey string
```
Digit rows, generated rather than written out ten times, and appended after `tab`:
```go
func digitBindings() []Binding {
	out := make([]Binding, 0, 10)
	for n := 1; n <= 9; n++ {
		out = append(out, Binding{ActionName: fmt.Sprintf("focus_column_%d", n),
			Key: strconv.Itoa(n), Action: ActionFocusColumn, Column: n,
			Long: fmt.Sprintf("focus column %d", n),
			HelpGroup: "focus a column by position, 0 the last", HelpKey: "0-9"})
	}
	return append(out, Binding{ActionName: "focus_column_last", Key: "0",
		Action: ActionFocusColumn, Column: LastColumn, Long: "focus the last column",
		HelpGroup: "focus a column by position, 0 the last", HelpKey: "0-9"})
}
```
`Bindings` is a `var`, so `var Bindings = append([]Binding{ ...existing rows..., tab row }, digitBindings()...)`
keeps q/esc where they are. Where the digits sit only affects the bar (no
`BarGroup`, so none) and the overlay order.

`BuildBindings`: the digit ActionNames are not in `validActions`, so a user
cannot remap them. Step 3's collision check still sees them, so `kill_pane = "1"`
errors. Add a test for that.

router.go:
```go
routeFocusColumn // in routeKind
Column int       // in route
case keys.ActionFocusColumn:
	return route{Kind: routeFocusColumn, Column: b.Column}
```
Digits have no ctrl form, so `sticky` is always false here, and the jump leaves control mode.

client.go:
```go
// FocusColumn focuses the n'th column from the left (keys.LastColumn
// for the rightmost), resolved against this client's strip and sent as
// the same MsgFocusPane a click sends. Out of range is a no-op.
func (c *Client) FocusColumn(ctx context.Context, n int) {
	c.mu.Lock()
	ids := c.strip.PaneIDs()
	c.mu.Unlock()
	i := n - 1
	if n == keys.LastColumn {
		i = len(ids) - 1
	}
	if i < 0 || i >= len(ids) {
		return
	}
	c.transport.SendClient(ctx, protocol.MsgFocusPane{PaneID: ids[i]})
}
```
Header: in `frameStateLocked`, build `positions` from `c.strip.PaneIDs()` as
`id → i+1`. In `composeFrameLocked`, the prefix becomes
`fmt.Sprintf(" %d [%d]", pos, p.PaneID)` when `st.positions[p.PaneID] > 0`,
and otherwise stays ` [%d]` (tests that build `frameState` literals keep working).

help.go `helpLines`:
```go
done := map[string]bool{}
for _, b := range bindings {
	if b.NeedsDetach && !detachable { continue }
	if b.HelpGroup == "" {
		lines = append(lines, fmt.Sprintf("%-4s  %s", b.Key, b.Long))
		continue
	}
	if done[b.HelpGroup] { continue }
	done[b.HelpGroup] = true
	label := b.HelpKey
	if label == "" { label = strings.Join(groupKeys(bindings, b.HelpGroup), "/") }
	lines = append(lines, fmt.Sprintf("%-4s  %s", label, b.HelpGroup))
}
```
(`groupKeys` collects the `Key` of every binding in the group, in table order.)

Tests (first):
- `TestFocusColumnSendsPaneAtPosition` (client, `newMouseClient`-style fixture with 3 columns ordered `[5, 2, 9]`): `FocusColumn(1)` sends `MsgFocusPane{5}`, `FocusColumn(3)` sends `{9}`, `FocusColumn(keys.LastColumn)` sends `{9}`, and `FocusColumn(4)` and `FocusColumn(0)` send nothing. Use the existing `sent`/`focusRequests` helpers from `mouse_test.go`.
- `TestHeaderShowsColumnPosition` (client): a snapshot with columns `[5, 2]` and a `Draw` into `newFakeHostScreen`. The header over pane 5 contains `" 1 [5]"` and the one over pane 2 contains `" 2 [2]"`. That the number is a position and not the ID is what `[5, 2]` proves, so the IDs are chosen not to match.
- `TestHelpGroupsCollapseToOneLine` (client/help_test.go): `helpLines("C-b", true)` has exactly one line starting `0-9` and no line starting with `1 ` … `9 `.
- `TestHelpGroupLabelFollowsRemap`: build a set with `HelpGroup` on two rows, remap one via `BuildBindings`, and the label reflects the new key. Phase 4 needs this label path, so the test lives here with the mechanism. Use a test-local table, since no default rows use a joined label until phase 4.
- `TestBuildBindingsRejectsRemapOntoADigit`: `BuildBindings({"kill_pane": "1"})` errors with "duplicate key".
- `TestTableIsWellFormed` still requires `Long` on every row. Digits have it.

Smoke, both extended:
```python
# case_digit_and_last_pane_jumps, continued: [1, 2]
s.type("\x02" "0")   # last column -> 2
# assert 2
s.type("\x02" "1")   # first column -> 1
# assert 1
# case_move_column_reorders, continued after C-y y ([4, 1, 3, 2], focus 4):
s.type("\x02" "2")   # position 2: pane 1 after two moves,
# assert focus_pane_id == 1   (one move [1,4,3,2] gives 4; none [1,3,4,2] gives 3)
```
The position-2 check tells all three orders apart, so it goes red if either the
repeat (`C-y`) or the plain move did not fire. To prove it can fail, break `MoveLeft` to a no-op, then
rebuild and rerun smoke in the same command, then restore and rebuild
(LESSONS: "a red check leaves a broken binary behind").

**Verification — automated:**
- [x] New tests fail first for the stated reason — **with scaffolding: `digit bindings = map[]`, `FocusColumn(1) sent [], want [5]`, header `" [5] ★"` want prefix `" 1 [5]"`, `found 0 "0-9" lines`, remap onto `1` `err = <nil>`**
- [x] Adaptation: two existing tests encoded now-changed premises. `TestUnknownKeysExitControlModeWithAnyModifier` used `5` as an unbound key → `g`. `TestHelpLinesNameEveryBinding` now requires a grouped binding's exact group line (label names the key, or HelpKey covers it) instead of its own Long
- [x] Smoke reorder case goes red with `MoveLeft` sabotaged — **`FAIL move column reorders`; also FocusColumn sabotaged → both new cases FAIL; restored+rebuilt**
- [x] `make quick` passes — **green**
- [x] `make smoke` passes — **35 passed, golden matches**

**Verification — manual:**
- [ ] Headers read ` 1 [3] …`, ` 2 [7] …`; after `C-b y` the positions renumber
- [ ] `C-b 3` focuses the third column; `C-b 0` the last; `C-b 9` with 3 columns does nothing and leaves control mode

---

## Phase 4: Help overlay fits again, docs (spec amendment)

**Files:**
- Modify: `internal/keys/keys.go`: set `HelpGroup` on h/l (`"focus the column left / right"`), j/k (`"scroll this pane's history down / up"`), o/p (`"shrink / grow this column's width"`) and y/u (`"move this column left / right"`)
- Test: `internal/client/help_overlay_test.go`: `TestHelpOverlayFitsAt80x24`
- Modify: `README.md`: key table rows `1`–`9` / `0`, `tab`, `y` / `u`, and a line saying the header number is the column's position
- Modify: `config.example.toml`: verify the three new actions are listed (phases 1 and 2 add them)

**Key changes:**

```go
// The overlay is the only place overlay-only bindings are discoverable,
// so it must not clip on the most common small terminal. It sat at
// exactly 24 rows before #80/#74 added four lines; grouping pairs is
// what made room.
func TestHelpOverlayFitsAt80x24(t *testing.T) {
	for _, detachable := range []bool{true, false} {
		lines := helpLines("C-b", detachable)
		if h := len(lines) + 2; h > 24 { // + top and bottom border
			t.Errorf("detachable=%v: overlay is %d rows, 24 available", detachable, h)
		}
		for _, l := range lines {
			if w := runeLen(l) + 4; w > 79 {
				t.Errorf("line %q is %d wide, 79 available", l, w)
			}
		}
	}
}
```
Write it before the grouping. With phases 1–3 in, it should fail at 26 rows (it
would be 28 without the digit group), then pass at 24 once the groups land.
Record the actual numbers.

**Verification — automated:**
- [x] `TestHelpOverlayFitsAt80x24` red before grouping, green after — **red at 28 rows (27 in-process), green at 24**
- [!] The plan predicted 26 rows before grouping — **DOES NOT HOLD**; I counted lines, not lines + border. Actual 28. Grouping four pairs still lands exactly on 24, so the design holds with zero slack
- [x] `make check` passes ×4 — **4/4 PASS after the smoke fix below** (the gate; memory says parallel check has a ~1-in-6 load flake on main, so compare failures against a main baseline before blaming the branch)
- [x] `case_help_overlay_opens_and_any_key_dismisses` still passes — **failed 4/4 first: asserted `scroll this pane's history up`, now grouped. Assertion moved to `scroll this pane's history down / up`, same strength** (it may assert on overlay text; adjust only if it asserted a now-grouped line, and say so)

**Verification — manual:**
- [ ] At 80x24, `C-b ?` shows the whole overlay including "any key closes this"
- [x] With `[keys] focus_left = "e"`, the overlay line reads `e/l  focus the column left / right` — **automated: `TestRemappedPairKeepsItsGroupLabel`**
