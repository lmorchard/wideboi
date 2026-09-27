# Implementation Plan: In-Place Upgrade State Preservation and Failure Recovery (Issues 259 & 273)

**Goal:** Fix client leaks on upgrade failures and preserve session state across in-place server upgrades (ownership, web server, terminal modes, sizing, startup status, and child output).

**Approach:**
1. Call `s.dropClient(ctx, tp)` on `execFn` error, and keep client attached on `PrepareUpgrade` failure.
2. Track terminal modes (`IsAltScreen`, bracketed paste, cursor keys) in `vtGrid`, enter alt-screen in `RestoreSnapshot` before writing cells, and snapshot grids right before `syscall.Exec`.
3. Preserve `OwnerPID`, `SizeOwnerPID`, and `StartupLaunched` in `UpgradeState`, re-establishing ownership upon socket reconnection.
4. Preserve web server active state across upgrade, and increase client reconnect timeout to 10s.

**Tech stack:** Go, `x/vt`, `x/ansi`, Unix domain sockets, `syscall.Exec`.

---

## Phase 1: Failed Upgrade Client Cleanup & Unit Tests (Issue #259)

Ensure that when in-place upgrade fails at either `PrepareUpgrade` or `execFn()`, the requesting client connection does not leak or leave zombie state.

**Files:**
- Modify: `internal/server/server.go` — call `s.dropClient(ctx, tp)` on `execFn()` error.
- Create/Modify: `internal/server/upgrade_test.go` — unit tests for failed upgrade paths.

**Key changes:**
In `internal/server/server.go`:
```go
if req, ok := msg.(protocol.MsgUpgradeRequest); ok {
    execFn, err := s.PrepareUpgrade(req.BinPath)
    if err != nil {
        tp.SendServer(ctx, protocol.MsgUpgradeResponse{Error: err.Error()})
        continue
    }
    tp.SendServer(ctx, protocol.MsgUpgradeResponse{})
    if d, ok := tp.(interface{ Drain(time.Duration) bool }); ok {
        d.Drain(500 * time.Millisecond)
    }
    if c, ok := tp.(io.Closer); ok {
        _ = c.Close()
    }
    if err := execFn(); err != nil {
        slog.Error("exec failed during upgrade", "err", err)
        s.dropClient(ctx, tp)
    }
    return
}
```

**Verification — automated:**
- [ ] `go test -v ./internal/server -run TestUpgradeFailure` passes
- [ ] `make quick` passes

**Verification — manual:**
- [ ] Verify `dropClient` removes `tp` from `s.transports` and `s.owner` if applicable.

---

## Phase 2: Terminal Modes & Output Snapshot Timing (Issue #273)

Capture alt-screen, bracketed paste, and cursor keys in `GridSnapshot`; restore alt-screen mode before setting cells; snapshot grids right before exec; fix `p.cols`/`p.rows` race.

**Files:**
- Modify: `internal/server/term/grid.go` — mode tracking in callbacks, `ExportSnapshot`, and `RestoreSnapshot`.
- Modify: `internal/server/upgrade.go` — snapshot inside `execFn()` right before exec; use `p.Size()`.
- Test: `internal/server/term/grid_test.go` — verify alt-screen and mode snapshot round-trip.

**Key changes:**
In `internal/server/term/grid.go`:
```go
type GridSnapshot struct {
    ...
    IsAltScreen   bool `json:"is_alt_screen"`
    BracketedPaste bool `json:"bracketed_paste"`
    CursorKeys    bool `json:"cursor_keys"`
}
```
In `RestoreSnapshot`:
```go
if snap.IsAltScreen {
    _, _ = g.em.Write([]byte("\033[?1049h"))
}
if snap.BracketedPaste {
    _, _ = g.em.Write([]byte("\033[?2004h"))
}
if snap.CursorKeys {
    _, _ = g.em.Write([]byte("\033[?1h"))
}
// Then populate screen cells...
```
In `internal/server/upgrade.go`:
- Move grid snapshotting and state writing into `execFn` so it happens immediately before `syscall.Exec`.
- Replace direct `p.cols`/`p.rows` reads with `cols, rows := p.Size()`.

**Verification — automated:**
- [ ] `go test -v ./internal/server/term -run TestGridSnapshotModes` passes
- [ ] `make quick` passes

**Verification — manual:**
- [ ] Verify snapshot restores cells into alternate screen when `IsAltScreen` is true.

---

## Phase 3: Ownership, Sizing & Startup Pane Preservation (Issue #273)

Preserve `OwnerPID`, `SizeOwnerPID`, and `StartupLaunched` across upgrade, and restore ownership when the client reconnects.

**Files:**
- Modify: `internal/server/upgrade.go` — add fields to `UpgradeState`, implement `PrepareUpgradeState` / restore logic, test `cleanExecArgs`.
- Modify: `internal/server/server.go` — track `expectedOwnerPID` and `expectedSizeOwnerPID`, restore on client handshake.
- Test: `internal/server/upgrade_test.go` — test `cleanExecArgs` and PrepareState -> state file -> RestoreState round trip without exec.

**Key changes:**
In `UpgradeState`:
```go
type UpgradeState struct {
    ...
    OwnerPID        int  `json:"owner_pid,omitempty"`
    SizeOwnerPID    int  `json:"size_owner_pid,omitempty"`
    StartupLaunched bool `json:"startup_launched,omitempty"`
}
```
In `admitSocketConn`:
```go
if s.expectedOwnerPID != 0 && peer.PID == s.expectedOwnerPID && s.owner == nil {
    s.owner = sConn
    s.expectedOwnerPID = 0
}
if s.expectedSizeOwnerPID != 0 && peer.PID == s.expectedSizeOwnerPID && s.sizeOwner == nil {
    s.sizeOwner = sConn
    s.expectedSizeOwnerPID = 0
}
```

**Verification — automated:**
- [ ] `go test -v ./internal/server -run TestCleanExecArgs` passes
- [ ] `go test -v ./internal/server -run TestUpgradeStateRoundTrip` passes
- [ ] `make quick` passes

**Verification — manual:**
- [ ] Verify `cleanExecArgs` handles `-owner-fd`, `--owner-fd`, `--owner-fd=3`, and adds `server`.

---

## Phase 4: Web Server Preservation & Client Reconnect Resilience (Issue #273)

Preserve web server running state across upgrade, and give the reconnecting client a 10s dial window.

**Files:**
- Modify: `internal/server/upgrade.go` — record web server state in `UpgradeState`.
- Modify: `cmd/wideboi/main.go` — restore web server if active in state; increase reconnect dial ceiling from 2s to 10s.
- Test: `cmd/wideboi/upgrade_e2e_test.go` — verify end-to-end upgrade keeps web server and ownership alive.

**Key changes:**
In `UpgradeState`:
```go
type UpgradeState struct {
    ...
    WebRunning    bool   `json:"web_running,omitempty"`
    WebAddr       string `json:"web_addr,omitempty"`
    WebToken      string `json:"web_token,omitempty"`
    WebTLSEnabled bool   `json:"web_tls_enabled,omitempty"`
}
```
In `cmd/wideboi/main.go`:
```go
reconnectConn, err := dialWithin(cfg.Socket, 10*time.Second)
```

**Verification — automated:**
- [ ] `go test -v ./cmd/wideboi -run TestUpgradeServer` passes
- [ ] `make check` passes

**Verification — manual:**
- [ ] Verify all tests pass including race detector and exit verification.
