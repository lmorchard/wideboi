# Research: Issue 198 Pane Control Subcommands

## 1. CLI Dispatch, Flags, and Socket Resolution

- **Subcommand & Flag Parsing**: `cmd/wideboi/main.go:64-130` (`parseCLI`).
  - Pre-scans `os.Args[1:]` (lines 69–94) to extract `opts.subcommand` before flags. Skips argument values for parameter-taking flags (`-c`, `-l`, `-p`, `-s`, `-L`, `--session`, `--socket`, etc., lines 85–93).
  - Uses `flag.FlagSet` with `flag.ContinueOnError` (lines 96–122) to parse flag values into `opts.flags` (`config.ConfigFlags`, `internal/config/config.go:59-68`).
  - Flags `-L` / `--session` bind to `opts.flags.Session`; `-s` / `--socket` bind to `opts.flags.Socket` (`cmd/wideboi/main.go:105-108`).
- **Configuration & Socket Resolution**:
  - `config.Load` (`internal/config/config.go:150-348`) resolves precedence: Defaults -> TOML -> Environment variables -> CLI flags.
  - `SessionDir()` returns `filepath.Join(os.TempDir(), fmt.Sprintf("wideboi-%d", os.Getuid()))` (`internal/config/config.go:87-89`).
  - `SessionSocketPath(name)` returns `filepath.Join(SessionDir(), name+".sock")` (`internal/config/config.go:92-94`).
  - `DefaultSocketPath()` returns `SessionSocketPath("default")` (`internal/config/config.go:97-99`).
- **Subcommand Dispatch**:
  - In `main()` (`cmd/wideboi/main.go:206-231`): dispatches `server`, `attach`, `kill-session`, `status`, `cleanup`, `ls`, or default `run`.
- **Short-Lived Commands**:
  - `runStatus` (`cmd/wideboi/status.go:25-110`) connects via `net.Dial("unix", cfg.Socket)`, performs `handshakeServer(conn, cfg.Socket)` (`cmd/wideboi/handshake.go:16-22`), wraps in `transport.NewClientSocketConn(conn, 256)`, runs pumps, sends request, waits for response message(s), prints output, and cancels context to close connection.

## 2. Pane Lifecycle in Server

- **Creation & ID**:
  - Monotonically increasing `s.nextPaneID++` in `internal/server/server.go:635-636`. Initial value 0, first pane 1.
  - `s.SpawnPane()` or `StartupPane` (`internal/server/server.go:610-666`).
  - PTY spawning: `internal/server/ptyx/pane.go:41-86`. Spawns `argv` with `setsid: true, setctty: true`, allocates PTY master/slave. If interactive: `argv = []string{s.shell}`. If command specified: `argv = []string{s.shell, "-c", spec.Command}`.
  - `NewPane` (`internal/server/pane.go:83-97`): creates `term.NewVT(cols, rows)`, input channel (cap 256).
  - Starts 3 pumps (`internal/server/pane.go:103-171`): PTY Reader (`Master.Read` -> `p.grid.Write`), PTY Writer (`p.grid.Read` -> `p.pty.WriteBounded`), Key Writer (`p.input` -> `p.grid.SendKey`).
- **Closing**:
  - Child exit EOF calls `s.onPaneExit(id)` (`internal/server/server.go:668-685`).
  - Explicit kill: `s.removePaneLocked(id)` (`internal/server/server.go:687-697`).
  - Teardown: `p.Close()` (`internal/server/pane.go:423-450`) calls `p.pty.Hangup(grace)` which closes master FD (SIGHUP) and waits for reaper.

## 3. Input Delivery

- Client sends `protocol.MsgInput{PaneID: id, Data: bytes, Key: KeyData}` (`internal/protocol/messages.go:200-204`).
- Server receives `MsgInput` in `handleClientMsg` (`internal/server/server.go:511-532`).
- If `len(m.Data) > 0`, calls `p.Write(m.Data)` -> `p.pty.WriteBounded(b, ptyWriteTimeout)`.
- If `!m.Key.IsZero()`, decodes key and sends to `p.SendKey(ev)` -> `p.input` queue -> `p.grid.SendKey`.

## 4. Terminal Grid and Scrollback Access

- `vtGrid` (`internal/server/term/grid.go:158-218`) wraps `vt.SafeEmulator`.
- `g.em.ScrollbackLen()` returns scrollback line count.
- `g.em.DrawAt(dst, area, offset)` draws visible rows + scrollback to an `uv.Screen`.
- `g.em.CellAt(x, y)` accesses visible grid cells; `g.em.ScrollbackCellAt(x, y)` accesses scrollback lines.
- `SafeEmulator` methods return pointers into live memory; `writeResizeMu` protects scrollback access during write/resize.
- Visible grid dimensions: `cols` and `rows`.

## 5. Adding Wire Messages to `internal/protocol`

- Files to update:
  1. `internal/protocol/wirepb/wideboi.proto`: add protobuf message types and register in `ClientMessage` or `ServerMessage` oneof.
  2. Regenerate protobuf Go bindings (`make proto` using `protoc-gen-go`).
  3. `internal/protocol/messages.go`: declare Go message struct with exported scalar fields.
  4. `internal/protocol/codec.go`: add cases to `MarshalClient`/`UnmarshalClient` or `MarshalServer`/`UnmarshalServer`.
  5. `internal/protocol/wire_test.go`: add new struct to `wireTypes`.
  6. `internal/protocol/codec_test.go`: `TestCodecRoundTripsEveryField` and `TestWireSchemaCoversEveryWireType` automatically run against `wireTypes`.
  7. `internal/transport/wire_test.go`: add message instances to `TestEveryMessageTypeRoundtrips`.
  8. `internal/protocol/version.go`: bump `protocol.Version`!
  9. Web client: `web/src/wideboi-protocol.ts` (if relevant, though web client uses WebSocket subprotocol `wideboi.v<Version>`).
