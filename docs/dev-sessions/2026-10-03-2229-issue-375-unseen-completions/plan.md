# Plan: Distinguish Unseen Completions from Idle Panes (Issue #375)

**Goal:** Badge panes with unseen completions as `Done` (`✓`) until the user focuses / views the pane, preventing background completions from silently decaying to `Idle` (`●`) unnoticed.

**Approach:** Track `unseenDone` per client in `Client` (and on `Server` for server dashboard). When an unfocused pane moves from `StatusWorking` to `StatusIdle`, display it as `StatusDone`. Clear the badge as soon as the pane is focused.

**Tech stack:** Go, TypeScript / Web Client, Wideboi client presentation.

---

## Phase 1: Terminal Client Unseen Completion Tracking

Implement `unseenDone` and `displayStatusLocked` in `internal/client/`.

**Files:**
- Modify: `internal/client/client.go` — `unseenDone map[int]bool`, `displayStatusLocked(id)`, clearing in focus methods
- Modify: `internal/client/viewport.go` — transition detection in `HandleServerMsg`
- Modify: `internal/client/statusbar.go` — use `displayStatusLocked`
- Modify: `internal/client/render.go` — use `displayStatusLocked` in card header and status bar
- Test: `internal/client/client_test.go` — test unseen completion lifecycle

**Key changes:**
- When `c.paneStatuses` updates: if `old == StatusWorking && new == StatusIdle && id != c.focusPaneID`, set `unseenDone[id] = true`.
- On focus change (`FocusPaneID`, `FocusLeft`, `FocusRight`, `FocusLast`, `SmartJump`, mouse click, etc.): delete `unseenDone[id]`.
- In `smartJumpTargetLocked`: rank using `displayStatusLocked(id)`.

**Verification — automated:**
- [x] `go test -v ./internal/client/...` passes — **verified TestClientUnseenCompletionBadging**

---

## Phase 2: Server Dashboard Unseen Completion Tracking

Track `unseenDone` on `Server` so the server-rendered dashboard pane reflects completions.

**Files:**
- Modify: `internal/server/server.go` — `s.unseenDone map[int]bool`
- Modify: `internal/server/server.go` — update `updateDashboardLocked` and status changes
- Test: `internal/server/server_test.go` — verify dashboard shows `✔ done` for background completions

**Verification — automated:**
- [x] `go test -v ./internal/server/...` passes — **verified TestServerDashboardUnseenDone**

---

## Phase 3: Web Client Unseen Completion Support & Verification

Add `unseenDone` tracking in the web client.

**Files:**
- Modify: `web/src/wideboi-app.ts` — `unseenDone: Record<number, boolean>`, display status in card headers, mobile bar, and smart jump
- Test: `web/src/wideboi-app.test.ts` (or relevant vitest test)

**Verification — automated:**
- [x] `cd web && npm test` passes — **183 tests passed**
- [x] `make quick` passes cleanly — **`go vet`, `seam-check`, Go test suite, and 183 Vitest web tests passed**
