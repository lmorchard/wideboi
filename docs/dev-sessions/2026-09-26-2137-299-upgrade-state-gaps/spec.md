# In-place upgrade: remaining state gaps (#299) Spec

**Goal:** An in-place upgrade should neither lose keystrokes nor leave a running TUI out of sync. The client should survive a slow restore and report it honestly.

**Source:** https://github.com/lmorchard/wideboi/issues/299 (follow-up to #273 / #295)

## Current state

See `research.md` for the full trace. These are the facts the design rests on:

- **Snapshot fields.** `GridSnapshot` (`internal/server/term/grid.go:145-162`) carries alt screen, bracketed paste and cursor keys. It does not carry the scroll region, keypad mode or the SGR pen.
- **How modes are captured.** They are wideboi atomics, fed by vt's `EnableMode`/`DisableMode` callbacks through `trackTerminalMode` (`grid.go:607-618`).
- **How restore works.** `RestoreSnapshot` re-applies modes by feeding escape sequences to `g.em.Write` (`grid.go:887-987`).
- **Keypad mode (?66) is already observable.** vt's `ESC =`/`ESC >` go through `setMode`, which fires those callbacks (`VT/handlers.go:347-357`, `VT/csi_mode.go:98-107`).
- **Scroll region and pen are not reachable.** They live in vt's unexported `Screen` (`scroll`, `cur.Pen`; `VT/screen.go:14-17`). `(*Screen).ScrollRegion()`/`Cursor()` are exported, but nothing on `Emulator` reaches a `Screen`.
- **The vt fork.** vt is already the fork `lmorchard/x/vt`, branch `vt-scrollback-ring`, via go.mod:40 (#205).
- **Input is dropped during an upgrade.** While `s.upgrading` is set, `handleClientMsg` drops every message (`internal/server/handlers.go:99-103`).
- **Ptys survive the exec.** Their master fds have CLOEXEC cleared, so bytes written to them sit in the kernel buffer across exec.
- **Snapshot and input can't interleave.** `execFn` snapshots under `s.mu` (`internal/server/upgrade.go:186-236`), and `handleInputLocked` also runs under `s.mu`.
- **Reconnect timing:**
  - The attached client redials with `dialWithin(cfg.Socket, 2*time.Second)` (`cmd/wideboi/main.go:983`).
  - The new process binds the socket early (`main.go:346`) but answers handshakes only after `RestoreState` (`main.go:392`) and `ListenSocket` (`main.go:498`).
  - `HandshakeCeiling` is 5s (`internal/transport/handshake.go:31`).
  - Any handshake error on reconnect is reported as `"reconnected to an incompatible server"` (`main.go:1009-1011`).

## Desired end state

1. **Keypad application mode** survives an upgrade. Set with `ESC =` (or `CSI ?66h`), it is still set after restore.
2. **Scroll region** survives an upgrade. Set with `CSI t;b r`, `ScrollRegion()` on the restored emulator matches, and the cursor is where it was, not homed.
3. **SGR pen** survives an upgrade. Text written after restore without a new SGR carries the pre-upgrade attributes and colours.
4. **Keystrokes during an upgrade reach the pane.** A `MsgInput` for a pty pane that arrives while `upgrading` is set is written to the pty, and the child reads it after exec.
5. **The reconnect tolerates a slow restore.** The upgrade reconnect path's *handshake* ceiling covers a heavy restore. A handshake that times out is reported as a timeout, not as an incompatible server. _(Revised after measuring: see the reconnect decision.)_
6. **Tests:**
   - `TestGridSnapshotModesRoundTrip` (or a sibling) covers keypad, scroll region and pen.
   - A server test shows `MsgInput` during `upgrading` reaches the pty while other messages are still dropped.
   - A test measures `RestoreState` with heavy scrollback across several panes.

## Design decisions

- **Decision:** Add exported accessors to the vt fork, then bump the `replace` pseudo-version.
  - The accessors are `Emulator.ScrollRegion() uv.Rectangle` and `Emulator.CursorPen() uv.Style` (names can follow the fork's conventions), with `SafeEmulator` wrappers under the read lock.
  - **Why:** The fork already exists and Les owns it. Accessors are exact and small, and they reuse vt's own state instead of mirroring it.
  - **Rejected:** Scanning `CSI r` and SGR bytes in wideboi's `Write`. That would have to reimplement vt's semantics (split sequences, RIS, alt-screen switches, resize resets) and could silently drift from vt.
  - **Rejected:** Dropping scroll region and pen. A lost scroll region corrupts scrolling in vim/less until they repaint.
  - **Cost, accepted:** The fork now carries more than upstream's pending PRs. Removing it is no longer a one-line go.mod deletion. Record this in `docs/LESSONS.md` or next to the `replace` line.
  - **Process:** The fork commit goes on `vt-scrollback-ring`. **Pushing to `lmorchard/x` needs Les's explicit go-ahead at that moment.**

- **Decision:** Capture keypad mode by extending `trackTerminalMode` with `ansi.ModeNumericKeypad`, in the same way as bracketed paste and cursor keys.
  - **Why:** vt already reports it through the callbacks, so this needs no fork change.

- **Decision:** Restore in this order:
  1. alt screen
  2. scrollback and screen cells
  3. scroll region (`CSI t;b r`)
  4. pen (SGR from `uv.Style.String()`)
  5. cursor position (CUP)
  6. the existing modes, plus keypad (`CSI ?66h`)

  - **Why:** `CSI r` homes the cursor (`VT/handlers.go:861-885`), so it must come before the CUP. The pen must be set after the cells are written by `SetCell` (which doesn't touch the pen) and before any fed text.
  - Serialize the pen with the existing `protocol.EncodeStyle`/`StyleData.Decode` (`internal/protocol/wire.go:122-133`).
  - Skip the region when it equals the full screen, and the pen when it is zero, so the default case feeds nothing.

- **Decision:** Let `MsgInput` bypass the `upgrading` gate in `handleClientMsg`, but only for pty panes. Every other message stays dropped.
  - **Why:** The pty carries the bytes across exec. `s.mu` orders them against the snapshot.
  - Resize and attach are re-sent by the client on reconnect (`internal/client/client.go:167-170`, `main.go:1024`).
  - Verbs, mouse, scroll and RPCs mutate server state that is being serialized.
  - Dashboard-pane input mutates dashboard state, which isn't carried across the upgrade, so it stays dropped too.
  - **Rejected:** Also passing mouse/scroll, because mouse can change focus and scroll offsets. Carrying a replay queue across exec is subtle and needs per-client identity. Only notifying the client still loses keystrokes.

- **Decision (revised after measurement):** Raise only the upgrade reconnect's **handshake** ceiling, to 60s. Keep the 2s dial ceiling. Report a handshake timeout as a timeout; only a `*transport.MismatchError` is reported as an incompatible server.
  - **Measured on 2026-09-26** (8 panes × 120×40 × 10,000 styled scrollback lines, on Les's machine):
    - The state file is **2040 MB of JSON**.
    - Encode + write takes **6.3s**, in the old process, under `s.mu`.
    - `RestoreState` takes **12.2s**. A second run via `BenchmarkRestoreStateHeavy` took 18.4s, so call it 12–18s.
  - **Why 60s:** more than 3× the slowest measured restore, to leave room for slower machines. Waits are ceilings, so it costs nothing when things are fast (CLAUDE.md).
  - **Why not raise the dial ceiling:** the socket is bound before restore (`main.go:346`), so a live upgrading server accepts the dial within milliseconds. A dead server looks the same to the dial (connection refused), so a longer dial ceiling would only delay giving up on one.
  - **Rejected:** moving `ListenSocket` earlier. Its placement is deliberate (`main.go:468` comment, #266).

- **Decision (added during planning, revised in PR review):** Hold `s.mu` from before the snapshot through `syscall.Exec`, and **before the snapshot** wait (with a ceiling) until each pane's queued input has been written to the pty.
  - **Why:** `MsgInput` with a `Key` goes through `p.input` to the key-writer goroutine, then vt's reply pipe, then the pty-writer (`internal/server/pane.go:175-222`). Any key still queued at exec dies with the process.
  - Holding `s.mu` stops new input from being queued. _(Revised after Copilot's review.)_ A **flush marker** (`inputFlush`) is queued behind pending input. The key-writer answers it with `grid.SendText("")`, which returns only once the pty-writer has come back to `Read`, and it does that only after its previous pty write has returned (io.Pipe semantics). The first version used a pending counter, but that dropped to zero while the pty-writer could still be inside `WriteBounded` (up to `ptyWriteTimeout`, 50ms).
  - The drain runs before the snapshot, so output caused by drained keys has the best chance of being captured.
  - **Residual gaps, accepted and documented:**
    - (a) Messages that arrive while `execFn` holds `s.mu` wait in the conn loop and die with the socket at exec. The window is the snapshot + encode time, up to ~6s in the heavy case. That is the snapshot-size problem, which is a follow-up.
    - (b) Output that the child produces in response to drained keys, but only after the snapshot, is still lost. The child reacts asynchronously.

## Patterns to follow

- Mode tracking: `trackTerminalMode` and its atomics (`grid.go:607-618`). Restoring by feeding sequences: `grid.go:943-967`.
- Snapshot tests: `internal/server/term/snapshot_test.go:88-130`.
- Upgrade unit tests that skip the real exec via `s.execSyscall`: `internal/server/upgrade_test.go`.
- Fork workflow: `docs/dev-sessions/2026-09-24-1602-issue-205-scrollback-ring/notes.md`.
- Read the fork's source before assuming an API (CLAUDE.md, `docs/LESSONS.md`).

## What we're NOT doing

- **`clientSizes`.** It doesn't exist, and the client re-sends its size on every reconnect. That item in #299 came from a misreading.
- **Other emulator state.** Not in scope: cursor style/blink, saved cursor (DECSC), origin and autowrap modes, focus-event mode, charsets, tab stops, palette colours, and the primary-screen contents while alt screen is active.
- **Queueing or replaying non-input messages** across exec, including `MsgSendInputRequest`. It is an RPC whose reply dies at exec.
- **Other reconnect-window behaviour.** No reconnect UI or indicator in the TUI, and no client-side buffering of keys during the reconnect. Keys are delayed, not lost, because the event loop resumes after reconnect.
- **Moving `ListenSocket` or restructuring server startup.**
- **Shrinking the snapshot** (a compact encoding instead of per-cell JSON, or encoding outside `s.mu`). This is follow-up #301: 2 GB and 6s + 12s in the heavy case.
- **Raising the reconnect dial ceiling** (see the reconnect decision).
- **Upstreaming the accessors** to charmbracelet/x.

## Open questions

- **Accessor naming in the fork.** Default: `ScrollRegion()` and `CursorPen()`, matching the existing `CursorPosition()`/`IsAltScreen()` style.
- **Pen plus hyperlink.** `Cursor.Link` is next to the pen. Default: leave it out, since it isn't part of #299.
