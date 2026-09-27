# Research: in-place upgrade state gaps (#299)

These are a documentarian subagent's findings from 2026-09-26, plus first-hand checks, which are marked ✔. Paths are relative to the worktree. `VT/` means the vt source actually built. That is the **fork** `github.com/lmorchard/x/vt v0.0.0-20260924233645-cacc71cdcc0f`, which replaces `charmbracelet/x/vt` (go.mod:40 ✔).

## 1. GridSnapshot / RestoreSnapshot (internal/server/term/grid.go)

- The `Snapshotter` interface is at grid.go:140-143. `GridSnapshot` is at grid.go:145-162 and holds Cols, Rows, CursorX/Y, CursorVisible, MouseModes, Status, Title, CWD, UserVars, ScrollOffset, Scrollback, Screen, IsAltScreen, BracketedPaste and CursorKeys.
- **Not captured:** scroll region, keypad mode, SGR pen, cursor style/blink, saved cursor (DECSC), origin/autowrap, focus events, mouse encoding (1006), charsets, tab stops, colours, and the primary-screen contents while alt screen is active. `CellAt` reads the active screen only.
- ExportSnapshot (822-885) runs under `writeResizeMu`:
  - Cells come from `em.CellAt` / `em.ScrollbackCellAt`, with style from `protocol.EncodeStyle`.
  - The cursor comes from `em.CursorPosition()` and `IsAltScreen` from `em.IsAltScreen()`.
  - The other modes are **wideboi's own atomics**, fed by vt callbacks:
    - `cursorVisible` from `CursorVisibility` (grid.go:275);
    - `mouseModes` from `EnableMode`/`DisableMode` → `trackMouseMode` (285-292, 579-605);
    - `bracketedPaste`/`cursorKeys` from `trackTerminalMode` (607-618).
- RestoreSnapshot (887-987) re-applies state in a fixed order:
  1. Feeds `?1049h` if the snapshot was in alt screen.
  2. Sets scrollback directly via `Scrollback().Push`.
  3. Sets the screen directly via `SetCell`.
  4. Feeds CUP for the cursor.
  5. Feeds `?9/1000/1002/1003h` + `?1006h` for mouse modes, `?2004h` for bracketed paste and `?1h` for cursor keys.
  6. Stores the atomics for cursorVisible (nothing is fed to vt), title, cwd, uservars and scrollOffset.

  Feeding goes through `g.em.Write`, not `vtGrid.Write`. `snap.Cols`/`snap.Rows` are unused because the grid is built at the pane size.
- Tests are in snapshot_test.go. `TestGridSnapshotRoundTrip` (8-86) covers title, cwd, mouse, cursor, cell content/width and scrollback length; it does not compare style. `TestGridSnapshotModesRoundTrip` (88-130) covers alt screen, bracketed paste and cursor keys.

## 2. What vt exposes

- The Emulator's active screen `scr` and `scrs [2]Screen` are unexported (VT/emulator.go:26-27), and nothing exported gets you from Emulator to a Screen.
- **Scroll region:**
  - It lives in `Screen.scroll` (VT/screen.go:16-17).
  - `(*Screen).ScrollRegion()` is exported (screen.go:217) but unreachable.
  - The CSI r handler is VT/handlers.go:861-885; it homes the cursor after setting margins.
  - The only way to set it from outside is to feed `CSI t;b r`.
- **Keypad application mode (?66):**
  - It is stored in the unexported `e.modes` (VT/mode.go:15).
  - `ESC =`/`ESC >` set it through `setMode` (handlers.go:347-357), and `setMode` **fires `EnableMode`/`DisableMode`** (csi_mode.go:98-107).
  - So it is observable exactly the way bracketed paste is. `trackTerminalMode` just doesn't record it (grid.go:612-617).
  - DECRQM replies go to the reply pipe, not to a getter.
- **SGR pen:**
  - It lives in the unexported `Screen.cur.Pen` (screen.go:14-15) and is set via `uv.ReadStyle` (csi_sgr.go:10-12).
  - `(*Screen).Cursor()` is exported but unreachable.
  - There is no pen callback. A `CursorStyle` callback exists but is unregistered by wideboi.
- **Hooks:** `RegisterCsiHandler`/`RegisterEscHandler`/`RegisterOscHandler` are exported (handlers.go:47-90). Wideboi uses OSC 133 (grid.go:294).

## 3. Upgrade flow

1. `wideboi upgrade-server <bin>` sends an RPC `MsgUpgradeRequest` over its own connection with a 5s timeout (main.go:271-275, 603-609). The attached TUI is not the requester.
2. `handleClientMsg` routes the upgrade request before the `upgrading` check (handlers.go:95-97).
3. `PrepareUpgrade` sets `s.upgrading = true` (upgrade.go:182) and returns `execFn`.
4. `applyEffects` sends the response, drains for 500ms, then calls execFn (handlers.go:570-586).
5. execFn snapshots under s.mu + resizeMu, clears CLOEXEC, writes a temp JSON file, sets `WIDEBOI_RESTORE_STATE` and calls `syscall.Exec`. On failure it rolls back and clears `upgrading` (upgrade.go:186-236).
6. **While `upgrading` is set, every non-upgrade message is dropped silently** (handlers.go:99-103): input, resize, mouse, scroll, verb, attach, RPC and so on. `stoppingLocked()` is true (lifecycle.go:170-173), so new socket and web connections are closed and spawns are refused.
7. In the new process:
   - `main.go:346` creates the socket listener ✔.
   - `RestoreState` runs at main.go:392 ✔. It adopts ptys, restores snapshots, and sets `expectedOwnerPID`/`expectedSizeOwnerPID`.
   - `ListenSocket` runs at main.go:498 ✔, then the web server is restarted if it was running before.
8. The attached TUI:
   - Sees the stream end with EOF and dials again with `dialWithin(cfg.Socket, 2*time.Second)`, which retries with 20ms→100ms backoff (main.go:683-696, 983).
   - Then handshakes (hello = version + PID), swaps the transport and calls `cli.Attach` ✔ (main.go:1014-1026).
   - The reconnect runs inline in the main select, so keys and window-size events aren't consumed during it.
   - There is no reconnect UI and no client-side buffering beyond the transport's 256-message send channel.

## 4. Client sizing

- **`clientSizes` does not exist.** Per-client size is `clientState.size` (clients.go:16), inside `s.clients` (server.go:65-66). `sizeOwner` is at server.go:75-79.
- **The client resends its size on every reconnect** ✔. `Client.Attach` sends `MsgAttach{Cols, Rows}` (client.go:167-170), and it is called after the transport swap (main.go:1024). `handleAttachLocked` stores it into `cs.size` (handlers.go:283-293).
- Size ownership after an upgrade is restored by PID match in `setPeerPIDLocked` (ancestry.go:88-98), called from `admitSocketConn` after the handshake (server.go:396). Session Cols/Rows and per-pane Cols/Rows are carried in `UpgradeState`.

## 5. Analogues

- `ExportSnapshot`/`RestoreSnapshot` are called only from upgrade.go (127, 291, 310).
- Wire frames, resync, history and capture all go through the live grid, not snapshots (pane.go:432-520, handlers.go:222-279).
- `vtGrid.Resize` reflow copies visible cells only and skips alt screen (grid.go:650-700).
- Per-client state is never serialized; it is rebuilt by `newClientState` (clients.go:34-47).
