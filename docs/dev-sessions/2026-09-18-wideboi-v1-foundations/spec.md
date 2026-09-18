# wideboi — v1 design

**Date:** 2026-09-18
**Module:** `github.com/lmorchard/wideboi`
**Go:** 1.27.1 (darwin/arm64 dev host)
**Status:** design approved, implementation plan pending

## What this is

A scrolling tiling terminal multiplexer for CLI coding agents, with animated
feedback when focus moves between panes.

Panes never shrink. Open more of them and the viewport scrolls instead of
squeezing what is already there. The design borrows its layout semantics from
[gwae](https://github.com/hongnoul/gwae), which adapted niri's scrolling tiling
to a character grid, and departs from it in three ways:

1. Written in Go rather than Rust.
2. Focus changes animate rather than snap.
3. Client and server are separated from the start, so detach/reattach and
   remote use over SSH are a transport change rather than a rewrite.

This is a learning project. Where a choice trades development speed against
depth of understanding, prefer whichever leaves a working program at the end of
the session.

## Non-goals for v1

Deferred deliberately, each recorded with the hook that keeps it cheap later:

| Deferred | Hook that keeps it cheap |
| --- | --- |
| Card layout (overlapping, fanned panes) | `layout.Strategy` interface + `[]Placement` + z-order |
| Detach / reattach / daemon | `internal/transport` boundary; swap channels for a socket |
| Config file | Defaults live in one struct; read a file into it later |
| Kitty graphics passthrough | None. Out of scope. |
| Self-update, hot reload | None. Out of scope. |
| Windows | None. Not supported, not planned. |

## Platform

macOS and Linux. `creack/pty` covers both with one code path. Windows would mean
ConPTY and a different capability story for no learning payoff.

Running over SSH is a first-class case, not an afterthought. It constrains two
decisions elsewhere in this document: animation must run client-side so frames
never cross the wire, and clipboard integration must use OSC 52 rather than
`pbcopy`.

## Dependencies

| Package | Role | Risk |
| --- | --- | --- |
| `github.com/creack/pty` | PTY spawn, `TIOCSWINSZ` | Low. Stable, no real alternative. |
| `github.com/charmbracelet/x/vt` | Terminal emulation, per-pane grid, scrollback | **High.** No tagged release. Pin `v0.0.0-20260913004009-c615ff2f7805`. |
| `github.com/charmbracelet/ultraviolet` | Cell buffers, diffing renderer, input decode | Medium. Same family as `x/vt`; pre-1.0. |
| `github.com/rivo/uniseg` | Grapheme clustering | Low. |
| `pgregory.net/rapid` | Property testing (`internal/layout`) | Low, test-only. |

`x/vt` supplies more than initially assumed, and the package map below depends
on it:

- `RegisterOscHandler(cmd int, handler)` — OSC 133 agent status needs no
  separate scanner teed off the byte stream.
- `Scrollback()`, `ScrollbackCellAt()`, `ScrollbackLen()`,
  `DefaultScrollbackSize = 10000` — history is built in.
- `SafeEmulator` — mutex-wrapped emulator, plus `Touched() []*uv.LineData` for
  dirty-line tracking.
- `Draw(scr uv.Screen, area uv.Rectangle)` — a pane paints itself into an
  Ultraviolet buffer at an arbitrary rectangle. This is the compositor
  primitive.
- `type Terminal interface` — the emulator abstraction already exists as an
  exported interface.

**Unverified, and load-bearing:** whether `Emulator.Resize(w, h)` reflows
primary-screen content or truncates it. gwae replaced its first emulator over
exactly this (their ADR-004: narrowing truncated cell tails, so widening could
not recover the text). Test this against a real pane in the first milestone that
has one, not later.

## Architecture

### The seam

The single most consequential structural decision. Client and server are
separate packages in one process, communicating only through typed messages over
a channel.

```
  SERVER — owns state, survives disconnect    CLIENT — owns the host terminal
  ├─ PTYs, one goroutine each                 ├─ raw mode, input decode
  ├─ x/vt emulators + scrollback              ├─ per-pane cell mirrors
  ├─ authoritative layout                     ├─ compositor (uv.Screen)
  └─ OSC 133 / agent status                   ├─ renderer (Render → Flush)
                                              └─ animation
                     └──── transport ─────────┘
```

v1 runs both halves in one process over an in-memory channel pair. There is no
socket, no daemon, and no attach command. v2 replaces `transport.InProc` with
`transport.Unix` and adds a reconnect handshake; nothing above the transport
changes.

Collapsing a seam is easy and cutting one into a monolith is not, which is why
this is paid for now rather than later.

### Package map

```
wideboi/
├── cmd/wideboi/            wiring, flags, signal setup
├── internal/layout/        PURE. strip, columns, widths, placement.
│                           no I/O, no PTY, no terminal. Shared by both sides.
├── internal/protocol/      message types crossing the seam. Types only.
├── internal/transport/
│   ├── inproc.go           channel pair                      ← v1
│   └── unix.go             socket + framing + reconnect      ← v2
├── internal/server/
│   ├── server.go           event loop, authoritative layout, outbound stream
│   ├── pane.go             pty + x/vt emulator + status, one goroutine
│   ├── ptyx/               spawn, TIOCSWINSZ, process-tree teardown
│   └── status/             OSC 133 handler + idle heuristic
└── internal/client/
    ├── client.go           event loop: input | inbound | frame tick
    ├── mirror.go           per-pane cell mirrors, applied from server deltas
    ├── compose.go          mirrors + layout + chrome → uv.Screen
    ├── anim/               retargetable springs, scheduler
    ├── input.go            decode → $mod verb, or forward to focused pane
    └── chrome.go           borders, titles, status glyphs, focus ring
```

**Enforced invariant:** `internal/client` must not import `internal/server`, and
neither may import the other's types. They share `layout` and `protocol` only. A
`go list` check in CI enforces this, because the boundary erodes silently
otherwise.

`internal/layout` is pure and serializable, so both sides can hold one: the
server mutates the authoritative copy, the client keeps a replica it never
writes to.

### Layout core

A single infinite horizontal strip. One pane per column. Column widths are
preset fractions of the viewport (`1/4` default, cycling `1/4 → 1/3 → 1/2`).
Adding, removing, or moving a column never changes another column's width.

Semantics follow gwae's `docs/LAYOUT-SPEC.md`, reduced to one dimension. Its
invariants become the property-test suite:

1. No implicit resize: layout operations never change another column's width.
2. Column order is total, with no gaps; closing compacts.
3. Focus fills left first on close.
4. A pane's logical width equals its column width, independent of what is
   visible.

The distinction in (4) is what makes the deferred card layout cheap. A pane's
**logical size** is what is reported to the PTY via `TIOCSWINSZ` and what the
child lays out against. Its **visible region** is a crop. A partly-covered pane
keeps its full logical width, so the child receives no `SIGWINCH` and never
learns it was occluded. Cards are clipping plus z-order, not resizing.

### Placement

The layout core does not expose a scroll offset. It emits placements:

```go
type Placement struct {
    PaneID PaneID
    Dest   image.Rectangle // where on screen
    Src    image.Point     // offset into the pane's grid (panning / clipping)
    Z      int             // paint order; 0 = bottom
}

type Strategy interface {
    Place(s *Strip, viewport image.Rectangle, focus int) []Placement
}
```

Geometry uses stdlib `image` types, so `internal/layout` depends on nothing
outside the standard library. This costs nothing at the boundary:
`uv.Rectangle` and `uv.Position` are *type aliases* for `image.Rectangle` and
`image.Point`, so placements pass into Ultraviolet's `Draw` with no conversion.

`ScrollStrategy` (v1) emits non-overlapping rects with ascending x and `Z` of
zero. `CardStrategy` (v2) emits overlapping full-width rects with a z-fan. The
compositor, the animator, and mouse hit-testing consume `[]Placement` and do not
know which strategy produced it.

This costs one struct and a `sort.Slice` today.

**Placement is computed client-side.** A placement depends on the strip, the
focus index, and the viewport — and the viewport belongs to the client, not the
server. So the server ships strip and focus; each client runs `Place()` against
its own geometry. Two clients of different sizes attached to one session are
then correct by construction, and the strategy becomes a client preference
rather than session state.

The server still needs `internal/layout` for **logical** pane sizes, which is
what it reports to each PTY via `TIOCSWINSZ`. Logical width is the column's
preset fraction of the viewport and does not depend on the placement strategy.
This is the same split as the crop rule above: logical size is authoritative and
server-owned; where those cells land on a screen is a client concern.

### Animation

Springs act on **placements, per pane** — not on a global scroll offset. A
scalar cannot express a column opening, a killed column collapsing, or a card
re-deal; per-pane springs get all three for free, and it is the same amount of
work.

On a focus change the client diffs the previous `[]Placement` against the new one
and retargets each pane's spring. Each spring holds `(current, velocity,
target)`, so retargeting is an assignment:

- **Retarget in flight.** A nav key pressed mid-transition moves logical focus
  immediately and updates the spring target. Five fast presses produce one
  smooth glide to the final column, not five chained hops.
- **Keystrokes route to the destination pane immediately.** The screen is frozen
  during the transition regardless, so buffering would only add latency to a
  round trip the multiplexer already sits on twice.
- **Content freezes during motion.** PTY readers keep draining and keep feeding
  their emulators, so no child ever blocks. The compositor renders the
  pre-transition snapshot until the springs settle, then repaints once with all
  accumulated state. Per-frame cost during a transition is therefore pure
  placement math with zero emulator work.

Accepted consequence: a pane that clears and redraws during the freeze pops into
its new state rather than transitioning to it. Tolerable at ~200ms.

Known future wrinkle: freeze-during-motion is in tension with showing live
activity in occluded panes. When `CardStrategy` lands, exempt chrome from the
freeze so status glyphs animate while pane content stays snapshotted. Not needed
in v1.

Animation runs client-side. Over SSH a nav keypress sends one small message
upstream and the client animates locally against cell data it already holds.
Server-side animation would push 60 full-screen repaints per second down the
wire.

The constraint that buys: the client needs mirrors for visible panes **plus the
columns it is about to scroll past**, or it animates into blank space.

### Protocol

```
client → server                     server → client
  Attach{cols, rows}                  LayoutSnapshot{strip, focus}
  Verb{FocusLeft|FocusRight|          PaneLines{id, []LineData}
       NewColumn|CycleWidth|          PaneStatus{id, working|needs-input|done|failed}
       KillPane|SmartJump}            PaneTitle{id, string}
  Input{paneID, []byte}               PaneClosed{id, exitCode}
  Mouse{paneID, event}                Bell{id}
  Scroll{paneID, delta}
  Resize{cols, rows}
  Detach
```

**The server ships cell data, not raw PTY bytes.** A client that has just
reconnected cannot reconstruct a screen from a byte stream it did not see, and
two clients fed raw bytes would drift. The server's emulator is the single
authority.

Mechanically: each pane renders itself with `Emulator.Draw()` into its own
off-screen `uv.Buffer`; the server diffs that against what it last sent and ships
changed lines; the client applies them to its mirror. `SafeEmulator.Touched()`
does most of the dirty-tracking.

v1 subscribes the client to every pane. Subscribing only to visible panes plus a
lookahead margin is the obvious bandwidth lever later, and is a protocol addition
rather than a redesign.

Text typed into a pane bypasses layout entirely: `Input{paneID, bytes}` →
`pane.pty.Write()`. One hop, nothing extra on the latency path.

### Constraints that keep other client kinds possible

Shipping cell data rather than PTY bytes means a client does not have to be a
terminal emulator — only a grid renderer. That is a low enough bar to reach from
a browser over a WebSocket, reusing this protocol unchanged. Three constraints
preserve that option at no cost to v1:

1. **`internal/protocol` stays codec-neutral.** Plain structs with concrete
   fields. No `any`, no interface fields requiring registration, no funcs or
   channels. `gob` is the tempting Go-to-Go default and would quietly foreclose
   every non-Go client. The encoding choice is not real until v2, because v1
   passes structs over a channel unencoded.
2. **The server never assumes exactly one client.** Focus is shared session
   state, following tmux: several clients see the same focused pane, each at its
   own geometry. Independent per-client focus is a much larger question and is
   explicitly not answered here.
3. **`Attach` carries a protocol version.** Other client kinds will lag the Go
   client; the server can refuse or degrade.

Note for whenever a web client is attempted: xterm.js is the wrong tool for it.
It expects a byte stream and runs its own emulator, which breaks the single
authority, and it gives one terminal per DOM element, which cannot express a
composited surface with overlapping panes and z-order. A canvas grid renderer
consuming `[]LineData` and `[]Placement` is the right shape — and it gains
sub-cell smooth animation, which a character grid cannot do at all.

### Concurrency

**Server:** one goroutine per pane running `io.Copy(emulator, ptyMaster)`, each
with `defer recover()` so a panic in one pane's parser cannot take the
multiplexer down. Panes signal dirty on a shared channel. A single server
goroutine owns the layout and the outbound stream, draining dirty signals in
batches.

**Client:** three inputs into one `select` — host stdin via Ultraviolet's
decoder, the inbound protocol stream, and a `time.Ticker`. That loop exclusively
owns the screen buffer.

**Never render per damage event.** Damage sets a dirty flag; the frame tick
renders. This is the difference between idling at 2% CPU and melting when an
agent streams tokens.

Two things Go supplies that gwae had to build by hand: `SafeEmulator` provides
the supervisor-pattern isolation, and `signal.Notify` delivers on an ordinary
goroutine, so unlike gwae's signal handler — which may not allocate, lock, or
fork — ours can allocate, take locks, and shell out to `ps`.

### Agent status

Register an OSC handler on 133 and track the prompt-start / command-start /
command-finished markers. Where a harness does not emit them, fall back to an
output-activity and idle-time heuristic, which is a hint and not a guarantee.

States and glyphs follow gwae: `»` working, `!` needs input, `✓` done, `✗`
failed. One key jumps focus to the pane that needs attention.

### Scrollback

Per pane, backed by `x/vt`'s scrollback at the default 10,000 lines. A pane in
scrollback renders from `ScrollbackCellAt` with a negative `Placement.Src.Y`,
which means the compositor needs no special case. Mouse wheel and keys scroll the
focused pane; focus changes do not reset scroll position.

### Mouse

Ultraviolet decodes the events. Click-to-focus hit-tests screen coordinates
against `[]Placement` in descending z order, which is already correct for the
deferred card layout. Drag selects; selection copies via OSC 52 so it works over
SSH.

### Teardown

A multiplexer that leaks background processes is worse than useless: the work
keeps burning CPU with no window left to find it in.

Register each pane's pgid at spawn. Every exit path funnels through one reaper
that performs three kills per pane, because each catches processes the others
miss:

1. `kill(-pgid)` — the pane's shell and its foreground job.
2. `kill(rootpid)` — the shell itself.
3. A `ps` tree walk, deepest-first — anything that left the group deliberately
   via `nohup` or `setsid`, and anything an interactive shell placed in its own
   group through job control.

Guarded by `defer` on the server loop, a `recover` path, and the signal
goroutine. gwae's teardown table — force-quit, last pane closed, each of
SIGTERM/SIGHUP/SIGINT/SIGQUIT, panic, early return, SIGKILL — is adopted as the
test matrix.

## Testing

| Target | Approach |
| --- | --- |
| `internal/layout` | Table-driven plus property tests (`rapid`) against the four invariants |
| `internal/protocol` | Round-trip encode/decode |
| Server | Driven by a scripted transport; assert on emitted messages. No terminal, runs in CI. |
| Client | Fed a canned message stream; snapshot the composed `uv.Screen` as text |
| Animation | Injected fake clock, step the springs, snapshot each frame |
| Teardown | Real PTY, real `/bin/sh`, assert against the real process table |
| Emulator resize | Write known text, narrow, widen, assert the text is recoverable |

The fake-clock split is worth the small effort up front. Animation that can only
be evaluated by watching it is animation that cannot be regression-tested.

## Milestones

Each leaves a running program.

1. Raw mode, read keys, restore cleanly on every exit path including signals.
2. Spawn a shell on a PTY and pipe it through. A terminal inside a terminal.
3. Cell buffer and diff renderer via Ultraviolet. Static two-pane frame.
4. `x/vt` behind `Terminal`, one emulator per pane. **A working two-pane mux.**
   Run the emulator resize-reflow test here.
5. `internal/layout`: strip, columns, widths, `ScrollStrategy`, `[]Placement`.
   Pure and property-tested.
6. Cut the seam: move state behind `protocol` + `transport.InProc`.
7. Focus, input routing, `$mod` keybindings.
8. Animation: springs on placements, retarget, freeze.
9. Agent status, scrollback, mouse.

Milestone 6 is placed after 5 rather than first on purpose: the message types are
easier to get right once the state they carry exists. The risk is that the seam
gets harder to cut the longer it waits, so 6 must not slip past 7.

## Open questions

- Does `Emulator.Resize` reflow? Resolved at milestone 4.
- Which key is `$mod`? gwae uses Option universally on macOS; over SSH that
  depends on the client terminal sending Meta. May need to differ by platform.
- Sliver content for `CardStrategy` is a chrome design, not a content crop — a
  four-cell slice of wrapped agent output carries no information. Settled when
  cards are built.
