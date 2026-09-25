# Notes: Issue 198 Pane Control Subcommands

## What We Built

Implemented four agent-friendly pane control subcommands in wideboi:
- `wideboi split [--cwd <dir>] [--after <pane-id>] [command...]`: creates a pane (running command or configured `$SHELL`), optionally setting working directory and placement after another pane, printing the assigned numeric pane ID on stdout.
- `wideboi send <pane-id> <text> [--enter|-e]`: delivers literal input to a pane's input stream, appending carriage return (`\r`) when `--enter` or `-e` is specified.
- `wideboi capture <pane-id> [--scrollback|-S] [--lines|-n <count>]`: captures terminal output as clean text, handling wide characters and trimming trailing whitespace from lines and unwritten blank rows from the bottom of the viewport. `--scrollback` includes scrollback history, and `--lines` limits output to the last N lines.
- `wideboi close <pane-id>`: closes a pane using standard hangup semantics (`pty.Hangup`).

All commands accept `-L <session>` / `--session` and `-s <socket>` / `--socket` to target any running session, and report clear errors on stderr with exit code 1 if a pane does not exist or the server is not reachable.

## Key Decisions and Learnings

1. **Synchronous Acknowledged RPC Wire Types:**
   Introduced `MsgSplitRequest` / `MsgSplitResponse`, `MsgSendInputRequest` / `MsgSendInputResponse`, `MsgCaptureRequest` / `MsgCaptureResponse`, and `MsgClosePaneRequest` / `MsgClosePaneResponse`. This ensures script commands reliably know if a target pane exists or if an operation succeeded.
2. **Wire Version Bump:**
   Bumped `protocol.Version` to 8 per protocol rules, and updated the web client's offered subprotocol to `wideboi.v8`.
3. **Capture Trailing Blank Row Trimming:**
   When capturing terminal text from the visible screen, unwritten blank rows at the bottom of the screen are trimmed before line-limiting is applied, so `-n 10` extracts the actual last 10 lines of content rather than empty screen rows.
4. **Shell Execution:**
   In `wideboi split [command...]`, the trailing arguments are passed as a command string to `$SHELL -c <command>`.

## Commits

- `70a888c`: Phase 1: wire protocol messages and codec for pane control subcommands
- `cac7d51`: Phase 2: implement CaptureText in term.Grid and Pane
- `5a9fe11`: Phase 3: handle split, send, capture, and close requests in server
- `daf8883`: Phase 4: CLI control subcommands split, send, capture, and close
- `caec7f0`: Phase 5: end-to-end integration tests for pane control subcommands
