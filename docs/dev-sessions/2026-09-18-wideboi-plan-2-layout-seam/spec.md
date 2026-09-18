# wideboi Plan 2 — layout core, the seam, input

**Date:** 2026-09-18
**Parent spec:** `../2026-09-18-wideboi-v1-foundations/spec.md` — still the binding architecture document
**Plan 1 ledger:** `../2026-09-18-wideboi-v1-foundations/execution-ledger.md` — 26 rulings, 29 deferred/parked findings
**Status:** draft, pending review

## What Plan 2 is

Milestones 5–7 of the v1 spec:

5. `internal/layout` — the pure core. Strip, columns, preset widths, `ScrollStrategy`, `[]Placement`. No I/O, stdlib only, property-tested.
6. Cut the seam — move state behind `internal/protocol` and `internal/transport.InProc`.
7. Focus, input routing, `$mod` keybindings.

The deliverable is a real scrolling multiplexer: open columns past the screen edge, move between them without anything shrinking, all state behind a transport boundary that a socket can later replace.

## What Plan 1 handed over

Working and merged: `internal/hostterm` (terminal restore on every exit path), `internal/server/ptyx` (PTY spawn, three-route process-tree teardown), `internal/server/term` (VT emulation behind a six-method `Grid`), `internal/client/compose` (off-screen surfaces, painter's-algorithm blit), and a two-pane binary with a hardcoded layout.

Explicitly temporary, to be dismantled here:

- `internal/client/pane.go` holds PTY, emulator, and surface in one struct. Plan 2 splits it across the seam: `pty` + `grid` server-side, `surface` + `dirty` client-side.
- `cmd/wideboi/main.go` computes placement arithmetic inline. That moves into `internal/layout`.
- `make seam-check` allowlists three Plan 1 boundary violations. Plan 2 should retire two of them and leave the allowlist empty or near-empty.

## The emulator decision — RESOLVED by spike, 2026-09-18

Plan 1 established that the pinned `x/vt` does not reflow on resize. A spike
(`/tmp/reflow-spike`, throwaway) probed fixing it in our own adapter behind
`Grid`, and turned up a fact that reframes the whole problem.

### `Resize` damages only the visible screen. Scrollback is untouched.

Measured directly. Write eight long lines into a 60x4 emulator so most of them
scroll into history, then narrow to 40 and grow back:

```
BEFORE narrow (60):  scrollback[0]: "line01-...-BBBBBBBBBBBBBBBBBBBB-tail01"  full
AFTER Resize(40,4):  scrollback[0]: "line01-...-BBBBBBBBBBBBBBBBBBBB-tail01"  still full
                     screen[0]:     "line06-...-BBBBBBBBBBBB"                 chopped
AFTER back to 60:    screen[0]:     "line06-...-BBBBBBBBBBBB"                 never returns
```

So the destructive case is narrower than feared: **only the screenful visible at
the moment of narrowing is lost.** Everything already scrolled into history
survives at its original width.

That leaves a presentation problem in place of a data-loss one: scrollback holds
lines wider than the viewport, which will need reflowing **for display** when
Plan 4 adds scroll-back navigation. Non-destructive, and deferrable at no
accumulating cost.

### What the division of labour actually is

An app owns what it is currently drawing; the terminal owns what has already
been written. Once a program emits text, it forgets it — only the terminal holds
it, and nothing can ask for it again.

- **Alt-screen apps** (`vim`, `top`, `htop`) repaint everything from their own
  model on `SIGWINCH`. Nothing for us to do; skip reflow entirely.
- **Shells and line-oriented programs** redraw only the current input line.
  Output scrolled above is ours. Demonstrated: a child that received `SIGWINCH`
  and kept working perfectly still had its earlier output permanently chopped.

Real terminals differ here — iTerm2, kitty and Terminal.app reflow; `xterm`
historically does not. "Accept lossy" is a legitimate product choice, not a
failure. It is rejected here only because this tool exists to hold long-running
agent output.

### Decision: reflow the visible screen only

| scenario | cost | disposition |
| --- | --- | --- |
| alt screen | 0 | skip; the app repaints |
| visible screen | **0.83 ms** | **reflow in the adapter — Plan 2** |
| scrollback | 177 ms if rebuilt | **do not touch**; undamaged, and Plan 4 reflows for display |

The 177 ms figure drove an earlier assumption that debouncing and incremental
reflow were mandatory. Since scrollback is not damaged, that work is not needed
at all — the real cost of fixing the real bug is sub-millisecond, on every
resize event, with no debouncing.

Mechanism: on `Grid.Resize`, read the screen cells out, rejoin wrapped runs
using the "row's last cell is non-blank, so it probably soft-wrapped" heuristic,
resize, re-split at the new width, and write back with `SetCell`. Verified in
the spike: bold/colour styles survive (`attrs=1 fg=196`), wide CJK glyphs
round-trip, and it fixes the blank-after-resize defect for free because
`SetCell` re-touches the lines that `Screen.Resize` cleared.

### Three hazards the spike found — all must be handled

1. **Blank rows must not be priced as full-width logical lines.** The naive
   implementation treated the cursor's own blank row as a wrapped continuation
   and pushed real, still-fitting content off the visible screen. Silently. This
   is worse than the documented heuristic failure and needs a used-vs-filler row
   model.
2. **Never write a wide glyph's placeholder cell explicitly.** It trips
   `uv.Line.Set`'s partial-overwrite protection and blanks the whole glyph.
   Advance by each cell's own `Width`.
3. **There is no public cursor setter.** `setCursor` is unexported, so restoring
   the cursor after reflow requires injecting a CUP sequence through `Write`,
   which must land before real PTY output resumes. Likely mitigated because
   `SIGWINCH` makes most programs reposition themselves — but untested, and the
   fallback is fragile.

The known heuristic failure — a hard line break landing at exactly the pane
width, joining two lines — is cosmetic and accepted.

## Open question: how much does this matter for coding agents?

The target workload is CLI coding agents, and how much reflow matters depends on
whether they use the alternate screen.

- A full-screen TUI agent repaints itself; we are off the hook.
- An agent using the primary screen with a live region (the Ink pattern, which
  Claude Code appears to use — its transcript stays in your scrollback after
  quitting) commits completed output upward into terminal-owned scrollback and
  redraws only the live region. Same split as a shell, larger live region.

Unverified per agent. Cheap check: run each in a pty and look for `ESC[?1049h`
in its first output. Worth doing before Plan 4 sizes the scrollback-display work.

## Testing strategy

Plan 1's tests were thorough and still missed two user-visible defects — every shifted key was silently dropped, and no cursor was ever rendered. Both were found in minutes of manual use, after eight reviews. The strategy below is derived from *why* they were missed, not from general testing principle.

### Assert on the wire, not on internal state

Every rendering assertion in Plan 1 ran against `compose.Text(surface)` — our own cell buffer. **The cursor is not a cell**; it is an out-of-band escape sequence, so no assertion could ever have failed for its absence. The bytes wideboi writes to the host pty are the product. Everything else is an implementation detail we happened to choose.

`scripts/ptycheck.py` already drives a real pty and drains the master. It only asserts about exit status today.

### Three additions, before feature work

**1. `make smoke` — a scripted pty acceptance runner.** Extend `ptycheck.py` into a tool that launches the binary in a pty, sends scripted input, and asserts on the bytes that come back. Every feature lands with a case. Would have caught the shift bug on the first run.

**2. Exhaustive key-encoding coverage.** Printable ASCII × modifier combinations is a small finite space — a loop, not a property test. The invariant is one line: *every key event carrying non-empty `Text` must produce at least one byte*. That single assertion covers the whole class, including the shifted-arrow variants nobody has tried. Add the round-trip property alongside it: for printable input, `bytes → decode → encode → bytes` must be the identity, which needs no enumeration at all and catches future encoding gaps for free.

**3. Golden wire snapshots.** Capture the full byte stream for a scripted session and commit it. A human reads it once and notices what *isn't* there; thereafter every change shows as a diff. This is specifically the technique that surfaces omitted behaviour, which is the class table tests structurally cannot catch.

### A user-journey list the acceptance tests derive from

The cursor was not a test gap — it was a **requirements gap**. Neither the spec nor the plan ever said "render a cursor", so no test could fail and no reviewer could flag it. Plan 2 keeps a short list of what a user actually does, written from the product rather than the package list, and `make smoke` derives from it:

- launch, see two panes, see where the cursor is
- type, see what you typed, in the pane you are focused on
- move focus, watch the cursor follow
- open a column past the screen edge, scroll to it, come back
- resize the window, have the layout still make sense
- run a full-screen app; have it lay out at pane width
- quit; get the terminal back, with nothing left running

### Run the thing, early and often

Plan 1 spent roughly 25 subagents and 8 reviews, each more sophisticated than the last at verifying *the code against the plan* — two artifacts that shared identical blind spots. Ten minutes of real use found two defects none of it could. Execution is the only check that does not inherit the author's assumptions.

`make smoke` must run in seconds and be wired into `make check`, so it is exercised continuously rather than at the end.

## Inherited items from Plan 1

Plan 2's implementation plan splits these across Plans 2-4 and re-homes each
explicitly; see its "Inherited items this plan does NOT take" table. The
column below records when each becomes *unavoidable*, not which plan owns it.

| Item | Source | Disposition |
| --- | --- | --- |
| Emulator reflow + blank-after-resize | Plan 1 Task 9 | **Blocker.** Resolve before milestone 5. |
| Host-window resize unimplemented | Plan 1 final review I7 | In scope — milestone 5/7. |
| No tests in `internal/client` | Plan 1 final review M1 | In scope — the seam gets covered as it is built. |
| Wedge: a child that stops reading stdin freezes rendering | Plan 1, parked | In scope. Root cause is the pty-writer pump parking in `Master.Write`; fix is a bounded write on the master, trading dropped child-bound bytes. |
| Leak: root exits before `Kill`, escapees never signalled | Plan 1, parked | In scope. Needs a descendant snapshot maintained while the root is alive — the server owns pane lifecycle here, which is where it belongs. |
| `ps -axo` portability on Linux unverified | Plan 1 final review M7 | Precondition. The anti-leak guarantee degrades silently if `Descendants` returns a short list. Needs one real Linux run. |
| `compose.Text` / `WriteString` ignore `Cell.Width` | Plan 1 Task 7 | Comes due when status glyphs (`»`, `✓`) land — Plan 4, but `WriteString` is what a reader reaches for sooner. |
| `seam-check` allowlist | Plan 1 final review I4 | Retire the two client→server entries. |

## Non-goals for Plan 2

Unchanged from the v1 spec: card layout, detach/reattach over a socket, config file, Kitty graphics, self-update, hot reload, Windows. Agent status, scrollback navigation, and mouse remain Plan 4. Animation remains Plan 3 — but note that Plan 2's `[]Placement` output is what Plan 3 animates, so the placement type must be right here.

## Open questions

1. ~~Which emulator option.~~ **Resolved by spike** — reflow the visible screen
   in the adapter; leave scrollback alone. See above.
2. **Does width-cycling ship in Plan 2?** Now nearly free either way: the layout
   verb is trivial arithmetic in the pure core, and the resize it triggers is the
   same sub-millisecond screen reflow that host-window resize already needs.
   Leaning yes, since it is the signature verb of the scrolling model and Plan 3
   will want to animate it.
3. **What is `$mod`?** The v1 spec notes gwae uses Option universally on macOS,
   but over SSH that depends on the client terminal sending Meta. Plan 1 verified
   `alt+l` encodes as `\x1bl`, so the mechanism works locally; the open part is
   the remote story and whether a different `$mod` is needed per platform.
4. **Do the target agents use the alternate screen?** See the section above —
   determines how much the reflow work matters in practice. One-line check per
   agent, worth doing before Plan 4.
