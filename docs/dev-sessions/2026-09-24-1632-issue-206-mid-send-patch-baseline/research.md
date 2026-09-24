# Research: Keep the sent frame as the patch baseline when a pane changes mid-send

## 1. Problem & Mechanism

In `internal/server/server.go`:
During `broadcastPaneUpdates(ctx, false)`:
1. Under `s.mu.Lock()`:
   - `underlyingGen := p.Generation()` (`server.go:1132`)
   - `targets` records `underlyingGen` and `wireGen` (`server.go:1193-1205`)
   - `baseline, hasBase := s.paneFrames[tp][id]` (`server.go:1187-1188`)
2. Outside `s.mu`:
   - `frame, ok := p.UpdateMessageForOffset(...)` renders the frame (`server.go:1234`)
   - `update := frame; update.Generation = target.wireGen` (`server.go:1273-1274`)
   - If `target.hasBase`, `patch, ok := protocol.BuildPanePatch(target.baseline, update)` (`server.go:1282`)
   - `accepted := target.tp.SendServer(ctx, message)` (`server.go:1290`)
3. Back under `s.mu.Lock()` (`server.go:1318-1385`):
   - Lines 1341-1349:
     ```go
     // Content can change while update was being rendered or sent.
     if r.pane.Generation() != r.underlyingGen {
         delete(s.paneGens[r.tp], r.paneID)
         delete(s.paneFrames[r.tp], r.paneID)
         delete(s.paneUnderlyingGens[r.tp], r.paneID)
         delete(s.paneOutputGens[r.tp], r.paneID)
         delete(s.paneSbLens[r.tp], r.paneID)
         continue
     }
     ```

When `r.pane.Generation() != r.underlyingGen`, the send was successful (`r.accepted == true`), and the client accepted and applied `r.frame` (either as full snapshot or patch).
However, because `r.pane.Generation()` moved while rendering or sending, the server deleted the client's baseline tracking entries (`paneGens`, `paneFrames`, etc.).
On the next tick:
- `s.paneGens[tp][id]` is missing (`!hasWireGen`).
- Therefore `hasBase` is `false`.
- The server is forced to send a full `MsgPaneUpdate` snapshot instead of a `MsgPanePatch`.

Under sustained or rapid output (such as vim paging or continuous text streams), `r.pane.Generation() != r.underlyingGen` triggers repeatedly, degrading patch transmission to full snapshots on nearly every tick.

## 2. Why Keeping `r.wireGen` + `r.frame` is Safe

- `r.frame` was transmitted to the client and accepted (`r.accepted == true`).
- The client's local mirror is now at `r.frame` with `Generation == r.wireGen`.
- The generation counter `p.Generation()` is monotonically increasing (atomic `AddUint64`).
- On the next tick:
  - `underlyingGen := p.Generation()` will be `> r.underlyingGen`.
  - `wireGen := underlyingGen + scrollGen` will be `> r.wireGen`.
  - `wireGen != lastWireGen` will be `true`, so `dirty` is `true`.
  - `hasWireGen` and `hasBase` will be `true`.
  - `target.baseline` is `r.frame`.
  - `protocol.BuildPanePatch(base, next)` compares rendered row slices `!slices.Equal(base.Lines[y], next.Lines[y])`.
  - If a patch cannot be constructed (e.g. dimensions changed or more than half rows changed and no clean shift), `BuildPanePatch` returns `ok = false`, falling back cleanly to a full update.
- Error recovery paths remain intact:
  - `!r.accepted` continues to delete baseline entries (`server.go:1333-1340`).
  - `MsgResync` continues to delete baseline entries (`server.go:572-575`).
  - Layout / size changes trigger `broadcastLayout` which passes `force = true`, disabling `hasBase` and sending full snapshots.
  - Client-side patch rejection triggers `MsgResync`.

## 3. Existing Tests & Coverage

- `internal/server/paneupdate_test.go`:
  - `TestGenerationChangingDuringRenderIsRetried` (`paneupdate_test.go:315`): Uses `changingDrawGrid` which bumps generation during `Draw`. Confirms that pane is resent on the next broadcast.
  - `TestDeliveryRecordsAreForgotten` (`paneupdate_test.go:481`): Confirms cleanup when panes exit or clients drop.
- `internal/server/pane_traffic_test.go`:
  - `TestPaneTrafficWorkloads` (`pane_traffic_test.go:20`): Simulates 30 ticks of various workloads and verifies client mirror matches full render. Currently only tests writes occurring *before* `broadcastPaneUpdates`.
- `scripts/traffic.py`:
  - Scenarios: `typing`, `scroll`, `scroll-paced`, `tui`, `large`.
  - `tui` scenario exercises vim paging with `paced(run, b"j", 150, 30)` at 30 Hz. Currently generates ~34 full updates due to mid-send generation bumps.

## 4. Analogue Patterns & Other Consumers

- `server.go:572-575`: `MsgResync` handler removes `s.paneGens[tp][resyncPaneID]`, `s.paneFrames[tp][resyncPaneID]`, and `s.paneUnderlyingGens[tp][resyncPaneID]`.
- `server.go:305-316`: `removeTransportLocked` deletes all tracking maps for departing transport.
- `server.go:1387-1473`: `cleanExitedPanesLocked` cleans up maps for closed panes.
All of these correctly remain in place.
