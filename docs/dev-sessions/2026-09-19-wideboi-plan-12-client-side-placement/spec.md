# wideboi Plan 12 — Client-Side Placement Calculation

**Date:** 2026-09-19
**Parent spec:** `docs/BEYOND-V1.md` §7
**Status:** In Progress

## Goal

Move placement computation (`ComputePlacements`) from being purely server-driven to being calculated client-side. The server ships layout strip structural data (`[]ColumnData`, `FocusPaneID`, `PaneStatuses`) in `MsgLayoutSnapshot`, and each attached client computes `[]PlacementData` locally for its own host viewport dimensions (`c.cols`, `c.rows`).

## Architecture & Design

1. **Protocol (`internal/protocol/messages.go`):**
   - Add `ColumnData`: `{ PaneID, Width, Height int }`.
   - Update `MsgLayoutSnapshot` to include `Columns []ColumnData`.

2. **Server (`internal/server/server.go`):**
   - `broadcastLayoutLocked` populates `snapshot.Columns` via `s.strip.Columns()`.
   - Populates `Placements` for backwards compatibility.

3. **Client (`internal/client/client.go`):**
   - Holds a local `Strip *layout.Strip`.
   - On `MsgLayoutSnapshot`, syncs local strip columns and recomputes `c.placements` locally using `c.cols` and `c.rows`.
   - On `SendResize`, immediately recomputes `c.placements` locally for the new dimensions without waiting for a roundtrip.

4. **Testing:**
   - Unit tests in `internal/client/client_test.go` verifying local placement calculation on snapshot and client resize.
