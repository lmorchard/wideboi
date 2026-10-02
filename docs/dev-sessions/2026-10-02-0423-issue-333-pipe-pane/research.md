# Research: Issue 333 (pipe-pane raw PTY stream tapping)

## 1. PTY Master Descriptor and Reader Loop
- Master PTY descriptor is created via `pty.StartWithSize` (`internal/server/ptyx/pane.go:153-156`), duplicated with `syscall.Dup`, marked non-blocking with `syscall.SetNonblock`, and stored in `p.pty.Master` (`*os.File`).
- The PTY reader pump runs as a dedicated goroutine inside `(p *Pane) Start(onExit func())` (`internal/server/pane.go:167-196`).
- It allocates `buf := make([]byte, 4096)` and reads:
  ```go
  n, err := p.pty.Master.Read(buf)
  ```
  `buf[:n]` is the raw, unparsed byte stream emitted directly by the child process PTY before any processing.
- Next, `qs.process(buf[:n], cols, rows)` strips CSI/OSC query sequences (`internal/server/queries.go:105-150`).
- The stripped bytes (`cleaned`) are written to the terminal emulator via `p.grid.Write(cleaned)` (`internal/server/pane.go:189`).
- When the child exits or the master is closed, `Read` returns EOF/EIO/error, terminating the loop and calling `onExit()`.

## 2. Tap Delivery Mechanism & Concurrency
- `Pane` struct (`internal/server/pane.go:26-89`) can maintain a set of active taps:
  ```go
  tapMu sync.RWMutex
  taps  map[uint64]chan []byte
  ```
- When `n > 0` in `internal/server/pane.go:178`, `buf[:n]` is the raw PTY chunk.
- A method `p.broadcastRawBytes(chunk []byte)` copies the slice (`append([]byte(nil), chunk...)`) and non-blockingly fans out to all registered tap channels:
  ```go
  p.tapMu.RLock()
  for _, ch := range p.taps {
      select {
      case ch <- chunk:
      default:
          // Drop on overflow to protect live pane from slow consumers
      }
  }
  p.tapMu.RUnlock()
  ```
- Channel buffer capacity of 256 chunks provides ample buffering (~1 MB) while dropping on overflow guarantees the PTY reader pump and child process will never block.
- When `p.Close()` or the reader loop finishes, `p.closeTaps()` closes all tap channels.

## 3. Streaming RPC over Wire Protocol
- Wire protocol is version 23 (`internal/protocol/version.go:40`). Adding streaming pipe messages bumps `protocol.Version` to 24.
- `internal/protocol/wirepb/wideboi.proto`:
  - `ClientMessage.msg` oneof field 24: `MsgPipePaneRequest pipe_pane_request = 24;`
  - `ServerMessage.msg` oneof field 22: `MsgPipePaneResponse pipe_pane_response = 22;`
- Structs in `internal/protocol/messages.go`:
  ```go
  type MsgPipePaneRequest struct {
      PaneID int `json:"pane_id"`
  }

  type MsgPipePaneResponse struct {
      PaneID int    `json:"pane_id"`
      Data   []byte `json:"data,omitempty"`
      Closed bool   `json:"closed,omitempty"`
      Error  string `json:"error,omitempty"`
  }
  ```
- Codec mapping in `internal/protocol/codec.go`.
- Server handler `handlePipePaneRequestLocked`:
  - Checks if pane exists. If not, sends `MsgPipePaneResponse{Error: "pane N not found"}`.
  - Subscribes a tap channel to the pane.
  - Spawns a pump forwarding chunks from the tap channel to `tp.SendServer(ctx, MsgPipePaneResponse{PaneID: id, Data: chunk})`.
  - When tap channel closes, sends `MsgPipePaneResponse{PaneID: id, Closed: true}`.
  - When connection terminates, unsubscribes the tap.

## 4. CLI Subcommand `pipe-pane`
- CLI command: `wideboi pipe-pane [pane-id] [-o output.raw] [-a|--append]`
- Argument resolution:
  - If `pane-id` omitted: defaults to `os.Getenv("WIDEBOI_PANE_ID")`. If not inside a wideboi pane, returns usage error.
- Output:
  - If `-o <file>` provided: opens file (truncate by default, append if `-a`), writes raw bytes.
  - If `-o` omitted: writes raw bytes directly to `stdout`.
- Exits when:
  - User sends SIGINT (Ctrl-C) or SIGTERM -> exits 0.
  - Pane closes (`Closed: true`) -> exits 0.
  - Server reports error -> prints error to stderr and exits non-zero.

## 5. Internal Command Registry (`:pipe-pane`)
- Registered in `internal/commands/registry.go`.
- ArgsUsage: `"[pane-id] [-o <file>] [-a|--append] [--stop]"`
- If `--stop` passed: cancels active background tap for that pane.
- If `-o <file>` passed: runs streaming tap in background writing to file until `--stop` or pane exit.
- If `-o` not passed and `inv.Stdout == nil`: returns error `output file required (-o <file>) when run from prompt`.
