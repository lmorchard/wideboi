# Keep the session when the owner is lost Spec

**Goal:** a dropped ssh connection (or any other owner loss that isn't a
deliberate stop) never takes the session and its panes with it.

**Source:** Les, 2026-09-26, after an owner-client SIGHUP ended session
`wideboi` while a second, reattached client was live.

## Evidence

`exits.log`, 2026-09-26:

```
20:54:37.605 client exit  session=wideboi pid=86828 reason=signal signal=hangup owner=true
20:54:37.674 server exit  pid=86960 reason=shutdown-request requesterPID=86828 requester=wideboi<-launchd
20:54:39.765 client exit  pid=77882 reason="server gone; reconnect failed..." owner=false
```

`last` shows that the ssh login on ttys000 ended at 20:54. Les had already
reconnected on ttys002 at 20:52. When sshd closed the stale tty, the owner got
SIGHUP and sent `MsgShutdown`, and the session ended under the live second
client.

## Current state

(Details and refs are in `research.md`.)

- The owner client arms SIGINT, SIGTERM, SIGHUP and SIGQUIT identically
  (`cmd/wideboi/main.go:825`). On any of them the guard's stop func sends
  `MsgShutdown` before it restores the terminal (`main.go:798-804`), then
  re-raises.
- The server ends the session when the owner's connection closes without a
  preceding `MsgDetach` (`internal/server/handlers.go:49-53`,
  `ReasonOwnerLeft`).
- The spawned server is `Setsid`, so the terminal's SIGHUP reaches only the
  client (`cmd/wideboi/spawn.go:63-65`).
- `wideboi ls` dials each socket, closes it, and prints names only
  (`cmd/wideboi/sessions.go:18-49`). No message reports a client count.
- Docs describe the old rule: `docs/LESSONS.md:161-169` and
  `docs/MANUAL.md:100-106`.

## Desired end state

A new config option, **`keep_session_on_owner_loss`**, defaults to **true**.

With it on (the default):

1. **Owner SIGHUP detaches.** The owner sends `MsgDetach` instead of
   `MsgShutdown`, restores the terminal, and re-raises SIGHUP as it does today.
   The server keeps running with no owner, exactly like a keyed detach. The
   exits.log `client exit reason=signal signal=hangup` line is unchanged.
2. **Owner EOF without detach keeps the session.** If the owner's connection
   closes with no `MsgDetach` (SIGKILL, crash), the server treats it as a
   detach: it drops the client, gives up ownership, and keeps running. It logs
   that the owner was lost and that the session is being kept.
3. SIGINT, SIGTERM and SIGQUIT to the owner still send `MsgShutdown` and end
   the session. `q`/`quit`, `kill-session`, last-pane-exit and signals to the
   server are unchanged.

With it off, behaviour is exactly as it is today for both 1 and 2.

The option is configured the same way `auto_cleanup` is:

- TOML `keep_session_on_owner_loss = false`
- env `WIDEBOI_KEEP_SESSION_ON_OWNER_LOSS` (parsed with `parseBoolEnv`)
- CLI `--end-session-on-owner-loss` (the negative form, like
  `--disable-auto-cleanup`)

Both the client (for SIGHUP) and the server (for EOF) read it. They agree
because the spawned server re-runs `config.Load` with the same argv and env.

**`wideboi ls` shows attachment state and the web address:**

```
wideboi   detached   https://127.0.0.1:8080
zoo       2 clients  -
scratch   1 client   http://0.0.0.0:9000
old       ?          ?        # handshake or status failed, e.g. an incompatible server
```

- The third column is the session's web server address, when it is running.
  `ls` builds it as `scheme://Addr` from the web status response's `Addr` and
  `TLSEnabled` fields, and prints `-` when the web server is not running.
  **The response's `URL` field is never printed**, because it embeds the
  auth token (`internal/server/web.go:304`). That token must not reach a
  terminal or scrollback through `ls`.
- The address comes from the existing
  `MsgWebServerControlRequest{Action: WebServerActionStatus}`, the same query
  `wideboi status --json` uses (`cmd/wideboi/status.go:43-45`). It needs no
  new protocol.

- Names are left-aligned and padded. The count means attached clients (those
  that sent `MsgAttach`), not probe connections.
- Getting the count means a handshake plus `MsgStatusRequest` per socket. The
  reply carries a new `AttachedClients` field on `MsgLayoutSnapshot`, set from
  `attachedCountLocked()` (`internal/server/clients.go`).
- A session whose status can't be read still gets listed, with `?`. It is
  never hidden.

## Design decisions

- **Only SIGHUP detaches; the other signals still end the session.**
  - **Why:** SIGHUP is the one signal that means "the terminal went away".
    SIGTERM and SIGINT are deliberate stops. The desktop app and ptycheck
    already treat SIGTERM as "end".
  - **Rejected:** detaching on every signal. That leaves no signal-based way to
    end an owned session from outside.
- **One switch covers both SIGHUP and owner EOF, and defaults to on.**
  - **Why:** both mean the owner was lost without saying goodbye. A single
    knob is easier to explain and to test. Les chose on-by-default: losing
    work is worse than a lingering server.
  - **Rejected:** SIGHUP-only with no switch; two separate switches.
- **Detach on SIGHUP is a best-effort send under `detachCeiling`.** The tty is
  already gone, but the socket is local and unaffected. If the detach isn't
  acknowledged, the resulting EOF lands on rule 2, which keeps the session
  anyway when the option is on.
- **Add the count to `MsgLayoutSnapshot`** rather than creating a new message.
  - **Why:** `status` already requests and reads a snapshot. A new proto field
    is wire-compatible, since older clients ignore it.
  - **Rejected:** a dedicated `MsgSessionInfo` request/response pair. That is
    more protocol surface for one integer.
- **The trade-off Les accepted:** closing a terminal window now leaves a
  detached server running. `wideboi ls` makes these visible, and
  `kill-session` or auto-cleanup removes them.

## Patterns to follow

- On-by-default bool config: `AutoCleanup *bool` / `AutoCleanupEnabled`
  (`internal/config/config.go:46-47,275-276,367-373,424-427,491`). Its tests
  are `TestLoadAutoCleanup` (config_test.go:344) and
  `TestParseCLIDisableAutoCleanup` (cli_test.go:354). It is documented in
  `config.example.toml:103-106` and `docs/MANUAL.md:474,543,549-550`.
- Detach send: `performDetach` / `hangUp(..., MsgDetach{}, detachCeiling)`
  (`main.go:893-910`, `hangup.go`).
- Server owner-EOF branch: `handlers.go:43-64`, with the tests next to
  `TestOwnerEOFWithoutDetachEndsTheSession` and `TestOwnerDetachGivesUpOwnership`
  (`internal/server/lifecycle_test.go:92,113`).
- Proto changes: edit `internal/protocol/wirepb/wideboi.proto`, run
  `make proto`, map the field in `internal/protocol/codec.go`. `make
  proto-check` gates the result.

## Tests that change meaning (not weakening: the behaviour changes)

- `scripts/ptycheck.py` SIGHUP case (verify-exit): with the default, a
  SIGHUP'd owner must still die by SIGHUP and emit `ESC[?1049l`, but the
  server and pane shells must **survive**. The case should then kill the
  session explicitly. Add a case with the option off that keeps today's
  "everything gone" assertion.
- `attachcheck.py case_sigkilled_owner_takes_the_session_with_it`: run it with
  the option off. Add the counterpart: with the default, a SIGKILLed owner
  leaves the session running and attachable.
- `TestOwnerEOFWithoutDetachEndsTheSession`: keep it with the option off, and
  add the default-on counterpart. The same goes for `TestOwnerEOFRecordsReason`.
- `attachcheck.py:632,646` parse `ls` with `.split()` and need to take the
  first column of each line instead.

## What we're NOT doing

- No change to SIGINT/SIGTERM/SIGQUIT handling, the attach client's signal
  handling, `control.go`, or the desktop app's quit dialog.
- No re-owning on reattach (`server.go:94-99` stays as is).
- No idle timeout or reaper for detached sessions.
- No change to `wideboi status` output. It may start carrying the count
  internally, but it prints nothing new.
- No attempt to detect "a second client is attached" (that was option A).
- No web token or tokenized URL in `ls` output.
- The protocol version **is** bumped (Les, 2026-09-26) — to 17, *revised at PR time*: main took 16 for #270 while this was in flight — following
  `version.go`'s rule and the #123 precedent.

## Open questions

- Should the `client exit` record for a SIGHUP detach also say it detached?
  **Default:** add `detached=true` to that exits.log line, so a later
  investigation can tell a kept session from an ended one without going to
  the server line.
- Should the server's log reason for a kept owner EOF be a new close reason?
  **Default:** no. The server isn't closing, so this is only an info log
  ("owner left without detaching; keeping the session") plus an exits.log
  record, `owner lost, session kept`, so the post-mortem trail stays complete.
