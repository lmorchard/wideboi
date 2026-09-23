# Client-owned layout mode Implementation Plan

**Goal:** Layout mode becomes a per-client choice that starts from the client's own config on every attach, and the server's layout state and placement computation go away (#92, #47). Each client shows its mode on the status line (#91).

**Approach:**
- Phase 1 moves ownership to the client and ships every user-visible behaviour: a local toggle, the config-driven start, and independence between clients. The server's state is left in place but becomes unused, so this phase is shippable on its own.
- Phase 2 deletes the server side and the two snapshot fields.
- Phase 3 is the docs.
- Phase 4 adds the status-line layout tag (#91).
- `VerbToggleCards` stays as a reserved enum slot.

**Tech stack:** Go, gob wire, the Python pty harnesses (`scripts/smoke.py`, `scripts/attachcheck.py`).

Conventions for every phase:
- `make quick` is the edit loop.
- `make check` is the gate. Per memory it has a load flake of about 1 in 6 on `main`, so rerun ×4 and compare against a `main` baseline before blaming the branch.
- Run ad-hoc Go tests with `-count=1`.
- Break the guarded code, rebuild, and watch each new test go red **in one command**, then restore and rebuild in that same command (LESSONS, "A red check leaves a broken binary behind").

---

## Phase 1: The client owns its layout mode

After this phase:
- `C-b c` flips only the client it was pressed in, and sends nothing.
- Each attach starts from that client's `--layout` / `WIDEBOI_LAYOUT` / TOML / `cards`.
- The client ignores `MsgLayoutSnapshot.Layout`.
- The server still computes and sends `Layout` and `Placements`, but no client reads them.

**Files:**
- Modify `internal/client/client.go`:
  - add `SetLayoutMode`, `ToggleLayout` and `setLayoutModeLocked`;
  - make `HandleServerMsg` stop reading `m.Layout`;
  - prune mirrors and `mouseTracking` by `m.Columns` instead of by placements.
- Modify `internal/keys/keys.go`: add `ActionToggleLayout` and change the `toggle_cards` row to use it.
- Modify `cmd/wideboi/router.go`: add `routeToggleLayout` and handle it in `fire`.
- Modify `cmd/wideboi/main.go`:
  - `runClient` calls `cli.SetLayoutMode(cfg.LayoutMode)` after `NewClient`;
  - dispatch `routeToggleLayout` to `cli.ToggleLayout()`.
- Test: new `internal/client/layout_mode_test.go`.
- Test: `internal/client/cards_test.go`, `motion_test.go`, `mouse_test.go`, `screen_test.go`, `focus_column_test.go`, `client_test.go`. Wherever a snapshot sets `Layout: protocol.LayoutCards/Scroll`, move that to `cli.SetLayoutMode(...)` before the first snapshot and drop the field.
- Test: `internal/keys/keys_test.go` (`TestToggleCardsIsOnC`) and `cmd/wideboi/router_test.go` (`assertAction`).
- Test: `scripts/attachcheck.py`, three new cases plus a `Client(args=...)` parameter.

**Key changes:**

`internal/client/client.go`:

```go
// SetLayoutMode installs this client's layout. Layout is presentation,
// and presentation is per-client (#92): the server never learns it, so
// cmd/wideboi calls this once, from the client's own config, before the
// first snapshot arrives.
func (c *Client) SetLayoutMode(mode protocol.LayoutMode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setLayoutModeLocked(mode)
}

// ToggleLayout flips between the card fan and the scrolling strip.
// Nothing is sent: other clients keep their own mode, and this one is
// forgotten on detach. It animates like any other change of geometry.
func (c *Client) ToggleLayout() {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := protocol.LayoutCards
	if c.layoutMode == protocol.LayoutCards {
		next = protocol.LayoutScroll
	}
	// Same two "previous" values HandleServerMsg keeps, for the same
	// reasons: prevTarget decides whether anything moved, prevOnScreen
	// is where the animation starts.
	prevTarget := c.placements
	prevOnScreen := c.currentPlacementsLocked()
	c.setLayoutModeLocked(next)
	if c.sel != nil && !c.selectionStillPlacedLocked() {
		c.sel = nil
	}
	if c.focusPaneID != 0 && !placementsEqual(prevTarget, c.placements) {
		c.motion = &motion{from: prevOnScreen, to: c.placements, total: motionFrames}
	}
}

// setLayoutModeLocked installs mode and recomputes placements from the
// strip as it stands. With no columns yet, ComputePlacements returns
// nil, which is right before the first snapshot. c.mu must be held.
func (c *Client) setLayoutModeLocked(mode protocol.LayoutMode) {
	c.layoutMode = mode
	layout.ApplyMode(c.strip, mode)
	c.placements = layout.ToProtocol(c.strip.ComputePlacements(c.cols, c.rows))
}
```

In `HandleServerMsg`'s snapshot case:
- Delete `c.layoutMode = m.Layout` and `layout.ApplyMode(c.strip, m.Layout)`, and rewrite the comment above them: only the strip sync remains unconditional.
- Replace the prune loop's key set:

```go
		// Prune against the panes that exist, not the ones placed. A
		// local ToggleLayout changes placements with no snapshot and
		// no resend, so a mirror pruned for being off-screen would come
		// back blank until its pane next changed (#92).
		live := make(map[int]bool, len(m.Columns))
		for _, col := range m.Columns {
			live[col.PaneID] = true
		}
		for id := range c.mirrors {
			if !live[id] {
				delete(c.mirrors, id)
			}
		}
		for id := range c.mouseTracking {
			if !live[id] {
				delete(c.mouseTracking, id)
			}
		}
```

- The allocation loop over `c.placements` (which fills `activeIDs`) stays as it is, minus `activeIDs`.

`internal/keys/keys.go`:
- Append to the `Action` enum. This is local only and never crosses the wire, but appending keeps the diff minimal:

```go
	// ActionToggleLayout flips this client between the card fan and the
	// scrolling strip. Client-local: layout is presentation (#92).
	ActionToggleLayout
```

- The `toggle_cards` row becomes `Action: ActionToggleLayout`, with no `Verb`. Keep `Key: "c"`, `NoRepeat: true`, the `Long` text and its comment. `ActionNameToggleCards` is unchanged, so `[keys] toggle_cards = ...` keeps working.
- Update the comment on `type Action` so the list of non-verb actions includes the layout toggle.

`cmd/wideboi/router.go`:

```go
	// routeToggleLayout flips this client's layout. The client does it
	// alone; nothing is sent (#92).
	routeToggleLayout
```

In `fire`:

```go
	case keys.ActionToggleLayout:
		// NoRepeat, so sticky is always false: toggles once and leaves,
		// as it did when it was a verb.
		return route{Kind: routeToggleLayout}
```

`cmd/wideboi/main.go` `runClient`:

```go
	cli := client.NewClient(cConn, width, height, cfg.PrefixLabel)
	cli.SetLayoutMode(cfg.LayoutMode)
```

In the key dispatch:

```go
				case routeToggleLayout:
					cli.ToggleLayout()
```

**Tests (write first, watch them fail):**

`internal/client/layout_mode_test.go`:
- `TestToggleLayoutSendsNothing`. Build the client on `transport.NewInProcChannel(16)` with `SetLayoutMode(LayoutScroll)` and a three-column snapshot, then `ToggleLayout()`. Assert:
  - `cli.layoutMode == LayoutCards`;
  - placements now match what a `CardStrategy` strip gives for the same columns (compute the expected value with `layout.NewStrip()` + `ApplyMode` + `SyncColumns` + `ComputePlacements`);
  - `tp.ClientSend` is empty (non-blocking `select` with `default`).
- `TestToggleLayoutArmsMotion`. With focus set and geometry that differs between the modes, `cli.motion != nil` after the toggle and `motion.to` equals the new placements. A second toggle back also arms.
- `TestSnapshotDoesNotChangeLayoutMode`. After `SetLayoutMode(LayoutCards)`, a snapshot carrying `Layout: LayoutScroll` leaves `cli.layoutMode == LayoutCards` and the strategy is still cards (placements match the card expectation).
  - This is the red proof for the ownership change: against current code the snapshot wins.
  - It uses the `Layout` field, which Phase 2 deletes, so Phase 2 deletes this test with it (its purpose then becomes structural).
- `TestSetLayoutModeBeforeFirstSnapshot`. `SetLayoutMode(LayoutCards)` on a fresh client, then a snapshot with no `Layout` field: placements are cards. Against current code the zero-value field drags it to scroll.
- `TestToggleRevealsOffscreenPaneContent`:
  - Scroll mode at a width where pane 3 has no placement.
  - Deliver snapshot → pane updates for 1, 2 and 3 (distinct text, e.g. `"CONTENT-THREE"`) → a second snapshot with the same columns (a status-only change, as `broadcastLayoutIfStatusChanged` sends).
  - Then `ToggleLayout()` into cards and draw with the existing `fakeHostScreen` helper used in `cards_test.go`.
  - Assert pane 3's placed region contains `CONTENT-THREE`.
  - Red against the placement-keyed prune: the second snapshot deletes mirror 3 and nothing re-sends it.
  - Pick the width with the same `threeColumns()` fixture `cards_test.go` uses. If pane 3 is placed in scroll mode at that width, the test must `t.Fatalf` as a setup bug rather than pass.

Migrations:
- `cards_test.go`:
  - `TestClientAppliesCardLayoutFromSnapshot` and `TestClientRevertsToScrollLayout` become `SetLayoutMode` / `ToggleLayout` versions: `TestSetLayoutModeCardsProducesCardPlacements` and `TestToggleBackToScrollLeavesNoSlivers`.
  - `TestEmptySnapshotStillAppliesLayoutMode` becomes `TestEmptySnapshotKeepsClientLayoutMode`: after `SetLayoutMode(LayoutCards)`, an empty snapshot leaves `layoutMode == LayoutCards`.
  - `TestClientDefaultsToScrollLayout` stays. `NewClient`'s zero value is still scroll; `cards` as the default is config's job. Reword its comment accordingly.
- `keys_test.go` `TestToggleCardsIsOnC`: match `b.ActionName == keys.ActionNameToggleCards`, assert `b.Action == keys.ActionToggleLayout` and `b.Verb == 0`, and keep the Key / BarGroup / NoRepeat / CtrlForm / Long assertions. The failure message becomes "no toggle_cards binding".
- `router_test.go` `assertAction`: add a case asserting `got.Kind == routeToggleLayout` for `keys.ActionToggleLayout`. `TestControlModeTable` then covers it: plain fires and leaves control mode.

`scripts/attachcheck.py`:
- `Client.__init__` gains `args=()`, and argv becomes `[BIN] + list(args)` or `[BIN, "attach", *args]`.
- Import `CUP` from `smoke`, and add:

```python
def cursor_col(out: bytes) -> int | None:
    """Column of the last cursor move: the focused pane's cursor, which
    is where the layout put that pane."""
    moves = CUP.findall(out)
    return int(moves[-1][1]) if moves else None
```

Each case opens a third pane first (`b"\x02n"`) so there is a fan to lay out.

- `case_toggle_affects_only_its_own_client`:
  - Start a server, then clients `a` and `b` (default layout). `a` types `C-b n`.
  - Record both clients' `cursor_col`.
  - `a` types `C-b c`.
  - Setup check: `a`'s col changed. If not, fail "layouts coincide at this size; case is vacuous".
  - Assert `b`'s col is unchanged. Wait for it with `wait_for` against a deadline, but expect no change.
  - Red against current code: the toggle is server state, so `b` moves too.
- `case_attach_layout_flag_is_honoured`:
  - Server, then `a = Client()`; `a` types `C-b n`; then `b = Client(args=["--layout", "scroll"])`.
  - Assert `cursor_col(b) != cursor_col(a)`.
  - Setup check: a third client `c = Client(args=["--layout", "cards"])` must equal `a`, so we know it is the flag and not attach timing.
  - Red against current code: attach ignores `--layout`.
- `case_reattach_starts_from_config`:
  - Server, then `a`; `a` types `C-b n` and we record `col0`.
  - `a` types `C-b c` and we record `col1`. Setup: `col1 != col0`.
  - `a.detach()`, then `c = Client()`: assert `cursor_col(c) == col0`.
  - Red against current code: the server remembers the toggle.
- Add all three to `CASES`, and `reap` / `stop` the server in `finally` like the neighbouring cases.

`scripts/smoke.py` `case_card_layout_toggles` should keep passing unchanged (the toggle is local now, still visible on the wire). Its `scroll_col` variable is really the default (cards) position; rename it to `default_col`. That is a one-word fix to a name the research flagged as misleading, and it's in a case this phase changes the meaning of.

**Verification — automated:**
- [x] New client tests fail against the unmodified client, each for the stated reason — **3 behavioural tests red for the stated reasons; 2 new-API tests red at compile; failure lines in notes.md**
- [x] The three attachcheck cases fail against a `main` build. `BIN` is hard-coded to `./bin/wideboi` (`scripts/attachcheck.py:50`), so do build, run and restore in **one** command:
  ```
  git worktree add -q /tmp/wb-main origin/main && (cd /tmp/wb-main && go build -o "$OLDPWD/bin/wideboi" ./cmd/wideboi) && for c in "toggle affects only" "own layout flag" "reattach starts"; do python3 scripts/attachcheck.py --only "$c"; done ; make build ; git worktree remove --force /tmp/wb-main
  ```
  Record which cases went red, and why, in notes.md.
  — **[x] all 3 red against main for the stated reasons (cols 23 vs 43); binary rebuilt; see notes.md.** `--only` matches display names, so the filter strings above were corrected.
- [x] `make quick` passes — **all packages ok, seam OK**
- [x] `go test -count=1 -race ./internal/client/ ./cmd/wideboi/ ./internal/keys/` — **3 ok**
- [x] `make check` passes ×4 (baseline main if any single failure) — **4/4 exit 0; smoke 35/35, attach 20/20 each run (rerun on the settled tree after the collision)**

**Verification — manual:**
- [ ] Plain `./bin/wideboi` starts in cards; `C-b c` flips to scroll with the slide animation; `C-b d`, then `./bin/wideboi` again comes back in cards
- [ ] Two terminals attached to one session: toggling in one leaves the other alone
- [ ] `./bin/wideboi attach --layout scroll` into a cards session shows scroll

---

## Phase 2: Remove the server's layout state and placement computation (#47)

The server has no notion of layout mode. `MsgLayoutSnapshot` loses `Layout` and `Placements`, the client always computes placements itself, and `parseLayout` is gone.

**Files:**
- Modify `internal/protocol/messages.go`:
  - delete `Layout` and `Placements` from `MsgLayoutSnapshot`;
  - rewrite the `LayoutMode` doc comment;
  - update the `VerbToggleCards` comment.
- Modify `internal/server/server.go`:
  - delete the `layout` field and `SetLayout`;
  - `VerbToggleCards` becomes an explicit ignored case;
  - `broadcastLayout` stops calling `ComputePlacements` and stops filling the two fields;
  - fix the `VerbMoveRight` comment that cites `VerbToggleCards`.
- Modify `internal/client/client.go`: always `c.placements = layout.ToProtocol(c.strip.ComputePlacements(c.cols, c.rows))` (drop the `len(m.Columns) > 0` branch and the `m.Placements` fallback).
- Modify `cmd/wideboi/main.go`: delete `srv.SetLayout(cfg.LayoutMode)`, and delete `parseLayout` and its comment.
- Test: `cmd/wideboi/main_test.go`, delete `TestParseLayoutRejectsUnknown` and `TestParseLayoutAcceptsKnown` (`config_test.go` already covers valid and invalid layout values in `TestLoadInvalidLayout` / `TestLoadDefaults`).
- Test: `internal/server/server_test.go`, rewrite the `snap.Placements` uses (lines ~54, 82, 142-145, 192, 241-254, 303-307).
- Test: `internal/server/status_test.go`, replace the two toggle tests.
- Test: `internal/transport/wire_test.go:160`, drop the `Placements` field from the round-trip fixture.
- Test: `internal/client/layout_mode_test.go`, delete `TestSnapshotDoesNotChangeLayoutMode`. The field no longer exists, and the compiler now enforces what it tested.
- Modify `CLAUDE.md`:
  - The premise pointer `internal/server/status_test.go:187` becomes the renamed test by name.
  - The Architecture bullet "The server still computes some of its own — that duplication is issue #47" is replaced by "The server computes none (#47)."

**Key changes:**

`internal/protocol/messages.go`:

```go
// LayoutMode is which Strategy a client is presenting. It is per-client
// presentation, not session state, and does not cross the wire (#92):
// each client resolves its own from config and toggles it locally. The
// type lives here because both config and layout name it.
type LayoutMode int
```

Keep the two constants and their comments. Drop the zero-value wire-compat sentence, which no longer applies.

```go
	// VerbToggleCards is reserved. Layout is client-local since #92, so
	// no client sends it and the server ignores it; the slot stays so
	// the verbs after it keep their wire values.
	VerbToggleCards
```

`internal/server/server.go`, in the verb switch:

```go
		case protocol.VerbToggleCards:
			// Reserved: layout is the client's (#92). An older client
			// may still send it; there is nothing to do.
```

`VerbMoveRight` comment: "for the same reason a layout toggle never did: order is presentation…".

`broadcastLayout`: remove the `placements := s.strip.ComputePlacements(s.cols, s.rows)` line and the `Placements:` / `Layout:` fields. If `s.cols` / `s.rows` then have no other reader, leave them: `resizePanesLocked` uses them for height (check with `grep -n 's\.rows\|s\.cols' internal/server/server.go` and record the result in notes).

`internal/server/server_test.go`, a new helper:

```go
// placementsAt computes what a scroll-mode client would place for snap
// at cols x rows. The server no longer computes placements (#47), so a
// test that needs to know what is on screen -- to confirm its own
// clipping scenario -- computes it the way a client does.
func placementsAt(snap protocol.MsgLayoutSnapshot, cols, rows int) []layout.Placement {
	s := layout.NewStrip()
	layout.ApplyMode(s, protocol.LayoutScroll)
	s.SyncColumns(snap.Columns, snap.FocusPaneID)
	return s.ComputePlacements(cols, rows)
}
```

Rewrites:
- `TestServerLifecycleAndAttach` (54) and the NewColumn test (82): `len(snap.Columns) != 2`.
- `TestResizePropagatesToPanes` (142-145): iterate `snap.Columns` (`col.PaneID`). The width and rows assertions are unchanged.
- `TestResizeKeepsFullWidthForClippedPane` (192): iterate `placementsAt(snap, 70, 20)`. The `sawClippedPlacement` setup guard keeps its meaning.
- `TestResizeCoversFullyScrolledOffPane` (241-254): collect pane IDs from `snap.Columns`; the setup check becomes `len(placementsAt(snap, 40, 20)) != 1`.
- [!] `TestServerVerbHandling` (82): `len(snap.Columns) != 2` is **wrong**: the session starts with 2 panes, so it's 3 after NewColumn. The old "2 placements" was visibility, not the verb. Now asserts 3; see notes.md.
- The kill test (303-307): `killID` and `surviveID` come from `snap.Columns[0/1].PaneID`, guarded by `len(snap.Columns) != 2`.

`internal/server/status_test.go`: replace `TestToggleCardsFlipsLayoutMode` and `TestToggleCardsLeavesColumnWidthsAlone` with:

```go
// Layout is client-local since #92. An older client can still send the
// reserved verb; it must change nothing -- above all not a column
// width, which is the no-shrink premise.
func TestReservedToggleVerbChangesNothing(t *testing.T) {
	s, _ := serverWithStatuses(t, map[int]term.PaneStatus{1: term.StatusIdle, 2: term.StatusIdle, 3: term.StatusIdle})
	ctx := context.Background()

	before := map[int]int{}
	for _, id := range s.strip.PaneIDs() {
		w, _ := s.strip.ColumnWidth(id)
		before[id] = w
	}
	focus := s.strip.FocusedPaneID()

	s.handleClientMsg(ctx, protocol.MsgVerb{Verb: protocol.VerbToggleCards})

	if got := s.strip.FocusedPaneID(); got != focus {
		t.Errorf("focus %d -> %d across the reserved toggle verb", focus, got)
	}
	for id, want := range before {
		got, ok := s.strip.ColumnWidth(id)
		if !ok || got != want {
			t.Errorf("pane %d width %d -> %d (ok=%v) across the reserved toggle verb", id, want, got, ok)
		}
	}
}
```

TDD opt-out: this phase is removal. The red step is the compiler. Deleting the fields must break every remaining reader, and each one is either rewritten above or deleted. `TestReservedToggleVerbChangesNothing` passes on arrival by design: it pins an invariant, not a new behaviour. To prove it can fail, temporarily add `s.strip.GrowWidth(10)` to the ignored case, watch it go red, and remove it (a Go test, so no rebuild hazard).

**Verification — automated:**
- [x] `go build ./...` fails after deleting the fields, at exactly the readers listed above (record the list in notes.md; any unlisted reader is a plan miss, so investigate it and don't just patch it) — **only listed readers; list in notes.md**
- [x] `TestReservedToggleVerbChangesNothing` proven red with the sabotage, then green — **"pane 3 width 40 -> 50", then ok**
- [x] `grep -rn 'Placements\b' internal/server` finds only comments that are still true (update or delete the rest) — **resize-rationale comments kept; still true**
- [x] `grep -rn 'parseLayout\|SetLayout\|\.Layout\b' --include=*.go .` finds nothing server-side or in cmd — **only config/flag parsing (`cfg.Layout`, `opts.flags.Layout`)**
- [x] `make quick` passes — **all ok, seam OK**
- [x] `make check` passes ×4 — **4/4 exit 0; smoke 35/35, attach 20/20 each run**

**Verification — manual:**
- [ ] Repeat Phase 1's manual checks on the Phase 2 build: behaviour must be identical
- [ ] Close every pane but one, then the last. The empty session draws no stale `+N` (the old fallback path)

---

## Phase 3: Docs

Say "starting layout for this client" everywhere the layout option is documented. Doc-only, so there is no TDD.

**Files:**
- `cmd/wideboi/main.go` help text:
  - `-l, --layout <mode>    Starting layout for this client: "cards" (default) or "scroll"`
  - `WIDEBOI_LAYOUT         Starting layout for this client ("cards" or "scroll")`
- `cmd/wideboi/cli_test.go` `TestPrintHelp`: still asserts `WIDEBOI_LAYOUT` is present. Change nothing unless it asserts the old wording.
- `README.md:163,176`: the same wording, plus one sentence near the keys table or the config section: "Layout is per client: `C-b c` flips only the terminal you press it in, and every attach starts from your configured layout."
- `config.example.toml:15`: `# Starting layout for each client: "cards" (default) or "scroll". C-b c toggles it for that client only.` Line 73's comment stays.

**Verification — automated:**
- [x] `make quick` passes (`TestLoadConfigExampleToml` still parses the example) — **all ok**
- [x] `./bin/wideboi --help | grep -i layout` shows the new wording — **both lines say "Starting layout for this client"**. Also added a `c` row to the README keys table, which never listed the toggle.

**Verification — manual:**
- [ ] Les reads the README paragraph

---

## Phase 4: Show the layout on the status line (#91)

The normal status line carries this client's mode, right-aligned before the hint. After Phase 1 the mode is purely client state, so this is rendering only, with no protocol change.

**Files:**
- Modify `internal/protocol/messages.go`: add `func (m LayoutMode) String() string` returning `"scroll"` / `"cards"` (and `fmt.Sprintf("LayoutMode(%d)", int(m))` for anything else).
- Modify `internal/client/client.go` `normalStatusLocked`: build the right-hand side as a fallback chain.
- Test: `internal/client/help_test.go`, next to `TestNormalStatusNamesTheConfiguredPrefix`.
- Test: `internal/protocol`, a table test for `String()`.
- Test: `scripts/smoke.py`, assert the tag in the first frame.
- Docs: `README.md`, one clause in the Phase 3 paragraph saying the status bar shows the current layout.

**Key changes:**

```go
	// The right-hand side is the layout tag and then the hint, and it
	// degrades hint first: the mode is live state that an accidental
	// C-b c changes (#91), the hint is a fixed string a user learns
	// once. Right-aligned because the left edge is pinned: smoke.py
	// finds the focused pane by its column in "focus: [pane N".
	mode := c.layoutMode.String()
	for _, right := range []string{mode + " · " + c.prefixLabel + " for commands", mode} {
		if pad := budget - runeLen(status) - runeLen(right); pad >= 2 {
			return truncateRunes(status+strings.Repeat(" ", pad)+right, budget)
		}
	}
	return truncateRunes(status, budget)
```

**Tests (write first, watch them fail):**

`help_test.go`:
- `TestNormalStatusShowsLayoutMode`: for each of cards and scroll, `SetLayoutMode(mode)`, then `statusLineLocked(99)` ends with `mode + " · C-b for commands"`.
- `TestNormalStatusDropsHintBeforeLayoutTag`: pick a budget from the actual status text, `runeLen(status) + 2 + len("cards")`, so the tag fits but tag plus hint does not. Assert it ends with `cards` and doesn't contain `for commands`. Setup check: the full right side must not fit at that budget.
- `TestNormalStatusDropsLayoutTagWhenNothingFits`: at `runeLen(status) + 1`, neither is present and `focus: [pane` still is.
- `TestToggleLayoutUpdatesStatusTag`: after `ToggleLayout()` from cards, the line shows `scroll`. This ties the tag to the local toggle, which the smoke case can't assert (below).
- Existing `TestNormalStatusNamesTheConfiguredPrefix` must still pass at 99 unchanged. Budget 99 fits both.

Red proof: the first and fourth tests fail against the current `normalStatusLocked` (no tag). The drop-order test fails against a naive `mode + " · " + hint` with no fallback, which proves the chain matters. Sabotage by collapsing the loop to its first element, run it, and restore. It's a Go test, so there's no rebuild hazard.

`scripts/smoke.py`:
- Where an existing case starts plain `wideboi` with the default layout, assert `b"cards \xc2\xb7 "` is in the output. Where one passes `--layout scroll` (`smoke.py:647`, `:308` or `:333`), assert `b"scroll \xc2\xb7 "`. The first frame draws the whole status line as one literal, so this is on-the-wire proof.
- Don't assert the tag changes after `C-b c` on the wire. The diffing renderer rewrites only the cells that changed (`"cards"` → `"scroll"` shares cells), so the literal need not appear, and the assertion would be flaky or vacuous. The unit test covers the toggle, and `case_card_layout_toggles` already proves the toggle reaches the screen.
- Prove red: run the new asserts against a Phase 3 build (build, run, restore in one command, as in Phase 1).

**Verification — automated:**
- [x] New unit tests red against Phase 3 code, then green (record in notes.md) — **3 red + String() compile-red; nothing-fits guard passes by construction; chain sabotage red; see notes.md**
- [x] smoke asserts red against a Phase 3 binary, then green — **both FAIL on 85763f3, OK on branch**
- [x] `make quick` passes — **all ok** (plus golden regenerated: +`cards` only, reviewed)
- [x] `make check` passes ×4 — **4/4 exit 0; smoke 35/35, attach 20/20 each run**

**Verification — manual:**
- [ ] At 80 columns: `cards · C-b for commands` on the right; `C-b c` → `scroll · C-b for commands`
- [ ] Narrow the terminal until the hint goes; `cards` stays, then goes last
- [ ] With three pane statuses showing at 80 columns, the tag and the statuses don't collide
