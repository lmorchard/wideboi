# Beyond v1

Things deliberately left out of v1, things parked with reasoning, and things we
explored in design and want to keep. Not a commitment — a record, so ideas don't
have to be rediscovered and decisions don't have to be re-argued.

v1 is: a scrolling tiling multiplexer with a client/server seam, a pure layout
core, `$mod` verbs, OSC 133 agent status (*written in v1, first actually
working in Plan 16*), scrollback navigation, and non-destructive resize. See `docs/dev-sessions/` for how it got there and
`docs/LESSONS.md` for what it taught us.

**Last reconciled against the code at `b21eb5f` (Plan 15), on 2026-09-20.**
Sections 1, 2 and 7 had drifted far enough that a reader trusting them would
have been wrong about what exists — Plans 7, 8, 9 and 12 shipped things these
sections still described as unbuilt. They are corrected below. That pass also
turned up one new defect and sharpened an existing one; both are in section 6.
When you finish a plan, re-read this file — it is only useful while it is
true.

---

## 1. Animation: motion, as of Plan 18

**This was the largest gap, and it is the thing the project was started for.**

The original brief was gwae's scrolling tiling *"with more lightly animated
feedback when switching between terminals."* The v1 spec designed it in detail,
it was scoped as its own plan, then the plan numbering shifted during execution
and it fell out.

**Plan 9 built a directional column wipe. Plan 16 fixed it. Plan 18 deleted
it.** The wipe blitted the new layout on one side of a moving seam and the old
layout on the other, so nothing actually moved — content teleported in
vertical bands. Tolerable in the scrolling strip, where a horizontal seam
loosely mimics horizontal scrolling. Incoherent once cards landed: a focus
change resizes and repositions *every* pane, so the two halves of the screen
showed different geometries at once. Les's report, which is the honest summary
of what a wipe is: *"I don't understand what the animation is doing, it seems
kind of random."*

**What runs now is placement interpolation** (`internal/client/motion.go`):
each pane's rect eases from where it was to where it is going, fed through the
ordinary compose path. Panes slide and resize; the card fan visibly re-deals.
Panes present in only one of the two layouts grow from, or collapse to, a
zero-width rect at their own edge, so opening and killing a column are
animated too.

This is the first cut of the spring design below, with eased interpolation
standing in for real springs. It is also *less* machinery than the wipe was:
no composed frame snapshots, no viewport-fit check, no direction, and
`drawLayer` drops from three states to two.

Two things Plan 18 measured that change the design's premises:

**Motion costs ~3.8x a snap, not ~20x.** Measured on a pty: 4,664 bytes for a
card-mode focus change against a 1,214-byte snap and the wipe's 3,712. The
~20x figure below assumed an N-frame animation costs N full repaints. It does
not — the renderer diffs, and a sliding pane changes far fewer cells per frame
than a whole screen. **The cost argument for preferring wipes over motion does
not survive measurement**, which is worth knowing before the table below is
used to justify anything again.

**An animation that moves nothing should not run.** Motion is armed on a
placement change, not on a focus change, and starts from whatever is currently
on screen so a second change mid-flight continues rather than jumps. Two panes
that both fit keep identical placements when focus moves between them, so
scroll mode measures 694 bytes for that keypress and animates nothing at all.
The wipe fired on every focus change regardless, which is part of why it read
as arbitrary.

Still unbuilt, and still wanted: real springs with velocity, and retargeting
that carries momentum rather than restarting. See below.

### The spring design, for when real motion lands

Still the answer for cards, which need genuine motion rather than a reveal.
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

### Why the wipe looked like the cheaper first cut (built Plan 9, removed Plan 18)

**Read the numbers below with Plan 18's measurement in hand.** The argument
here is that motion costs ~20x a snap and a wipe ~2x, which made the wipe look
like most of the benefit for a tenth of the cost. Measured, motion is ~3.8x —
so the gap this section is built on is roughly 1.2x, not 10x, and the wipe's
advantage was very nearly all of its justification.

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

**This is what shipped in Plan 9 and was removed in Plan 18: a directional
column wipe.** Reveal left-to-right when focus moves right, right-to-left when
it moves left. That buys back the one thing a
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

Two hazards were called out here before it was built. One was handled, one was
never checked:

- **Wide glyphs must be atomic in the change set.** A cell-level mask can reveal the left half of a double-width glyph from B beside its right half from A. This codebase has been bitten by that class twice already — see `docs/LESSONS.md`. **Status: moot for the wipe, which no longer exists, but the class did not go away.** The wipe cut at a hard column index; Plan 18 deleted it. Placement interpolation clips whole pane rects instead, so the hazard is now wherever a pane's `Dst` boundary can land mid-glyph during a transition — untested, and harder to reason about than a single seam was, because every pane edge moves.
- **Hide the cursor for the duration.** Its position is meaningless mid-wipe. **Status: done** — `client.go`'s `layerWipe` branch calls `scr.HideCursor()`.

**What a wipe does not replace.** Cards need genuine motion — slivers sliding
and re-dealing — so the spring design above stays the answer there. A wipe is
for focus switches, column open and column close. Both mechanisms are meant to
coexist: springs for placement changes, wipes for focus.

**Superseded by Plan 18.** There is one mechanism now, not two. Focus
switches animate the same way everything else does — the placements
interpolate — which turned out to be both simpler and, measured, no more
expensive than the wipe. What is still missing from the design above is the
spring itself: velocity, and a retarget that carries momentum rather than
restarting from the current rects.

**What shipped is simpler than what was designed here.** The design above
composes A and B, diffs them into a change set, and reveals that set in slices
so total writes equal the size of the change. The wipe that shipped (Plan 9,
removed Plan 18)
blits whole clipped rectangles of A and B on either side of a moving split. The
renderer's own diffing recovers most of the benefit, so the byte costs in the
table above are roughly right — but the change-set framing is what makes
retargeting mid-wipe ("snapshot the live screen as the new A") cheap, and that
is not implemented either: a focus change during a wipe simply replaces the
transition.

## 2. Card layout — shipped in Plan 17, proportioned in Plan 18

Instead of columns scrolling out of view, off-screen columns compress into
"cards", each showing a sliver, so you see every pane at once and reveal one
fully by focusing it.

**Plan 8 built `internal/layout/card.go`.** `CardStrategy` implements
`Strategy`, has unit and `rapid` property tests, and honours the invariant that
matters: `ColumnWidth` is untouched, so an occluded pane's child never learns it
is partly covered — no `SIGWINCH`, no reflow, no redraw.

**Plan 17 made it reachable and gave the slivers something worth showing.**
`WIDEBOI_LAYOUT=cards` selects it at startup and `$mod c` toggles it at
runtime. The mode is shared session state carried on `MsgLayoutSnapshot`
beside focus, because placements are computed client-side (Plan 12) and two
clients attached to one session have to agree about the layout.

An occluded card renders **chrome, not content**: its status glyph, its
terminal title, and a spine that brightens while the pane is producing
output.

**Plan 18 replaced the fixed sliver width with a share.** Plan 17 used a
constant — 4 cells, then 10 — which knew nothing about the viewport, so the
fan simply stopped partway across: a 120-column window with three 30-wide
panes used 50 columns and left 70 dead. Now the focused pane keeps its own
width and the rest divide what is left, with the remainder spread one cell at
a time so the fan's right edge lands exactly on the viewport edge. A 90-cell
window with a 60-cell focused pane and three others gives each of them 10.

Two consequences fall out of dividing rather than fixing:

- **A card whose share covers its whole pane renders full, not chrome.**
  Nothing is occluded, and a 45-cell "sliver" showing a spine and a short
  title wastes what it was given. Card mode degrades toward scroll-like when
  everything fits, which makes the sliver/full split a property of available
  space rather than of the mode.
- **`MinSliverWidth` (4) is a floor, not a governing number.** With enough
  columns the even share rounds below what chrome needs, so only cards
  clearing the floor are shown — taken from nearest the focused pane outward
  — and the rest overflow into the `+N` marker. `DefaultSliverWidth` decided
  how wide cards were; a floor only decides how many fit.

**Cards draw no dividers.** They are contiguous, so a divider at one card's
right edge is the next card's first column and was painted over the moment
that card drew — every card divider but the last was invisible, and the
survivor sat at the fan's outer edge separating nothing. Each sliver's spine
is the separator. `ScrollStrategy` reserves a column and keeps its dividers.

The insight that made it cheap: **cards are clipping plus z-order, not
resizing.** `Placement` already carries `Dst`, `Src` and `Z`; the compositor,
the animator, and mouse hit-testing consume `[]Placement` and don't care which
strategy produced it.

**The title is the payoff, and it arrived for a reason worth recording.**
Measured 2026-09-20, Claude Code emits no OSC 133 at all but keeps a live
terminal title carrying a spinner and a summary of the current turn — `◐ Claude
Code`, `✳ Pong reply`. `x/vt` had been parsing OSC 0/1/2 into a title all
along and wideboi registered no callback for it, so it was parsed and
discarded. For the agent workload this project exists for, that title is the
status signal that actually exists. See §8.

**A sliver is distinguishable from a clipped pane on the wire.**
`protocol.PlacementKind` (`PlacementFull`, `PlacementSliver`) rides on
`Placement` and `PlacementData`. Geometry could not tell them apart — both are
a narrow `Dst` over a cropped `Src`, and `ScrollStrategy` emits `Z=0` for
everything — so without the mark the client would have painted chrome over the
visible edge of a legitimately clipped pane, which
`case_partly_clipped_pane_keeps_full_width` exists to prevent.

One way the implementation still differs from the sketch this section used to
carry:

- **It emits non-overlapping rects, not overlapping full-width ones.** Slivers are laid side by side at `i*sliverWidth` with the focused card between them; `Z` is 0 for slivers and 1 for the focused pane, but nothing actually occludes anything, so the z-fan is currently decoration. "Panes that slip under each other" is the name, not yet the behaviour.

Two things learned from mocking it up, both still unbuilt:

- **Cards don't eliminate scrolling, they defer it.** How many fit is no longer a fixed count: it is however many clear `MinSliverWidth` once the focused pane has taken its width. At a 200-column terminal with a 100-cell focused pane that is 25 cards; with a wider focused pane, fewer. Past that the strip still has to scroll, now scrolling a row of slivers. **Partly addressed:** Plan 17 stopped them vanishing silently — cards that do not fit collapse into a `+N` marker on the header row — but actually scrolling the sliver row is still unbuilt.

  **`ScrollStrategy` has the same problem and no marker.** It skips any column whose `Dst` is empty, so a pane scrolled fully out of view has no placement either. Plan 17's `hiddenCountsLocked` finds those too, and the marker is deliberately gated to card mode: surfacing them would change the chrome every user sees in the default layout, which was outside that change. Worth deciding on its own.
- **A 4-cell sliver of real terminal content is visual noise. Fixed in Plan 17.** The sliver that earns its space is *chrome*, and that is now what one shows: the status glyph, a truncated terminal title, and a spine that brightens while the pane is producing output. The activity signal reads the existing `»` status glyph rather than a new wire field — `Write`'s heuristic already sets it on every write and lets it decay after three seconds — so "is this pane doing something" crosses the socket for free. Titles are truncated with `compose.TruncateWidth`, by display width rather than rune count, because a child can put anything in its title.

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

Landed since (Plans 13-15): a Unix socket transport, `wideboi server` and
`wideboi attach`, `C-b d`, and multi-client broadcast. Placement moved
client-side, closing the first drift in §7. `scripts/attachcheck.py` drives
the real pair of processes and is part of `make check`.

### Wishlist: detach from plain `wideboi`

`wideboi` with no subcommand still runs both halves in one process and binds
no socket, so there is nothing to detach *from*: the process that owns the
panes is the one that would be leaving. `C-b d` is therefore offered only to a
client that reached its server over a socket — `Client.SetDetachable`, mirrored
by `router.detachable` — and in-process the verb is both hidden from the bar
and swallowed by the router. That is honest, but it makes the commonest way to
start wideboi the one way you cannot detach from, which is backwards.

What it would take, and why it was not just done:

- **Default mode spawns a detached `wideboi server` and attaches to it.** One
  code path for every mode, and detach works everywhere. The cost is that the
  panes then belong to a background process, so `SIGTERM` to the foreground
  client no longer reaps them — and that is precisely the guarantee
  `docs/LESSONS.md` calls load-bearing and `make verify-exit` asserts. The
  teardown contract would have to be redesigned around the server, not merely
  re-pointed: "nothing wideboi spawned outlives it" has to become "nothing
  outlives the last client, unless a detach said so."
- **Or: hand the panes off on detach.** Keep the in-process fast path, and on
  `C-b d` re-exec the session into a background server. Avoids the daemon on
  every launch, but moving live PTY master fds across an exec is real work and
  the failure mode is orphaned shells.
- **Then: what happens when the last client detaches?** Still unanswered, and
  it gets sharper here — an idle background server per forgotten `wideboi`
  invocation is exactly the leak the teardown guarantee exists to prevent. A
  timeout, an explicit `wideboi kill-session`, or both.

Remaining work elsewhere: a reconnect handshake (today a dropped connection
means running `attach` again), a session directory so more than one session
can exist at once (the socket path is currently a fixed `default.sock`), and
the last-client-detaches question above.

### Drift: the protocol is gob over interfaces, not the codec-neutral wire §4 assumes

§4 records the v1 constraint as "protocol types stay codec-neutral (no `gob`,
no `any`, no interface fields)". Two of those three are not true today:
`transport` encodes with `gob`, and `ClientMessage`/`ServerMessage` are bare
`interface{}` so the concrete message type rides as a registered gob name.

The third one — no interface *fields* — was violated too, and that is what
took `attach` down: `CellData.Style` was a `uv.Style`, whose colour fields are
`color.Color` interfaces, so the first coloured cell a child printed failed to
encode and killed the socket. It is now enforced rather than documented, by
`protocol.TestWireTypesCarryNoInterfaces`, with concrete mirrors in
`protocol/wire.go`.

The remaining two matter for §4's web client, which cannot speak gob. The wire
types being flat scalars is most of the work; swapping the codec is then a
`transport` change with nothing above it affected — which was the point of the
constraint.

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
| `compose.Text` ignores `Cell.Width` — **row corrected 2026-09-20** | Reading back, not writing; only tests and snapshots consume it | The `WriteString` half of this row was **wrong**, and had been since it was written. `WriteString`/`WriteStyled` advance by `cell.Width` and always did (`surface.go:66-71`, now pinned by `TestWriteStyledAdvancesByMeasuredWidth`) — the row was resting on `WriteString`'s own doc comment, which described a bug the code did not have. Plan 17 corrected the comment and nearly built a redundant width-aware writer on the strength of it. What remains true: `Text` emits exactly one rune per cell, so a wide glyph reads as its base rune plus whatever the continuation cell holds, and combining marks are dropped. Adequate for the ASCII snapshots it serves. Separately, rune-counting truncation *was* a real gap and is closed by `compose.TruncateWidth` |
| ~~`transport.SendServer` is called under `s.mu`~~ — **no longer true; re-verified 2026-09-20** | The headline claim was stale and is corrected here rather than left standing | Both `SendServer` call sites in `internal/server` (`server.go:512` in `broadcastLayout`, `server.go:528` in `broadcastPaneUpdates`) copy the transport slice and `s.mu.Unlock()` *before* sending, so neither holds the lock across the send. There is no `broadcastLayoutLocked` function at all — the name survives only in two comments (`server.go:338`, `server_test.go:272`), which should be reworded. **Not re-derived:** whether the wedge chain this row described has any surviving path to an unkillable `srv.Close()` by some other route. The first row's wedged-render defect is unchanged, and `SendServer` on a socket transport can still block — just not while holding `s.mu`. Worth one focused pass before trusting that the whole chain is gone |
| Four exported symbols have no callers at all | Found 2026-09-20 by an exported-surface audit, not by a failure | `Pane.Dead` (`pane.go:153`), `Pane.SendText` (`pane.go:148`), `SocketListener.Path` (`socket.go:122`) and `Server.SpawnPane` (`server.go:216`) are referenced from nowhere — not production, not tests. Either they are API for a caller that was never written, or they are dead. Decide per symbol rather than deleting in bulk |
| `Client.FocusPaneID` is test-only | Harmless on its own, but it is the same audit's finding and worth knowing before trusting it as API | Its five call sites are all tests. Note the name collision: `layout.Strip.FocusPaneID(id)` is a *setter* with a real production caller at `server.go:186`. Do not conflate them |
| The width cycle cannot reach a pane's spawn width | Absolute presets are load-bearing (see the v1 spec's layout core); a cycle seeded from the spawn width is a behaviour change, not a bug fix | `CycleWidth` steps `40 → 60 → 80`, but a pane spawns at `max((cols-1)/2, 40)`. On a 200-column terminal a pane spawns 99 cells wide and the *first* `alt+w` **shrinks** it to 80, which it can never exceed again. Either fold the spawn width into the cycle, or make the presets viewport-aware without making the *width* viewport-dependent. |

## 7. Spec-vs-code drifts: all three closed

This section used to list three places where v1 shipped short of the v1 spec.
Plans 7 and 12 closed all three. They are kept here rather than deleted because
the *spec* is unchanged, so a reader who trusts it is still wrong about the
code — just in the other direction now.

- **Placement is computed client-side, as the spec wanted.** Plan 12 moved it.
  `Client.HandleServerMsg` runs `strip.SyncColumns` then `ComputePlacements`
  against its own `c.cols`/`c.rows`, so two clients of different sizes are
  correct by construction and §3 is no longer assuming work that hasn't
  happened. One wrinkle to know about: the server still computes and ships
  `Placements` too, and the client falls back to that field when
  `len(m.Columns) == 0`. Two producers of the same value is a drift waiting to
  happen; the fallback should probably go.
- **The `Strategy` interface exists.** `internal/layout/layout.go` defines
  `Strategy`, `Strip` holds one, and there are two implementations —
  `ScrollStrategy` (the default from `NewStrip`) and `CardStrategy`. The seam
  the spec wanted is real and has a second implementation exercising it, though
  only from tests; see §2.
- **The layout core has `rapid` property tests.** `TestLayoutPropertyInvariants`
  covers all four invariants, including the one this section called out as
  having no layout-level test at all: invariant 4, a pane's logical width equals
  its column width independent of what is visible. `TestCardStrategyPropertyInvariants`
  asserts the same invariant against `CardStrategy`, which is the real payoff —
  the no-shrink premise now holds across both strategies by construction rather
  than by inspection.

## 8. Open questions worth answering cheaply

- **Do the target coding agents use the alternate screen?** ~~Open.~~
  **Measured 2026-09-20 against Claude Code v2.1.278: yes.** One
  `ESC[?1049h` at startup and no exit until killed, so it is a full-screen
  TUI that repaints itself, not the Ink-style scrollback-committing shape
  this bullet guessed. It also emits 3 synchronized-update sequences
  (`ESC[?2026h`) at boot. Reflow therefore matters less for the agent
  workload than for a shell pane. Not yet measured for any other agent.

- **Agent status: OSC 133 is the wrong protocol for the motivating use
  case.** Measured the same day, across a full turn: Claude Code emits
  **zero** OSC 133 sequences — at startup, during the turn, or on
  completion. So Plan 16 made the glyphs work and the actual target still
  drives none of them. What it *does* emit is usable, and is the real
  integration path:

  | sequence | when | meaning |
  | --- | --- | --- |
  | `ESC]9;4;3;BEL` | turn starts | ConEmu/WT progress, state 3 = indeterminate ("busy") |
  | `ESC]9;4;0;BEL` | turn ends | state 0 = clear progress |
  | `ESC]2;◐ Pong reply BEL` | continuously | title: spinner glyph + a short summary of the turn |

  `OSC 9;4` maps onto `PaneStatus` almost directly — `3` → `StatusWorking`,
  `0` → done/idle, and the protocol's `2` (error) and `4` (warning) states
  would give `StatusFailed` for free if any agent emits them. Worth
  checking whether Claude Code ever sends `9;4;2`; this session only
  observed `3` and `0`.

  **Plan 17 shipped the title tracking, and Claude Code still does not feed
  it.** Measured 2026-09-20 after sliver chrome landed: Claude Code boots
  normally inside a wideboi pane and its banner renders, but no title reaches
  the pane's emulator, so its sliver shows only the status spine. The chrome
  is not at fault — a title set by hand (`printf '\033]2;compiling\007'`)
  renders correctly in a sliver. Ruled out: the alternate screen
  (`Grid.Title()` captures OSC 2 issued after `ESC[?1049h`), `TERM` (panes get
  `xterm-256color`, `ptyx/pane.go:48`), and being a shell's child (Claude Code
  under `/bin/sh` in a plain pty still emits one title sequence). Remaining
  suspect, **unverified**: a terminal capability probe wideboi's emulator does
  not answer, leading Claude Code to conclude titles are unsupported. This is
  the difference between the card fan being useful for agent panes and merely
  being decorative, so it is worth one focused pass — and `OSC 9;4` above may
  be the better signal regardless.

  The **title** is the other prize. §2 says a card sliver that earns its
  space shows "a vertical spine with the status glyph, a truncated title,
  and a colour that pulses on activity" — and here is a live, per-pane,
  self-updating title with a spinner already in it. `x/vt` already parses
  OSC 0/1/2 into `Emulator.title` and fires a `Title` callback
  (`handlers.go:305-343`, `osc.go:21`); wideboi registers neither. That is
  a small, well-understood addition.

  **Recommendation:** keep OSC 133 (it is correct, tested, and what
  shell-integration users get) and add OSC 9;4 plus title tracking
  alongside it, feeding the same `PaneStatus`. Decide precedence when both
  arrive — the obvious rule is last-writer-wins per pane, but note the
  `sawOSC133` latch already disables the activity heuristic, so a second
  authoritative source needs the same treatment rather than a second latch.
- **What should `$mod` be, per platform?** Option-as-Meta works locally but depends on the client terminal over SSH, and it requires terminal configuration users won't guess at.
- **Session persistence.** v1 deliberately has none — resume is the agent harness's job (`claude --resume`). Worth revisiting only if detach lands.
- **Config file, and key remapping for control mode.** Defaults live in one
  struct; reading a file into it is small and unexciting whenever it's wanted.
  The part worth designing rather than assuming is **remapping the control-mode
  verbs**, which is the thing people will actually want it for — `WIDEBOI_PREFIX`
  already concedes that one key is not everyone's key, and the same argument
  applies to the whole table.

  Plan 15 builds `internal/keys` as a single table (letter, verb, labels,
  whether it needs a detachable session) read by the router, the status bar and
  the help overlay, so remapping is populating that table rather than patching
  three places. Three constraints it already carries that a config format has to
  respect:

  - **`i`, `m` and `[` can never be bound.** Their control bytes are Tab, Enter
    and Escape, so a verb on those letters has no working repeat form. Enforced
    by a test today; a config file has to reject them with an error rather than
    silently producing a dead binding.
  - **An unmatchable key name is indistinguishable from a key nobody pressed.**
    `uv.KeyPressEvent.MatchString` returns `false` either way, which is how the
    `pgdn` binding shipped dead. Config-supplied names must be validated at load
    against the set ultraviolet can actually produce — `parsePrefix`'s narrow
    allowlist is the existing precedent, and it exists for exactly this.
  - **The status bar has a hard 79-cell budget at 80 columns.** User-supplied
    labels can overflow it, so the bar's existing drop-from-the-end behaviour
    has to stay, and the help overlay becomes load-bearing rather than a
    convenience.
