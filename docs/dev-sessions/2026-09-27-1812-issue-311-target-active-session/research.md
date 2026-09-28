# Research: Issue 311

## 1. Where session socket and name are known

- `cmd/wideboi/main.go:339`: `runServer(cfg config.Config, ownerFD int)` has `cfg.Socket` and `cfg.Session`.
  - `cfg.Socket` is always resolved to an absolute path (`$TMPDIR/wideboi-<uid>/<name>.sock` or custom path).
  - `cfg.Session` is the session name (e.g. "default", "dev") or empty string if a custom socket was specified via `-s` / `--socket`.
- `internal/server/server.go`: `Server` instance is created via `server.NewServer(ownerConn, cfg.Shell, cwd)` (`main.go:395`).
  - `srv.ListenSocket(ctx, sl)` is called at `main.go:508`, where `sl.Path()` returns the socket path (`internal/transport/socket.go:156`).
  - `Server` currently does not store `socket` or `session`.
- `internal/server/ptyx/pane.go:113`: `ptyx.Spawn(argv []string, cols, rows int, dir string)`:
  - Line 120: `cmd.Env = append(os.Environ(), "TERM=xterm-256color")`.
  - Does NOT set `WIDEBOI_SOCK` or `WIDEBOI_SESSION`.

## 2. Environment precedence and `applySessionLayer`

- `internal/config/config.go:185`:
  ```go
  func applySessionLayer(cfg *Config, layer, session, socket string) error {
      switch {
      case session != "" && socket != "":
          return fmt.Errorf("%s sets both a session name (%q) and a socket path (%q); set one, not both", layer, session, socket)
      case session != "":
          ...
          cfg.Socket = SessionSocketPath(session)
          cfg.Session = session
      case socket != "":
          cfg.Socket = socket
          cfg.Session = ""
      }
      return nil
  }
  ```
- `internal/config/config.go:371`:
  `applySessionLayer(&cfg, "environment", getenv("WIDEBOI_SESSION"), getenv("WIDEBOI_SOCK"))`
- **Key finding:** If BOTH `WIDEBOI_SESSION` and `WIDEBOI_SOCK` are set in the environment, any command calling `config.Load` fails with `"environment sets both a session name ... and a socket path ...; set one, not both"`.
- If `socket == SessionSocketPath(session)`, they are completely consistent. Allowing matching values in `applySessionLayer` removes this conflict while preserving the error when they diverge.

## 3. How prompt and palette are invoked

- Console TUI (`cmd/wideboi/main.go:1151,1162`):
  ```go
  exe, err := os.Executable()
  cmd := fmt.Sprintf("%s prompt --caller-pane=%d --socket=%s",
      shellQuote(exe), cli.FocusedPaneID(), shellQuote(cfg.Socket))
  ```
  Passes resolved executable and explicit `--socket`.
- Web client (`web/src/wideboi-app.ts:797,805`):
  ```typescript
  command: `wideboi prompt --caller-pane=${this.focusedPaneId}`
  command: `wideboi palette --caller-pane=${this.focusedPaneId}`
  ```
  Does not know host executable path or host socket path. Sends bare command string via `MsgSplitRequest`.
- Subcommands (`cmd/wideboi/prompt.go:27,36` and `cmd/wideboi/palette.go:28,37`):
  `addTargetFlags(fs, &session, &socket)`
  `applySessionFlags(&cfg, session, socket)`
  If `--socket` / `--session` flags are absent, falls back to `cfg.Socket` from `config.Load`.
  Without `WIDEBOI_SOCK` in the child environment, `config.Load` falls back to `DefaultSocketPath()` (`default.sock`).

## 4. Pane spawning in Server

- `internal/server/handlers.go:210`: `handleSplitRequestLocked` creates `spec := StartupPane{Command: m.Command, Dir: m.Cwd, Keep: m.Keep}` and calls `s.spawnPaneWithSpecLocked(spec, m.AfterPaneID)`.
- `internal/server/server.go:470`: `spawnPaneWithSpecLocked`:
  - `argv = []string{s.shell, "-c", spec.Command}`
  - calls `NewPane(id, argv, paneCols, paneRows, cwd)` (`internal/server/pane.go:98`)
  - `NewPane` calls `ptyx.Spawn(argv, cols, rows, dir)`
- If wideboi is not in `$PATH`, `sh -c "wideboi prompt ..."` fails with command not found.
- The server process knows `os.Executable()`, the active socket, and session.

## 5. Class-of-bug analogues

- User shells: users typing `wideboi` subcommands in their shell panes (e.g. `wideboi new`, `wideboi split`, `wideboi status`) currently talk to `default.sock` rather than the active session. Setting `WIDEBOI_SOCK` / `WIDEBOI_SESSION` in all spawned panes fixes this for all user shells too.
- Startup panes (`cfg.Startup`): configured panes running commands on server start also lacked session environment.
