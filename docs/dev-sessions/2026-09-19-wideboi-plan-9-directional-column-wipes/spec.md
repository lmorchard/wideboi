# wideboi Plan 9 — Directional Column Wipes

**Date:** 2026-09-19
**Parent spec:** `docs/BEYOND-V1.md` §1
**Status:** In Progress

## Goal

Implement progressive directional column wipes during focus transitions (left-to-right when focusing right, right-to-left when focusing left).

## Design

1. **`WipeTransition` (`internal/client/wipe.go`):**
   - Captures frame A (previous surface) and frame B (target surface).
   - Direction: `WipeLeftToRight` (focus right) or `WipeRightToLeft` (focus left).
   - Step count: 8 steps (smooth and fast over ~120 ms).
   - Progressive column reveal using `compose.Blit` rect clipping.
   - Hides cursor for duration of the transition per spec.

2. **Client Integration (`internal/client/client.go`):**
   - When focus changes (`focusPaneID` shifts), triggers a new `WipeTransition` if frame A is available.
   - `Client.Draw` delegates to active `WipeTransition` until complete, then resumes normal rendering.

3. **Testing:**
   - Unit tests in `internal/client/wipe_test.go` verifying frame progression, column reveal bounds, direction, and completion.
