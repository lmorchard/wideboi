# Codebase Research: Teardown, Config, and Cleanup

## 1. Server and Client Teardown / Exit Paths

### Client Teardown & Exit Points
- **Normal Detach** (`routeDetach`):
  - Keybinding triggers detach action: `cmd/wideboi/main.go:786-810`.
  - Client invokes `hangUp(ctx, hConn, protocol.MsgDetach{}, detachCeiling)` (`cmd/wideboi/hangup.go:32-48`).
  - Sets `hungUp.Store(true)` (`cmd/wideboi/main.go:797`).
  - Checks `awaitReRaise()` (`cmd/wideboi/main.go:652-657`).
  - Restores terminal via `guard.Stop()` (`cmd/wideboi/main.go:806`), calling `scr.ShowCursor()`, `scr.ExitAltScreen()`, `scr.Flush()`, `t.Stop()` (`cmd/wideboi/main.go:628-638`).
  - Prints detach notice to stdout via `printDetachNotice(os.Stdout, cfg.Socket)` (`cmd/wideboi/main.go:808, 861-876`).
  - Exits loop and returns `nil` (`cmd/wideboi/main.go:810`).
  - Defers run: context cancel (`cmd/wideboi/main.go:601`), profile write (`cmd/wideboi/main.go:562`), close client log handle (`cmd/wideboi/main.go:560`).

- **Normal Quit / Session End** (`routeQuit` or `kill-session`):
  - User triggers quit: `cmd/wideboi/main.go:811-823`.
  - Client sends `protocol.MsgShutdown{}` via `hangUp(...)` (`cmd/wideboi/main.go:813`).
  - Or via CLI `kill-session` (`cmd/wideboi/main.go:462-479`), dialing socket and sending `MsgShutdown{}`.
  - Sets `hungUp.Store(true)`, calls `awaitReRaise()`, returns `nil` (`cmd/wideboi/main.go:818-823`).
  - `defer guard.Stop()` restores terminal (`cmd/wideboi/main.go:645`).

- **Signal / Abrupt Exit**:
  - `hostterm.NewGuard` catches `SIGINT, SIGTERM, SIGHUP, SIGQUIT` (`cmd/wideboi/main.go:646`, `internal/hostterm/guard.go:68-91`).
  - If `owner && !hungUp.Load()`: sends `MsgShutdown{}` to server (`cmd/wideboi/main.go:620-625`).
  - Restores terminal: cursor visibility, exit alternate screen, flush, stop ultraviolet terminal (`cmd/wideboi/main.go:634-638`).
  - Signal re-raised via `syscall.Kill(os.Getpid(), sig)` or exits `128 + int(sig)` (`internal/hostterm/guard.go:79-90`).

### Server Teardown & Exit Points
- **MsgShutdown Received**:
  - In `internal/server/server.go:286-293`, server calls `s.Close()`.
- **Owning Client Disconnects Without Detaching**:
  - In `internal/server/server.go:278-285`, EOF from owner calls `s.dropClient(ctx, tp)` and `s.Close()`.
- **`s.Close()` Execution** (`internal/server/server.go:1532-1594`):
  - Closes `s.stopCh` (`server.go:1536`), terminating `s.Run` loop (`server.go:382-383`).
  - Closes `s.listener` -> `SocketListener.Close()` (`internal/transport/socket.go:165-178`):
    - Closes `net.Listener`.
    - Deletes Unix domain socket file: `os.Remove(sl.path)`.
    - Closes flock handle: `sl.lock.Close()`. (`.lock` file is retained).
  - Closes each pane concurrently (`server.go:1560-1569`) via `p.Close()`.
  - Closes remaining client transports (`server.go:1583-1587`).
- **Server Main Exit Post-Run** (`cmd/wideboi/main.go:400-413`):
  - `srv.Run(ctx)` unblocks and returns.
  - Shuts down HTTP/WebSocket server (`cmd/wideboi/main.go:401-403`).
  - Deferred `os.Remove(webTokenPath(cfg.Socket))` deletes `.web-token` file (`cmd/wideboi/main.go:382`).
  - Deferred `sl.Close()` closes listener (`cmd/wideboi/main.go:275`).
  - Deferred profile stops (`cmd/wideboi/main.go:262`).
  - Deferred server log file close (`cmd/wideboi/main.go:260`).

---

## 2. Configuration Definition, Loading, and Access in `internal/config`

- **Definitions**:
  - `Config` struct: `internal/config/config.go:21-44`.
  - `ConfigFlags` struct: `internal/config/config.go:59-68`.
- **Precedence & Loading Mechanism**:
  - `config.Load(flags ConfigFlags, getenv func(string) string)` (`internal/config/config.go:150-374`):
    - Defaults: `Layout: "cards"`, `Prefix: "ctrl+b"`, `Session: "default"`, `Socket: DefaultSocketPath()`, `Shell: $SHELL`.
    - TOML file: `fileCfg` unmarshaled into struct (`internal/config/config.go:172-259`).
    - Environment variables: `WIDEBOI_*` overrides (`internal/config/config.go:261-283`).
    - CLI flags: `flags.*` overrides (`internal/config/config.go:284-303`).
    - Validation: checks layout, prefix, session dir (`os.MkdirAll`), log level, mouse, keys, widths (`internal/config/config.go:304-373`).

---

## 3. Manual `cleanup` Command and `runCleanup`

- **CLI Routing**:
  - Subcommand `cleanup` parsed in `cmd/wideboi/main.go:76`.
  - Invoked: `case "cleanup": fatal(runCleanup(os.Stdout, config.SessionDir()))` (`cmd/wideboi/main.go:224-225`).
- **`runCleanup(w io.Writer, dir string) error`** (`cmd/wideboi/cleanup.go:24-93`):
  - Helper `isSessionActive(sock string)` dials socket with 100ms timeout (`cleanup.go:13-21`).
  - Removes legacy `server.log` and `client.log` unconditionally (`cleanup.go:25-32`).
  - Cleans dead `*.sock` files (`cleanup.go:45-56`).
  - Cleans dead `*.log` files (`*.server.log`, `*.client.log`) for dead sessions (`cleanup.go:58-78`).
  - Cleans dead `*.web-token` files for dead sessions (`cleanup.go:80-91`).
  - Retains `*.lock` files.

---

## 4. Existing Teardown or Exit Hooks for Cleanup

- No existing general cleanup/garbage collection hooks exist on server or client shutdown.
- Targeted cleanup on exit:
  - `SocketListener.Close()` deletes the current server's `.sock` file (`internal/transport/socket.go:175`).
  - Stale `.sock` is unlinked on server startup if lock is held (`internal/transport/socket.go:136-146`).
  - Deferred `os.Remove` deletes generated `.web-token` on server exit (`cmd/wideboi/main.go:382`).
  - Log files and lock files remain on disk.
