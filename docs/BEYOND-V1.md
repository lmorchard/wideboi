# Beyond v1

Things deliberately left out of v1, things parked with reasoning, and things we
explored in design and want to keep. Not a commitment — a record, so ideas don't
have to be rediscovered and decisions don't have to be re-argued.

v1 is: a scrolling tiling multiplexer with a client/server seam, a pure layout
core, `$mod` verbs, OSC 133 agent status, scrollback navigation, and
non-destructive resize. See `docs/dev-sessions/` for how it got there and
`docs/LESSONS.md` for what it taught us.

---

## 1. The motivating feature that isn't built yet: animation

**This is the largest gap, and it is the thing the project was started for.**

The original brief was gwae's scrolling tiling *"with more lightly animated
feedback when switching between terminals."* The v1 spec designed it in detail.
It was scoped as its own plan, then the plan numbering shifted during execution
and it fell out. Today `scrollX` snaps instantly — `internal/layout/layout.go`
recomputes it and the next frame draws at the new offset.

Everything it needs already exists. The design, decided and recorded:

- **Springs on placements, per pane** — not on a global scroll offset. A scalar cannot express a column opening, a killed column collapsing, or a card re-deal; per-pane springs get all three for the same work. `[]Placement` is already the layout core's output type, chosen for exactly this.
- **Retarget in flight.** A nav key pressed mid-transition moves logical focus immediately and updates the spring *target*. Five fast `$mod+l` presses produce one smooth glide to the final column, not five chained hops. The spring holds `(current, velocity, target)` so retargeting is an assignment.
- **Keystrokes route to the destination pane immediately.** The screen is frozen during the transition anyway, so buffering would only add latency to a round trip the multiplexer already sits on twice.
- **Content freezes during motion.** PTY readers keep draining so no child ever blocks; the compositor renders the pre-transition snapshot until the springs settle, then repaints once. Per-frame cost during a transition is therefore pure placement math with zero emulator work.
- **Animation runs client-side.** Over SSH a nav keypress sends one small message upstream and the client animates locally against cell data it already holds. Server-side animation would push 60 full-screen repaints per second down the wire.

Accepted consequence: a pane that clears and redraws during the freeze pops into
its new state rather than transitioning to it. Tolerable at ~200 ms.

Constraint this buys: the client needs mirrors for visible panes **plus the
columns it is about to scroll past**, or it animates into blank space.

Open: the frame budget. Horizontal scroll is a full repaint — terminals cannot
blit horizontally — so every animation frame redraws everything. gwae budgets
under 4 ms for a 300×80 viewport and gates its scroll animation on synchronized
updates plus frame budget. Ours should measure before committing to 60 fps.

### A cheaper first cut: wipes instead of motion

Motion is expensive for a structural reason. Terminals cannot blit
horizontally, so every frame of a scroll animation is a **full repaint** — an
N-frame spring costs N screen redraws, which is what gwae gates its own scroll
animation on and what makes motion painful over SSH.

A **wipe** inverts that. Reveal the destination frame progressively, cell by
cell, and each frame only writes the cells that change *in that frame*. The
total across the whole transition is roughly one repaint's worth of bytes,
spread over time — you pay about what snapping already costs.

Measured against the pinned renderer, 100x30 screen, full-screen change:

| transition | frames | bytes | vs. snapping |
| --- | --- | --- | --- |
| snap (today's behaviour) | 1 | 3,064 | 1.0x |
| **row wipe** | 20 | **3,064** | **1.0x — free** |
| column wipe (directional) | 20 | 5,972 | 1.9x |
| column wipe | 8 | 4,350 | 1.4x |
| random scatter | 20 | 41,341 | 13.5x |
| *spring / motion, for comparison* | 20 | *~61,000* | *~20x* |

**The reveal pattern is the entire design — the spread is 13x.** A row wipe is
free because rows are contiguous: the diffing renderer emits each revealed row
as one run behind one cursor move, exactly as it would inside a snap. A random
scatter is catastrophic for the mirror-image reason — one `CUP` sequence per
cell. "Cell-by-cell dissolve" in the naive sense is the shape to avoid.

**Recommendation: a directional column wipe.** Reveal left-to-right when focus
moves right, right-to-left when it moves left. That buys back the one thing a
wipe otherwise loses — motion tells you *which way you went*, and scrolling
tiling is a spatial model — at roughly 2x a snap, still an order of magnitude
under real motion. Fewer frames is cheaper (8 frames costs less than 20,
because each frame repeats the per-row cursor moves), so frame count trades
smoothness against bytes with plenty of headroom either way.

**It is also markedly simpler to build than springs.** No per-pane placement
springs, no retarget arithmetic, and the freeze-during-motion decision falls
out for free:

1. On transition start, compose frame A (current) and frame B (target) once.
2. Compute the set of cells where they differ.
3. Order that set by the wipe pattern.
4. Each frame, write the next slice into the live screen and let the renderer
   diff it.

Total writes equal the size of the change set. Retargeting mid-wipe is just
"snapshot the live screen as the new A and recompose B".

Two hazards:

- **Wide glyphs must be atomic in the change set.** A cell-level mask can reveal the left half of a double-width glyph from B beside its right half from A. This codebase has been bitten by that class twice already — see `docs/LESSONS.md`.
- **Hide the cursor for the duration.** Its position is meaningless mid-wipe.

**What a wipe does not replace.** Cards need genuine motion — slivers sliding
and re-dealing — so the spring design above stays the answer there. A wipe is
for focus switches, column open and column close. If cards ever land, both
mechanisms coexist: springs for placement changes, wipes for focus.

## 2. Card layout — panes that slip under each other

Deferred from v1 with the hook already paid for. Instead of columns scrolling out
of view, off-screen columns compress into overlapping "cards", each showing a
sliver, so you see every pane at once and reveal one fully by focusing it.

The insight that makes it cheap: **cards are clipping plus z-order, not
resizing.** An occluded card keeps its full *logical* width, so its child never
learns it is partly covered — no `SIGWINCH`, no reflow, no redraw. `Placement`
already carries `Dst`, `Src` and `Z`; `ScrollStrategy` emits non-overlapping
rects with `Z=0` and a `CardStrategy` would emit overlapping full-width rects
with a z-fan. The compositor, the animator, and mouse hit-testing consume
`[]Placement` and don't care which produced it.

Two things learned from mocking it up:

- **Cards don't eliminate scrolling, they defer it.** At a 200-column terminal with 4-cell slivers you can fan maybe 20–25 cards before the focused pane has no room. Past that the strip still has to scroll — now scrolling a row of slivers.
- **A 4-cell sliver of real terminal content is visual noise.** The sliver that earns its space is *chrome*: a vertical spine with the status glyph, a truncated title, and a colour that pulses on activity. So occluded cards show a representation of activity, not a peek at content.

Known tension with animation: freeze-during-motion undercuts the point of
slivers, which is watching peripheral agents. The fix is to exempt chrome from
the freeze — animate spines live while pane content stays snapshotted.

**Preview-and-commit belongs here, not in focus movement.** Plan 6 considered
`prefix → navigate → Enter to commit` for switching columns and rejected it:
preview is meaningless when the thing you are previewing is the viewport
position itself. Browsing a fan of cards before settling on one *is* a distinct
action from landing on it, so the commit step earns its keystroke here. See
`dev-sessions/2026-09-19-wideboi-plan-6-modal-input/spec.md`.

## 3. Detach, reattach, and remote use

The reason the client/server seam exists. v1 runs both halves in one process over
`transport.InProc`; v2 replaces it with a Unix socket plus a reconnect handshake
and nothing above the transport changes.

Already true and load-bearing:

- The server ships **cell data, not raw PTY bytes**. A client that just reconnected cannot reconstruct a screen from a byte stream it did not see, and two clients fed raw bytes would drift. The server's emulator is the single authority.
- Placement is computed **client-side**, so two clients of different sizes attached to one session are correct by construction.
- Focus is shared session state (the tmux model). *Independent* per-client focus is a much larger question and is explicitly not answered.

Remaining work: socket framing, a reconnect handshake, a session directory and
attach command, and deciding what happens when the last client detaches.

## 4. A web client

Falls out of the seam almost for free, and is the third payoff from it. Because
the server ships cell data, **a client does not have to be a terminal emulator —
only a grid renderer**, which is a low enough bar to reach from a browser over a
WebSocket with this protocol essentially unchanged.

**xterm.js is the wrong tool for it**, for reasons that aren't limitations of
xterm.js: it wants a byte stream and runs its own emulator, which breaks the
single authority, and it gives one terminal per DOM element, which cannot express
a composited surface with overlapping panes and z-order. A canvas grid renderer
consuming `[]LineData` and `[]Placement` is the right shape — and it gains
**sub-cell smooth animation**, which a character grid cannot do at all. The card
fan would look best here.

Three constraints already recorded in the v1 spec keep this free: protocol types
stay codec-neutral (no `gob`, no `any`, no interface fields), the server never
assumes exactly one client, and `Attach` carries a protocol version.

Honest cost: a JS cell renderer plus compositor plus animation is a real second
client, perhaps 1,500–2,500 lines. Browser font metrics differ from terminal cell
metrics, so the width-oracle problem returns in a new form.

## 5. Terminal fidelity

**Own the grid, or fix `x/vt` upstream.** The pinned `x/vt` does not reflow, and
`uv.Line` is a bare `[]Cell` with nowhere to record a soft wrap — that single
missing bit is why correct reflow is impossible in the current data model. v1
compensates with a heuristic in our adapter (`term.Reflow`), whose accepted
failure is a hard line break landing at exactly the pane width, joining two
lines. Options if that becomes intolerable: fork `x/vt` and track real wrap
state, contribute reflow upstream, or own the cell storage ourselves.

**Scrollback reflow for display.** `Resize` leaves scrollback untouched at its
original width — measured — so nothing is destroyed, but history lines are wider
than the viewport. Reflowing them at view time costs ~177 ms for 10,000 lines
against 0.83 ms for the visible screen, so it wants debouncing or an incremental
approach.

**Kitty graphics passthrough.** gwae supports pane-local direct RGB/RGBA
placements. Out of scope so far; it is what image-heavy TUIs need.

**Clipboard over OSC 52**, so copy works over SSH rather than shelling out to
`pbcopy`.

**Mouse.** Click-to-focus hit-tests screen coordinates against `[]Placement` in
descending z order — already the right shape for cards. Drag selects.

## 6. Parked defects, with their reasoning

Each was found, understood, and deliberately deferred. None is a mystery.

| Defect | Why it's parked | What fixing it needs |
| --- | --- | --- |
| A child that stops reading stdin freezes rendering for every pane | Teardown still works from the wedged state, which is what makes it survivable | A bounded write (write deadline) on the PTY master, trading dropped child-bound bytes |
| A pane whose root exits before `Kill` leaves escapees unsignalled | Needs state the current design doesn't keep | A descendant snapshot maintained while the root is alive, which server-owned pane lifecycle now makes possible |
| `ps -axo` parsing unverified on Linux | No Linux host available | One `make check` run on Linux. The anti-leak guarantee degrades **silently** if `Descendants` returns a short list. |
| Upstream `x/vt` data race on `e.closed` | Practically inert — single bool, `Close` is its only writer | Give the pump goroutine sole ownership of the emulator lifecycle so `Close` never races `Read` |
| `compose.Text`/`WriteString` ignore `Cell.Width` | Current chrome is single-width | Real grapheme handling; comes due if status glyphs go wide |
| `transport.SendServer` is called under `s.mu` | Only reachable behind the wedged-render defect above, which is its real fix | Hoist the broadcast out of `s.mu`, or bound the send. `broadcastLayoutLocked` runs under `s.mu` and `SendServer` blocks once `ServerSend`'s 256-deep buffer fills, bounded only by a context `main` cancels *after* `guard.Stop()`. So the chain "child stops reading stdin → pty-writer parks → reply pipe fills → `vtGrid.Write` parks holding `se.mu` → main loop parks in `Draw` → `ServerSend` stops draining" ends with `srv.Close()` waiting forever. The `*Locked`-release discipline in `resizePanesLocked`/`PaneSize`/`CursorInfo` removes one way to hold `s.mu` forever, not this one. |
| The width cycle cannot reach a pane's spawn width | Absolute presets are load-bearing (see the v1 spec's layout core); a cycle seeded from the spawn width is a behaviour change, not a bug fix | `CycleWidth` steps `40 → 60 → 80`, but a pane spawns at `max((cols-1)/2, 40)`. On a 200-column terminal a pane spawns 99 cells wide and the *first* `alt+w` **shrinks** it to 80, which it can never exceed again. Either fold the spawn width into the cycle, or make the presets viewport-aware without making the *width* viewport-dependent. |

## 7. Spec-vs-code drifts left standing

The v1 spec is a design document, not a conformance target, and v1 shipped
against it with three differences that are worth naming rather than quietly
carrying. None is a defect today; each is a place where a reader who trusts
the spec will be wrong about the code.

- **Placement is computed server-side, not client-side.** The spec argues at
  length that `Place()` should run per client so two clients of different
  sizes are correct by construction, and §3 above still leans on that. In v1
  the server runs `ComputePlacements` and ships the result. With one in-process
  client the distinction is invisible; it becomes real the moment detach lands,
  and moving it then is the work §3 is quietly assuming is already done.
- **The `Strategy` interface does not exist.** The spec specifies
  `Strategy.Place(...)` with `ScrollStrategy` as the first implementation, and
  §2's card layout is written as "add a `CardStrategy`". `internal/layout` has
  one concrete function instead. Introducing the interface is small, but it is
  not free, and nothing today exercises the seam it is supposed to create.
- **The layout core has table tests, not `rapid` property tests.** The spec
  turns its four invariants into a property-test suite. What exists is
  hand-picked rows. The gap that matters most: invariant 4 — a pane's logical
  width equals its column width, independent of what is visible — has **no
  test at the layout layer at all**. It is covered only end-to-end, by
  `scripts/smoke.py`'s `case_partly_clipped_pane_keeps_full_width`. That is the
  invariant this project's no-shrink premise rests on hardest, and it is the
  one a layout-level refactor could break without a unit test noticing.

## 8. Open questions worth answering cheaply

- **Do the target coding agents use the alternate screen?** Determines how much reflow matters for the actual workload. A full-screen TUI agent repaints itself; an Ink-style agent (Claude Code appears to be one — its transcript stays in your scrollback) commits output upward into terminal-owned scrollback, same split as a shell. One-line check: run each in a pty and look for `ESC[?1049h`.
- **What should `$mod` be, per platform?** Option-as-Meta works locally but depends on the client terminal over SSH, and it requires terminal configuration users won't guess at.
- **Session persistence.** v1 deliberately has none — resume is the agent harness's job (`claude --resume`). Worth revisiting only if detach lands.
- **Config file.** Defaults live in one struct; reading a file into it is small and unexciting whenever it's wanted.
