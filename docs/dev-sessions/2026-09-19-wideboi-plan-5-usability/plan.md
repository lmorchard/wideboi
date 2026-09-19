# wideboi Plan 5 — Usability Repair Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the four defects that make the shipped v1 binary frustrating to actually use, the largest of which is that resize is not wired at all — leaving Plan 2's reflow work unreachable.

**Architecture:** Four independent repairs to existing code. No new packages, no new abstractions. Each lands with a wire-level acceptance case in `scripts/smoke.py`, because every defect here was found by running the binary and none by the existing suite.

**Tech Stack:** Go 1.27.1 · `charmbracelet/x/vt` (pinned) · `charmbracelet/ultraviolet` · Python 3 stdlib for the harness

**Spec:** No separate spec. The binding authority is `../2026-09-18-wideboi-v1-foundations/spec.md`; this plan repairs deviations from it. Forward-looking work is in `docs/BEYOND-V1.md`.

## Why this plan exists

v1 shipped with a green gate — `make check` runs fmt, vet, seam-check, unit tests, `verify-exit` across six signal/size combinations, nine smoke cases and a golden snapshot, and all of it passes. Four defects got through anyway, all four found within minutes of a human using the binary:

| # | Defect | How it got past the gate |
| --- | --- | --- |
| 1 | The status line renders the literal string `$mod` | No test reads the status line's text |
| 2 | `ctrl+w`/`ctrl+l`/`ctrl+n`/`ctrl+h` are swallowed, never reaching the pane | No test asserts that an *unbound* key still passes through |
| 3 | Resize is not wired: no pane's emulator or PTY is ever resized | No test resizes the host and then asks a pane its size |
| 4 | `go test -race` fails | `make test` doesn't pass `-race` |

That table is the point. Each fix below ships with the assertion that would have caught it.

## Global Constraints

- Module path: `github.com/lmorchard/wideboi`. Go 1.27.1.
- Platforms: macOS and Linux only. No Windows, no build tags for it.
- `github.com/charmbracelet/x/vt` is pinned to `v0.0.0-20260913004009-c615ff2f7805`. Never `@latest`. Do not move it.
- Geometry parameters are declared as stdlib `image.Rectangle` / `image.Point`.
- Every exit path must restore the host terminal and reap child processes.
- `make seam-check` allows exactly one crossing (`internal/server/term -> internal/client/compose`, test-only). **Do not add another.**
- `make check` must not modify the working tree. Verify with `git status` after running it.
- Each task ends with a commit.

### Verified facts — established by measurement, do not re-derive

- **`server.Pane.Resize` has zero callers.** `grep -rn '\.Resize(' internal/server/*.go` finds only its own definition. `protocol.MsgResize` is handled at `internal/server/server.go:95`, but that handler only stores `s.cols, s.rows` and re-broadcasts the layout.
- Consequently **`term.Reflow` is unreachable in the running binary.** It is reached only via `vtGrid.Resize` ← `server.Pane.Resize` ← nothing.
- **`ptyx.Pane.Resize` (the `TIOCSWINSZ` call) also has zero production callers.** Measured end to end: host 120×30 → 70×20, wideboi emitted 2458 bytes redrawing, and a pane's `stty size` still reported `29 59`.
- **`ctrl+w`, `ctrl+l`, `ctrl+n`, `ctrl+h` do not reach the pane.** Proven with `cat -v` inside a pane: none of `^W`, `^L`, `^N`, `^H` appear in the output.
- The literal `$mod` lives at `internal/client/client.go:129`.
- `go test -race ./internal/server/` fails on the known upstream `x/vt` race: `Emulator.Close()` writes `e.closed` (emulator.go:264) while the pump's `Read()` reads it (emulator.go:251). Documented at `internal/server/term/grid.go`. It is inert in practice but real, and it is fixable on our side by ownership rather than locking.

---

### Task 1: Tell the user which keys actually exist

**Files:**
- Modify: `internal/client/client.go`
- Create: `README.md`
- Modify: `scripts/smoke.py`

**Interfaces:**
- Consumes: `compose.WriteString`.
- Produces: no Go API change.

The status line currently reads `$mod+o switch   $mod+n new col   $mod+w cycle width   $mod+q quit`. Three things are wrong: `$mod` is a spec placeholder that was never substituted, `$mod+o` is a legacy fallback rather than the real binding, and six verbs are missing entirely.

- [ ] **Step 1: Write the failing smoke case**

Add to `scripts/smoke.py`, and register it in `CASES`:

```python
def case_status_line_names_real_keys(fail):
    s = Session()
    out = s.output()
    if b"$mod" in out:
        fail("status line renders the literal placeholder '$mod'")
    # The bindings the user actually has. If a binding changes, this
    # fails loudly and someone updates both together.
    for key in (b"alt+h", b"alt+l", b"alt+n", b"alt+q"):
        if key not in out:
            fail(f"status line never mentions {key.decode()}")
    s.quit_and_reap()
```

- [ ] **Step 2: Run it to verify it fails**

Run: `make build && python3 scripts/smoke.py --only "status line"`
Expected: FAIL — `status line renders the literal placeholder '$mod'`.

- [ ] **Step 3: Fix the status line**

In `internal/client/client.go`, replace the hardcoded help string. Keep it inside
the terminal width; truncate rather than wrap, since `compose.WriteString` drops
out-of-range writes silently and a too-long line would vanish without warning.

```go
	// Key help. These strings must match cmd/wideboi's binding matrix;
	// scripts/smoke.py asserts they do, so the two cannot drift apart
	// silently. "alt+" rather than a glyph because it has to be legible
	// in a terminal that may not render one, and because it is what a
	// user would type into their terminal's own key configuration.
	help := "  alt+h/l focus  alt+n new  alt+w width  alt+x kill  alt+j jump  alt+u/d scroll  alt+q quit"
	status += help
	if len(status) > c.cols {
		status = status[:c.cols]
	}
```

- [ ] **Step 4: Run the case to verify it passes**

Run: `make build && python3 scripts/smoke.py --only "status line"`
Expected: OK.

- [ ] **Step 5: Write the README**

There is no README. Create one, and make the Option-as-Meta requirement
prominent — it is the single thing most likely to make a new user conclude the
program is broken.

```markdown
# wideboi

A scrolling tiling terminal multiplexer for CLI coding agents. Panes keep their
width; open more and the viewport scrolls instead of squeezing what is already
there.

Status: v1. Working, and rough in places — see `docs/BEYOND-V1.md`.

## Build and run

    make build
    ./bin/wideboi

## Your terminal must send Option as Meta

wideboi's verbs are bound to `alt`. On macOS most terminals send Option as a
composed character (`˜`, `∆`) rather than as Meta, and **wideboi will appear to
ignore every shortcut** until you change that.

| Terminal | Setting |
| --- | --- |
| Terminal.app | Settings → Profiles → Keyboard → *Use Option as Meta Key* |
| iTerm2 | Settings → Profiles → Keys → Left Option key → *Esc+* |
| Ghostty | `macos-option-as-alt = true` |
| WezTerm | `send_composed_key_when_left_alt_is_pressed = false` |

To check: press `alt+n`. A new column should open.

## Keys

| Key | Action |
| --- | --- |
| `alt+h` / `alt+l` | focus left / right |
| `alt+n` | new column |
| `alt+w` | cycle column width |
| `alt+x` | kill focused pane |
| `alt+j` | jump to the pane that wants attention |
| `alt+u` / `alt+d` | scroll the focused pane's history |
| `alt+q` | quit |

Everything else goes to the focused pane, including `ctrl+w`, `ctrl+l` and
`ctrl+c`.

## Development

    make check    # fmt, vet, seam boundary, unit tests, exit contract, smoke
    make smoke    # scripted acceptance cases, asserted on the pty wire
    make race     # unit tests under the race detector

`docs/LESSONS.md` is worth reading before changing anything.
```

- [ ] **Step 6: Run the gate and commit**

```bash
make check
git status --porcelain   # must be empty
git add internal/client/client.go README.md scripts/smoke.py
git commit -m "fix(client): name the real keys in the status line

The help text rendered a literal '\$mod' -- spec prose copied into a
string -- advertised a legacy fallback as the primary binding, and
omitted six verbs. Adds a README making the Option-as-Meta requirement
prominent, since without it every shortcut silently does nothing."
```

---

### Task 2: Stop stealing keys the shell needs

**Files:**
- Modify: `cmd/wideboi/main.go`
- Modify: `scripts/smoke.py`

**Interfaces:**
- Consumes: `protocol.Verb*`, `cli.SendVerb`, `cli.SendKey`.
- Produces: no API change.

The binding matrix claims `ctrl+h`, `ctrl+l`, `ctrl+n` and `ctrl+w` as fallbacks
for focus-left, focus-right, new-column and cycle-width. All four are swallowed
and never reach the pane. They are also four of the most load-bearing keys in a
shell: delete-word-backward, clear-screen, next-history, and backspace on
terminals that send `^H`.

gwae took the opposite position deliberately — *"Ctrl+J/K always reach the pane;
gwae never claims them"* — and used Option as the universal modifier precisely so
that `ctrl` stays with the application.

Keep `ctrl+q` (quit) and `ctrl+o` (focus right): `ctrl+q` is XON flow control
almost nobody relies on interactively, and `ctrl+o` is rarely bound. Those two
give a usable escape hatch when Option-as-Meta is not configured, which is what
the fallbacks were for.

- [ ] **Step 1: Write the failing smoke case**

Add to `scripts/smoke.py` and register it in `CASES`:

```python
def case_shell_control_keys_pass_through(fail):
    # cat -v echoes control bytes visibly as ^X, so we can see exactly
    # which ones survive the multiplexer's binding matrix.
    s = Session()
    s.type("cat -v\r", settle=1.3)
    before = len(s.output())
    for byte in (b"\x17", b"\x0c", b"\x0e", b"\x08"):  # ctrl+w l n h
        os.write(s.fd, byte)
        time.sleep(0.5)
    s.type("\r", settle=1.2)
    seen = s.output()[before:]
    for name, mark in (("ctrl+w", b"^W"), ("ctrl+l", b"^L"),
                       ("ctrl+n", b"^N"), ("ctrl+h", b"^H")):
        if mark not in seen:
            fail(f"{name} was swallowed by the multiplexer; the shell needs it")
    s.quit_and_reap()
```

- [ ] **Step 2: Run it to verify it fails**

Run: `make build && python3 scripts/smoke.py --only "control keys"`
Expected: FAIL, naming all four keys.

- [ ] **Step 3: Drop the four fallbacks**

In `cmd/wideboi/main.go`, amend the binding matrix. Only these four match arms change:

```go
				// ctrl fallbacks are deliberately minimal. ctrl+w, ctrl+l,
				// ctrl+n and ctrl+h are delete-word, clear-screen,
				// next-history and backspace: claiming them makes every
				// shell in every pane worse. ctrl+q and ctrl+o survive as
				// the escape hatch for a terminal that is not sending
				// Option as Meta.
				case ev.MatchString("alt+q") || ev.MatchString("ctrl+q"):
					return nil
				case ev.MatchString("alt+h") || ev.MatchString("alt+left"):
					cli.SendVerb(ctx, protocol.VerbFocusLeft)
				case ev.MatchString("alt+l") || ev.MatchString("alt+right") || ev.MatchString("ctrl+o"):
					cli.SendVerb(ctx, protocol.VerbFocusRight)
				case ev.MatchString("alt+n"):
					cli.SendVerb(ctx, protocol.VerbNewColumn)
				case ev.MatchString("alt+w"):
					cli.SendVerb(ctx, protocol.VerbCycleWidth)
```

- [ ] **Step 4: Run the case to verify it passes**

Run: `make build && python3 scripts/smoke.py --only "control keys"`
Expected: OK.

Then confirm nothing else regressed — the existing `alt mod keybindings route verbs` case must still pass:

Run: `python3 scripts/smoke.py`
Expected: all cases OK.

- [ ] **Step 5: Commit**

```bash
make check
git add cmd/wideboi/main.go scripts/smoke.py
git commit -m "fix(cmd): stop claiming ctrl+w/l/n/h

They were fallbacks for a terminal without Option-as-Meta, but they are
delete-word, clear-screen, next-history and backspace -- claiming them
degrades every shell in every pane. ctrl+q and ctrl+o remain as the
escape hatch."
```

---

### Task 3: Wire resize end to end

The largest defect. Nothing resizes a pane — not its emulator, not its PTY. A
pane is sized once at spawn and keeps that size forever.

Two consequences. The child never receives `SIGWINCH`, so a full-screen app lays
out at the wrong width and a shell keeps wrapping at the old one. And
`term.Reflow` — the whole of Plan 2, its spike, its hazard handling and its
un-skipped tests — is **never executed in the running binary**.

**Files:**
- Modify: `internal/server/server.go`, `internal/server/pane.go`
- Modify: `scripts/smoke.py`
- Test: `internal/server/server_test.go`

**Interfaces:**
- Consumes: `layout.Strip.ComputePlacements(cols, rows) []layout.Placement`, `ptyx.Pane.Resize(cols, rows) error`, `term.Grid.Resize(cols, rows)`.
- Produces: `server.Pane.Resize(cols, rows int) error` — now returns an error (the `TIOCSWINSZ` call can fail) and resizes **both** the emulator and the child's PTY.

- [ ] **Step 1: Write the failing smoke case**

This is the assertion whose absence let the defect ship. Add to `scripts/smoke.py`
and register it in `CASES`:

```python
def case_host_resize_resizes_panes(fail):
    # A pane's child must learn its new size, or it keeps wrapping at the
    # old width and full-screen apps lay out wrong.
    s = Session(cols=120, rows=30)
    s.type("stty size\r", settle=1.4)
    first = re.findall(rb"(\d+) (\d+)", s.output())
    if not first:
        fail("could not read the pane's initial size")
        s.quit_and_reap()
        return
    before = first[-1]

    fcntl.ioctl(s.fd, termios.TIOCSWINSZ, struct.pack("HHHH", 20, 70, 0, 0))
    time.sleep(1.5)
    mark = len(s.output())
    s.type("stty size\r", settle=1.6)
    after = re.findall(rb"(\d+) (\d+)", s.output()[mark:])
    if not after:
        fail("pane produced no size output after the host resized")
    elif after[-1] == before:
        fail(f"pane size unchanged after host resize: {before} -- SIGWINCH never reached the child")
    s.quit_and_reap()
```

Add `import fcntl`, `import struct`, `import termios` and `import re` to `scripts/smoke.py` if absent.

- [ ] **Step 2: Run it to verify it fails**

Run: `make build && python3 scripts/smoke.py --only "resize"`
Expected: FAIL — `pane size unchanged after host resize`.

- [ ] **Step 3: Make `Pane.Resize` resize the child too**

In `internal/server/pane.go`:

```go
// Resize changes the pane's logical size: the emulator's grid and the
// child's PTY window, in that order.
//
// The emulator first because term.Reflow reads its cells out, reflows
// them and writes them back, so the grid must be consistent before the
// child is told to redraw against it. The child second because
// TIOCSWINSZ raises SIGWINCH, and a well-behaved full-screen app repaints
// immediately.
func (p *Pane) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return fmt.Errorf("pane %d: refusing resize to %dx%d", p.id, cols, rows)
	}
	if cols == p.cols && rows == p.rows {
		return nil
	}
	p.cols, p.rows = cols, rows
	p.grid.Resize(cols, rows)
	return p.pty.Resize(cols, rows)
}
```

The non-positive guard matters: Plan 1 shipped a startup panic because
`paneRows` reached `-1` on a pty with no winsize, and `uv.NewBuffer` panics on a
negative dimension rather than erroring.

- [ ] **Step 4: Call it when the layout changes**

In `internal/server/server.go`, add a helper and call it from every path that
changes geometry. Placements already carry each pane's destination rect, which is
its logical size.

```go
// resizePanesLocked pushes each pane's current placement size down to its
// emulator and child. Call it after anything that changes geometry: a host
// resize, a new column, a width cycle, a pane closing.
//
// Errors are collected rather than returned: one child failing TIOCSWINSZ
// must not stop the others from being resized.
func (s *Server) resizePanesLocked() {
	for _, pl := range s.strip.ComputePlacements(s.cols, s.rows) {
		p, ok := s.panes[pl.PaneID]
		if !ok {
			continue
		}
		w, h := pl.Dest.Dx(), pl.Dest.Dy()
		if err := p.Resize(w, h); err != nil {
			log.Printf("wideboi: resize pane %d to %dx%d: %v", pl.PaneID, w, h, err)
		}
	}
}
```

Call `s.resizePanesLocked()` immediately before `s.broadcastLayoutLocked(ctx)` in
the `protocol.MsgResize` handler, and in the `VerbNewColumn`, `VerbCycleWidth`
and `VerbKillPane` arms — every place a pane's share of the viewport can change.

Check whether `s.panes` is a map keyed by pane ID and adapt the lookup if not.
Add `"log"` and `"fmt"` to imports as needed.

- [ ] **Step 5: Write the unit test**

Add to `internal/server/server_test.go`:

```go
// The emulator and the child must both learn a new size. Before this,
// Pane.Resize had no callers at all and term.Reflow was dead code in the
// running binary.
func TestResizePropagatesToPanes(t *testing.T) {
	srv, cli, cleanup := newTestServer(t)
	defer cleanup()

	cli.send(protocol.MsgResize{Cols: 100, Rows: 40})
	waitFor(t, func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		for _, p := range srv.panes {
			c, r := p.Size()
			if c <= 0 || r <= 0 || (c == 0 && r == 0) {
				return false
			}
		}
		return len(srv.panes) > 0
	})

	srv.mu.Lock()
	defer srv.mu.Unlock()
	for id, p := range srv.panes {
		c, r := p.Size()
		if c > 100 || r > 40 {
			t.Errorf("pane %d has size %dx%d, larger than the 100x40 viewport", id, c, r)
		}
	}
}
```

Adapt `newTestServer`, `cli.send` and `waitFor` to whatever the existing tests in
that file use — read `TestServerLifecycleAndAttach` first and follow its shape
rather than inventing helpers.

- [ ] **Step 6: Verify reflow is now reachable**

The point of this task is that Plan 2's work becomes live. Confirm it:

```bash
make build
python3 scripts/smoke.py --only "resize"
```

Expected: OK — the pane reports a different size after the host resize.

Then confirm text survives a narrow/widen round trip through the *real* binary,
which was previously unverifiable because no pane ever resized. Record the
result in your report.

- [ ] **Step 7: Run the gate and commit**

```bash
make check
git status --porcelain   # must be empty
git add internal/server/ scripts/smoke.py
git commit -m "fix(server): wire resize through to emulators and child PTYs

server.Pane.Resize had zero callers: no pane's emulator or PTY was ever
resized, so children never received SIGWINCH and kept wrapping at their
spawn width. It also meant term.Reflow -- the whole of Plan 2 -- was
unreachable in the running binary."
```

---

### Task 4: Make the emulator lifecycle race-free, and let the gate see races

`go test -race ./internal/server/` fails today. The race is upstream's —
`SafeEmulator` doesn't override `Close`, so the promoted `(*Emulator).Close`
writes `e.closed` with no lock while the pump's `Read` reads it — and we
documented it in Plan 1 as inert but real.

It is fixable on our side without touching upstream and without adding a lock: give
the pump goroutine sole ownership of the emulator's lifecycle, so `Close` and
`Read` never run concurrently. Locking would deadlock instead, because `Read`
blocks indefinitely.

**Files:**
- Modify: `internal/server/pane.go`, `Makefile`

**Interfaces:**
- Consumes: `term.Grid.Close()`, `Pane.closed` (the existing `chan struct{}`).
- Produces: no API change. `Pane.Close` no longer calls `grid.Close` directly.

- [ ] **Step 1: Confirm the race, so the fix has a baseline**

Run: `go test -race -count=1 ./internal/server/ 2>&1 | head -20`
Expected: `WARNING: DATA RACE`, with `Emulator.Close` writing and `Emulator.Read` reading.

Record that output in your report — it is the "red" half of this task.

- [ ] **Step 2: Move the close into the reader goroutine**

In `internal/server/pane.go`'s `Start`, the emulator→PTY pump is the goroutine
blocked in `grid.Read`. Have it close the grid on its way out, and have
`Pane.Close` ask it to stop rather than closing the grid itself.

In the emulator→PTY pump's `defer`, after the existing `recover` handling:

```go
		// This goroutine owns the emulator's lifetime. Closing here rather
		// than in Pane.Close means Close and Read are never concurrent,
		// which avoids x/vt's unsynchronised write to e.closed. A mutex
		// would not work: Read blocks indefinitely, so a Close waiting on
		// it would deadlock.
		defer func() { _ = p.grid.Close() }()
```

Then in `Pane.Close`, replace the direct `p.grid.Close()` call. The `closed`
channel already exists and is already closed there; the PTY teardown unblocks the
pump, which then closes the grid:

```go
	errs := []error{p.pty.Kill(CloseGrace)}
	// The emulator is closed by the pump goroutine that owns it; ptyx.Kill
	// closes the PTY master, which unblocks that pump. Wait briefly so the
	// close has landed before Close returns.
	select {
	case <-p.gridClosed:
	case <-time.After(time.Second):
		errs = append(errs, fmt.Errorf("pane %d: emulator did not close within 1s", p.id))
	}
```

Add a `gridClosed chan struct{}` to `Pane`, created in the constructor and closed
by the pump after it closes the grid. Verify the ordering claim yourself: does
`ptyx.Kill` closing the master actually unblock `grid.Read`? Read Plan 1's
finding that `Kill` always closes `Master`, then confirm it holds here. **If it
does not, stop and report rather than adding a timeout that masks a hang.**

- [ ] **Step 3: Verify the race is gone**

Run: `go test -race -count=1 ./... 2>&1 | tail -10`
Expected: all packages ok, no `DATA RACE`.

- [ ] **Step 4: Put `-race` in the gate**

In the `Makefile`:

```makefile
# The race detector belongs in the gate: a data race that only appears
# under load is exactly what a green suite hides. Roughly 3x slower than
# plain `test`, which is worth it here.
race:
	go test -race -count=1 ./...
```

Add `race` to `check`'s prerequisites, after `test`.

- [ ] **Step 5: Update the stale comment**

`internal/server/term/grid.go`'s `Close` comment says the race "will flag it for
anyone who runs this path with the race detector." That is no longer true of our
code. Rewrite it to say the upstream defect is unchanged, that we avoid it by
giving the pump goroutine sole ownership of the emulator lifecycle, and that
calling `Grid.Close` from anywhere else would reintroduce it.

- [ ] **Step 6: Run the gate and commit**

```bash
make check
git status --porcelain   # must be empty
git add internal/server/pane.go internal/server/term/grid.go Makefile
git commit -m "fix(server): give the pump goroutine the emulator's lifetime

x/vt's SafeEmulator does not override Close, so the promoted
(*Emulator).Close writes e.closed unsynchronised while Read reads it.
Ownership fixes it where a mutex cannot -- Read blocks indefinitely, so
locking would deadlock. Adds -race to the gate, which would have caught
this."
```

---

## Done criteria

- `make check` passes, now including `race`, and does not modify the tree.
- `make smoke` reports 12 passed (9 existing + 3 new) plus a matching golden snapshot.
- Each new smoke case has been observed failing before its fix and passing after.
- The status line names real keys; no `$mod` anywhere in the binary's output.
- `ctrl+w`, `ctrl+l`, `ctrl+n` and `ctrl+h` reach the focused pane.
- A host resize changes what `stty size` reports inside a pane.
- `go test -race ./...` is clean.
- `README.md` exists and documents the Option-as-Meta requirement.

## Out of scope

Everything in `docs/BEYOND-V1.md`, and in particular:

- **Animation.** The original motivating feature, still unbuilt. It deserves its own plan; do not start it here.
- The parked wedge (a child that stops reading stdin freezes rendering).
- The parked leak (a root that exits before `Kill`).
- Linux `ps` verification — needs a machine, not a task.
- Scrollback reflow for display.

## Process note for whoever executes this

Plan 1 kept an execution ledger of every ruling and parked finding; Plans 2–4
did not, and three plans' worth of decisions were lost with their scratch
workspace. **Keep a ledger this time**, and promote it into this directory before
deleting the workspace. `docs/dev-sessions/2026-09-18-wideboi-v1-foundations/execution-ledger.md`
is the format.
