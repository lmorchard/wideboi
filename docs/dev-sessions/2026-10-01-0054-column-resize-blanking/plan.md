# Plan: Fix CLI client column resize content blanking

## Goals

1. Prevent CLI client from discarding pane contents during and after column resizing.
2. Ensure clients connecting to a detached session (or when `sizeOwner == nil`) properly become `sizeOwner` or are permitted to resize columns.
3. Keep tests clean, fast, and comprehensive.

## Implementation Steps

### Phase 1: Client-side mirror preservation
- File: `internal/client/viewport.go`
- In `applySnapshotLocked`:
  - Only allocate a new mirror if one does not already exist (`!ok`).
  - Do not re-allocate or delete `paneUpdates` for existing mirrors.
  - In `ensureMirrorLocked`, if resizing an existing mirror, preserve existing cells by copying them over.
- Verification:
  - Run `go test -v ./internal/client -run TestResizeColumnDoesNotBlankOnSubsequentSnapshot` and watch it pass.
  - Run all tests in `./internal/client/...`.

### Phase 2: Server-side `sizeOwner` and width control
- File: `internal/server/handlers.go`
  - In `handleAttachLocked`: if `s.sizeOwner == nil && (s.rows == 0 || s.attachedCountLocked() == 1)`, assign `s.sizeOwner = tp`, `s.cols = m.Cols`, `s.rows = m.Rows`.
  - In `handleSetPaneWidthLocked`: allow if `tp == s.sizeOwner || s.sizeOwner == nil || !s.isAttachedLocked(tp)`.
  - In `handleVerbLocked`: for `VerbCycleWidth`, `VerbGrowWidth`, `VerbShrinkWidth`, allow if `tp == s.sizeOwner || s.sizeOwner == nil`, and set `eff.needBroadcast = true`.
- Verification:
  - Run `go test -v ./internal/server -run TestReattachingClientBecomesSizeOwner` and watch it pass.
  - Run all tests in `./internal/server/...`.

### Phase 3: Full suite verification
- Run `make quick` (fmt-check, lint, seam-check, test, web-test).
- Run `make check`.
