# Research: socket paths, startup, CLI, tests (at da5f090)

## Socket path resolution
- `defaultSocketPath()` cmd/wideboi/main.go:32-43 — **dead code**, never called. Honours `WIDEBOI_SOCK`, MkdirAll 0700.
- `config.DefaultSocketPath()` internal/config/config.go:63-67 — `$TMPDIR/wideboi-<uid>/default.sock`, pure.
- `config.Load` (config.go:71) precedence: default (83) < TOML `socket` (115-117) < `WIDEBOI_SOCK` (144-146) < `-s/--socket` (161-163); empty → default (190-192); then `MkdirAll(dir(Socket), 0700)` (193-195).
- Logs: `logger.Path(c)` = `$TMPDIR/wideboi-<uid>/<c>.log` (internal/logger/logger.go:47-49), O_APPEND, regardless of socket. One `server.log` / `client.log` shared by every server/client.
- References elsewhere: help text main.go:140,149; `printDetachNotice` main.go:543-550 (adds `-s <path>` only when not default); cli_test.go:132,175,179; socket.go:111 comment; README 128,181,192,194; config.example.toml 10,23-24,39; LESSONS.md:546-560; Makefile:37-40.
- Scripts isolate via `WIDEBOI_SOCK`: attachcheck.py `mkdtemp(...)/default.sock` (72-104); smoke.py `RUNTIME_DIR/s<N>.sock` (95-131); golden.py `$TMPDIR/wideboi-golden-<pid>.sock` (64-71); ptycheck.py `$TMPDIR/wideboi-ptycheck-<pid>.sock` (115-123).

## Plain `wideboi` startup
- `run()` main.go:318-327: dial `cfg.Socket` → `runClient(owner=false)`; else `spawnServer(os.Args[1:])` → `runClient(owner=true)`.
- `spawnServer` cmd/wideboi/spawn.go:25-63: socketpair (CLOEXEC under ForkLock), child = `exe server --owner-fd 3 <args>` (`serverArgs` 72-84), `ExtraFiles=[theirs]`, `Setsid`, stdio /dev/null, env/cwd inherited, `go cmd.Wait()`.
- `runServer` main.go:211-289: `logger.Init("server")`; `NewSocketListener(cfg.Socket)` first — error → slog.Error + return → `fatal` (stderr=/dev/null, exit 1); `defer sl.Close()`; owner fd → `NewServerSocketConn`; `NewServer`; signal guard runs `srv.Close()` + `sl.Close()`; `ListenSocket`; `Run`.
- `NewSocketListener` internal/transport/socket.go:105-132: probe dial (success → "a wideboi server is already listening") → Lstat (non-socket → refuse; socket → Remove) → `net.Listen`. Three steps, no lock.
- Owner-side failure detection runClient main.go:431-449: on `ServerSendChan` close — `cConn.Err()` → "connection ... failed"; `owner && !gotMsg` → "wideboi server exited during startup; see <server.log>"; else nil. `fatal` → exit 1. No reason is passed back from server to owner.

## CLI
- `parseCLI` main.go:64-119: first bare `server|attach|kill-session|version|help` is the subcommand (76-79); value-taking flags listed at 81-88 (`skipNext`); flagset 91-107: `c/config l/layout p/prefix s/socket shell owner-fd v/version h/help`.
- `runAttach` 308-314, `runKillSession` 293-306: dial `cfg.Socket`, errors "no wideboi server running at %s".
- No session names, no list command.

## Tests
- cli_test.go: parseCLI defaults/subcommands/flags (11-145), owner-fd (147), detach notice (173), serverArgs (197). String-only.
- main_test.go: attach/detach/reattach (18), kill-session (108), idle (163). Private sockets (`t.TempDir()` or `MkdirTemp` — darwin 104-byte sun_path limit). None exercise `run()`/`spawnServer`.
- socket_test.go: non-socket refusal (20), round trip (41). No live-probe test.
- lifecycle_test.go:188 stop listening before reaping.
- attachcheck.py cases (list 772-792) incl. "second server refuses to steal the socket" (534-550), "attach without a server says so" (553), kill-session (592, 629), plain detach survives (443), SIGKILLed owner (493), plain attaches to running (515). ptycheck asserts socket removed after signal (244-254).
- No test of concurrent plain `wideboi` startup.

## Locking / listing / cleanup
- No flock/lockf/lock files anywhere. No enumeration of the runtime dir.
- `SocketListener.Close` socket.go:145-155: once; `listener.Close()`; `os.Remove(path)` unconditionally.
- `Server.Close` server.go:810-866 closes listener first. Called on MsgShutdown (157-164), owner EOF without detach (150-155), signal guard.
- SIGKILLed server leaves the socket; next `NewSocketListener` probes and removes it as stale.
