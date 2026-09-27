# Research: owner signals, owner EOF, config, `wideboi ls`

Documentarian pass, 2026-09-26. Paths relative to the worktree root.

## Client signal handling

- `internal/hostterm/guard.go`: `Guard` wraps stop in a `sync.Once` (:16-23);
  `stopFor(sig)` records the winning signal (:40-53), `Signal()` returns it
  (:59-62). `Arm(sigs...)` (:81-116) samples `signal.Ignored`, notifies on a
  1-buffer chan, first signal → `stopFor(s)`, then `signal.Stop`+`Reset`, and
  re-raises (`syscall.Kill`) or `os.Exit(128+n)` if inherited SIG_IGN.
- `runClient` (`cmd/wideboi/main.go:719-1157`) serves owner and attach;
  `owner := serverExit != nil` (:720). Plain `wideboi` spawning a server is
  the owner (:640-661); `attach` or plain `wideboi` joining an existing server
  is not.
- Guard stop func (:785-823): `stopped=true`; if a signal, writes exits.log
  `client exit reason=signal signal=<sig> owner=<bool>` (:787-790); **if
  `owner && !hungUp` → `hangUp(ctx, cConn, MsgShutdown{}, shutdownCeiling)`**
  (:798-804) before terminal restore (verify-exit asserts that order,
  :791-797); then restores the terminal (:806-817).
- Arm: `guard.Arm(SIGINT, SIGTERM, SIGHUP, SIGQUIT)` (:825). All four take the
  same path today. Server arms the same four (main.go:457).
- `performDetach` (:893-910): `MsgDetach` with `detachCeiling` (2s,
  `hangup.go:17`), `hungUp=true`, `awaitReRaise`, `guard.Stop()`, detach notice
  only if not signalled.
- `hangUp()` (`cmd/wideboi/hangup.go:32-48`): send, drain until server closes;
  close is the ack.
- Spawned server is `Setsid` (`spawn.go:63-65`): "the host terminal's SIGHUP
  and ^C belong to the client, which decides what they mean for the session."
- `control.go:168-196` (split auto-spawn) and `desktop.go` (`release` →
  `MsgDetach`, `stopOwned` → `MsgShutdown`) arm no OS signals.

## Server: owner EOF vs detach

- `Server.owner` (`internal/server/server.go:94-99`), nil once detached; never
  re-owned. `SetOwner` :257-266; `runServer` sets it from `--owner-fd`
  (main.go:410-413); `serverArgs` always passes `--owner-fd 3` (spawn.go:123-135).
- `handlers.go:43-64`: channel close → `dropClient`; if wasOwner, logs "owning
  client left without detaching; ending the session" and
  `CloseFor(ReasonOwnerLeft)` (:49-53).
- `dropClient` (`clients.go:137-165`) clears `s.owner` if it was the owner.
- `MsgDetach` → `handleDetachLocked` (:162-164) → `applyEffects` drops client
  (:558-561); owner already nil so no owner-left.
- Close reasons in `closereason.go:8-16`; first wins.
- No server-side flag governs owner-EOF today; `ConfigFlags`
  (`internal/config/config.go:98-112`) has nothing for it.

## Config

- `config.Load(flags, getenv)` (`internal/config/config.go:194`), called once
  in `main()` (main.go:254) for every subcommand including `server`.
- Precedence: defaults (:202-214) < TOML (`~/.config/wideboi/config.toml` then
  `./.wideboi.toml`, :317-330) < env (:344-404) < CLI flags (:406-444) <
  derived (:446-491).
- The spawned server re-runs `config.Load` with the user's argv and inherited
  env/cwd (`spawn.go:115-135`), so it sees the same config.
- On-by-default booleans: `*bool` + derived `...Enabled` — `Mouse` (:42-43,
  :488), `AutoCleanup` (:46-47, :491).
- `auto_cleanup` end to end: TOML :275-276, env `WIDEBOI_AUTO_CLEANUP`
  (:367-373, `parseBoolEnv` :612), CLI `--disable-auto-cleanup` (main.go:86,
  :206; config.go:424-427). Docs `config.example.toml:103-106`,
  `docs/MANUAL.md:474,543,549-550`. Tests `TestLoadAutoCleanup`
  (config_test.go:344), `TestParseCLIDisableAutoCleanup` (cli_test.go:354),
  `TestServerAutoCleanup` (cleanup_test.go:119).

## `wideboi ls`

- `listSessions(dir)` (`cmd/wideboi/sessions.go:18-37`): glob
  `SessionDir()/*.sock`, valid names only, `net.DialTimeout` 1s and close — no
  handshake, no message. `runList` prints one name per line (:40-49). No
  attached/detached/client-count info.
- Desktop reuses `listSessions` (desktop.go:132,275,392), not the printed
  output.
- `attachcheck.py:632,646` asserts `ls` stdout `.split()` equals the name list.
- `wideboi status` (`cmd/wideboi/status.go:27`) handshakes, sends
  `MsgStatusRequest`, reads a `MsgLayoutSnapshot`. No client count anywhere in
  the status/snapshot messages.

## Tests touching this behaviour

- Go: `TestOwnerEOFWithoutDetachEndsTheSession` (lifecycle_test.go:92),
  `TestOwnerDetachGivesUpOwnership` (:113), `TestDetachHangsUpOnlyThatClient`
  (:69), `TestNonOwnerEOFLeavesTheSession` (:140), `TestOwnerEOFRecordsReason`
  (closereason_test.go:26), `hostterm/signal_test.go` guard tests.
- `scripts/ptycheck.py` (`make verify-exit`, Makefile:137-179): signals the
  owning `wideboi`; asserts death by signal, `ESC[?1049l` before death, server
  and pane shells gone, no strays. Matrix: SIGTERM ×4 sizes, SIGINT and
  **SIGHUP** at 80x24.
- `scripts/attachcheck.py`: `case_sigkilled_owner_takes_the_session_with_it`
  (:523), `case_plain_wideboi_detaches_and_the_session_survives` (:473),
  `case_signalled_attached_client_restores_and_detaches` (:406),
  `case_owner_reports_a_signalled_server` (:766).

## Docs stating the current rule

- `docs/LESSONS.md:161-169` (owner signal → `MsgShutdown`; owner EOF without
  detach ends the session).
- `docs/MANUAL.md:100-106` ("Session Lifecycle and Teardown": closing the
  window before detaching tears down the session).
- `README.md:44-56` covers detach/kill-session, not the signal rule.
