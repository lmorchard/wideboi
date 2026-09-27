# In-Place Upgrade State Preservation and Failure Recovery (Issues 259 & 273) Spec

**Goal:** Ensure in-place server upgrades preserve complete session state (ownership, web server, terminal modes, sizing, and startup status) without data loss, and ensure failed upgrade attempts cleanly recover without leaking client connections.

**Source:** GitHub Issues #259 and #273

## Current state
- In `internal/server/server.go:491-508`, when `MsgUpgradeRequest` triggers `execFn()` and `execFn()` fails, the transport has been closed but `s.dropClient(ctx, tp)` is never called. The dead transport stays in `s.transports` and receives broadcasts.
- In `internal/server/upgrade.go:116-151`, `PrepareUpgrade` snapshots grids and writes the state file early, while pane pumps continue reading child PTY output during the subsequent 500ms client drain. Output read during this drain is lost on `syscall.Exec`.
- Session ownership is lost across upgrade because `--owner-fd` is stripped by `cleanExecArgs` and the reconnecting owner client is treated as an ordinary guest. If the owner exits, the server does not exit (`server.ReasonOwnerLeft` does not fire).
- Web server state is lost if the web server was started or configured dynamically at runtime.
- Terminal emulator modes (`IsAltScreen`, bracketed paste, cursor key mode) are not captured in `term.GridSnapshot`. Restoring a pane running a full-screen TUI (e.g. vim) renders alt-screen contents into the normal screen buffer.
- `s.startupLaunched` is lost, causing startup panes defined in config to spawn a second time upon client reconnection.
- `upgrade.go:126-127` reads `p.cols` and `p.rows` without acquiring `p.resizeMu`.
- `cmd/wideboi/main.go:877` uses a tight 2s dial timeout for reconnection, which fails if state restoration is slow.
- There are no unit tests for `PrepareUpgrade`, `RestoreState`, `cleanExecArgs`, or upgrade failure recovery.

## Desired end state
1. **Clean failure recovery (#259):**
   - If `execFn()` fails, `s.dropClient(ctx, tp)` is called to remove the closed transport from server maps and broadcast sets.
   - If `PrepareUpgrade` fails, the error response is sent and the client connection remains attached and serviced.
2. **Timing & minimal output loss (#273):**
   - Grid snapshotting and state serialization happen right before `syscall.Exec` inside `execFn`, eliminating the 500ms drain window where output was previously lost.
3. **Ownership preservation (#273):**
   - `UpgradeState` carries `OwnerPID int`. If the server had an owner before upgrade, its PID is recorded.
   - When the client with matching PID connects over the Unix domain socket post-upgrade, the server restores `s.owner = tp`.
   - If the session was unowned (detached), `OwnerPID` is 0 and it remains unowned.
4. **Web server preservation (#273):**
   - `UpgradeState` carries web server state (`WebRunning bool`, `WebAddr string`, `WebToken string`, `WebTLSEnabled bool`).
   - On restore, if the web server was active, `srv.StartWebServer` is invoked to resume listening on the same address and token.
5. **Terminal mode preservation (#273):**
   - `term.GridSnapshot` carries `IsAltScreen bool`, `BracketedPaste bool`, `CursorKeys bool`.
   - `RestoreSnapshot` checks `IsAltScreen`: if true, it enters alt-screen mode (`\033[?1049h`) before populating the visible screen cells.
   - Restores bracketed paste (`\033[?2004h`) and cursor key mode (`\033[?1h`) if they were active.
6. **Sizing and startup preservation (#273):**
   - `UpgradeState` carries `StartupLaunched bool` and `SizeOwnerPID int`.
   - On restore, `s.startupLaunched = state.StartupLaunched` prevents duplicate pane spawning.
   - `s.sizeOwner` is restored when the matching client connects.
   - `p.cols`/`p.rows` access in `upgrade.go` uses `p.Size()`.
7. **Client reconnection resilience (#273):**
   - Reconnect dial timeout in `cmd/wideboi/main.go` is increased to 10s with retry loop.
8. **Comprehensive test coverage (#259 & #273):**
   - Unit tests verify `cleanExecArgs` with various argument shapes.
   - Unit tests verify `PrepareUpgrade` -> state file -> `RestoreState` round trip preserving panes, modes, web server state, and ownership.
   - Unit tests verify failure recovery: `PrepareUpgrade` error keeps client served; `execFn` error drops client without leaking.

## Design decisions
- **Decision:** Snapshot grids inside `execFn()` rather than `PrepareUpgrade`.
  - **Why:** During the 500ms client drain, pty pumps continue reading output. Snapshotting inside `execFn` immediately before `syscall.Exec` ensures that all output up to the moment of exec is captured in the snapshot.
  - **Rejected:** Parking or stopping the pty reader pumps during drain. Blocking pty reads in Go netpoll cannot be safely paused without closing the fd or risking deadlock.
- **Decision:** Reconnect owner by matching `peer.PID`.
  - **Why:** The owner process (`wideboi`) does not terminate during server upgrade; only the server process execs. The owner client's PID remains identical when it reconnects over the Unix socket.
  - **Rejected:** Passing the owner socketpair fd across `syscall.Exec`. Go's `net.FileConn` duplicates and sets non-blocking flags that can complicate fd re-adoption across arbitrary Go versions; socket reconnection is already implemented and reliable.
- **Decision:** Enter alt-screen mode in `RestoreSnapshot` before writing screen cells.
  - **Why:** `vt.Emulator` maintains separate primary and alternate screen cell buffers. Populating cells while in primary mode and then switching to alt-screen leaves the alt-screen blank.
- **Decision:** Save web server config and active state in `UpgradeState`.
  - **Why:** Allows any running web server (even dynamically toggled ones) to resume without needing CLI flag changes.

## Patterns to follow
- Transport cleanup and locking: `s.dropClient(ctx, tp)` in `internal/server/server.go:514-540`.
- Socket admission and peer identification: `internal/server/server.go:367-396`.
- Emulator mode callbacks: `vt.Callbacks.AltScreen`, `EnableMode`, `DisableMode` in `internal/server/term/grid.go:269-282`.
- Unit test harnesses: `internal/server/remote_control_test.go` and `internal/server/lifecycle_test.go`.

## What we're NOT doing
- Refactoring the entire transport layer or replacing Unix domain sockets with another IPC.
- Preserving ephemeral in-memory scrollback ring buffers beyond what `term.GridSnapshot` already serializes.
- Adding new CLI flags or configuration file formats for upgrades.
- Changing child process exit / SIGHUP semantics during normal operation.

## Open questions
None. All design choices resolved.
