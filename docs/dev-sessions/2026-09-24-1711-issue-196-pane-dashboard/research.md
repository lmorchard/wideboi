# Research: Server-Managed Status Dashboard Pane (Issue #196)

## 1. Pane Architecture & Lifecycle

- `internal/server/pane.go:26`: `Pane` struct currently assumes a PTY process (`pty *ptyx.Pane`) and VT emulator (`grid term.Grid`).
- `internal/server/pane.go:83-97`: `NewPane` spawns an argv process via `ptyx.Spawn`, creates `term.NewVT(cols, rows)`, and sets up input queues (`p.input`).
- `internal/server/pane.go:103-171`: `Start(onExit)` runs three goroutines:
  - `pty-reader`: reads from `pty.Master`, writes to `grid.Write(...)`. Calls `onExit()` when `pty.Master.Read` errors (process exits).
  - `pty-writer`: reads emulator responses from `grid.Read(...)` and writes to `pty.Master`.
  - `key-writer`: pulls `uv.KeyEvent` and `uv.MouseEvent` from `p.input` and sends to `grid.SendKey` / `grid.SendMouse`.
- `internal/server/server.go:628-666`: `spawnPaneWithSpecLocked` creates a pane, assigns `nextPaneID`, calculates initial width/height from presets and layout, adds it to `s.strip.AddColumn(id, cols, rows, afterPaneID)`, and registers `onPaneExit(id)`.
- `internal/server/server.go:668-685`: `onPaneExit(id)` removes pane from `s.panes`, `s.strip.KillPane(id)`, cleans CWD/user vars, resizes surviving panes, and broadcasts layout.
- `internal/server/server.go:687-697`: `removePaneLocked(id)` handles explicit `VerbKillPane` (`<prefix> x`), calling `s.strip.KillPane(id)`, deleting from `s.panes`, and closing asynchronously.

## 2. Input and Event Routing

- `cmd/wideboi/router.go:113-178`: The router maps prefix commands to actions and verbs. Unprefixed keystrokes return `routeForward`, forwarded to the focused pane via `cli.SendKey(ctx, uv.KeyEvent)`.
- `internal/client/client.go:1060-1087`: `SendVerb` intercepts client-local navigation verbs (`VerbFocusLeft`, `VerbFocusRight`, `VerbFocusLast`, `VerbSmartJump`) and handles them locally in `c.strip`. Only mutations (`VerbNewColumn`, `VerbKillPane`, `VerbGrowWidth`, etc.) are sent to server over wire.
- `internal/client/client.go:1187-1191`: `FocusedPaneID()` tracks focus locally. Focus is client-local presentation (`TestClientsKeepIndependentFocusAcrossSnapshots`).
- `internal/server/server.go:511-537`: `handleClientMsg` receives `MsgInput` and `MsgMouse`. Keystrokes decode and queue to `p.SendKey(...)`.
- `internal/client/mouse.go:139-150`: If `c.mouseTracking[paneID]` is active (set when VT emulator enables DEC 1000/1006 mouse tracking), clicks on the focused pane are sent to server as `protocol.MsgMouse`.

## 3. Rendering and Screen Updates

- `internal/server/term/grid.go`: `term.Grid` interface defines terminal emulator behavior (`Write`, `Resize`, `Snapshot`, `Generation`, `SendKey`, `SendMouse`). `term.NewVT(cols, rows)` provides a complete VT-compatible screen buffer.
- `internal/server/server.go:1108-1200`: `broadcastPaneUpdates` monitors `grid.Generation()` for each pane. When generation changes, it computes delta patches (`MsgPaneUpdate`) or full resends (`MsgPaneData`) and broadcasts them to clients.
- Any text written to `grid.Write([]byte(...))` using ANSI formatting (e.g. SGR colors, cursor position escape codes `\x1b[H`, clear screen `\x1b[2J`, mouse mode `\x1b[?1000h\x1b[?1006h`) is emulated and broadcast seamlessly to both terminal and web clients.

## 4. Protocol & Wire Rules

- `internal/protocol/messages.go`:
  - `VerbType`: enum of actions. Adding a verb (e.g. `VerbToggleStatus`) requires adding it to protobuf schema (`wideboi.proto`), Go messages, codec mappings, and bumping `protocol.Version` (`internal/protocol/version.go`).
  - Wire messages must avoid interfaces and reflect concrete protobuf types (`docs/LESSONS.md`).
- To allow the server to navigate client focus upon `Enter` or mouse click in the dashboard pane:
  - We can introduce a server->client message `MsgFocusPane{PaneID: int}` or `MsgSelectPane`. When received, the client focuses that pane (`c.strip.FocusPaneID(m.PaneID)`).
