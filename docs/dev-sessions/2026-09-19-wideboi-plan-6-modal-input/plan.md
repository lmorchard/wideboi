# Modal Input Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace wideboi's `alt`-based key bindings with a tmux-style `ctrl+b` prefix and a sticky control mode, so the shortcuts work on every terminal without configuration.

**Architecture:** All key routing moves into one testable `router` type in `cmd/wideboi`, which owns the mode flag and returns a decision rather than acting. The client gains a control-mode flag that changes what the status bar says, inverts it, and hides the cursor while it is set. `compose` gains a styled write, which it does not currently have.

**Tech Stack:** Go 1.27.1; `charmbracelet/ultraviolet` (cell buffers, key decoding, `uv.Style`/`uv.AttrReverse`); Python 3 pty harness for wire-level acceptance.

**Spec:** `docs/dev-sessions/2026-09-19-wideboi-plan-6-modal-input/spec.md` — read it before Task 1. The mode-indicator section carries measurements this plan depends on.

## Global Constraints

- **Never run `go mod tidy`.** `github.com/charmbracelet/x/vt` has no tagged release and is pinned to a pseudo-version; tidy evicts it. Use `go get` if a dependency genuinely must change.
- **Never write the terminal's last column.** Ultraviolet brackets a write to the final cell with `ESC[?7l` … `ESC[?7h`, which splits text across escape sequences on the wire. The status bar's budget is `cols - 1`.
- **Every new test must be verified red before green.** Break the thing it guards, watch it fail, restore it. Six tests in this repo have passed against the bug they were written to catch.
- **`go test` caches.** Pass `-count=1` whenever the outcome depends on code you just changed.
- **`make check` must pass** before any task is reported done: `fmt-check lint seam-check test race verify-exit smoke`.
- **`internal/client` must not import `internal/server`**, in either direction. `make seam-check` enforces it.
- Cell pointers from `CellAt` point into live backing arrays. Copy the cell (`cc := *c`) if it outlives the call.
- macOS and Linux only.

## Decisions this plan makes that the spec left open

Recorded here so a reviewer judges the code against a stated position rather than guessing.

1. **`Escape` only, no idle timeout.** A timeout would silently drop the user out of control mode, which makes the indicator lie for the interval between the last keypress and the user noticing. Sticky with one explicit exit is predictable, and the coalescing motivation for a timeout does not exist until animation lands.
2. **No verb stays unprefixed.** The wedged-state escape is the signal path, which `make verify-exit` already covers.
3. **Unknown keys in control mode are swallowed, not forwarded.** The mode is sticky, so forwarding would let a typo land in a pane while the user still believes they are issuing commands.
4. **`pgup` loses its global binding too.** The spec names only `alt+*`, `ctrl+q` and `ctrl+o`, but v1 also claimed PageUp for scroll, and `less`, `vim` and every pager need it. Same defect the spec is arguing against, so it goes with the rest. Scroll remains available as `C-b u` / `C-b d`.

   **`pgdn` was never claimed in the first place.** `cmd/wideboi/main.go:139` matches the name `"pgdn"`, and Ultraviolet's name for that key is `"pgdown"` — verified against the pinned version, where `MatchString("pgdn")` is false for every event. So PageDown scroll has never worked, and PageDown has always reached the pane. Removing the binding removes nothing; the point of recording it is that a binding nobody exercised sat in the matrix for two plans looking correct. Task 3 deletes it with the rest, and Task 4's normal-mode test enumerates both keys so neither can be silently re-stolen.

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/client/compose/surface.go` | **Modify.** Add `WriteStyled`; `WriteString` becomes a zero-style call to it. |
| `internal/client/compose/surface_test.go` | **Modify.** Cover styled writes, including styled blanks. |
| `internal/client/client.go` | **Modify.** Control-mode flag, mode-aware status line, cursor hidden in control mode. Replaces the `helpSegments`/`helpFor` machinery. |
| `internal/client/help_test.go` | **Modify.** Rewrite for the new status-line builders. |
| `cmd/wideboi/router.go` | **Create.** The `router` type, the verb table, and `parsePrefix`. All input decisions live here. |
| `cmd/wideboi/router_test.go` | **Create.** Table-driven coverage of the mode machine. |
| `cmd/wideboi/main.go` | **Modify.** Read `WIDEBOI_PREFIX`, construct the router, replace the key switch with a dispatch on its decision. |
| `scripts/ptylib.py` | **Modify.** `spawn_in_pty` takes an optional environment overlay. |
| `scripts/smoke.py` | **Modify.** Rewrite alt-based cases for the prefix; add control-mode, double-tap and custom-prefix cases. |
| `README.md` | **Modify.** Delete the Option-as-Meta section; rewrite Keys. |
| `testdata/golden/startup.txt` | **Regenerate.** The status line's first frame changes. |

---

### Task 1: Styled writes in `compose`

There is no way to write a styled cell today. `compose.WriteString` builds cells with `uv.NewCell` and never touches `Cell.Style`, so the inverted status bar has nothing to call.

**Files:**
- Modify: `internal/client/compose/surface.go`
- Test: `internal/client/compose/surface_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `func WriteStyled(s uv.Screen, x, y int, text string, style uv.Style)`. `WriteString` keeps its existing signature and behaviour.

**Verified against the pinned Ultraviolet** (`v0.0.0-20260910203606-6c9e17dc7a16`), so these are facts, not assumptions:
- `uv.Cell` has a value field `Style uv.Style`; `uv.Style` has `Attrs uint8`.
- `uv.Style{Attrs: uv.AttrReverse}.String()` is `"\x1b[7m"`, and the reset is `"\x1b[m"`.
- `uv.NewCell(method, " ")` returns `EmptyCell.Clone()` — a fresh pointer, so assigning `.Style` to it cannot corrupt the package-level `EmptyCell`.
- The renderer's `canClearWith` rejects `AttrReverse`, so reverse-styled blanks are written as real cells instead of being optimised into an erase. The inverted row genuinely reaches the wire.

- [ ] **Step 1: Write the failing tests**

Add to `internal/client/compose/surface_test.go`. The import block gains `uv "github.com/charmbracelet/ultraviolet"`.

```go
func TestWriteStyledPutsTheStyleOnEveryCellItWrites(t *testing.T) {
	s := compose.NewSurface(6, 1)
	compose.WriteStyled(s, 1, 0, "hi", uv.Style{Attrs: uv.AttrReverse})

	for _, x := range []int{1, 2} {
		if got := s.CellAt(x, 0).Style.Attrs; got&uv.AttrReverse == 0 {
			t.Errorf("cell %d: attrs %d, want AttrReverse set", x, got)
		}
	}
	// Cells outside the written range must be left alone, or the status
	// bar's inversion would bleed across the whole row.
	for _, x := range []int{0, 3} {
		if got := s.CellAt(x, 0).Style.Attrs; got != 0 {
			t.Errorf("cell %d was not written but carries attrs %d", x, got)
		}
	}
}

func TestWriteStyledStylesBlanks(t *testing.T) {
	// The inverted status bar is mostly spaces. uv.NewCell special-cases
	// " " by returning EmptyCell.Clone(), so a blank takes a different
	// path through the constructor than any other glyph -- the one place
	// a style could plausibly be dropped.
	s := compose.NewSurface(3, 1)
	compose.WriteStyled(s, 0, 0, "   ", uv.Style{Attrs: uv.AttrReverse})

	for x := 0; x < 3; x++ {
		if s.CellAt(x, 0).Style.Attrs&uv.AttrReverse == 0 {
			t.Errorf("blank cell %d lost its style", x)
		}
	}
}

func TestWriteStringStaysUnstyled(t *testing.T) {
	// WriteString delegates to WriteStyled. If it ever passes anything
	// but a zero style, every pane's chrome picks up an attribute.
	s := compose.NewSurface(3, 1)
	compose.WriteString(s, 0, 0, "ab")

	for x := 0; x < 2; x++ {
		if !s.CellAt(x, 0).Style.IsZero() {
			t.Errorf("cell %d picked up a style", x)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/client/compose/ -run WriteStyled -count=1 -v
```

Expected: build failure, `undefined: compose.WriteStyled`.

- [ ] **Step 3: Implement**

In `internal/client/compose/surface.go`, replace the body of `WriteString` and add `WriteStyled` beneath it. Keep `WriteString`'s existing doc comment — its one-rune-per-cell caveat is still accurate and still load-bearing — and append one line pointing at the delegation.

```go
func WriteString(s uv.Screen, x, y int, text string) {
	WriteStyled(s, x, y, text, uv.Style{})
}

// WriteStyled writes text into s at (x, y) with every cell carrying
// style. A zero style is exactly what an unstyled write produces, which
// is why WriteString is one of these.
//
// Shares WriteString's one-rune-per-cell caveat; see there.
//
// Kept as a separate function rather than a variadic option on
// WriteString because every existing call site wants the unstyled form
// and should not have to say so.
func WriteStyled(s uv.Screen, x, y int, text string, style uv.Style) {
	currX := x
	for _, r := range []rune(text) {
		// NewCell returns a fresh cell in every case -- for " " it
		// clones the package-level EmptyCell rather than handing it
		// back -- so assigning Style here is safe.
		cell := uv.NewCell(s.WidthMethod(), string(r))
		cell.Style = style
		s.SetCell(currX, y, cell)
		w := 1
		if cell.Width > 1 {
			w = cell.Width
		}
		currX += w
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./internal/client/... -count=1
```

Expected: PASS, including the pre-existing `Blit` and `WriteString` tests.

- [ ] **Step 5: Prove the tests can fail**

Temporarily delete the `cell.Style = style` line, re-run, and confirm the two `WriteStyled` tests go red. Restore it.

- [ ] **Step 6: Commit**

```bash
git add internal/client/compose/
git commit -m "feat(compose): add a styled write path"
```

---

### Task 2: Control mode in the client

The client learns it has two modes. In control mode the bottom row becomes the verb menu, every cell of it inverted, and the cursor is hidden for the mode's duration.

**Files:**
- Modify: `internal/client/client.go`
- Modify: `internal/client/help_test.go`
- Modify: `cmd/wideboi/main.go` (constructor call only — Task 3 does the rest)

**Interfaces:**
- Consumes: `compose.WriteStyled(s uv.Screen, x, y int, text string, style uv.Style)` from Task 1.
- Produces:
  - `func NewClient(tp *transport.InProcChannel, cols, rows int, prefixLabel string) *Client` — the signature gains a trailing parameter.
  - `func (c *Client) SetControlMode(on bool)`
  - unexported, for tests in this package: `func controlHelp(budget int) string`, `func (c *Client) statusLineLocked(budget int) (string, uv.Style)`

**Why `statusLineLocked` returns a style instead of drawing:** `Draw` takes a `*uv.TerminalScreen`, which a unit test cannot cheaply construct. Returning the text and the style together makes the inversion decision assertable in a plain test, leaving `Draw` with a single unconditional `WriteStyled` call. The *rendering* is proved at the wire in Task 4. This split is deliberate: `docs/LESSONS.md` records that cell-buffer tests are exactly where two user-visible defects hid in Plan 1.

- [ ] **Step 1: Write the failing tests**

Replace the entire contents of `internal/client/help_test.go`. (The existing file has a doubled `import` — two separate single-import statements — which this rewrite also resolves.)

```go
package client

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// A user in control mode who cannot see how to quit or how to get back
// out has no affordance at all: there is no unprefixed escape hatch any
// more, by design. Both verbs survive every plausible width.
func TestControlHelpAlwaysNamesQuitAndExit(t *testing.T) {
	for cols := 40; cols <= 200; cols++ {
		line := truncateRunes(controlHelp(cols-1), cols-1)
		for _, want := range []string{"q quit", "esc exit"} {
			if !strings.Contains(line, want) {
				t.Errorf("cols=%d: control help omits %q: %q", cols, want, line)
			}
		}
		if got := len([]rune(line)); got > cols-1 {
			t.Errorf("cols=%d: control help is %d cells, budget is %d: %q", cols, got, cols-1, line)
		}
	}
}

// The measurement the whole indicator decision rests on: inverting the
// bar costs nothing, so at 80 columns -- the commonest width and
// cmd/wideboi's own fallback -- every verb fits. If a verb is ever added
// that breaks this, the spec's argument needs revisiting, so fail loudly
// rather than silently dropping one.
func TestControlHelpFitsEveryVerbAt80Columns(t *testing.T) {
	at80 := controlHelp(79)
	for _, want := range append(append([]string{}, controlVerbs...), controlTail...) {
		if !strings.Contains(at80, want) {
			t.Errorf("80 columns: control help omits %q: %q", want, at80)
		}
	}
	if got := runeLen(at80); got > 79 {
		t.Errorf("control help is %d cells at 80 columns, budget is 79", got)
	}
}

// Normal mode's only affordance is the hint, so it has to name the
// prefix the user actually has -- not a hardcoded C-b.
func TestNormalStatusNamesTheConfiguredPrefix(t *testing.T) {
	c := &Client{cols: 100, rows: 30, focusPaneID: 1, prefixLabel: "C-a"}
	got, style := c.statusLineLocked(99)
	if !strings.Contains(got, "C-a for commands") {
		t.Errorf("normal status does not name the prefix: %q", got)
	}
	if !style.IsZero() {
		t.Errorf("normal status carries a style: %+v", style)
	}
}

// The inversion is the mode indicator. A partly-inverted row reads as a
// rendering glitch rather than a mode, so the line must fill the whole
// budget.
func TestControlStatusInvertsTheWholeRow(t *testing.T) {
	c := &Client{cols: 100, rows: 30, focusPaneID: 1, prefixLabel: "C-b", controlMode: true}
	got, style := c.statusLineLocked(99)
	if style.Attrs&uv.AttrReverse == 0 {
		t.Errorf("control status is not reverse video: %+v", style)
	}
	if n := runeLen(got); n != 99 {
		t.Errorf("control status is %d cells, want the full 99 so the whole row inverts: %q", n, got)
	}
	if !strings.Contains(got, "q quit") {
		t.Errorf("control status does not name the quit verb: %q", got)
	}
}

func TestSetControlModeSwitchesTheStatusLine(t *testing.T) {
	c := &Client{cols: 100, rows: 30, focusPaneID: 1, prefixLabel: "C-b"}
	normal, _ := c.statusLineLocked(99)
	c.SetControlMode(true)
	control, _ := c.statusLineLocked(99)
	if normal == control {
		t.Errorf("status line is identical in both modes: %q", normal)
	}
	c.SetControlMode(false)
	back, _ := c.statusLineLocked(99)
	if back != normal {
		t.Errorf("leaving control mode did not restore the status line:\n  got  %q\n  want %q", back, normal)
	}
}

// Truncation is by rune, not byte: the status glyphs are three bytes
// and one cell each, so a byte-indexed cut could both overcount the
// budget and leave a partial UTF-8 sequence on the wire.
func TestTruncateRunesCutsOnRuneBoundaries(t *testing.T) {
	const s = "focus: pane 1  [1 ✓]  [2 ✗]"
	for n := 0; n <= len([]rune(s)); n++ {
		got := truncateRunes(s, n)
		if len([]rune(got)) != n {
			t.Errorf("truncateRunes(%q, %d) = %q, %d runes", s, n, got, len([]rune(got)))
		}
		if !strings.HasPrefix(s, got) {
			t.Errorf("truncateRunes(%q, %d) = %q is not a prefix", s, n, got)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/client/ -count=1
```

Expected: build failure — `undefined: controlHelp`, `controlVerbs`, `controlTail`, and `Client` has no field `prefixLabel` or `controlMode`.

- [ ] **Step 3: Implement the client changes**

In `internal/client/client.go`:

**3a.** Add `"strings"` to the imports. Add two fields to `Client`:

```go
	prefixLabel  string
	controlMode  bool
```

**3b.** Widen the constructor:

```go
// NewClient initializes a Client instance. prefixLabel is the short
// display form of the configured prefix key ("C-b"), used in the
// normal-mode hint -- the client never sees the key itself, only how to
// name it.
func NewClient(tp *transport.InProcChannel, cols, rows int, prefixLabel string) *Client {
	return &Client{
		transport:   tp,
		cols:        cols,
		rows:        rows,
		prefixLabel: prefixLabel,
		mirrors:     make(map[int]*PaneMirror),
	}
}
```

**3c.** Replace the whole `helpSegments`/`helpQuit`/`helpFor` block — its doc comment included, since every claim in it is about the alt bindings — with:

```go
// Control-mode verbs, in display order, most essential first. These
// strings must match cmd/wideboi's router table; scripts/smoke.py
// asserts they do, so the two cannot drift apart silently.
//
// Unprefixed letters rather than a modifier because that is the whole
// point of the mode: the terminal has already told us a prefix arrived,
// so no modifier needs to survive the trip.
var controlVerbs = []string{
	"h/l focus",
	"n new",
	"w width",
	"x kill",
	"j jump",
	"u/d scroll",
}

// Never dropped. With no unprefixed bindings left, a user who cannot
// read these two out of the bar has no way forward except a signal.
var controlTail = []string{"q quit", "esc exit"}

// controlHelp returns as much of the control-mode verb menu as fits in
// budget cells, always including controlTail.
//
// The full menu is 71 cells, against a budget of 79 at an 80-column
// terminal, so in practice nothing is dropped at any width wideboi is
// usable at. The dropping exists for narrower terminals and for
// whatever verbs get added later.
func controlHelp(budget int) string {
	tail := strings.Join(controlTail, "  ")
	used := runeLen(tail)
	taken := make([]string, 0, len(controlVerbs))
	for _, v := range controlVerbs {
		if used+2+runeLen(v) > budget {
			break
		}
		used += 2 + runeLen(v)
		taken = append(taken, v)
	}
	return strings.Join(append(taken, tail), "  ")
}

// statusLineLocked returns the bottom row's text and the style every one
// of its cells carries. c.mu must be held.
//
// It returns the style rather than drawing, because Draw needs a
// *uv.TerminalScreen that a unit test cannot cheaply build -- this is
// what makes the control-mode inversion assertable at all. That the
// style actually reaches the wire is proved by scripts/smoke.py.
func (c *Client) statusLineLocked(budget int) (string, uv.Style) {
	if budget < 0 {
		budget = 0
	}
	if c.controlMode {
		menu := truncateRunes(controlHelp(budget), budget)
		// Pad to the full budget: a partly-inverted row reads as a
		// rendering glitch, not as a mode.
		menu += strings.Repeat(" ", budget-runeLen(menu))
		return menu, uv.Style{Attrs: uv.AttrReverse}
	}
	return c.normalStatusLocked(budget), uv.Style{}
}

// normalStatusLocked builds the ordinary status line: what is focused,
// which panes want attention, and how to reach the verbs. c.mu must be
// held.
func (c *Client) normalStatusLocked(budget int) string {
	status := fmt.Sprintf("focus: pane %d", c.focusPaneID)
	for _, p := range c.placements {
		if glyph, ok := c.paneStatuses[p.PaneID]; ok && glyph != "" && glyph != " " {
			status += fmt.Sprintf("  [%d %s]", p.PaneID, glyph)
		}
	}
	// The hint is right-aligned and is the first thing to go when the
	// terminal is too narrow for it: the pane statuses are live
	// information, the hint is a fixed string a user learns once.
	hint := c.prefixLabel + " for commands"
	if pad := budget - runeLen(status) - runeLen(hint); pad >= 2 {
		status += strings.Repeat(" ", pad) + hint
	}
	return truncateRunes(status, budget)
}

// SetControlMode switches the client between forwarding keys to the
// focused pane and showing the verb menu. Called by cmd/wideboi after
// every key event, so the bar can never disagree with the router.
func (c *Client) SetControlMode(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.controlMode = on
}
```

**3d.** Keep `runeLen` and `truncateRunes` exactly as they are, except correct `runeLen`'s doc comment, which currently claims rune count is the right unit. It is not — it is the unit `compose.WriteString` happens to consume. Replace that comment with:

```go
// runeLen counts cells the way compose.WriteString consumes them: one
// per rune.
//
// This is the right unit only because WriteString advances one cell per
// rune regardless of the glyph. It is not the true display width -- a
// double-width glyph is one rune and two columns, and WriteString gets
// that wrong too. The two agree by sharing a bug, so if WriteString is
// ever widened to grapheme clusters with measured widths, this has to
// move to Cell.Width in the same change.
```

**3e.** In `Draw`, replace the status-bar block. The existing comment about the final column stays — it is still the reason for the `-1`:

```go
	// Status bar on bottom row.
	//
	// Leave the final column untouched: ultraviolet's terminal renderer
	// writes the last cell of a row with autowrap toggled off and back
	// on around it, which splits whatever glyph lands there across a
	// mode escape sequence on the wire. Budgeting one cell short of
	// c.cols keeps the whole line contiguous in the raw output.
	statusText, statusStyle := c.statusLineLocked(c.cols - 1)
	compose.WriteStyled(scr, 0, c.rows-1, statusText, statusStyle)
```

**3f.** In `Draw`, hide the cursor in control mode. Replace the opening of the cursor block:

```go
	// Host cursor position and visibility.
	//
	// In control mode the cursor is hidden outright. Keystrokes are not
	// reaching the pane, so a blinking pane cursor would be claiming
	// otherwise -- and unlike the bar's inversion, cursor visibility is
	// a DECTCEM escape on the wire, which is what lets smoke.py assert
	// that the mode was entered at all.
	switch {
	case c.controlMode:
		scr.HideCursor()
	case focusedPlacement != nil && cursorInfo != nil:
		cp, visible := cursorInfo(c.focusPaneID)
		fx := focusedPlacement.Dst.Min.X + cp.X - focusedPlacement.Src.Min.X
		fy := focusedPlacement.Dst.Min.Y + cp.Y - focusedPlacement.Src.Min.Y
		if visible && fx >= focusedPlacement.Dst.Min.X && fx <= focusedPlacement.Dst.Max.X &&
			fy >= focusedPlacement.Dst.Min.Y && fy <= focusedPlacement.Dst.Max.Y {
			fx = min(fx, max(focusedPlacement.Dst.Max.X-1, 0))
			fy = min(fy, max(focusedPlacement.Dst.Max.Y-1, 0))
			scr.SetCursorPosition(fx, fy)
			scr.ShowCursor()
		} else {
			scr.HideCursor()
		}
	default:
		scr.HideCursor()
	}
```

**3g.** In `cmd/wideboi/main.go`, update the one constructor call so the tree still builds. Task 3 replaces the literal:

```go
	cli := client.NewClient(tp, width, height, "C-b")
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./internal/client/... -count=1 && go build ./...
```

Expected: PASS, and a clean build.

- [ ] **Step 5: Prove the tests can fail**

Two separate breaks, each restored afterwards:
1. In `statusLineLocked`, drop the padding line. `TestControlStatusInvertsTheWholeRow` must go red on the cell count.
2. In `statusLineLocked`, return `uv.Style{}` in the control branch. The same test must go red on the attribute.

- [ ] **Step 6: Commit**

```bash
git add internal/client/ cmd/wideboi/main.go
git commit -m "feat(client): give the status bar a control mode"
```

---

### Task 3: The prefix and the mode machine

All input decisions move out of `main.go`'s inline switch into a `router` that can be tested without a terminal. The `alt` bindings go away.

**Files:**
- Create: `cmd/wideboi/router.go`
- Create: `cmd/wideboi/router_test.go`
- Modify: `cmd/wideboi/main.go`

**Interfaces:**
- Consumes: `client.NewClient(tp, cols, rows, prefixLabel string)` and `(*client.Client).SetControlMode(bool)` from Task 2.
- Produces: nothing later tasks import — Task 4 exercises this through the binary.

**Verified against the pinned Ultraviolet,** so the matching semantics below are facts:
- `uv.KeyPressEvent{Code: 'b', Mod: uv.ModCtrl}.MatchString("ctrl+b")` is true.
- `uv.KeyPressEvent{Code: uv.KeyEscape}.MatchString("esc")` is true (`"escape"` also matches).
- `uv.KeyPressEvent{Code: 'q', Mod: uv.ModShift, Text: "Q"}.MatchString("q")` is **false**. Shifted letters do not match their lowercase name, so a capital `Q` in control mode is an unknown key and is swallowed. That is correct — the verbs are lowercase — but it has to be deliberate rather than discovered.
- `uv.DefaultEscTimeout` is 50 ms. A lone `ESC` decodes as Escape only after that gap; within it, `ESC` plus a letter decodes as `alt+letter`. **Known wart:** a user who hits Escape and then a verb key inside 50 ms gets an `alt+` event, which control mode swallows, so they stay in the mode. Rare enough to accept, and documented here rather than engineered around.

- [ ] **Step 1: Write the failing tests**

Create `cmd/wideboi/router_test.go`:

```go
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
	// believes they are giving commands. Shift+Q is in here because
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
	// verbs and no way out but a signal. Refuse at startup instead.
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
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./cmd/wideboi/ -count=1
```

Expected: build failure — `undefined: router`, `route`, `parsePrefix`, `routeForward`.

- [ ] **Step 3: Write the router**

Create `cmd/wideboi/router.go`:

```go
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
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./cmd/wideboi/ -count=1 -v
```

Expected: PASS.

- [ ] **Step 5: Wire the router into `main.go`**

**5a.** Read and validate the prefix **before** the terminal starts. After the alt screen is entered, an error message would be painted into it and vanish on exit. Insert directly after the `cwd, _ := os.Getwd()` line:

```go
	prefixName := os.Getenv("WIDEBOI_PREFIX")
	if prefixName == "" {
		prefixName = defaultPrefix
	}
	prefix, prefixLabel, err := parsePrefix(prefixName)
	if err != nil {
		return err
	}
```

The existing `width, height, err := t.GetSize()` further down then needs `=` rather than `:=` for `err`, or a rename — pick whichever `go vet` is happy with.

**5b.** Pass the label to the client, replacing Task 2's literal:

```go
	cli := client.NewClient(tp, width, height, prefixLabel)
```

**5c.** Construct the router next to it:

```go
	rt := &router{prefix: prefix}
```

**5d.** Replace the whole `case uv.KeyPressEvent:` block — the binding matrix and its comment included — with:

```go
			case uv.KeyPressEvent:
				act := rt.route(ev)
				switch act.Kind {
				case routeQuit:
					return nil
				case routeVerb:
					cli.SendVerb(ctx, act.Verb)
				case routeScroll:
					cli.SendScroll(ctx, act.Scroll)
				case routeForward:
					cli.SendKey(ctx, uv.KeyEvent(ev))
				case routeIgnore:
				}
				// After every key, not only the ones that changed the
				// mode: the bar must never be able to disagree with the
				// router about which mode is active.
				cli.SetControlMode(rt.control)
```

- [ ] **Step 6: Build and run the full gate**

```bash
make build && go test ./... -count=1 && make fmt-check lint
```

Expected: clean. `make smoke` will still fail — its cases type `alt` sequences. Task 4 fixes them.

- [ ] **Step 7: Drive the real binary**

`docs/LESSONS.md`: no review tier substitutes for running it, and both of Plan 1's shipped defects were found in ten minutes of manual use. Run `./bin/wideboi` and confirm by hand:
1. `ctrl+b` inverts the status bar and hides the cursor.
2. `l` then `h` move focus, and the bar stays inverted between them.
3. `Escape` restores the bar and the cursor.
4. `ctrl+b` `ctrl+b` puts a literal `^B` into the pane (`stty -icanon -iexten -echo; cat -v` first).
5. `ctrl+q`, `ctrl+o` and `PageUp` now reach the pane.
6. `WIDEBOI_PREFIX=ctrl+a ./bin/wideboi` works, and the hint reads `C-a for commands`.
7. `WIDEBOI_PREFIX=bogus ./bin/wideboi` prints a readable error and exits without touching the terminal.

Report anything that surprised you; do not fix it silently.

- [ ] **Step 8: Prove the tests can fail**

In `route`, make the normal-mode branch return `routeIgnore` instead of `routeForward`. `TestNormalModeForwardsEveryKeyIncludingTheOnesV1Stole` must go red. Restore it.

- [ ] **Step 9: Commit**

```bash
git add cmd/wideboi/
git commit -m "feat(cmd): replace the alt bindings with a configurable prefix"
```

---

### Task 4: Wire-level acceptance

`make smoke` asserts on the bytes wideboi writes to the pty, and it is currently asserting about a key scheme that no longer exists. Every alt case is rewritten, and the new behaviour gets cases of its own.

**Files:**
- Modify: `scripts/ptylib.py`
- Modify: `scripts/smoke.py`

**Interfaces:**
- Consumes: the built binary's behaviour from Task 3.
- Produces: `spawn_in_pty(argv, cols, rows, set_winsize, env=None)` — a fifth, optional parameter. Existing callers in `ptycheck.py` and `golden.py` are unaffected.

**The control byte for `ctrl+b` is `\x02`** (`ctrl+a` is `\x01`). **Escape is `\x1b`**, and decodes as the Escape key only after a ≥50 ms gap — `Session.type`'s default settle of 0.8 s is comfortably over.

- [ ] **Step 1: Let the harness set environment variables**

In `scripts/ptylib.py`, change the signature and the child's environment construction:

```python
def spawn_in_pty(argv: list[str], cols: int, rows: int, set_winsize: bool,
                 env: dict[str, str] | None = None) -> tuple[int, int]:
```

Extend the docstring with one line: `env, if given, overlays the inherited environment in the child.` Then in the child branch, replace the two environment lines with:

```python
            child_env = dict(os.environ)
            child_env["SHELL"] = "/bin/sh"
            if env:
                child_env.update(env)
            os.execvpe(argv[0], argv, child_env)
```

- [ ] **Step 2: Write the failing smoke cases**

In `scripts/smoke.py`:

**2a.** Give `Session` the same passthrough:

```python
    def __init__(self, cols=100, rows=30, startup=1.2, env=None):
        self.pid, self.fd = spawn_in_pty(["./bin/wideboi"], cols, rows, True, env)
```

**2b.** Add a helper beside `divider_columns`:

```python
# Cursor visibility is the one part of control mode that is assertable
# on the wire. The bar's inversion is an SGR attribute, which survives
# a test only if someone looks for the exact byte; the cursor is a mode
# escape the renderer emits on change. Taking the last one in stream
# order gives the state as of the end of the capture.
CURSOR_VIS = re.compile(rb"\x1b\[\?25(h|l)")


def cursor_visible(out: bytes) -> bool | None:
    """Whether the cursor was last shown or hidden, in stream order."""
    found = CURSOR_VIS.findall(out)
    return None if not found else found[-1] == b"h"
```

**2c.** Rewrite the cases that type `alt` sequences. In `case_new_column_opens_pane`, `case_cycle_width` and `case_osc133_status_and_smart_jump`, replace each escape-prefixed keystroke and update its inline comment:

| Was | Becomes |
| --- | --- |
| `s.type("\x1bn")  # alt+n` | `s.type("\x02n")  # C-b n -> new column` |
| `s.type("\x1bw")  # alt+w` | `s.type("\x02w")  # C-b w -> cycle width` |
| `s.type("\x1bl")  # alt+l` | `s.type("\x02l")  # C-b l -> focus right` |
| `s.type("\x1bj")  # alt+j` | `s.type("\x02j")  # C-b j -> smart jump` |

The surrounding `fail(...)` strings in those cases name `alt+n` and `alt+w`; update them to `C-b n` and `C-b w` so a failure message describes a key that exists.

In `case_osc133_status_and_smart_jump` the two keystrokes are consecutive. Control mode is sticky, so the second no longer needs its own prefix — `s.type("\x02l")` then `s.type("j")`. Leave a comment saying that is what is being relied on; it is free coverage of stickiness.

In `case_focus_switch_moves_the_cursor`, replace `s.type("\x0f")  # ctrl+o` with `s.type("\x02l")  # C-b l -> focus right`.

**2d.** Delete `case_alt_mod_keybindings` entirely and replace it with:

```python
def case_prefix_routes_verbs(fail):
    # The whole point of the plan: verbs reachable without the terminal
    # being configured to send Option as Meta.
    s = Session()
    s.type("\x02n")  # C-b n -> new column
    s.type("echo prefix-pane3\r")
    if b"prefix-pane3" not in s.output():
        fail("C-b n failed to create a new column and accept input")
    s.type("\x02h")  # C-b h -> focus left
    s.type("echo back-in-pane2\r")
    if b"back-in-pane2" not in s.output():
        fail("C-b h failed to move focus left")
    s.quit_and_reap()


def case_control_mode_is_visible_and_escapable(fail):
    # A mode you cannot tell you are in is worse than no mode. The bar
    # inverts (SGR 7) and the cursor hides (DECTCEM), and both have to
    # come back on the way out.
    s = Session()
    s.type("echo before-mode\r")
    if cursor_visible(s.output()) is not True:
        fail("cursor was not visible before entering control mode")
    before = len(s.output())

    s.type("\x02")  # C-b, and stay there
    entered = s.output()[before:]
    if b"\x1b[7m" not in entered:
        fail("entering control mode did not invert the status bar (no SGR 7 on the wire)")
    if cursor_visible(s.output()) is not False:
        fail("entering control mode did not hide the cursor")
    if b"q quit" not in entered:
        fail("control mode did not show the verb menu")
    mid = len(s.output())

    s.type("\x1b")  # escape
    if cursor_visible(s.output()) is not False and cursor_visible(s.output()) is None:
        fail("no cursor state observed after leaving control mode")
    if cursor_visible(s.output()) is not True:
        fail("leaving control mode did not restore the cursor")
    if b"for commands" not in s.output()[mid:]:
        fail("leaving control mode did not restore the normal status line")

    s.type("echo after-mode\r")
    if b"after-mode" not in s.output():
        fail("input did not reach the pane after leaving control mode")
    s.quit_and_reap()


def case_control_mode_is_sticky(fail):
    # C-b l l must move two columns on one prefix. If the mode were
    # one-shot the second l would land in a pane as a literal letter.
    s = Session()
    s.type("\x02n")  # a third column, so there is somewhere to go
    s.type("\x02")
    first = focus_pane_id(s.output(), s.rows)
    s.type("h")
    middle = focus_pane_id(s.output(), s.rows)
    s.type("h")
    last = focus_pane_id(s.output(), s.rows)
    if first is None or middle is None or last is None:
        fail("no focus-pane-id observed across a sticky-mode sequence")
        return
    if middle == first or last == middle:
        fail(f"focus did not move twice on one prefix: {first} -> {middle} -> {last}")
    s.type("\x1b")
    s.quit_and_reap()


def case_doubled_prefix_reaches_the_pane(fail):
    # Without this there is no way to type the prefix byte at all, and
    # readline's backward-char becomes unreachable in every pane.
    #
    # stty first for the same reason case_reclaimed_control_keys_pass_through
    # needs it: on a canonical-mode pty the kernel's line discipline eats
    # some control bytes before cat ever reads them.
    s = Session()
    s.type("stty -icanon -iexten -echo; cat -v\r", settle=1.3)
    before = len(s.output())
    os.write(s.fd, b"\x02\x02")
    time.sleep(0.6)
    s.type("\r", settle=1.2)
    if b"^B" not in s.output()[before:]:
        fail("a doubled prefix did not put a literal ctrl+b into the pane")
    s.quit_and_reap()


def case_reclaimed_control_keys_pass_through(fail):
    # ctrl+q and ctrl+o existed only as the escape hatch for a terminal
    # that would not send Option as Meta. The prefix is that hatch now,
    # so these belong to the pane again -- ctrl+q is XON/XOFF resume and
    # ctrl+o is readline's operate-and-get-next.
    s = Session()
    s.type("stty -icanon -iexten -ixon -echo; cat -v\r", settle=1.3)
    before = len(s.output())
    for byte in (b"\x11", b"\x0f"):  # ctrl+q, ctrl+o
        os.write(s.fd, byte)
        time.sleep(0.5)
    s.type("\r", settle=1.2)
    seen = s.output()[before:]
    for name, mark in (("ctrl+q", b"^Q"), ("ctrl+o", b"^O")):
        if mark not in seen:
            fail(f"{name} is still claimed by the multiplexer; the pane should have it now")
    s.quit_and_reap()


def case_custom_prefix_from_env(fail):
    # Configurability is not a later nicety: running wideboi inside tmux
    # collides on ctrl+b, and the whole justification for claiming a
    # shell key is that the user can move it.
    s = Session(env={"WIDEBOI_PREFIX": "ctrl+a"})
    out = s.output()
    if b"C-a for commands" not in out:
        fail("status line does not name the configured prefix")
    s.type("\x01n")  # C-a n -> new column
    s.type("echo custom-prefix-pane\r")
    if b"custom-prefix-pane" not in s.output():
        fail("the configured prefix did not route a verb")
    s.quit_and_reap()
```

**2e.** Rewrite the two status-line cases, which currently assert on `alt+` strings:

```python
def case_status_line_names_the_prefix(fail):
    # Normal mode's only affordance is the hint. If it goes missing, a
    # new user has no way to discover that any verbs exist.
    s = Session()
    out = s.output()
    if b"$mod" in out:
        fail("status line renders the literal placeholder '$mod'")
    if b"C-b for commands" not in out:
        fail("status line never tells the user how to reach the verbs")
    if b"alt+" in out:
        fail("status line still advertises the removed alt bindings")
    s.quit_and_reap()


def case_control_mode_names_every_verb_at_80_columns(fail):
    # 80 is the commonest terminal width and cmd/wideboi's own fallback
    # when the host reports no size. Inverting the bar rather than
    # spending cells on a badge is what makes the whole menu fit here;
    # the spec's measurement is 71 cells against a budget of 79. If a
    # verb ever falls off, that argument needs revisiting.
    s = Session(cols=80, rows=24)
    s.type("\x02")
    out = s.output()
    for verb in (b"h/l focus", b"n new", b"w width", b"x kill",
                 b"j jump", b"u/d scroll", b"q quit", b"esc exit"):
        if verb not in out:
            fail(f"at 80 columns control mode never shows {verb.decode()!r}")
    s.type("\x1b")
    s.quit_and_reap()
```

**2f.** Replace the `CASES` entries for the removed and renamed cases, keeping the rest in place:

```python
    ("prefix routes verbs", case_prefix_routes_verbs),
    ("control mode is visible and escapable", case_control_mode_is_visible_and_escapable),
    ("control mode is sticky", case_control_mode_is_sticky),
    ("doubled prefix reaches the pane", case_doubled_prefix_reaches_the_pane),
    ("reclaimed control keys pass through", case_reclaimed_control_keys_pass_through),
    ("custom prefix from env", case_custom_prefix_from_env),
    ("status line names the prefix", case_status_line_names_the_prefix),
    ("control mode names every verb at 80 columns", case_control_mode_names_every_verb_at_80_columns),
```

Remove the entries for `alt mod keybindings`, `status line names real keys` and `status line names quit at 80 columns`. Keep `shell control keys pass through` as it is — `ctrl+w/l/n/h` are still the pane's.

- [ ] **Step 3: Run the new cases against the current binary to watch them fail**

Before building Task 3's binary is not possible — it is already built. Instead prove each new case can fail by breaking the behaviour it guards, one at a time:

```bash
python3 scripts/smoke.py --only "control mode is visible"
```

Comment out `scr.HideCursor()` in the client's control-mode branch, rebuild, and confirm the case reports the cursor failure. Restore it. Do the same for the doubled-prefix case by making `route` return `routeIgnore` for a doubled prefix.

- [ ] **Step 4: Run the full smoke suite**

```bash
make smoke
```

Expected: every case passes except the golden comparison, which Task 5 regenerates.

- [ ] **Step 5: Commit**

```bash
git add scripts/
git commit -m "test(smoke): assert the prefix, the mode, and the keys we gave back"
```

---

### Task 5: Documentation and the golden snapshot

The README's largest section explains a workaround for a problem that no longer exists.

**Files:**
- Modify: `README.md`
- Modify: `docs/LESSONS.md`
- Regenerate: `testdata/golden/startup.txt`

**Interfaces:** none.

- [ ] **Step 1: Rewrite the README**

Delete the entire `## Your terminal must send Option as Meta` section (lines 14–32 in the current file), heading included. Replace the `## Keys` section with:

```markdown
## Keys

wideboi uses a prefix key, like tmux. Press `ctrl+b` to enter control
mode, then a verb. The status bar inverts and the cursor disappears
while control mode is active.

Control mode is sticky: it stays until you press `Escape`, so
`ctrl+b l l l` moves three columns right.

| In control mode | Action |
| --- | --- |
| `h` / `l` | focus left / right (arrow keys work too) |
| `n` | new column |
| `w` | cycle column width |
| `x` | kill focused pane |
| `j` | jump to the pane that wants attention |
| `u` / `d` | scroll the focused pane's history |
| `q` | quit |
| `esc` | leave control mode |
| `ctrl+b` | send a literal `ctrl+b` to the pane, and leave control mode |

`ctrl+b` is the only key wideboi keeps for itself. Everything else goes
to the focused pane, including `ctrl+c`, `ctrl+q`, `ctrl+w`, `ctrl+l`
and `PageUp`.

### Changing the prefix

Set `WIDEBOI_PREFIX` to any `ctrl+<letter>`, or to `ctrl+space`:

```
WIDEBOI_PREFIX=ctrl+a wideboi
```

**If you run wideboi inside tmux, change it.** tmux's own default prefix
is `ctrl+b`, and whichever of the two is outermost will swallow it.
`ctrl+a` is the conventional alternative.
```

- [ ] **Step 2: Record the lesson**

Append to `docs/LESSONS.md`:

```markdown
## A binding that depends on terminal configuration is a broken binding

v1 bound every verb to `alt`. On macOS, terminals do not send Option as
Meta by default, so on the author's own machine **every shortcut did
nothing** — and did it silently, with no error and no clue. It survived
a full plan, twelve reviews and a merge, because every test drove the
key *bytes* directly and so could never observe that a real terminal
would not produce them.

Plan 5 treated this by documenting the fix and adding `ctrl+q`/`ctrl+o`
as an escape hatch. That was the wrong layer: it accepted a dependency
on per-terminal, per-profile configuration and tried to explain it.
Plan 6 removed the dependency.

The general form: **prefer input that every terminal produces
unconditionally over input that most terminals can be configured to
produce.** A control byte is one of the former; a Meta-modified key is
one of the latter. When you cannot avoid the latter, the question to ask
is not "have we documented it" but "what does a user see when their
terminal does not do this" — and the answer must not be "nothing".

Corollary for tests: a test that synthesises the input bytes is testing
your decoder, not your binding. Neither the unit tests nor the pty smoke
suite could have caught this, because both typed the escape sequence an
already-configured terminal would send.
```

- [ ] **Step 3: Regenerate the golden snapshot**

The first frame's status line changed, so the committed transcript must too.

```bash
make golden
git diff testdata/golden/startup.txt
```

Read the diff before staging it. Expected: the `alt+…` help text is replaced by `C-b for commands`. `CURSOR_SHOW`/`CURSOR_HIDE` should still both be present. **If anything else moved, stop and say so** — an unexplained golden diff is the signal this file exists to give.

- [ ] **Step 4: Run the full gate**

```bash
make check
```

Expected: all of `fmt-check lint seam-check test race verify-exit smoke` pass.

- [ ] **Step 5: Commit**

```bash
git add README.md docs/LESSONS.md testdata/golden/startup.txt
git commit -m "docs: document the prefix, and why alt was the wrong layer"
```

---

## Self-Review

**Spec coverage.** Every "In" scope item maps to a task: prefix handling with double-tap (3), sticky mode with Escape (3), inverted bar and hidden cursor (2), styled write path (1), verb set rebound (3), removal of `alt+*` and `ctrl+q`/`ctrl+o` (3), status line reworked for two modes (2), README rewritten (5). Configurability lands in Task 3 rather than being deferred, as the spec requires. All four of the spec's open questions are answered — the indicator in the spec itself, the other three in this plan's "Decisions" section.

**Type consistency.** `route`/`routeKind`/`router`/`parsePrefix` appear only in Tasks 3 and 4 and match. `WriteStyled`'s signature is identical in Tasks 1 and 2. `NewClient`'s fourth parameter is introduced in Task 2 (with main.go updated in the same task so the tree builds) and supplied for real in Task 3. `controlVerbs`, `controlTail` and `controlHelp` are used by the Task 2 tests exactly as Task 2 defines them. `spawn_in_pty`'s new `env` parameter is added and used within Task 4.

**One risk worth naming.** Task 4's cursor assertions depend on Ultraviolet's renderer emitting DECTCEM on change rather than every frame. If it emits per-frame, `cursor_visible` still reads the last state correctly, so the assertion holds either way — but if the renderer ever *coalesces* a hide-then-show inside one frame, `case_control_mode_is_visible_and_escapable` could see a stale state. The 0.8 s settle between keystrokes is many frames, so this is unlikely; if that case proves flaky, the fix is to assert on the presence of a hide after the prefix rather than on the last state, not to weaken it.
