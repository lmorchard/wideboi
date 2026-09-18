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

## The emulator decision comes due here, and earlier than expected

Plan 1 established that the pinned `x/vt` **does not reflow**: narrowing a pane truncates each line and widening pads with blanks, so text is destroyed permanently. A second defect compounds it — `Emulator.Draw` paints nothing after a `Resize` until lines are touched again, so a resized pane renders blank.

The decision was to keep `x/vt` and revisit when width-cycling made reflow real. **That framing was too narrow.** Plain host-window resize also requires resizing every pane's emulator, and host-window resize is already broken in the merged binary (the screen buffer resizes; panes and their PTYs do not). So Plan 2 must call `Grid.Resize` in production code regardless of whether width-cycling ships, and the moment it does, both defects fire.

This is therefore a Plan 2 blocker, not a Plan 2 nice-to-have, and it should be resolved **before** milestone 5 rather than discovered during milestone 7. The four options from Plan 1, unchanged:

1. Keep `x/vt`, accept destroyed text on narrow. Cheapest; makes width-cycling and window resize quietly lossy, in a tool for long-running agent output. Hard to defend.
2. Own the grid behind `Grid` — keep `x/vt` for VT parsing, implement our own cell storage, resize, and reflow. Substantial, and effectively the "write the emulator" fork declined at the outset, scoped down to the grid half.
3. Fix reflow upstream in `charmbracelet/x/vt`. Best outcome for everyone; blocked on someone else's review cycle.
4. Switch emulators. No good Go target is known.

A cheap partial exists for the second defect independent of the first: force a full redraw after `Resize` by touching every line, which fixes blank-after-resize without addressing reflow. Worth doing either way.

**Open question, to be answered first:** which option. This spec does not pick one.

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

## Inherited items that come due in Plan 2

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

1. **Which emulator option.** Blocking; see above.
2. **Does width-cycling ship in Plan 2?** The layout verb is trivial arithmetic in the pure core, so it costs nothing to *implement*; the cost is entirely in the emulator resize it triggers. Once question 1 is answered this is nearly free either way.
3. **What is `$mod`?** The v1 spec notes gwae uses Option universally on macOS, but over SSH that depends on the client terminal sending Meta. Plan 1 verified `alt+l` encodes as `\x1bl`, so the mechanism works locally; the open part is the remote story and whether a different `$mod` is needed per platform.
