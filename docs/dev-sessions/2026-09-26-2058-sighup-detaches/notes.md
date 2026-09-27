# Notes: keep the session on owner loss

## State (2026-09-26, end of execute)

All five phases are committed on branch `sighup-detaches` in
`.worktrees/sighup-detaches`. Nothing is pushed and there is no PR yet.

- **Automated:** every automated plan box is ticked with evidence.
  - `make check` passed 4× after phase 2 and 4× after phase 3.
  - It passed once after phase 4 (not a timing change), and `make quick`
    passed after phase 5.
- **Pending (manual, Les):** a real ssh drop.
  1. ssh in and run `wideboi -L hup`, then open a second pane.
  2. Kill the connection with `~.`.
  3. Reconnect. `wideboi ls` should list `hup  detached  -`, and
     `wideboi attach -L hup` should show both panes.
  4. `exits.log` should have
     `client exit ... signal=hangup owner=true detached=true`.

## What was built

- **`keep_session_on_owner_loss`** (default true). It can be set as a TOML key,
  with `WIDEBOI_KEEP_SESSION_ON_OWNER_LOSS`, or with
  `--end-session-on-owner-loss`. Client and server agree because the spawned
  server re-runs `config.Load` with the same argv and env.
- **Client:** `ownerFarewell(sig, keep)` in `cmd/wideboi/hangup.go` picks
  `MsgDetach`/`detachCeiling` for SIGHUP+keep, and
  `MsgShutdown`/`shutdownCeiling` otherwise. The guard stop func in
  `runClient` uses it, and the exits record gains `detached=`.
- **Server:** `Server.ownerLeftWithoutDetaching` (handlers.go) either keeps
  the session and calls the `SetOnOwnerLost` hook (main.go writes
  `owner lost, session kept` to exits.log), or runs
  `CloseFor(ReasonOwnerLeft)`. The bare server's zero value is "end", so the
  old tests still cover that path.
- **`wideboi ls`:** `querySession` does a handshake, then `MsgStatusRequest`
  plus a web status request. `describeClients` and `describeWeb` format the
  result. The web column is built from `Addr`+`TLSEnabled` and **never from
  `URL`**, which embeds the token.
- **Protocol v17** (revised from v16 at rebase: #270 took 16): `MsgLayoutSnapshot.attached_clients`.

## Deviations from the plan

- The plan said to bump only `version.go`. The guard tests also need
  `web/src/version.ts` and a schema hash in
  `internal/protocol/version_guard_test.go`. Its header comment lists the
  steps, so check there before any future proto change.
- `make proto-check` diffs the generated code against HEAD, so it fails until
  the regenerated files are committed. That is by design; run it after the
  commit.
- Phase 2's Go test was first run with no-op setters so that it failed on the
  assertion rather than the build.

## Slips

- I ran a `git stash push` by mistake mid-phase-2. It was tagged; I restored
  it with `git stash apply <sha>` and dropped only that entry. The other
  sessions' stash entries were untouched.

## After merge, for Les

- The v17 bump means a rebuilt client refuses running pre-v17 sessions until
  they are upgraded in place, and `ls` shows them as `? ?`.
- Behaviour change to expect: closing a terminal window no longer ends its
  session. `wideboi ls` shows the detached leftovers.

## Copilot review (PR #300)

- **Fixed:** a status query broadcast a layout snapshot *and* a pane-update
  round to every attached client. I had listed this as a follow-up, but
  Copilot was right that `ls` makes it worse, because it probes every
  session. The fix: `handleStatusRequestLocked` now returns `sendLayout`, so
  `sendLayoutTo` answers only the asker. `layoutSnapshotLocked` is the one
  shared builder, also used by `broadcastLayout`. Pinned by
  `TestStatusRequestAnswersOnlyTheAsker`, which failed first with a bystander
  receiving both a `MsgLayoutSnapshot` and a `MsgPaneUpdate`.
- **Fixed:** the harness `sleep(0.5)`s are replaced with observed state.
  - attachcheck waits for the `owner lost, session kept` record.
  - ptycheck uses a `wideboi status` round-trip after the owner dies. The
    owner only dies once the server has acknowledged, so a shutdown would
    already have closed the listener by then.
  - Both still fail against the old binary for the right reason.
- `make check` passed 4× after these fixes.

## PR-time rebase

- main had gained #270 (protocol v16, `MsgConfigSnapshot`). This branch
  rebased onto it as **v17**, with `version.ts` set to 17, the v17 hash
  recorded, and the TS regenerated. The `main.go` `runServer` conflict was
  resolved by keeping both `SetBindings` and the owner-loss wiring.
- Self-review found that `control.go`'s split auto-spawn error ("could not
  detach ... its session ends with this command") went false under the new
  default, because an unacknowledged detach now keeps the session. It now
  returns that error only when the option is off. There's no test: faking an
  unacknowledged detach on a real spawned server isn't practical, and the
  change is one condition.
