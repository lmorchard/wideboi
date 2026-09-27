# Research: Issues 259 & 273 (In-Place Upgrade Fixes & State Preservation)

## Issue 259: Client leak on failed in-place upgrade
- `internal/server/server.go:491-508`:
  - `PrepareUpgrade` failure sends `MsgUpgradeResponse{Error: err.Error()}` and does `continue`. The client loop continues and the client remains attached.
  - `execFn()` failure: `tp` has already had `d.Drain(...)` called and `c.Close()` called (`server.go:501-503`). When `execFn()` returns an error, `server.go` logs `exec failed during upgrade` and does a bare `return`.
  - On `execFn()` error, `s.dropClient(ctx, tp)` is never called. As a result:
    - `tp` stays in `s.transports`, `s.startedTransports`, `s.attachedTransports`, and `s.peerPIDs`.
    - If `tp` was `s.owner`, `s.owner` still references the closed transport.
    - Subsequent server broadcasts continue to iterate over `s.transports` and attempt to send to the dead transport.
- Fix: On `execFn()` failure, call `s.dropClient(ctx, tp)`. Ensure rollback properly unsets `s.upgrading` and restores `unix.FD_CLOEXEC` on pty descriptors. Unit test both `PrepareUpgrade` failure and `execFn` failure.

## Issue 273: State survival across in-place upgrade

### 1. Output / Input loss
- Currently:
  - In `PrepareUpgrade`, `sn.ExportSnapshot()` is called while pty-reader goroutines in `p.Start()` continue to read from `p.pty.Master` and write into `p.grid` (`internal/server/pane.go:163-166`).
  - The client is drained for 500ms (`server.go:499`), during which any child output is absorbed into the old process's memory and lost upon `syscall.Exec`.
  - While `s.upgrading` is true, all client input is silently dropped (`server.go:619`).
- Analysis & Solution:
  - Defer the snapshotting and state writing until inside `execFn()`, immediately before `syscall.Exec`.
  - In `execFn()`, before exec, take `p.resizeMu` and snapshot each grid. At this point, the time delta between snapshot and `syscall.Exec` is sub-millisecond.
  - Any bytes sitting in the pty master buffer that were not yet read by the old process are preserved across exec because the file descriptor is inherited.

### 2. Ownership
- Currently:
  - `cleanExecArgs` (`internal/server/upgrade.go:46-67`) strips `--owner-fd` so the new process starts without an owner.
  - The owner client reconnects over the Unix socket (`cmd/wideboi/main.go:877`).
  - Because `--owner-fd` is missing, `s.owner` is `nil` in the new process. If the owner client later closes, `owner-left` does not fire, and the session lingers.
- Solution:
  - Save `OwnerPID` in `UpgradeState`. If the session had an owner before upgrade (`s.owner != nil`), record `s.peerPID(s.owner)`.
  - When the reconnecting owner client handshakes over the Unix socket (`admitSocketConn`), if `peer.PID == state.OwnerPID`, restore ownership: `s.owner = tp`.
  - If the session was detached before upgrade (`s.owner == nil`), `state.OwnerPID` is 0 and it remains ownerless.

### 3. Web server
- Currently:
  - In `cmd/wideboi/main.go:484`, the web server is only started on launch if `cfg.Websocket != ""` (configured via CLI flag or config file).
  - If the web server was started or toggled dynamically at runtime (`MsgWebServerControlRequest`), or if it was running on an ephemeral port or had TLS settings changed, it is dead after upgrade.
- Solution:
  - In `UpgradeState`, record web server state: `WebRunning bool`, `WebAddr string`, `WebToken string`, `WebTLSEnabled bool`.
  - In `main.go`, after `server.RestoreState(srv)`, if `WebRunning` was true, start the web server with those parameters (if not already started by config).

### 4. Terminal emulator modes
- Currently:
  - `internal/server/term/grid.go:842-857`: `ExportSnapshot` saves cells, cursor position, cursor visibility, mouse modes, title, cwd, user vars, scrollback.
  - Missing:
    - Alt-screen mode (`g.em.IsAltScreen()`). If a TUI like `vim`, `htop`, or `less` is running, restoring into normal screen buffer puts alt-screen cells into history/normal screen, and the emulator buffer mode is wrong.
    - Bracketed paste (`ansi.ModeBracketedPaste` = 2004).
    - Cursor keys application mode (`ansi.ModeCursorKeys` = 1).
    - Keypad application mode (`ansi.ModeNumericKeypad` = 66).
- Solution:
  - Track active modes via `vt.Callbacks.EnableMode` / `DisableMode` (or query `g.em.IsAltScreen()`).
  - In `GridSnapshot`, add `IsAltScreen bool`, `BracketedPaste bool`, `CursorKeys bool`, `KeypadApp bool`.
  - In `RestoreSnapshot`:
    - If `snap.IsAltScreen`, enter alt-screen mode (`\033[?1049h`) *before* populating screen cells.
    - Restore bracketed paste (`\033[?2004h`) and cursor keys (`\033[?1h`) if active.

### 5. Sizing & Startup panes
- Currently:
  - `s.startupLaunched` is reset to `false` on restore, causing startup panes defined in config to spawn again when the client reconnects (`server.go:756`).
  - `s.sizeOwner`: not recorded.
  - Race in `upgrade.go:126-127`: `p.cols` and `p.rows` read without lock instead of `p.Size()`.
- Solution:
  - Save `StartupLaunched bool` in `UpgradeState`. Restore `s.startupLaunched = state.StartupLaunched`.
  - Save `SizeOwnerPID int` in `UpgradeState`. Restore `s.sizeOwner` when the matching client connects.
  - Use `cols, rows := p.Size()` in `upgrade.go`.

### 6. Client reconnection dial timeout
- Currently:
  - `cmd/wideboi/main.go:877`: `reconnectConn, err := dialWithin(cfg.Socket, 2*time.Second)`.
  - If state restore takes >2s (or under load), the client aborts and exits.
- Solution:
  - Increase reconnect ceiling to e.g. 10 seconds.
