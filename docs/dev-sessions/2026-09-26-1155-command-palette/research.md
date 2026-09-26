# Codebase Research: Pane-as-UI Command Palette

*Research conducted via documentarian subagent across `issue-251-command-palette` worktree.*

## 1. Pane Spawning, `Keep` / Exit Tracking, Teardown, and Focus Restoration

### Pane Spawning via `split` / `MsgSplitRequest`
- **CLI invocation**: `cmd/wideboi/control.go:170-238` (`runSplit`). Flags parsed: `--cwd`, `--after`, `--keep`. Remaining arguments are shell-quoted via `shellJoin` (`cmd/wideboi/control.go:240-252`). Constructs `protocol.MsgSplitRequest{Command, Cwd, AfterPaneID, Keep}` (`cmd/wideboi/control.go:192-197`).
- **Connection**: `connectOrSpawn` connects to `cfg.Socket` or spawns a background server (`cmd/wideboi/control.go:205`). Sends request via `rpcOn[protocol.MsgSplitResponse]` (`cmd/wideboi/control.go:214`). If auto-spawned, sends `protocol.MsgDetach{}` on success (`cmd/wideboi/control.go:222`) or `protocol.MsgShutdown{}` on error (`cmd/wideboi/control.go:226`).
- **Server request handling**: `internal/server/server.go:500-518`.
  - Validates `AfterPaneID`: if `> 0` and not in `s.panes`, returns `protocol.MsgSplitResponse{Error: "pane <id> not found"}` (`internal/server/server.go:501-506`).
  - Calls `s.spawnPaneWithSpecLocked(StartupPane{Command: m.Command, Dir: m.Cwd, Keep: m.Keep}, m.AfterPaneID)` (`internal/server/server.go:507-508`).
  - Sets `s.startupComplete = true`, returns `protocol.MsgSplitResponse{PaneID: p.ID()}`, and marks `needBroadcast = true` (`internal/server/server.go:512-517`).
- **Spawning implementation**: `internal/server/server.go:967-1019` (`spawnPaneWithSpecLocked`).
  - Allocates ID (`s.nextPaneID++`), determines columns from width presets and rows (`max(s.rows-2, 20)`) (`internal/server/server.go:974-985`).
  - Determines `argv`: `[s.shell]` or `[s.shell, "-c", spec.Command]` (`internal/server/server.go:986-989`).
  - Calls `NewPane(id, argv, paneCols, paneRows, cwd)` (`internal/server/server.go:994`, `internal/server/pane.go:92-105`), calling `ptyx.Spawn` (`internal/server/ptyx/pane.go:57-102`).
  - Adds pane to server state: `s.panes[id] = p` and inserts into layout: `s.strip.AddColumn(id, paneCols, paneRows, afterPaneID)` (`internal/server/server.go:1000-1001`).

### Exit Code and `Keep` Tracking
- **Child process exit detection**: In `ptyx.Spawn`, a background goroutine runs `cmd.Wait()`, stores exit code via `exitStatus(cmd.ProcessState)` into `p.exitCode`, and closes `p.done` channel (`internal/server/ptyx/pane.go:107-111`).
- **Kept panes (`spec.Keep == true`)**:
  - `p.Start(func() { close(drained) })` runs until PTY reader EOF (`internal/server/server.go:1008-1009`).
  - Watched by `go s.watchKeptPane(id, p, drained)` (`internal/server/server.go:1010, 1027-1050`):
    - Waits for `<-p.pty.Done()`, then `<-drained` (or `keptDrainCeiling = 3s`), gets `p.pty.ExitCode()`, calls `p.markExited(code)` (`internal/server/pane.go:497-502`).
    - Kept panes **remain in `s.panes` and in `s.strip`**; their buffer is retained on screen.
    - Notifies waiters via `s.notifyWaiters(id, code, "")` (`internal/server/server.go:1048, 1058-1070`).
    - Triggers `s.broadcastLayout(context.Background())` (`internal/server/server.go:1049`).
  - Waiting on panes: `MsgWaitRequest` returns `protocol.MsgWaitResponse{ExitCode}` immediately if exited, or enqueues in `s.waiters[m.PaneID]` (`internal/server/server.go:548-566`).
- **Unkept panes (`spec.Keep == false`)**:
  - `p.Start(func() { s.onPaneExit(id) })` triggers on PTY reader EOF (`internal/server/server.go:1012-1015`).

### Teardown and Focus Restoration
- **Teardown paths**:
  - Child exit of unkept pane triggers `s.onPaneExit(id)` (`internal/server/server.go:1097-1136`).
  - Explicit close request `protocol.MsgClosePaneRequest` or verb `protocol.VerbKillPane` triggers `s.removePaneLocked(id)` (`internal/server/server.go:540-547, 694-698, 1138-1174`).
  - Both call `s.strip.KillPane(id)` (`internal/server/server.go:1108, 1150`), delete pane from `s.panes`, `s.lastCWD`, `s.lastUserVars`, and close `p.Close()`.
  - Waiters answered via `s.finishWaiters(id, p)` (`internal/server/server.go:1090-1095, 1130, 1167`).
  - If no terminal panes remain, server triggers `s.Close()` (`internal/server/server.go:1132-1135, 1173`).
- **Server strip focus adjustment**: In `internal/layout/layout.go:275-291` (`KillPane`):
  - Clears `lastFocusPaneID` if equal to dead pane ID (`internal/layout/layout.go:279-281`).
  - Removes column: `s.columns = append(s.columns[:i], s.columns[i+1:]...)` (`internal/layout/layout.go:284`).
  - If `s.focusIndex >= len(s.columns)`, clamps `s.focusIndex = max(len(s.columns)-1, 0)` (`internal/layout/layout.go:285-287`). Focus shift is not recorded for `FocusLast` (`internal/layout/layout.go:276-277`).
- **Client focus restoration on layout update**: When `MsgLayoutSnapshot` arrives, client calls `c.strip.SyncColumns(displayColumns, c.focusPaneID)` (`internal/client/client.go:292`). If the focused pane no longer exists (`newIndex < 0`), it focuses `min(max(oldIndex-1, 0), len(s.columns)-1)` (`internal/layout/layout.go:489-491`).

---

## 2. Input Keystroke Routing and Message Types

### Routing Architecture
- **Terminal client event loop**: `cmd/wideboi/main.go:882-956`. Receives `uv.KeyPressEvent`, clears selection, and routes through `rt.route(ev)` (`cmd/wideboi/router.go:101-191`).
- **Route kinds (`routeKind`)**: `cmd/wideboi/router.go:26-53` (`routeForward`, `routeIgnore`, `routeVerb`, `routeScroll`, `routePan`, `routeToggleFollowPTY`, `routeQuit`, `routeDetach`, `routeFocusColumn`, `routeToggleLayout`, `routeSearchStart`, `routeSearchEdit`, `routeSearchCommit`, `routeSearchNavigate`, `routeSearchCancel`, `routeSearchAccept`, `routeSearchLive`).

### Sub-modes and Keystroke Transitions
- **Normal vs. Control mode**:
  - Not in control mode: if key matches `r.prefix` (default `"ctrl+b"`, parsed in `cmd/wideboi/router.go:258-275`), enters control mode (`r.control = true`) and returns `routeIgnore` (`cmd/wideboi/router.go:151-155`). Otherwise returns `routeForward` (`cmd/wideboi/router.go:156`).
  - In control mode:
    - Double prefix exits control mode and forwards literal prefix key: `routeForward` (`cmd/wideboi/router.go:159-165`).
    - Matches bindings (`internal/keys/keys.go:88-124`):
      - Verbs: `keys.ActionVerb` -> `routeVerb{Verb}` (`cmd/wideboi/router.go:199-200`).
      - Scrolling: `keys.ActionScroll` -> `routeScroll{Scroll: b.Scroll}` (`cmd/wideboi/router.go:201-202`).
      - Search trigger (`/`): `keys.ActionSearch` -> sets `r.search = 1`, `r.control = false`, returns `routeSearchStart` (`cmd/wideboi/router.go:214-217`).
      - Help trigger (`?`): `keys.ActionHelp` -> sets `r.help = true`, `r.control = true`, returns `routeIgnore` (`cmd/wideboi/router.go:226-232`).
      - Repeating: holding Ctrl on repeatable verbs sets `r.control = true` (sticky); unmodified keys exit control mode (`cmd/wideboi/router.go:175-180, 196`).
      - Unmatched keys exit control mode and return `routeIgnore` (`cmd/wideboi/router.go:189-190`).
- **Help mode**: Any key while `r.help == true` dismisses help (`r.help = false`) and returns `routeIgnore` (`cmd/wideboi/router.go:146-149`).
- **Search mode** (`cmd/wideboi/router.go:102-141`):
  - `Esc`: sets `r.search = 0`, returns `routeSearchCancel` (`cmd/wideboi/router.go:103-106`).
  - `Enter`: commits search or accepts match (`cmd/wideboi/router.go:111-118`).
  - `n` / `N`: navigates matches (`cmd/wideboi/router.go:134-139`).
- **Web client router (`KeyRouter`)**: `web/src/key-router.ts:86-186`. Dispatches in `web/src/wideboi-app.ts:1543-1612`.

### Outbound Wire Messages
- Regular keystrokes forwarded: `protocol.MsgInput{PaneID, Key, Data}` (`internal/client/client.go:1563-1585`).
- Verbs:
  - Focus verbs (`VerbFocusLeft`, `VerbFocusRight`, `VerbFocusLast`, `VerbSmartJump`, `FocusColumn`) are handled **client-locally** on `c.strip` (`internal/client/client.go:1461-1479, 1547-1560`).
  - Width verbs (`VerbCycleWidth`, `VerbGrowWidth`, `VerbShrinkWidth`): client adjusts width on `c.strip`, then sends `protocol.MsgSetPaneWidth{PaneID, Width}` (`internal/client/client.go:1480-1502`).
  - Remote verbs (`VerbNewColumn`, `VerbKillPane`, `VerbMoveLeft`, `VerbMoveRight`, `VerbToggleStatus`): sent as `protocol.MsgVerb{Verb, PaneID}` (`internal/client/client.go:1510-1512`).
- Scroll: `protocol.MsgScroll{PaneID, Delta}` (`internal/client/client.go:1589-1597`).
- Detach / Quit: `protocol.MsgDetach{}` / `protocol.MsgShutdown{}` (`cmd/wideboi/main.go:894, 913`).

---

## 3. CLI Server IPC, Child Process Environment, and Handshake

### CLI Socket Communication via `WIDEBOI_SOCK`
- **Socket path resolution precedence**: Defaults -> TOML -> Environment variables -> Command line flags (`internal/config/config.go:202-204`).
  - Default: `$TMPDIR/wideboi-<uid>/default.sock` via `DefaultSocketPath()` (`internal/config/config.go:63-67`).
  - Environment: `applySessionLayer(&cfg, "environment", getenv("WIDEBOI_SESSION"), getenv("WIDEBOI_SOCK"))` (`internal/config/config.go:368`).
  - Subcommands apply CLI flags `-L` / `-session` or `-s` / `-socket` via `applySessionFlags` (`cmd/wideboi/control.go:110-118`).
- **Socket connection & RPC**:
  - `net.Dial("unix", cfg.Socket)` connects to Unix domain socket (`cmd/wideboi/control.go:27`).
  - Wrapped in `transport.NewClientSocketConn(conn, 256)` (`cmd/wideboi/control.go:37, 211`). Wire framing uses 4-byte big-endian length prefix encoding Protobuf messages (`internal/transport/frame.go:13-33`, `internal/protocol/codec.go:18-72`).
  - Synchronous query waits for typed response via `rpcOn[Resp]` (`cmd/wideboi/control.go:45-71`).

### Environment Variables Passed to Pane Child Processes
- Defined in `internal/server/ptyx/pane.go:64`:
  ```go
  cmd.Env = append(os.Environ(), "TERM=xterm-256color")
  ```
- The child process inherits the complete host server environment (`os.Environ()`), with `TERM=xterm-256color` appended.
- Note: When a server starts, `server.go` or `cmd/wideboi/main.go` sets `WIDEBOI_SOCK` in the server's environment or passes it along, so child panes inherit `WIDEBOI_SOCK`.

### Authentication and Handshake
- **Unix domain socket authentication**: Filesystem permissions only (`0700` socket directory `internal/config/config.go:172-179`).
- **Protocol version handshake**: Every Unix socket connection executes `transport.Handshake(conn)` (`cmd/wideboi/handshake.go:16-22`, `internal/transport/handshake.go:65-83`).
  - Magic bytes: `"WIDEBOI\x00"` (8 bytes) (`internal/transport/handshake.go:24`).
  - Protocol version: `protocol.Version` (`internal/protocol/version.go:27`).
  - Sender PID: `os.Getpid()` (`internal/transport/handshake.go:85-91`).

---

## 4. Pane Layout Ordering and Focus Dispatching

### Server Strip Ordering (`AfterPaneID`)
- Panes are stored in an ordered slice: `Strip.columns []Column` (`internal/layout/layout.go:29`).
- Column insertion in `Strip.AddColumn(paneID, width, height, afterPaneID)` (`internal/layout/layout.go:126-146`):
  - If `afterPaneID` matches an existing column, inserts immediately after it (`insertIdx = i + 1`).
  - If `afterPaneID == 0` or is not found, appends to end (`insertIdx = len(s.columns)`).
  - Moves server strip focus to newly added column: `s.focusIndex = insertIdx` (`internal/layout/layout.go:144`).

### Focus Dispatching to Clients
- **Client-local focus**: Focus is tracked independently on each client (`c.focusPaneID` in `internal/client/client.go:88`, `focusedPaneId` in `web/src/wideboi-app.ts:912`).
- **Server-directed focus events**:
  - `protocol.MsgFocusPane{PaneID}`: Sent to a specific client when the server demands a focus change (e.g. `internal/server/server.go:879-881`).
  - Client handles `MsgFocusPane` by calling `c.strip.FocusPaneID(m.PaneID); c.focusPaneID = m.PaneID; c.revealCursorLocked(m.PaneID); c.updatePlacementsLocked()` (`internal/client/client.go:397-404`).
