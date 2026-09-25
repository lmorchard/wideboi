# Headless agent ergonomics Spec

**Goal:** Let an agent run a command in a wideboi pane, wait for it to finish,
read its output and exit code, and clean up. It should do that reliably, in a
handful of calls, without fixed sleeps.

**Source:** https://github.com/lmorchard/wideboi/issues/227, plus review of
`docs/skills/wideboi-control/SKILL.md` after #224 (2026-09-24).

## Current state

See `research.md` for the file:line references.

- `split` fails with "no wideboi server running" unless a server is already up.
  Bare `wideboi` spawns one (`cmd/wideboi/spawn.go:33-89`) and uses the
  socketpair handshake as its readiness signal.
- A pane is removed as soon as its pty hits EOF (`Server.onPaneExit`,
  `internal/server/server.go:842-877`). If it was the last pane, the server
  shuts down. For `split make test`, the output is gone before anyone can
  capture it.
- The child's exit status is discarded (`internal/server/ptyx/pane.go:91-94`).
  `MsgPaneClosed{ExitCode}` exists but is never sent.
- `status --json` emits statuses as integers and columns as
  `PaneID/Width/Height`, while metadata uses snake_case
  (`cmd/wideboi/status.go:17-22`). SKILL.md documents string statuses, so the
  doc is wrong.
- `send` silently drops every text argument after the first
  (`cmd/wideboi/control.go:183`). `split` joins its args with spaces, which
  loses quoting (`control.go:129`).

## Desired end state

```bash
P=$(wideboi split --keep --cwd "$PWD" make test)   # spawns a server if needed
wideboi wait "$P"; RC=$?                           # blocks; exits with make's code
wideboi capture "$P" -S -n 200                     # screen still there
wideboi close "$P"
```

1. **`split` auto-spawns** an ownerless background server when no server
   answers on the target socket, then creates the pane. The other
   subcommands (`send`, `capture`, `close`, `wait`) still error if there is no
   server.
2. **`split --keep`.** When a kept pane's process exits, the pane stays in the
   layout with its grid intact. Its status becomes `done` (exit 0) or `failed`
   (non-zero or signal), and its exit code is recorded. It stays until
   `wideboi close`, or until the TUI kill-pane action removes it. Kept exited
   panes count as panes for the "last pane gone → server exits" rule. Without
   `--keep`, behaviour is unchanged.
3. **`wideboi wait <pane-id> [--timeout <dur>]`** blocks until the pane's
   process has exited, then exits with that code. A signal death is reported
   as 128+signal, like a shell would. If the pane has already exited and was
   kept, it returns immediately. If a waiter is registered when a non-kept
   pane exits, that waiter still gets the code. An unknown pane id is an error
   (exit 1 plus a message). `--timeout` expiry exits 124, matching
   `timeout(1)`. There is no timeout by default.
4. **`send` to an exited kept pane** errors with "pane N has exited".
   `capture` works.
5. **`status --json` is cleaned up:**
   - statuses are strings: `idle`, `working`, `needs_input`, `done`, `failed`.
   - columns use `pane_id`, `width`, `height`.
   - pane metadata gains `exited` (bool) and `exit_code` (int, always
     present, meaningful only when `exited` is true — simpler than a nullable
     field through proto).
   - `scripts/attachcheck.py`, `scripts/traffic.py` and `status_test.go` are
     updated to match.
   - The text table uses `needs_input` too, for consistency.
6. **`send` with more than one text operand** errors with a usage message
   saying to quote the text.
7. **`split` with a multi-arg command** shell-quotes each arg before
   `$SHELL -c`. A single arg still passes through verbatim as a shell string.
8. **SKILL.md is rewritten** to match all of the above:
   - the `split --keep` → `wait` → `capture` → `close` pattern.
   - no fixed `sleep`s; readiness is polling on observed state.
   - sending Ctrl-C and other control keys (`send $P $'\x03'`).
   - correct status JSON.
   - no `server &` prelude in the smoke test.

## Design decisions

- **Remain-on-exit is opt-in per pane, via `split --keep`.** (Les)
  - **Why:** closing a pane on exit is the right default for the TUI. Only
    headless callers need the corpse.
  - **Rejected:** keeping every pane by default (changes TUI behaviour);
    a `--remain-on-exit` spelling (long; tmux-ism).
- **`wait` requires the exit code to still be reachable. There is no
  server-side history of closed panes.** (Les, option a)
  - **Why:** it's simpler, and `--keep` is the documented way to get
    reliability. A waiter that registers before the exit is served anyway,
    which covers most non-kept races for free.
  - **Rejected:** a TTL cache of recent exit codes (more state for little
    gain).
- **Exit status comes from `cmd.Wait()` in ptyx.** The pane's `onExit` path
  waits on `Done()` before recording the code.
  - **Why:** pty EOF is how the server notices an exit, but the status only
    exists after the child is reaped. Background jobs holding the pty can make
    EOF and reap happen at different times. Reading the code after `Done()` is
    the only ordering that is always correct.
- **Auto-spawn reuses `spawnServer` and then detaches.** `split` spawns with
  the owner socketpair, handshakes (the readiness signal), and sends
  `MsgSplitRequest` on that connection. It then sends `MsgDetach`, so the
  server runs ownerless. It exits when its last pane goes, like any detached
  session.
  - **Why:** readiness has no polling and no sleeps. The session-taken race
    reuses the existing `errSessionTaken` → `dialWithin` redial path
    (`cmd/wideboi/main.go:540-577`).
  - **Rejected:** `wideboi server &` plus polling the socket (the fixed-sleep
    pattern we're trying to remove); a new "headless" server mode (more
    surface).
- **`wait` is a server-side blocking RPC** (`MsgWaitRequest` →
  `MsgWaitResponse{ExitCode, Error}`), not client polling of `status`.
  - **Why:** it's exact, and there is no poll interval. `rpcQuery`'s fixed 5s
    timeout gets parameterised; `wait` passes "no timeout" or `--timeout`.
- **Exit info in status travels on `MsgPaneMetadata`.**
  - **Why:** `status` already collects metadata per pane, and the metadata
    struct already has snake_case JSON tags.
- **The status JSON change is breaking, and that's accepted.** (Les)
  - **Why:** the project is pre-1.0 and has only two in-repo consumers, both
    updated here.
- **Protocol version bumps 8 → 9**, following the file list in `research.md`.
- **`send`: extra operands are an error, not joined.** (Les)
  - **Why:** predictable; no silent whitespace collapse.
- **`split`: multiple args are shell-quoted; a single arg is verbatim.** (Les)
  - **Why:** `split grep "a b" f` then does what argv implies, and
    `split 'make && echo ok'` keeps working.

## Patterns to follow

- Request/response RPC shape: the #198 handlers in `internal/server/server.go:436-477`.
  Responses go to the requester only (`:726-742`).
- New message plumbing: the complete file list in `research.md` → "Adding a message
  type"; regenerate with `make proto`.
- Owner spawn + handshake-as-readiness: `cmd/wideboi/spawn.go`, `main.go:531-620`.
- Detach: `hangUp(ctx, conn, protocol.MsgDetach{}, ceiling)` in `cmd/wideboi/hangup.go`.
- E2E tests over a real socket: `cmd/wideboi/control_e2e_test.go`.
- Waits are ceilings; no `sleep` in tests (CLAUDE.md).

## What we're NOT doing

- Sending `MsgPaneClosed` to clients, or any TUI/web rendering of exited panes
  beyond the existing status glyph.
- A TTL/history of exit codes for closed panes.
- Auto-spawn for `send`/`capture`/`close`/`wait`/`status`.
- Named keys for `send` (e.g. `C-c`, `Enter`). Literal bytes only; SKILL.md shows
  `$'\x03'`.
- Installing/linking SKILL.md into a skills directory.
- Changing `statusName` users other than the text table, or the dashboard.
- A `--keep` equivalent for config startup panes.

## Open questions

- *Should a kept exited pane show anything in its grid (e.g. "[exited 2]")?*
  Default: no. The status glyph conveys it and capture stays pure.
- *`--timeout` format?* Default: Go `time.ParseDuration` (`30s`, `5m`).
