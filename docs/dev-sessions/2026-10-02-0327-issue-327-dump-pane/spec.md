# Dump-Pane CLI & Command Spec

**Goal:** Provide a `wideboi dump-pane` command (and alias `capture`) in both the root CLI and internal command registry to programmatically inspect pane screen and scrollback content in plain or ANSI format, with pagination (`--offset`, `--limit`) and line counting (`--count`), writing to stdout or a file, and update `docs/skills/wideboi-control/SKILL.md`.

**Source:** https://github.com/lmorchard/wideboi/issues/327

## Current state

- The CLI currently provides `wideboi capture [flags] <pane-id>` (`cmd/wideboi/control.go:268-317`), which requires `<pane-id>` as a positional argument. Flags: `-S, --scrollback` (bool) and `-n, --lines` (int). It writes plain text to stdout.
- `capture` is NOT registered in `internal/commands/registry.go`, so it cannot be invoked from the command prompt or palette.
- Server maintains emulator state in `internal/server/term/grid.go:vtGrid` wrapping `*vt.SafeEmulator`. `vtGrid.CaptureText` (`internal/server/term/grid.go:1090-1150`) extracts plain text under `writeResizeMu.Lock()`, right-trimming spaces and blank viewport lines.
- Cells in `vtGrid` are `*uv.Cell` (`github.com/charmbracelet/ultraviolet`), carrying `Content string`, `Width int`, and `Style uv.Style`. `uv.Style` provides `Diff(from *Style) string` and `IsZero() bool`, and `ansi.ResetStyle` provides reset escapes.
- Protocol wire version is currently 22 (`internal/protocol/version.go:40`, `web/src/version.ts:2`).
- Protocol uses request/response RPC over Unix domain socket with Protobuf framing (`internal/protocol/wirepb/wideboi.proto`, `internal/protocol/codec.go`, `internal/commands/commands.go:RPCQuery`).
- Agent skill `docs/skills/wideboi-control/SKILL.md:76-89` documents `capture` for headless workflows.

## Desired end state

### 1. Root CLI command `wideboi dump-pane`
```bash
wideboi dump-pane [pane-id] [flags]
```
- Positional `[pane-id]`: Optional target pane ID.
  - When invoked inside a wideboi pane: defaults to `$WIDEBOI_PANE_ID`.
  - When invoked outside a wideboi session: requires explicit `<pane-id>`; exits with error if omitted: `usage: wideboi dump-pane [flags] [pane-id] (pane-id required outside wideboi pane)`.
- Flags:
  - `-s, --scrollback [lines]`: Include scrollback history in addition to the active screen. If an integer argument is supplied (or `--limit` / `-n`), limits the output to the last N lines. If `--scrollback` is passed without an argument, dumps all scrollback + active screen.
  - `--offset <N>`: 0-indexed starting line from the top of the selected buffer (top of scrollback if `--scrollback`, or top of active screen). Defaults to 0 when `--limit` is specified.
  - `--limit <N>`, `-n, --lines <N>`: Maximum number of lines to return. If `--offset` is NOT specified, defaults to tailing behavior (last N lines of the buffer). If `--offset` IS specified, returns up to N lines starting at `--offset`.
  - `-c, --count`: Query and print only the total line count for the requested buffer scope (active screen rows, or scrollback + screen rows) and exit without dumping text.
  - `--ansi`: Preserve ANSI color and style formatting escape sequences.
  - `--plain`: Emit plain text with all formatting stripped (default).
  - `-o, --output <file>`: Write output to `<file>` (overwrites existing file with 0666 permissions subject to umask). If omitted, writes to stdout.
  - `-s, --socket <path>` / `-L, --session <name>`: Target session socket / session name flags (`addTargetFlags`).
- `wideboi capture`: Aliased to `dump-pane`, supporting identical syntax and flags for full backward compatibility.

### 2. Internal Command Registry (`internal/commands/`)
- Register `dump-pane` (with aliases `dump`, `capture`) in `internal/commands/registry.go`.
- Category: `"Panes"`.
- ArgsUsage: `"[pane-id] [-s|--scrollback [lines]] [--offset <N>] [--limit|-n <N>] [-c|--count] [--ansi|--plain] [-o|--output <file>]"`.
- Target pane ID resolution:
  - If `pane-id` positional arg is supplied, parse as int.
  - Otherwise, defaults to `inv.CallerPaneID`.
  - If `inv.CallerPaneID <= 0`, return error: `usage: dump-pane [pane-id] [flags] (pane-id required outside wideboi pane)`.
- Output routing:
  - If `-o, --output <file>` is given: write to `<file>`.
  - Otherwise, write to `inv.Stdout`. If `inv.Stdout == nil` (e.g. invoked from interactive TUI prompt without `-o`), return error: `output file required (-o <file>) when run from prompt`.

### 3. Server & Terminal Grid Extraction
- Add `DumpText(scrollback bool, offset int, limit int, tailLines int, ansi bool) (string, int)` to `term.Grid` interface (`internal/server/term/grid.go`) and `Pane` (`internal/server/pane.go`).
  - Returns `(formattedText, totalLines)`.
- `CaptureText(scrollback bool, maxLines int) string`: delegates to `DumpText(scrollback, 0, 0, maxLines, false)`.
- Paging logic:
  - Total buffer lines = `rows` (if `!scrollback`) or `rows + sbLen` (if `scrollback`).
  - If `tailLines > 0` and `offset == 0 && limit == 0`: `startLine = max(0, totalLines - tailLines)`, `count = tailLines`.
  - If `offset >= 0` and `limit > 0`: `startLine = offset`, `count = limit`.
  - If `offset >= 0` and `limit == 0 && tailLines == 0`: `startLine = offset`, `count = totalLines - offset`.
  - Clamp `startLine` and `endLine = min(totalLines, startLine + count)`. If `startLine >= totalLines`, returns `("", totalLines)`.
- Formatting:
  - When `ansi == false`: plain text with trailing spaces per line trimmed, trailing blank lines at bottom trimmed.
  - When `ansi == true`:
    - For each line, scan to find rightmost non-blank / styled cell (`lastX`).
    - Transition styles using `cell.Style.Diff(&curStyle)` (via `uv.StyleDiff(&curStyle, &cell.Style)`).
    - Emit content and advance by `cell.Width`.
    - At line end, if `!curStyle.IsZero()`, emit `ansi.ResetStyle` and reset `curStyle = uv.Style{}` so styles never bleed across lines or into terminal.
    - Blank lines at bottom trimmed.

### 4. Protocol & Wire
- Add `MsgDumpPaneRequest` and `MsgDumpPaneResponse` to `internal/protocol/messages.go`:
  ```go
  type MsgDumpPaneRequest struct {
      PaneID     int
      Scrollback bool
      Offset     int
      Limit      int
      TailLines  int
      ANSI       bool
      CountOnly  bool
  }
  type MsgDumpPaneResponse struct {
      PaneID     int
      Text       string
      Error      string
      TotalLines int
      Offset     int
      Lines      int
  }
  ```
- Add Protobuf schemas to `internal/protocol/wirepb/wideboi.proto`:
  - `ClientMessage.msg` oneof field 23: `MsgDumpPaneRequest dump_pane_request = 23;`
  - `ServerMessage.msg` oneof field 21: `MsgDumpPaneResponse dump_pane_response = 21;`
  - Schema messages `MsgDumpPaneRequest` and `MsgDumpPaneResponse`.
- Update `internal/protocol/codec.go` marshaling / unmarshaling.
- Bump `protocol.Version` to 23 in `internal/protocol/version.go` and `web/src/version.ts`.
- Server handler `handleDumpPaneRequestLocked` in `internal/server/handlers.go` calls `p.DumpText(...)` and populates `MsgDumpPaneResponse`.

### 5. Documentation & Agent Skill
- Update `docs/skills/wideboi-control/SKILL.md`:
  - Replace / alias `capture` with `dump-pane`.
  - Document paging through scrollback via `--offset <N> --limit <N>`.
  - Document `--count` to discover total available lines.
  - Document `--ansi` / `--plain`.
  - Document `-o, --output <file>`.

## Design decisions

- **Decision:** Default `--scrollback` scope includes both scrollback history and active screen; optional line limit limits to last N lines.
  - **Why:** Matches how Zellij (`dump-screen --full`) and tmux (`capture-pane -S -`) operate; users inspecting pane output usually want the recent context leading up to the current screen.
  - **Rejected:** Scrollback-only dump (which omits the active screen where prompt/command exit status lives).

- **Decision:** `--offset <N>` and `--limit <N>` pagination, with tail behavior when only `-n/--limit` is passed without `--offset`.
  - **Why:** Standard API/CLI pagination convention for paging through streams/buffers from top to bottom (offset 0, limit 100), while preserving standard `tail -n` semantics for simple tailing queries (`-n 100`).
  - **Rejected:** `--start` / `--end` line numbers (more ambiguous regarding 0 vs 1-indexing and bounds).

- **Decision:** Add `-c, --count` and `total_lines` in protocol response.
  - **Why:** Allows agents and scripts to query total lines up-front to calculate pages or check if new output appeared without transferring full buffer text.

- **Decision:** Alias `capture` to `dump-pane`.
  - **Why:** Preserves 100% backward compatibility for existing scripts calling `wideboi capture` while avoiding code duplication.

- **Decision:** New wire messages `MsgDumpPaneRequest`/`MsgDumpPaneResponse` and bump `protocol.Version` to 23.
  - **Why:** Explicitly requested in Issue #327. Follows strict repo invariant from `docs/LESSONS.md`: any wire message change requires bumping `protocol.Version`.

- **Decision:** Plain text by default, `--ansi` enables formatting.
  - **Why:** CLI tools and agents grepping or analyzing pane output want clean plain text without ANSI regex stripping.

- **Decision:** Truncate/overwrite output files with 0666 (respecting umask).
  - **Why:** Consistent with standard shell redirection (`> file`) and dump commands.

## Patterns to follow

- CLI subcommand structure and RPC invocation: `cmd/wideboi/control.go:362-445` (`runRenamePane`).
- Environment fallback for caller pane: `cmd/wideboi/control.go:382-416`.
- Server handler RPC pattern: `internal/server/handlers.go:247-256` (`handleCaptureRequestLocked`).
- Terminal cell extraction and buffer locking: `internal/server/term/grid.go:1090-1150` (`CaptureText`).
- Codec mapping: `internal/protocol/codec.go:85-92, 140-150`.
- Internal registry command registration: `internal/commands/registry.go:279-341` (`rename-pane`).
- Skill documentation pattern: `docs/skills/wideboi-control/SKILL.md:76-89`.

## What we're NOT doing

- Not adding layout dumping (`dump-layout` from Zellij is out of scope for #327).
- Not adding live streaming/following of pane output (tailing is handled by attach or separate tools).
- Not modifying browser web client UI components for dump-pane (web client only needs generated protobuf types and version bump).
- Not adding image/sixel dumping.

## Open questions

*(None remaining — all resolved during brainstorm)*
