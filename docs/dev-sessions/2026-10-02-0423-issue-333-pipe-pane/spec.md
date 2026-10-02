# Pipe-Pane Raw PTY Stream Tapping Spec

**Goal:** Provide a `wideboi pipe-pane` command in the root CLI and internal command registry to tap and stream the raw, unparsed PTY byte stream of a pane in real time to stdout or a file for debugging, test fixture generation, and recording.

**Source:** https://github.com/lmorchard/wideboi/issues/333

## Current state

- The server reads from `p.pty.Master` (`*os.File`) in a dedicated reader goroutine inside `internal/server/pane.go:178`.
- Bytes are passed through `qs.process` to strip terminal queries (`internal/server/queries.go`), and stripped bytes are passed to `p.grid.Write` (`internal/server/term/grid.go`).
- There is currently no way to tap or inspect the unparsed raw bytes emitted by the child process before query processing and emulator parsing.
- Wire protocol is version 23 (`internal/protocol/version.go:40`).

## Desired end state

### 1. Root CLI command `wideboi pipe-pane`
```bash
wideboi pipe-pane [pane-id] [flags]
```
- Positional `[pane-id]`: Optional target pane ID.
  - When invoked inside a wideboi pane: defaults to `$WIDEBOI_PANE_ID`.
  - When invoked outside a wideboi session: requires explicit `<pane-id>`; exits with error if omitted: `usage: wideboi pipe-pane [flags] [pane-id] (pane-id required outside wideboi pane)`.
- Flags:
  - `-o, --output <file>`: Write raw bytes to `<file>` instead of stdout. Truncates file by default.
  - `-a, --append`: When `-o` is given, append to the output file instead of truncating.
  - `-s, --socket <path>` / `-L, --session <name>`: Target session socket / session name flags (`addTargetFlags`).
- Lifecycle:
  - Streams incoming raw byte chunks to `stdout` (or `-o <file>`) as they arrive from the server.
  - Unbuffered raw byte output (flushed on each chunk received).
  - Exits with status 0 upon Ctrl-C / SIGINT / SIGTERM or when the target pane closes.

### 2. Internal Command Registry (`:pipe-pane`)
- Registered in `internal/commands/registry.go`.
- Category: `"Panes"`.
- ArgsUsage: `"[pane-id] [-o <file>] [-a|--append] [--stop]"`.
- Behavior:
  - If `--stop` is given: cancels any active background tap for that pane.
  - If `-o <file>` is given: runs streaming tap in a background goroutine writing to `<file>` until `--stop`, pane close, or client exit.
  - If `-o` is omitted and `inv.Stdout == nil`: returns error `output file required (-o <file>) when run from prompt`.
  - If `inv.Stdout != nil`: streams to `inv.Stdout`.

### 3. Server Pane Raw Tap Management
- In `internal/server/pane.go`, `Pane` manages tap subscribers:
  - `AddTap(id uint64, ch chan<- []byte)`
  - `RemoveTap(id uint64)`
- In `internal/server/pane.go:178` (PTY reader pump):
  - When `n > 0`, copies `buf[:n]` and delivers to all active tap channels non-blockingly.
  - Buffer capacity: 256 chunks. If a tap channel is full, chunk is dropped for that tap to guarantee the live pane never stalls.
  - When PTY reader finishes (EOF/close), closes all tap channels.

### 4. Wire Protocol & Codec
- Bump `protocol.Version` to 24 in `internal/protocol/version.go` and `web/src/version.ts`.
- In `internal/protocol/wirepb/wideboi.proto`:
  - `ClientMessage.msg` oneof field 24: `MsgPipePaneRequest pipe_pane_request = 24;`
  - `ServerMessage.msg` oneof field 22: `MsgPipePaneResponse pipe_pane_response = 22;`
  - Message definitions:
    ```protobuf
    message MsgPipePaneRequest {
      int32 pane_id = 1;
    }

    message MsgPipePaneResponse {
      int32 pane_id = 1;
      bytes data = 2;
      bool closed = 3;
      string error = 4;
    }
    ```
- Go structs in `internal/protocol/messages.go`: `MsgPipePaneRequest` and `MsgPipePaneResponse`.
- Codec mapping in `internal/protocol/codec.go`.
- Server handler `handlePipePaneRequestLocked` in `internal/server/handlers.go` sets up streaming subscription on the client's transport.

### 5. Documentation & Agent Skill
- Update `docs/skills/wideboi-control/SKILL.md` documenting `pipe-pane` for live streaming and raw fixture capture.

## Design decisions

- **Decision:** Stream raw bytes directly from `pty.Master.Read` before `queryScanner`.
  - **Why:** The primary motivation in #333 is diagnosing tricky terminal issues, OSC title parser traps, and capturing reproducible real-world test fixtures. Capturing before `queryScanner` preserves the authentic, unparsed byte stream.
  - **Rejected:** Capturing after `queryScanner` (which strips terminal query sequences).

- **Decision:** Client-driven streaming for CLI invocations.
  - **Why:** Running `wideboi pipe-pane [pane-id]` as a foreground process naturally integrates with standard Unix pipelines (`|`, `>`, `tee`, `asciinema rec`). Stopping the command via Ctrl-C stops the tap automatically.
  - **Rejected:** Server-only file logging with mandatory `--start`/`--stop` flags.

- **Decision:** Non-blocking tap broadcast with drop-on-overflow (256 chunks).
  - **Why:** Protecting live child processes and terminal sessions is a core wideboi invariant. A slow logger or debug client must never stall a live pane's PTY pump.

- **Decision:** Add `--stop` in the internal command registry.
  - **Why:** When started via `:pipe-pane -o file` in the interactive TUI prompt without a blocking terminal, `--stop` provides a clean way to terminate the tap.

## Patterns to follow

- CLI subcommand structure: `cmd/wideboi/control.go` (`runDumpPane`).
- Stream reading loop on Unix domain socket: `internal/commands/commands.go`.
- Transport pump and frame encoding: `internal/transport/frame.go`.
- Protocol version bump and wire test guards: `internal/protocol/version_guard_test.go`, `internal/protocol/wire_test.go`.

## What we're NOT doing

- Not modifying or injecting input into the PTY stream (tapping is strictly read-only monitoring).
- Not intercepting child process stdin/keyboard input (only PTY master output).
- Not altering terminal emulation, snapshots, or web client UI.

## Open questions

*(None remaining — all resolved during brainstorm)*
