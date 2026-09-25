# Agent-Friendly Pane Control Subcommands Spec

**Goal:** Provide deterministic, scriptable CLI subcommands (`split`, `send`, `capture`, `close`) for agents and shell tools to manage and interact with wideboi panes by pane ID.

**Source:** https://github.com/lmorchard/wideboi/issues/198

## Current state

- **CLI Dispatch:** `cmd/wideboi/main.go:64-130` (`parseCLI`) parses flags and subcommands (`server`, `attach`, `kill-session`, `status`, `cleanup`, `ls`, etc.).
- **Session Dialing & Short-Lived Commands:** `cmd/wideboi/status.go:25-45` connects via `net.Dial("unix", cfg.Socket)`, executes the versioned handshake `transport.Handshake`, wraps in `transport.NewClientSocketConn`, and uses request/response messaging without launching full TUI/client.
- **Pane Lifecycle:** `internal/server/server.go:610-666` (`SpawnPane`) assigns monotonic `nextPaneID`, configures dimensions, spawns PTY command (either `$SHELL` or `$SHELL -c <command>`), inserts pane into layout strip, and starts I/O pumps.
- **Pane Input:** `internal/server/server.go:511-532` handles `protocol.MsgInput` by calling `p.Write(data)` or `p.SendKey(key)`. Currently fire-and-forget without acknowledgement.
- **Pane Teardown:** `internal/server/server.go:687-697` (`removePaneLocked`) and `internal/server/pane.go:423-450` (`Close`) hang up the PTY master (`pty.Hangup`) with SIGHUP, waiting up to `CloseGrace` for the child process to exit.
- **Terminal Buffer Access:** `internal/server/term/grid.go:158-218` and `internal/server/pane.go:311-369` access visible cells via `DrawAt`/`CellAt` and history via `ScrollbackCellAt`.
- **Wire Protocol:** `internal/protocol` defines protobuf schemas in `wirepb/wideboi.proto`, message structs in `messages.go`, and binary codec in `codec.go`. Connection uses `protocol.Version` (`internal/protocol/version.go`).

## Desired end state

A user or agent script can run four new CLI subcommands targeting any running session (respecting `-L` / `--session` and `-s` / `--socket`):

1. **`wideboi split [--cwd <dir>] [--after <pane-id>] [command...]`**
   - Connects to the session server, requests pane creation.
   - If `[command...]` arguments are provided, joins them with spaces to run as a shell command under the configured shell; if omitted, launches the default shell.
   - Accepts optional `--cwd <dir>` (sets working directory) and `--after <pane-id>` (inserts pane after specified pane in layout).
   - On success: prints the assigned integer pane ID (e.g. `2\n`) to stdout and exits `0`.
   - On failure: prints error message to stderr and exits `1`.

2. **`wideboi send <pane-id> <text> [--enter|-e]`**
   - Connects to the session server and sends the literal bytes of `<text>` to the specified pane's input stream.
   - If `--enter` (or `-e`) is specified, appends a carriage return (`\r`) to trigger line execution in standard terminal modes.
   - On success: exits `0` with no stdout.
   - On failure (e.g., non-existent pane ID, server unreachable): prints error message to stderr and exits `1`.

3. **`wideboi capture <pane-id> [--scrollback|-S] [--lines|-n <count>]`**
   - Connects to the session server and reads the current terminal content for the specified pane.
   - Default behavior captures the visible grid rows, converted to UTF-8 text, trailing whitespace trimmed per row, lines joined with `\n`.
   - `--scrollback` (or `-S`): prepends scrollback history before visible screen rows.
   - `--lines <count>` (or `-n <count>`): restricts the output to the most recent `<count>` lines from the captured output.
   - On success: prints captured text to stdout and exits `0`.
   - On failure (e.g., non-existent pane ID, server unreachable): prints error message to stderr and exits `1`.

4. **`wideboi close <pane-id>`**
   - Connects to the session server and requests closure of the specified pane using existing hangup semantics.
   - On success: exits `0` with no stdout.
   - On failure (e.g., non-existent pane ID, server unreachable): prints error message to stderr and exits `1`.

## Design decisions

- **Decision:** Use explicit request/response messages for `split`, `send`, `capture`, and `close`.
  - **Why:** Issue #198 explicitly requires reporting clear errors for unknown pane or failed operation with deterministic exit codes. Unacknowledged fire-and-forget messages (like raw `MsgInput`) cannot report whether the pane existed or the operation succeeded before the CLI process exits.
  - **Rejected:** Sending unacknowledged `MsgInput` for `send` and `VerbKillPane` for `close`. That would exit 0 even if the pane did not exist.

- **Decision:** Literal text by default with `--enter` (`-e`) flag for `wideboi send`.
  - **Why:** Prevents accidental script execution from unescaped newlines while providing an explicit, reliable option when sending shell commands.
  - **Rejected:** Implicit trailing newline (can accidentally execute dangerous or partially formed commands).

- **Decision:** Default `capture` to visible grid plain text, with `--scrollback` (`-S`) and `--lines` (`-n`) options.
  - **Why:** Most agent/automation tasks want to read what's currently showing on the screen without dealing with unbounded historical lines or terminal escape sequences. Trimming trailing row whitespace produces clean text.
  - **Rejected:** Capturing raw escape sequences by default (pollutes agent context and breaks simple string matching).

- **Decision:** Subcommand argument parsing integrated in `parseCLI` in `cmd/wideboi/main.go`.
  - **Why:** Matches existing subcommands (`status`, `server`, `kill-session`, `cleanup`, `ls`). Keeps flag handling for `-L`/`-s` consistent across all subcommands.
  - **Rejected:** Completely separate standalone binaries.

- **Decision:** Bump `protocol.Version`.
  - **Why:** LESSONS.md explicitly mandates bumping `protocol.Version` whenever wire types are added or changed, ensuring protocol version checks prevent silent corruption or decoding panics between mismatched client/server binaries.

## Patterns to follow

- CLI connection, handshake, and short-lived execution: `cmd/wideboi/status.go:25-45` and `cmd/wideboi/handshake.go:16-22`.
- CLI subcommand registration and parsing: `cmd/wideboi/main.go:64-130`.
- Server message handling and response dispatch: `internal/server/server.go:409-420` and `internal/server/server.go:580-584`.
- Pane creation and dimension setup: `internal/server/server.go:610-666` (`s.SpawnPane`).
- Pane teardown: `internal/server/server.go:687-697` (`s.removePaneLocked`) and `internal/server/pane.go:423-450` (`p.Close`).
- Grid rendering and text extraction: `internal/server/term/grid.go:706-735` (`DrawAt`) and `internal/server/pane.go:311-350`.
- Wire protocol message definition and tests: `internal/protocol/messages.go`, `internal/protocol/wirepb/wideboi.proto`, `internal/protocol/codec.go`, `internal/protocol/wire_test.go:12-30`, `internal/protocol/codec_test.go:85-108`.

## What we're NOT doing

- **Not modifying web client:** The web client does not use these CLI subcommands; its protocol version subprotocol negotiation remains compatible with the bumped version.
- **Not adding interactive TUI bindings for these commands:** These are headless CLI subcommands only.
- **Not implementing interactive REPL or streaming capture:** `capture` is snapshot-based, not a streaming tail/follow tool.
- **Not adding rich process inspection / process tree walking:** `capture` reads emulator buffer text; process status is covered by existing `status` command.
- **Not changing existing pane hangup or signal escalation models:** Teardown follows `ptyx.Hangup` per LESSONS.md ("Pane teardown is a hangup").

## Open questions

*(None. All design decisions confirmed).*
