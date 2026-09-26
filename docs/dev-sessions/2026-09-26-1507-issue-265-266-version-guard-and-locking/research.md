# Research: Issue 265 & 266

## Issue 265: Guard protocol.Version against missed wire bumps

### Current State
1. `internal/protocol/version.go:32`:
   `const Version uint32 = 14`
2. `internal/protocol/wirepb/wideboi.proto`:
   Defines the protobuf wire messages. Compiled into `internal/protocol/wirepb/wideboi.pb.go`.
   `wideboi.pb.go` exposes `File_internal_protocol_wirepb_wideboi_proto protoreflect.FileDescriptor`.
3. `web/src/client.ts:37`:
   `const versionProtocol = "wideboi.v14";`
   Hardcoded literal.
4. `web/src/client.test.ts`, `web/src/lifecycle.test.ts`, and 8 Playwright specs (`cards.spec.js`, `horizontal-viewport.spec.js`, `lifecycle.spec.js`, `mobile.spec.js`, `pane-borders.spec.js`, `search.spec.js`, `settings.spec.js`, `vertical-viewport.spec.js`):
   All hardcode `'wideboi.v14'` in tests and mock WebSocket constructors.

### Mechanism for Guarding Version
- Go standard protobuf descriptor: `protodesc.ToFileDescriptorProto(wirepb.File_internal_protocol_wirepb_wideboi_proto)`.
- Deterministic marshal via `proto.MarshalOptions{Deterministic: true}.Marshal(descProto)`.
- Hash via `sha256.Sum256`.
- Golden mapping: `map[uint32]string{ 14: "<sha256>" }`.
  - If wirepb descriptor changes without Version bump: hash mismatch -> fails with explicit message.
  - If Version is bumped without updating golden: missing version in map -> fails with explicit message.
- Web literal sync:
  - Create `web/src/version.ts` exporting `PROTOCOL_VERSION = 14;` and `VERSION_PROTOCOL = "wideboi.v14";`.
  - In `internal/protocol/version_test.go`: read `web/src/version.ts` and verify it contains `fmt.Sprintf("wideboi.v%d", Version)`.
  - In `web/src/client.ts`, `web/src/client.test.ts`, `web/src/lifecycle.test.ts`, and `web/tests/browser-fixture.ts`: import and use `VERSION_PROTOCOL`.

---

## Issue 266: Lock discipline: sends and writes under client/server mutexes

### Current State
1. **Client sends under `c.mu`:**
   - `SendVerb` (`internal/client/client.go:1456-1512`): calls `c.transport.SendClient` while holding `c.mu`.
   - `SendSplit` (`internal/client/client.go:1617-1626`): holds `c.mu` around `c.transport.SendClient`.
   - `HandleServerMsg` (`internal/client/client.go:388-390`): under `MsgPanePatch` baseline mismatch, calls `c.transport.SendClient` under `c.mu`.
   - `SearchCommit` / `SearchNavigate` / `SearchEnd` / `applyHistoryLocked` (`internal/client/search.go`): calls `c.transport.SendClient` under `c.mu`.
   - Result: If the transport send channel is full or blocked, `c.mu` is held, which freezes `c.Draw` and all client operations.
   - Contrast: `HandleMouse` (`internal/client/mouse.go:94,122-126`) and `SendKey` (`client.go:1563-1573`) unlock `c.mu` before sending.

2. **Server `resizePanesLocked`:**
   - `internal/server/server.go:1343-1381`:
     `resizePanesLocked` performs `s.mu.Unlock()` and `defer s.mu.Lock()` around calls to `j.pane.ResizeOrdered(...)`.
   - Calls in `handleClientMsg` (12 places) and `onPaneExit` split the critical section of the caller.
   - For example, in `handleClientMsg`, `dropClient` can run during the unlocked window, removing `tp`, but `handleClientMsg` afterwards writes `s.pendingPaneCreated[tp]`, leaking state.

3. **Server pane input writes under `s.mu`:**
   - `internal/server/server.go:817`: `_, _ = p.Write(m.Data)` in `MsgInput` handler runs under `s.mu`.
   - `p.Write` calls `p.pty.WriteBounded(b, ptyWriteTimeout)` which can block up to 50ms per write.
   - A paste into a stuck child blocks `s.mu` for up to 50ms per chunk, stalling all clients on the server.
   - Contrast: `p.SendKey` queues into `p.input` non-blockingly (`keyQueueDepth = 256`), drained by a background goroutine.

4. **Server double-reader race:**
   - `internal/server/server.go:338-341` (`admitSocketConn`) and `2237-2240` (`ListenWebSocket`):
     Appends `sConn` to `s.transports`, unlocks, and starts `go s.handleClientConnLoop(ctx, sConn)`.
   - `internal/server/server.go:460-466` (`Run`):
     Copies `initialTransports := append([]transport.Transport{}, s.transports...)`, and starts `go s.handleClientConnLoop(ctx, tp)` for every transport!
   - If a socket or web client connects between listener startup and `Run()`, it gets two concurrent `handleClientConnLoop` reader loops competing for `ClientSendChan()`, which can reorder keystrokes and messages.
