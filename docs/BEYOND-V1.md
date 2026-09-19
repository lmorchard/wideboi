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

## 2. Card layout — panes that slip under each other

Deferred from v1 with the hook already paid for. Instead of columns scrolling out
of view, off-screen columns compress into overlapping "cards", each showing a
sliver, so you see every pane at once and reveal one fully by focusing it.

The insight that makes it cheap: **cards are clipping plus z-order, not
resizing.** An occluded card keeps its full *logical* width, so its child never
learns it is partly covered — no `SIGWINCH`, no reflow, no redraw. `Placement`
already carries `Dest`, `Src` and `Z`; `ScrollStrategy` emits non-overlapping
rects with `Z=0` and a `CardStrategy` would emit overlapping full-width rects
with a z-fan. The compositor, the animator, and mouse hit-testing consume
`[]Placement` and don't care which produced it.

Two things learned from mocking it up:

- **Cards don't eliminate scrolling, they defer it.** At a 200-column terminal with 4-cell slivers you can fan maybe 20–25 cards before the focused pane has no room. Past that the strip still has to scroll — now scrolling a row of slivers.
- **A 4-cell sliver of real terminal content is visual noise.** The sliver that earns its space is *chrome*: a vertical spine with the status glyph, a truncated title, and a colour that pulses on activity. So occluded cards show a representation of activity, not a peek at content.

Known tension with animation: freeze-during-motion undercuts the point of
slivers, which is watching peripheral agents. The fix is to exempt chrome from
the freeze — animate spines live while pane content stays snapshotted.

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

## 7. Open questions worth answering cheaply

- **Do the target coding agents use the alternate screen?** Determines how much reflow matters for the actual workload. A full-screen TUI agent repaints itself; an Ink-style agent (Claude Code appears to be one — its transcript stays in your scrollback) commits output upward into terminal-owned scrollback, same split as a shell. One-line check: run each in a pty and look for `ESC[?1049h`.
- **What should `$mod` be, per platform?** Option-as-Meta works locally but depends on the client terminal over SSH, and it requires terminal configuration users won't guess at.
- **Session persistence.** v1 deliberately has none — resume is the agent harness's job (`claude --resume`). Worth revisiting only if detach lands.
- **Config file.** Defaults live in one struct; reading a file into it is small and unexciting whenever it's wanted.
