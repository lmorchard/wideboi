# Issue 206: Keep the sent frame as the patch baseline when a pane changes mid-send Spec

**Goal:** Allow `broadcastPaneUpdates` to retain successfully transmitted frames as patch baselines even when pane generation advances mid-send, enabling subsequent updates under continuous output to send lightweight patches rather than full snapshots.

**Source:** GitHub Issue #206

## Current state

In `internal/server/server.go:1341-1349`, when a frame is sent to a client, the server checks:
```go
if r.pane.Generation() != r.underlyingGen {
    delete(s.paneGens[r.tp], r.paneID)
    delete(s.paneFrames[r.tp], r.paneID)
    delete(s.paneUnderlyingGens[r.tp], r.paneID)
    delete(s.paneOutputGens[r.tp], r.paneID)
    delete(s.paneSbLens[r.tp], r.paneID)
    continue
}
```
If the pane's generation moved during render or transmission (common during sustained child output), the server purges the baseline records for that client. Consequently, on the following tick, the server lacks a baseline (`hasBase == false`) and emits a full `MsgPaneUpdate` snapshot instead of a `MsgPanePatch`. In live workloads such as vim paging (`make traffic --only tui`), this causes dozens of unnecessary full snapshots (e.g. 34 fulls in an 11s run).

## Desired end state

1. When an update is accepted (`r.accepted == true`), the server records `r.wireGen`, `r.frame`, `r.underlyingGen`, `r.outputGen`, `r.sbLen`, `r.offset`, and `r.unread` as the client's current baseline, regardless of whether `r.pane.Generation() != r.underlyingGen`.
2. On the subsequent tick, the server sees `wireGen != lastWireGen` (dirty), builds a `MsgPanePatch` against `s.paneFrames[tp][paneID]`, and delivers a patch rather than a full snapshot.
3. If the send was rejected (`!r.accepted`), the baseline is cleared as before (`server.go:1333-1340`).
4. In `make traffic` under `tui` (vim paging), full snapshots are reduced from ~34 down to only the initial/forced snapshots (e.g. ~2), with nearly all updates delivered as row or shift patches.
5. In-process tests in `internal/server` verify that when writes land during render, updates continue to send patches and reconstruct to match a full emulator render.

## Design decisions

- **Decision:** Remove the `if r.pane.Generation() != r.underlyingGen` deletion block in `server.go:1341-1349` and allow successful sends to update the baseline maps.
  - **Why:** The client received and applied `r.frame` labeled `r.wireGen`. Keeping `r.wireGen` and `r.frame` accurately reflects the client's state. When the next frame is rendered at a higher generation, `protocol.BuildPanePatch` diffs the actual rendered rows against `r.frame`. If changes fit within row/shift patch limits, a patch is emitted; if not, it cleanly falls back to a snapshot.
  - **Rejected:** Keeping a flag to force full snapshot on next tick. That defeats the entire purpose of delta patching under sustained output.
  - **Rejected:** Freezing or locking the emulator across the entire network send. That would block pane output and client input pumps, introducing unacceptable latency and stutter.

## Patterns to follow

- Baseline tracking in `internal/server/server.go:1351-1385`:
  `s.paneGens`, `s.paneFrames`, `s.paneUnderlyingGens`, `s.paneOutputGens`, `s.paneSbLens`, `s.paneOffsets`, `s.paneUnreads`.
- Test harness pattern in `internal/server/pane_traffic_test.go:TestPaneTrafficWorkloads`:
  Simulating server broadcasts with real `term.VT` emulator, `InProcChannel` transport, applying patches with `protocol.ApplyPanePatch`, and comparing against `pane.UpdateMessage()` full render.

## What we're NOT doing

- We are not changing `protocol.BuildPanePatch` or `ApplyPanePatch` algorithms.
- We are not changing the wire protocol or bumping `protocol.Version` (no wire schema changes).
- We are not modifying unattached connection forced-resend behavior (that is Issue #202).
- We are not enabling WebSocket permessage-deflate (that is Issue #203).
- We are not changing emulator scrollback storage (that is Issue #205).

## Open questions

None. The mechanism and safety invariants are fully analyzed in `docs/dev-sessions/2026-09-24-1354-measure-traffic/profile-analysis.md`.
