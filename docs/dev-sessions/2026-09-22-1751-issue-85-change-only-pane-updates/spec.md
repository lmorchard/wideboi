# Change-only pane updates Spec

**Goal:** An idle session sends ~0 `MsgPaneUpdate`s per second, down from ~30 per pane per client.
The server also stops rendering panes that haven't changed.

**Source:** https://github.com/lmorchard/wideboi/issues/85

## Current state

See `research.md`. The load-bearing facts:

- Every 33ms tick, `broadcastPaneUpdates` (`internal/server/server.go:687-701`) renders every pane in full
  (`Pane.UpdateMessage`, `internal/server/pane.go:281-327`) and sends each one to every transport. It ignores
  the send result and does no change detection.
- Neither `Pane` nor `vtGrid` tracks changes (`pane.go:27-62`, `internal/server/term/grid.go:151-199`).
- Everything in a `MsgPaneUpdate` changes through one of three paths:
  - `vtGrid.Write` (`grid.go:335-344`). Cells, cursor position, cursor visibility and mouse modes all change
    inside its parse.
  - `vtGrid.Resize` (`grid.go:447-505`), including reflow.
  - `vtGrid.SetScrollOffset` (`grid.go:523-532`).
- A layout snapshot makes the client drop the mirror of any pane that isn't placed, and replace a mirror with a
  blank surface when a placement outgrows it (`internal/client/client.go:188-200`). The client depends on full
  pane updates following every snapshot.
- `InProcChannel.SendServer` drops and returns false when full. `ServerSocketConn.SendServer` blocks, and
  returns false only when the connection is closed or ctx is cancelled.

## Desired end state

- Once a pane's output has settled, an attached socket client receives no more `MsgPaneUpdate`s for it until
  its content, cursor, mouse mode, size or scroll offset changes.
- The server does not call `UpdateMessage` for a pane that no transport needs.
- A newly accepted transport receives every pane on its first tick, with or without a layout broadcast.
- Every `broadcastLayout` still sends every pane to every transport, as it does today.
- A dropped update (`SendServer` false) is retried on the next tick, for that client only.

## Design decisions

- **Decision:** a generation counter on the grid. `term.Grid` gains `Generation() uint64`. `vtGrid` holds an
  `atomic.Uint64` and bumps it:
  - in `Write`
  - in `Resize`
  - in `SetScrollOffset`, only when the clamped offset differs from the current one
  - **Why:** these are the only mutation paths (see above). An idle pane then skips both the render and the send.
  - **Rejected:** comparing rendered output per client. It can't miss a change, but it keeps ~60 full renders a
    second plus a stored copy per client. Also rejected: the emulator's `Touched()`, which resize clears
    (LESSONS) and which wideboi doesn't use today.
- **Decision:** read the generation *before* rendering. Record that value as sent.
  - **Why:** if a write lands between the read and the render, the recorded generation is older than the
    content, so the next tick resends. This errs toward sending one update too many, never one too few, and
    needs no new locking.
- **Decision:** per-transport delivery tracking. The server holds `sent map[transport.Transport]map[int]uint64`
  (pane ID → last generation delivered).
  - A pane goes to a transport when its current generation differs from that transport's record, or the pane
    has no record.
  - The record is updated only when `SendServer` returns true.
  - Entries are removed in `removeTransportLocked` and `Close`. Records for pane IDs no longer in `s.panes` are
    pruned during the broadcast.
  - **Why:** the retry is per client, so one full client doesn't cause resends to the others. A new transport
    starts with no records, so it gets every pane. This mirrors the retry in `broadcastLayout`
    (`server.go:667-682`), but per client.
  - **Rejected:** a slow periodic full resend. It would hide a mutation path that forgets to bump the counter.
    We would rather such a bug show up as a stale pane.
- **Decision:** `broadcastLayout` forces its pane updates. It sends every pane to every transport
  unconditionally, and records the generations it delivered.
  - **Why:** snapshots can prune or blank client mirrors (`client.go:188-200`), so a pane must be resent after
    any layout change even if its content hasn't changed.
  - **How:** `broadcastPaneUpdates(ctx, force bool)`. The tick path passes false and `broadcastLayout` passes true.

- **Decision:** serialize `broadcastPaneUpdates` with a `paneSendMu`, held across render, send and record.
  Lock order is `paneSendMu`, then `s.mu`.
  - **Why:** several goroutines broadcast: the Run loop, each client's message loop (through
    `broadcastLayout`), and `onPaneExit`. Two overlapping rounds could deliver an older render last while
    recording the newer generation. That client would stay stale with nothing left to trigger a resend. Today
    the next tick hides this.
  - **Cost:** none new. Every broadcaster already blocks on a wedged socket's `SendServer`.
  - **Rejected:** per-record "only move forward" checks. They don't help, because the problem is the order
    messages reach the socket, not the order records are written.

## Patterns to follow

- Delivery bookkeeping follows `broadcastLayout`'s "mark only once it actually went somewhere"
  (`server.go:656-682`).
- Retry tests follow `TestUndeliveredStatusBroadcastIsRetried` / `TestUndeliveredToOneOfTwoClientsIsRetried`
  (`internal/server/status_test.go:136, 264`). They use small InProc buffers and the fake `statusGrid`
  (`status_test.go:22-62`), which needs a controllable `Generation()`.
- The wire-level test extends the real-socket setup in `cmd/wideboi/main_test.go:17-105`: attach, let output
  settle, then count `MsgPaneUpdate` over ~1s and expect 0.
  - It must also show that a change is still delivered: type into a pane and see an update arrive.
- Use `-count=1` for new Go tests, run timing-sensitive tests four times, and prove each new test fails before
  the fix (break it, see it go red, restore).

## What we're NOT doing

- **Delta encoding.** A changed pane still sends a full grid. Row-level diffs are a separate optimisation.
- **Other senders.** No change to `MsgLayoutSnapshot` cadence or to status/title change detection.
- **Server placements.** No fixing the duplicated placement computation (#47).
- **Client changes.** No client change is expected, apart from the trace-log comment at `client.go:208-209`
  that says the message fires "whether or not anything changed".
- **Draw comment.** No fixing `vtGrid.Draw`'s stale "fast path" comment (`grid.go:534-537`). Record it as a note.
- **`MsgPaneClosed`.** No sending it or removing it.
- **Blocking sends.** No change to the blocking `SendServer` on sockets (#38).
- **Periodic resend.** No slow resend timer (see decisions).

## Open questions

- **Should a MsgScroll that changes the offset broadcast immediately?** Default: no. The next tick (≤33ms)
  picks it up through the generation bump, the same as today.
- **Should the generation also bump on title or status changes?** Default: no. Neither is carried in
  `MsgPaneUpdate`. Both ride the snapshot through `broadcastLayoutIfStatusChanged`, which already forces pane
  updates.
