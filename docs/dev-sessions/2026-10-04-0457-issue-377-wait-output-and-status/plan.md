# Plan: Native wait-output and wait-status Commands (Issue #377)

**Goal:** Add `wait-output` and `wait-status` server primitives, wire protocol messages, CLI subcommands, and internal commands.

**Tech stack:** Go, protobuf, Cobra/flag CLI.

---

## Phase 1: Protocol & Schema Updates

Add `MsgWaitOutputRequest`, `MsgWaitOutputResponse`, `MsgWaitStatusRequest`, and `MsgWaitStatusResponse`.

**Files:**
- Modify: `internal/protocol/wirepb/wideboi.proto`
- Regenerate bindings: `npx @bufbuild/buf generate`
- Modify: `internal/protocol/messages.go`
- Modify: `internal/protocol/codec.go`
- Modify: `internal/protocol/wire_test.go`
- Modify: `internal/protocol/version.go` (bump to 28)
- Modify: `web/src/version.ts` (bump to 28)
- Modify: `internal/protocol/version_guard_test.go` (update schema hash)

**Verification — automated:**
- [x] `go test -v ./internal/protocol/...` passes — **verified wirepb roundtrips and version guard at 28**

---

## Phase 2: Server Implementation

Implement output and status waiters on `Server`.

**Files:**
- Modify: `internal/server/pane.go` — add `onOutput func()` hook invoked on PTY read loop writes.
- Modify: `internal/server/server.go` — initialize waiter maps and hook `onOutput` on created panes.
- Modify: `internal/server/handlers.go` — implement `handleWaitOutputRequestLocked` and `handleWaitStatusRequestLocked`, plus check functions.
- Modify: `internal/server/lifecycle.go` — fail pending output and status waiters when a pane is closed.
- Modify: `internal/server/clients.go` — clean up waiters when transport disconnects.
- Test: `internal/server/server_test.go` — add tests for `wait-output` and `wait-status` (immediate and delayed matches).

**Verification — automated:**
- [x] `go test -v ./internal/server -run TestWait` passes — **verified immediate and delayed matches for output and status**

---

## Phase 3: CLI Subcommands, Internal Commands & Documentation

Implement CLI commands and documentation.

**Files:**
- Modify: `cmd/wideboi/control.go` — implement `runWaitOutput` and `runWaitStatus`.
- Modify: `cmd/wideboi/main.go` — route subcommands and add help text.
- Modify: `internal/commands/registry.go` — register `:wait-output` and `:wait-status`.
- Modify: `docs/skills/wideboi-control/SKILL.md` — document patterns.
- Test: `cmd/wideboi/control_test.go` — add CLI integration tests for both commands.

**Verification — automated:**
- [x] `go test -v ./cmd/wideboi -run TestWait` passes — **verified CLI execution, flag parsing, until filtering, and timeout exit code 124**
- [x] `make quick` passes cleanly — **`go vet`, `seam-check`, Go test suite, and 183 Vitest web tests passed**
