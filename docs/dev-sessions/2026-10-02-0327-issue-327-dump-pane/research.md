# Research: Issue 327 (dump-pane CLI command)

## 1. CLI Subcommand Registration & Dispatch
- `cmd/wideboi/main.go:68-102`: `newFlagSet` defines CLI global flags (`-s`/`--socket`, `-L`/`--session`, `-c`/`--config`, etc.).
- `cmd/wideboi/main.go:104-157`: `parseCLI` identifies subcommands and separates preceding global flags from trailing subcommand arguments. Subcommands taking command lines or arguments (`split`, `send`, `capture`, `close`, `wait`, `upgrade-server`, `web`, `prompt`, `palette`, `trust`, `untrust`, `rename-pane`) are matched at `cmd/wideboi/main.go:127-132`.
- `cmd/wideboi/main.go:319-384`: Subcommands are dispatched in `main()` using a switch statement.
- `cmd/wideboi/control.go:28-30`: `rpcQuery[Resp]` wraps `commands.RPCQuery` to send a typed Protobuf request to the server Unix domain socket and await a typed response.
- `cmd/wideboi/control.go:80-96`: `addTargetFlags` and `applySessionFlags` configure `-s`/`--socket` and `-L`/`--session` flags.
- `cmd/wideboi/control.go:362-445`: `runRenamePane` demonstrates argument parsing, pane ID fallback logic (`WIDEBOI_PANE_ID`), and RPC dispatch.

## 2. Existing `capture` Subcommand vs Proposed `dump-pane`
- `cmd/wideboi/control.go:268-317`: `runCapture` implements `wideboi capture [flags] <pane-id>`:
  - Requires `<pane-id>` as first positional argument (`cmd/wideboi/control.go:291-299`).
  - Flags: `-S`/`--scrollback` (bool), `-n`/`--lines` (int).
  - Sends `protocol.MsgCaptureRequest{PaneID, Scrollback, Lines}` (`cmd/wideboi/control.go:301-305`).
  - Calls `CaptureText(scrollback, lines)` on server (`internal/server/handlers.go:247-256`).
  - Writes plain text to `stdout`.
  - Not registered in `internal/commands/registry.go` (cannot be run from prompt or palette).
- Proposed `dump-pane` in Issue #327:
  - Registered in root CLI (`cmd/wideboi/`) and internal registry (`internal/commands/registry.go`).
  - Optional `[pane-id]` (defaults to caller/focused pane if inside wideboi session, or requires ID if outside).
  - `-s, --scrollback [lines]`: Dump scrollback history (all or up to N lines) in addition to or instead of active screen.
  - `--ansi` / `--plain`: Toggle whether ANSI formatting/color escapes are preserved or stripped (default plain).
  - `-o, --output <file>`: Write output to a file instead of stdout.
  - Dedicated RPC messages: `MsgDumpPaneRequest` / `MsgDumpPaneResponse`.

## 3. Server Pane Emulator State, Screen Buffer, and Scrollback
- `internal/server/pane.go:26-89`: `Pane` struct wraps `pty *ptyx.Pane`, `grid term.Grid`, `cols, rows int`, `resizeMu sync.Mutex`, `renderMu sync.RWMutex`.
- `internal/server/pane.go:652-662`: `Pane.CaptureText` acquires `p.renderMu.RLock()` and delegates to `p.grid.CaptureText(scrollback, maxLines)`.
- `internal/server/term/grid.go:229-293`: `vtGrid` implements `term.Grid` wrapping `em *vt.SafeEmulator` (`github.com/charmbracelet/x/vt`).
- `internal/server/term/grid.go:770`: `vtGrid.CellAt(x, y)` reads screen cells from emulator.
- `internal/server/term/grid.go:782`: `vtGrid.ScrollbackLen()` returns scrollback ring length.
- `internal/server/term/grid.go:786-813`: `vtGrid.HistoryRows()` acquires `writeResizeMu.Lock()` and reads physical history rows using `g.em.ScrollbackCellAt(x, y)` and `g.em.CellAt(x, y-sbLen)`.
- `internal/server/term/grid.go:1090-1150`: `vtGrid.CaptureText(scrollback bool, maxLines int) string`:
  - Acquires `g.writeResizeMu.Lock()`.
  - Iterates rows from `startLine` to `totalLines`.
  - For each cell, checks `cell.Content` and advances by `cell.Width` (`cell.Width <= 0 -> 1`).
  - Trims trailing spaces per line and trailing empty lines from viewport.
  - Returns `strings.Join(lines, "\n") + "\n"`.

## 4. ANSI Styling & Formatting Generation
- Cells returned by `g.em.CellAt` / `g.em.ScrollbackCellAt` have type `*uv.Cell` (`github.com/charmbracelet/ultraviolet`).
- Each `uv.Cell` has `Content string`, `Width int`, and `Style uv.Style`.
- `uv.Style` provides:
  - `s.IsZero() bool`: whether default style.
  - `s.Equal(o *Style) bool`: style equality.
  - `s.String() string`: returns ANSI SGR escape sequence for the style.
  - `s.Diff(from *Style) string`: returns ANSI SGR escape sequence setting the style diff from another style.
- `ansi.ResetStyle` (`github.com/charmbracelet/x/ansi`) is `\x1b[m` to reset formatting.
- `term.Grid` interface can be extended or have a method `DumpText(scrollback bool, maxLines int, ansi bool) string` (or `CaptureText` with format options) to emit ANSI SGR escapes cell-by-cell when `ansi` is requested.

## 5. Wire Protocol & Codec
- `internal/protocol/version.go:40`: `const Version uint32 = 22`. Adding new wire messages requires bumping `protocol.Version` to 23 and updating `web/src/version.ts:2`.
- `internal/protocol/wirepb/wideboi.proto`:
  - `ClientMessage.oneof msg`: field 23 for `MsgDumpPaneRequest`.
  - `ServerMessage.oneof msg`: field 21 for `MsgDumpPaneResponse`.
  - Message definitions:
    ```protobuf
    message MsgDumpPaneRequest {
      int32 pane_id = 1;
      bool scrollback = 2;
      int32 lines = 3;
      bool full = 4; // or scrollback lines
      bool ansi = 5;
    }
    message MsgDumpPaneResponse {
      int32 pane_id = 1;
      string text = 2;
      string error = 3;
    }
    ```
- `internal/protocol/codec.go`: `MarshalClient`, `UnmarshalClient`, `MarshalServer`, `UnmarshalServer`.
- `internal/protocol/messages.go`: Go struct definitions for `MsgDumpPaneRequest` and `MsgDumpPaneResponse`.
- `internal/server/handlers.go:134-177`: `handleClientMsg` dispatches request to `handleDumpPaneRequestLocked`.
- `internal/server/handlers.go:678-783`: `applyEffects` sends `MsgDumpPaneResponse` to transport.

## 6. Target Pane ID & Session Resolution
- Inside a wideboi pane:
  - CLI: `os.Getenv("WIDEBOI_PANE_ID")` contains caller pane ID; `WIDEBOI_SOCK` contains socket path (`internal/server/server.go:396-408`).
  - Internal registry: `inv.CallerPaneID` carries caller pane ID (`internal/commands/commands.go:26`).
- Outside wideboi pane:
  - If `[pane-id]` operand is omitted, CLI returns clear error explaining pane-id is required outside wideboi pane (same as `rename-pane` in `cmd/wideboi/control.go:393, 407`).
