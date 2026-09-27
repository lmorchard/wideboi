# Research: Command palette / prompt `detach` and `set-width`

## 1. Command Palette & `:` Prompt Invocation Flow

- **Trigger**:
  - TUI: `internal/keys/keys.go:202-205` maps `:` to `ActionPrompt` and `Space` to `ActionPalette`. Handled in `cmd/wideboi/router.go:220-225`.
  - Web: `web/src/key-router.ts:146-151` maps `:` to `{ type: 'prompt' }` and `Space` to `{ type: 'palette' }`.
- **Ephemeral Pane Spawn**:
  - TUI (`cmd/wideboi/main.go:1025-1036`): formats command `wideboi prompt --caller-pane=<id> --socket=<path>` (or `palette`), sends `cli.SendSplit(ctx, cmd, "", cli.FocusedPaneID(), false)`.
  - Web (`web/src/wideboi-app.ts:1706-1733`): sends `splitRequest` with `keep: false`.
  - Server (`internal/server/server.go:650-669`): spawns ephemeral pane running `wideboi prompt` or `wideboi palette`.
- **Subcommand Execution**:
  - Runs in pane via `cmd/wideboi/prompt.go` or `cmd/wideboi/palette.go`.
  - Builds `commands.Invocation` with `CallerPaneID`, `Cfg`, `Socket`, `Stdout`, `Stderr`.
  - On Enter:
    - `prompt.go:154-166`: calls `commands.DefaultRegistry.Execute(context.Background(), inv, line)`.
    - `palette.go:185-191`: calls `commands.DefaultRegistry.Execute(context.Background(), inv, matches[selected].Name)`.
  - `commands.DefaultRegistry.Execute` (`internal/commands/registry.go:96-112`) runs `cmd.Run(ctx, inv)`.
  - Subcommand exits, server reaps ephemeral pane, restores focus to `CallerPaneID`.

## 2. Command Execution & Socket Transport in `internal/commands/`

- `internal/commands/commands.go`:
  - `Invocation` carries `Cfg`, `Socket`, `CallerPaneID`, `Args`, `Stdout`, `Stderr`.
  - No client identity, transport handles, or size owner state.
  - `SendClientMsg(ctx, inv, msg)` dials a *new* Unix socket connection (`conn`), sends `msg`, and closes the connection (`defer conn.Close()`).
  - `RPCQuery[Resp](ctx, inv, msg)` dials a new Unix socket connection, sends `msg`, waits for response frame, and closes connection.
  - `SendVerb(ctx, inv, verb)` sends `protocol.MsgVerb{Verb: verb, PaneID: inv.CallerPaneID}` over a new connection.

## 3. How `detach` and `set-width` Fail

- **`detach`**:
  - `internal/commands/registry.go:321-328`: calls `SendClientMsg(ctx, inv, protocol.MsgDetach{})`.
  - Server (`internal/server/server.go:483-490`): calls `s.dropClient(ctx, tp)` where `tp` is the transport delivering the message.
  - Since `tp` is the ephemeral throwaway connection from `wideboi prompt/palette`, server removes `tp` from `s.transports`. The actual user client connection is untouched.
- **`set-width`**:
  - `internal/commands/registry.go:269-288`: sends `protocol.MsgSetPaneWidth{PaneID: inv.CallerPaneID, Width: width}` via `SendClientMsg`.
  - Server (`internal/server/server.go:806-813`):
    ```go
    if tp == s.sizeOwner && m.Width >= layout.MinColumnWidth && m.Width <= layout.MaxColumnWidth {
        s.strip.SetColumnWidth(m.PaneID, m.Width)
        ...
    }
    ```
  - Since `tp` is the throwaway connection and never `s.sizeOwner`, the message is ignored.

## 4. Other Registered Commands in `internal/commands/registry.go`

- `new-column` (`new`, `n`): `MsgSplitRequest` via `RPCQuery`. Does not depend on connection identity or `sizeOwner`.
- `split`: `MsgSplitRequest` via `RPCQuery`. Independent of connection identity.
- `run` (`sh`, `!`, `exec`): `MsgSplitRequest` via `RPCQuery`. Independent of connection identity.
- `kill-pane` (`kill`, `k`, `close`, `x`): `MsgClosePaneRequest` via `RPCQuery`. Independent of connection identity.
- `move-left` (`ml`), `move-right` (`mr`): `VerbMoveLeft`, `VerbMoveRight` via `SendVerb`. Server handles on `s.strip` regardless of `tp == s.sizeOwner`.
- `toggle-status`: `VerbToggleStatus` via `SendVerb`. Handled regardless of connection.
- `quit` (`q`): `MsgShutdown` via `SendClientMsg`. Shuts down server completely.
- `help` (`?`): Local output to `inv.Stdout`.

Only `detach` and `set-width` fail due to connection / size ownership requirements!

## 5. Client-Side Detach and Pane Width Handling

- **Detach**:
  - `cmd/wideboi/main.go:967-992`: `hangUp(ctx, hConn, protocol.MsgDetach{}, detachCeiling)`.
  - `hConn` is the attached client's socket connection (`*transport.ClientSocketConn`).
  - Terminal exits raw mode, prints detach notice, client process exits.
- **Width**:
  - `internal/client/client.go:1488-1525`: `(c *Client).SendVerb(ctx, act.Verb)`.
  - Updates client's `c.displayWidths[id]`, local layout placements, and sends `MsgSetPaneWidth` over `c.transport.SendClient(ctx, msg)`.
  - Since client is typically `sizeOwner` on server, server updates layout and broadcasts.
  - Web client (`web/src/wideboi-app.ts:281-297`) also updates local `displayWidths` and sends `setPaneWidth` over WebSocket.
