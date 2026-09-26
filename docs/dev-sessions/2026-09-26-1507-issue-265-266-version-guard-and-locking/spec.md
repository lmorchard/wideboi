# Issues 265 & 266 Spec

**Goal:** Guard `protocol.Version` against accidental schema drift and web client skew, and enforce strict lock discipline across client and server so blocking I/O never stalls mutex-guarded operations or splits critical sections.

**Source:** GitHub Issues #265 and #266.

## Current state
- Described in `research.md`.
- `protocol.Version` has no schema hash check; web client hardcodes literal `"wideboi.v14"` across tests.
- Client sends messages to transport while holding `c.mu`, blocking `Draw`.
- Server `resizePanesLocked` drops and retakes `s.mu`, splitting callers' critical sections.
- Server `MsgInput` executes `p.Write(m.Data)` under `s.mu` with a 50ms timeout.
- Server starts two reader loops if a client connects before `Run()`.

## Desired end state
1. **Schema & Version Guard (#265):**
   - A golden test in `internal/protocol` hashes the deterministic marshal of `wirepb.File_internal_protocol_wirepb_wideboi_proto` and maps `protocol.Version -> sha256`.
   - Modifying `wideboi.proto` (and regenerating) without bumping `Version` causes `go test ./internal/protocol` (`make quick`) to fail with message:
     `wire schema changed: bump protocol.Version and update this golden`.
   - Bumping `protocol.Version` without updating `web/src/version.ts` causes `go test ./internal/protocol` to fail with message:
     `web client version wideboi.v... does not match protocol.Version ...`.
   - `web/src/version.ts` exports `VERSION_PROTOCOL = "wideboi.v14"`. All web components and tests import from this module.
2. **Client Locking Discipline (#266):**
   - No `transport.SendClient` is called while holding `c.mu`.
   - `SendVerb`, `SendSplit`, `HandleServerMsg`, and search methods gather outgoing messages under `c.mu`, release `c.mu`, and dispatch outside the lock.
   - Unit test verifies `c.Draw` completes even when `transport.SendClient` blocks on a full transport.
3. **Server Critical Section & Resize Atomicity (#266):**
   - Critical sections in `handleClientMsg` and `onPaneExit` are never broken by unlocking and re-locking `s.mu`.
   - Resize computation is separated into `prepareResizePanesLocked()` (pure state update under `s.mu` returning jobs) and `applyResizeJobs()` (runs after `s.mu.Unlock()`).
4. **Server Input Queueing (#266):**
   - `Pane.SendBytes(b []byte)` queues raw bytes onto `p.input` non-blockingly, matching `SendKey`.
   - The background input pump processes queued raw bytes and forwards them via `p.grid.SendText`.
   - `MsgInput` calls `p.SendBytes(m.Data)` without holding `s.mu` during PTY write.
   - `MsgSendInputRequest` checks pane validity under `s.mu` and executes `p.Write(m.Data)` after releasing `s.mu`.
   - `input_queue_test.go` verifies keys, mouse events, and byte pastes maintain order in `p.input`.
5. **Server Double-Reader Prevention (#266):**
   - Server tracks which transports have active reader loops (`startedTransports map[transport.Transport]bool`).
   - A reader loop is started at most once per transport.
   - Unit test verifies a client connected before `Run()` receives exactly one reader loop.

## Design decisions
- **Decision:** Deterministic `FileDescriptorProto` marshal hash over hashing the `.proto` file text.
  - **Why:** Whitespace, formatting, or comment edits in `.proto` should not trigger a protocol version bump; only true wire schema changes should.
  - **Rejected:** Plain file hashing of `wideboi.proto`.
- **Decision:** Go test checking `web/src/version.ts` content.
  - **Why:** Avoids adding a mandatory code generator step for web during `make quick` while guaranteeing that web and Go never drift.
  - **Rejected:** Complex codegen build step that requires external tools during `make quick`.
- **Decision:** Queue `MsgInput` bytes through `p.input` as `RawBytes`.
  - **Why:** Ensures strict in-order interleaving of typed keys, pasted text, and mouse tracking, while keeping `handleClientMsg` completely non-blocking under `s.mu`.
  - **Rejected:** Separate paste goroutine (which could reorder pastes relative to keys).
- **Decision:** Separate `prepareResizePanesLocked` and `applyResizeJobs` effects.
  - **Why:** Guarantees critical sections in message handlers remain strictly atomic and avoids race conditions like recreation of dropped clients.
  - **Rejected:** Retaining `s.mu.Unlock()` / `defer s.mu.Lock()` inside `resizePanesLocked`.

## What we're NOT doing
- We are not changing the wire protocol format itself (no bump needed now; we are establishing the guard so future changes don't miss it).
- We are not rewriting ultraviolet or vt input decoders.
- We are not touching unrelated client UI or web styles.

## Patterns to follow
- `internal/client/mouse.go:94,122-126`: collect messages under lock, unlock, then send.
- `internal/server/pane.go:217-224`: non-blocking channel send with dropped counter for queueing.
