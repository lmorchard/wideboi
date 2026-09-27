# Palette/Prompt `detach` and `set-width` Spec

**Goal:** Ensure `detach` and `set-width` invoked from the command palette or `:` prompt perform their intended actions instead of silently doing nothing.

**Source:** https://github.com/lmorchard/wideboi/issues/258

## Current state

- Ephemeral commands in `internal/commands/` are run from `wideboi prompt` (`cmd/wideboi/prompt.go:166`) and `wideboi palette` (`cmd/wideboi/palette.go:189`) via `commands.DefaultRegistry.Execute(...)`.
- `detach` sends `protocol.MsgDetach{}` over an ephemeral socket connection (`internal/commands/registry.go:326`, `internal/commands/commands.go:70-78`). The server drops that ephemeral connection (`internal/server/server.go:483-490`), while the attached user client (`cmd/wideboi/main.go:967-992`) remains untouched. Furthermore, if a server unilaterally closes a connection, the client attempts to auto-reconnect (`cmd/wideboi/main.go:880-928`) rather than detaching.
- `set-width` sends `protocol.MsgSetPaneWidth{PaneID: inv.CallerPaneID, Width: width}` via `internal/commands/registry.go:283-286`. The server ignores `MsgSetPaneWidth` unless `tp == s.sizeOwner` (`internal/server/server.go:806-813`). Because the ephemeral command connection is never `s.sizeOwner`, the message is silently discarded.
- In `cmd/wideboi/palette.go:189`, Enter only passes `matches[selected].Name` rather than preserving trailing arguments from the search query.

## Desired end state

1. **`set-width` execution**:
   - `internal/server/server.go:807` accepts `MsgSetPaneWidth` when `tp == s.sizeOwner || !s.attachedTransports[tp]`. Viewer clients attached to the session remain restricted from changing PTY width, but unattached command connections (like CLI, scripts, or ephemeral prompt/palette) can change the pane's width.
   - `internal/commands/registry.go:set-width` validates that `inv.CallerPaneID > 0`, returning an error ("no focused pane") if not set.
   - If width is invalid or missing, `set-width` returns an informative error.

2. **`detach` execution**:
   - `cmd/wideboi/main.go` sets up a temporary detach trigger file (`--detach-file=<path>`) when spawning `wideboi palette` or `wideboi prompt`.
   - `cmd/wideboi/palette.go` and `cmd/wideboi/prompt.go` accept `--detach-file` flag and pass it in `commands.Invocation.DetachFile`.
   - `internal/commands/registry.go:detach` checks `inv.DetachFile`. If provided, it writes to the file to signal the client. If not provided (standalone CLI), it falls back to `SendClientMsg(ctx, inv, protocol.MsgDetach{})`.
   - In `cmd/wideboi/main.go`, on pane exit / event loop tick, if the detach file is present, `main.go` consumes the trigger and initiates a clean client detach (`routeDetach`), unbinding raw mode, printing the detach notice, and exiting 0 while leaving the server alive.

3. **Palette argument preservation**:
   - In `cmd/wideboi/palette.go`, when Enter is pressed, if the user typed arguments after the command name/alias (e.g. `set-width 60`), the argument string is forwarded to `commands.DefaultRegistry.Execute`.

## Design decisions

- **Decision:** Server allows `MsgSetPaneWidth` from unattached transports (`!s.attachedTransports[tp]`).
  - **Why:** Commands run over fresh connections rather than attached client transports. Distinguishing between attached viewer clients (restricted) and unattached commands allows command execution without breaking the multi-client sizing invariant from #184 / #234.
  - **Rejected:** Removing size ownership checks completely, which would allow viewer clients to corrupt the host PTY size.

- **Decision:** Detach signal via client-provided `--detach-file`.
  - **Why:** `main.go` controls the client process lifecycle. An attached client auto-reconnects on unexpected socket EOF; clean detach requires client-side coordination (`hungUp.Store(true)` and `guard.Stop()`). Passing a trigger file from `main.go` to the ephemeral pane allows `detach` to trigger the client's own `routeDetach` flow without modifying the wire protobuf schema (which would require `protoc` and bump `protocol.Version`).
  - **Rejected:** Changing protobuf `ServerMessage` to add a server-initiated detach message, which requires `protoc` (not installed in current environment) and protocol version bump across all platforms.
  - **Rejected:** Sending a OS signal (SIGTERM/SIGINT) to client PID, because `main.go`'s signal guard interprets SIGTERM/SIGINT as fatal session termination.

- **Decision:** Preserve arguments in palette query on Enter.
  - **Why:** Allows commands requiring arguments (like `set-width 60`) to be typed directly into the palette query input and executed.

## Patterns to follow

- Ephemeral pane invocation in `cmd/wideboi/main.go:1033-1044`.
- Client detach teardown in `cmd/wideboi/main.go:967-992`.
- Server size owner checks in `internal/server/server.go:806-813` and `internal/server/width_owner_test.go:11-41`.
- Command execution and registry in `internal/commands/registry.go:269-288, 321-328`.

## What we're NOT doing

- Not modifying protobuf schema or bumping `protocol.Version`.
- Not removing size owner restrictions for attached viewer clients.
- Not replacing ephemeral panes with in-client modals.

## Open questions

*(None. All design choices resolved.)*
