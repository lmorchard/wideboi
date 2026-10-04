# Plan: Direct Agent Status Reporting (Issue #382)

**Goal:** Provide `wideboi set-pane-status` command and socket message to allow agents and hooks to explicitly set or clear a pane's status with 100% determinism.

**Approach:** Add `MsgSetPaneStatusRequest`/`MsgSetPaneStatusResponse` to wire protocol and codec (bump to v27). Implement explicit status storage on `server.Pane`. Implement server handler in `handlers.go`. Add `runSetPaneStatus` in `cmd/wideboi/control.go` and `:set-pane-status` in `internal/commands/registry.go`.

**Tech stack:** Go, protobuf wire protocol, Cobra/flag CLI.

---

## Phase 1: Protocol & Schema Updates

Add `MsgSetPaneStatusRequest` and `MsgSetPaneStatusResponse`.

**Files:**
- Modify: `internal/protocol/wirepb/wideboi.proto` — add messages, add to `ClientMessage` and `ServerMessage`
- Regenerate bindings: `npx @bufbuild/buf generate`
- Modify: `internal/protocol/messages.go` — add Go structs
- Modify: `internal/protocol/codec.go` — marshal/unmarshal
- Modify: `internal/protocol/version.go` — bump to 27
- Modify: `internal/protocol/version_guard_test.go` — update schema hash
- Modify: `web/src/version.ts` — bump to 27

**Verification — automated:**
- [x] `go test -v ./internal/protocol/...` passes — **verified wirepb roundtrips and version guard at 27**

---

## Phase 2: Server Implementation

Implement explicit status storage and server handler.

**Files:**
- Modify: `internal/server/pane.go` — add `explicitStatus`, `SetExplicitStatus`, `ClearExplicitStatus`
- Modify: `internal/server/handlers.go` — add `handleSetPaneStatusRequestLocked`
- Modify: `internal/server/server.go` — route `MsgSetPaneStatusRequest` in `handleClientMsg`
- Test: `internal/server/server_test.go` — add `TestSetPaneStatusExplicitOverride`

**Key changes:**
- Explicit status override takes precedence over PTY output heuristics.
- Process exit code in `--keep` mode still governs reaped processes.

**Verification — automated:**
- [x] `go test -v ./internal/server -run TestSetPaneStatus` passes — **verified setting status, clear, and error handling**

---

## Phase 3: CLI Subcommand, Internal Commands & Documentation

Implement CLI and command palette commands and hook docs.

**Files:**
- Modify: `cmd/wideboi/control.go` — implement `runSetPaneStatus`
- Modify: `cmd/wideboi/main.go` — register `"set-pane-status"` subcommand and help text
- Modify: `internal/commands/registry.go` — register `:set-pane-status`
- Modify: `docs/skills/wideboi-control/SKILL.md` — document `set-pane-status` and agent hooks
- Test: `cmd/wideboi/control_test.go` — add `TestSetPaneStatusCommand`

**Verification — automated:**
- [x] `go test -v ./cmd/wideboi -run TestSetPaneStatus` passes — **verified explicit ID, WIDEBOI_PANE_ID env, clear, and error handling**
- [x] `make quick` passes cleanly — **`go vet`, `seam-check`, Go test suite, and 183 Vitest web tests passed**
