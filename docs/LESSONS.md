# Working lessons for this repo

Evergreen. Not session-scoped — session artifacts live under `docs/dev-sessions/`.
Add to this when the project teaches you something that would cost the next person
a wasted round.

## The dependencies are pre-1.0 and do not behave as you would assume

`charmbracelet/x/vt` has **no tagged release** and is pinned to a pseudo-version.
`charmbracelet/ultraviolet` is pre-1.0. Between them they have surprised us at
least eight times, each of which would have cost a fix round if it had been
discovered during implementation rather than during planning.

**Probe before you plan.** A throwaway Go program in `/tmp` that imports the
pinned versions and prints what an API actually does takes two minutes. Things
found that way, none of which were guessable:

- `*uv.Buffer` does not satisfy `uv.Screen` — it lacks `WidthMethod()`. `uv.ScreenBuffer` does.
- `uv.KeyPressEvent.String()` returns a display name (`"ctrl+q"`), not forwardable bytes.
- `SendKey` writes to an `io.Pipe` and **blocks until something reads**; call it without a concurrent drain and the process deadlocks.
- `SendKey` emits **nothing at all** for any event carrying `ModShift`. Use `SendText` for printable input — but not when Ctrl/Alt is held, since those change the encoding.
- `uv.Buffer.Resize` truncates on narrow (`Lines[i][:width]`) and there is no soft-wrap metadata anywhere (`type Line []Cell`), so correct reflow is impossible and a heuristic is required.
- `Emulator.Draw` paints only `Touched()` lines and `Screen.Resize` clears `Touched`, so a pane renders blank after a resize until something re-touches it.
- Writing a wide glyph's placeholder cell explicitly trips `uv.Line.Set`'s partial-overwrite protection and blanks the whole glyph. Advance by each cell's own `Width`.
- There is no public cursor setter — `setCursor` is unexported.

The `term.Grid` interface exists precisely so upstream surprises stay confined to
one file. Keep it narrow, and fix upstream gaps behind it rather than forking.

## Resize damages the visible screen only — scrollback is untouched

Measured, not assumed. This is why `term.Reflow` handles the screen and
deliberately leaves history alone: repairing the screen costs 0.83 ms, rebuilding
scrollback costs 177 ms, and scrollback is not damaged in the first place. It will
need reflowing *for display* when scroll-back navigation lands, which is a
presentation concern and non-destructive to defer.

Full reasoning: `docs/dev-sessions/2026-09-18-wideboi-plan-2-layout-seam/spec.md`.

## What a terminal owes its children, and what it does not

An app owns what it is currently drawing; the terminal owns what has already been
written. Once a program emits text it forgets it — only the terminal holds it.

- **Alt-screen apps** (`vim`, `top`, `htop`) repaint from their own model on `SIGWINCH`. Skip reflow entirely; anything we do is overwritten.
- **Shells and line-oriented programs** redraw only the current input line. Everything scrolled above is ours, and no signal can ask for it back.

A child receiving `SIGWINCH` and working perfectly is not evidence that resize was
handled — it only means the child adapted. Check what happened to the text.

## Test at the wire, and prove every test can fail

Two user-visible defects shipped in Plan 1 past eight reviews: every shifted key
was dropped, and no cursor was ever rendered. Both were found in ten minutes of
manual use. The rendering tests asserted against our own cell buffer, where a
cursor cannot appear, so no assertion could have failed.

- `make smoke` asserts on the bytes wideboi writes to the pty. New features get a case there.
- `scripts/ptycheck.py` asserts the signal-exit contract under a real pty.
- `testdata/golden/` holds a wire snapshot — the technique that catches *omitted* behaviour, which table tests structurally cannot.

Three separate times a test here passed against the bug it was written to catch.
**Break the thing a new test guards, watch it go red, restore it.** For finite
input spaces, enumerate in a loop rather than hand-picking rows.

## Teardown is the load-bearing guarantee

A multiplexer that leaks background processes is worse than useless — the work
keeps burning CPU with no window left to find it in. `ptyx.Kill` does three kills
per pane because each catches processes the others miss, and the ordering is
deliberate: `closePanes` runs **before** the render lock, so children are reaped
even when rendering is wedged against a stalled consumer.

Known parked gaps, recorded with reasoning in the v1 spec: a root that exits
before `Kill` leaves escapees unsignalled, and `ps -axo` parsing is unverified on
Linux.

## Reviews verify code against the plan — and one author wrote both

Model selection that worked: cheap models where the plan contains the complete
code, mid-tier where debugging judgment is needed, **the most capable model for
reviews of concurrent or safety-critical code**. That last one repeatedly caught
what cheaper reviews did not.

But no review tier substitutes for running the binary. When the same author writes
the plan and the code, the review inherits both blind spots.

## `CellAt` returns a live pointer. Clone before you keep it.

This bug shape has now appeared **three times** in this repo, which makes it a
hazard of the API rather than three coincidences.

`uv.Line` is `[]Cell` and `Line.At` is `return &l[x]`. So `CellAt`,
`ScrollbackCellAt` and friends hand you a pointer **into the emulator's live
backing array** — and `SafeEmulator` releases its read lock before you get it.
Two consequences, both of which bit us:

1. **Hold it past the lock and it is a data race.** `Draw`'s scrollback branch
   dereferenced one while a pump goroutine mutated the same slot.
2. **Hold it across a `Resize` and it is silent data loss.** `uv.Buffer.Resize`
   narrows by reslicing in place, so the arrays survive. A capture-then-reflow-
   then-write-back loop therefore clobbers source rows that later output rows
   still need, and the first line smears over everything below it.

The second one destroyed pane content on every narrowing, survived an entire
plan plus twelve reviews, and was found only by driving the real binary.

The rule: **if a cell pointer outlives the call that produced it, copy the
cell.** `cc := *c` is enough — `Line.Set` copies by value, and the emulator
replaces whole cells rather than mutating their fields.

## Never write the terminal's last column

Ultraviolet brackets a write to the final column with autowrap-toggle escapes
(`ESC[?7l` … `ESC[?7h`). On screen this is invisible. On the wire it splits your
text across escape sequences, so a raw-byte assertion sees `alt+` and `q` rather
than `alt+q`. Truncate to `cols - 1` and leave the last cell alone.

Related: measure truncation in **cells**, not bytes or runes.
`compose.WriteString` advances by `Cell.Width`, so a double-width glyph consumes
two columns while `len()` counts three bytes and a rune count counts one. All
three disagree, and only `Cell.Width` is right.

## `go test` caches, and a cached pass looks exactly like a real one

An implementer here re-ran a concurrency test after a change, saw 20/20 clean,
and nearly reported it. It was the result cache. With `-count=1` the true rate
was 14 failures in 15 runs.

**Always pass `-count=1`** when a test's outcome depends on code you just
changed, on scheduling, or on the race detector. `make race` does this; ad-hoc
runs must too.

## The shape of your test fixture is part of your coverage

Every reflow test in this repo wrote exactly **one line** of content. With one
line, the aliasing bug above is benign — the destination row was blank, so
nobody's source got clobbered. That single-line fixture shape is the entire
reason a content-destroying bug survived a plan and twelve reviews.

When a bug class depends on interaction *between* rows, columns, panes or
messages, a fixture with one of the thing cannot see it. Ask what the fixture's
shape makes structurally invisible, not just whether the assertion is right.
