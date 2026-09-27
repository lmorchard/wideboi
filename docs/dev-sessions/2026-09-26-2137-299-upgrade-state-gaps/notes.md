# Notes: #299 in-place upgrade state gaps

## Measurements

These cover the heavy case: 8 panes × 120×40 × 10,000 styled scrollback lines.

| Run | State file | Encode + write (old process, under `s.mu`) | `RestoreState` |
|---|---|---|---|
| Spike (throwaway test, 2026-09-26) | 2040 MB | 6.3s | 12.2s |
| `BenchmarkRestoreStateHeavy` | 2041 MB | n/a | 18.4s |

The 60s `reconnectHandshakeCeiling` rests on these numbers. The variance is probably machine load from other agents.

## Decisions made during planning and execution

- **Only the handshake ceiling was raised; the dial ceiling was not.** Before planning, the spec said both would be raised. The socket is bound before restore, and a dead server refuses the dial just as a not-yet-bound one does. So raising the dial ceiling would only delay giving up on a dead server.
- **`s.mu` is held from before the snapshot through exec, with an input drain before the snapshot.** Added during planning:
  - `MsgInput` with a `Key` travels `p.input` → key-writer → vt reply pipe → pty-writer.
  - An open gate alone would still lose keys queued at exec time.
  - **Revised after Copilot's review on #302.** The first version used an `inputPending` counter. It was wrong: it hit zero once the pty-writer had *read* the bytes, while that goroutine could still be in `WriteBounded` (up to 50ms). It is now a flush marker, answered with `grid.SendText("")`. An empty io.Pipe write still waits for the reader's next `Read`, which proves the previous pty write has returned.
  - `TestDrainInputWaitsForPtyWrite` slows each pty write through the `Pane.ptyWritten` seam. It failed deterministically against the counter (`4 of 5 key bytes written`).
  - A first attempt at that test relied on filling the pty. It proved nothing on macOS, where pty master writes never blocked. Its `settled == 0` guard caught that.
  - The drain also moved **before** the snapshot, per the same review.
- **Residual gaps, accepted:**
  - (a) Messages that arrive while `execFn` holds `s.mu` (snapshot + encode, up to ~6s heavy) die with the socket at exec.
  - (b) Output the child produces in reaction to drained keys, but after the snapshot, is still lost.
  - (c) If the key-writer has died (panic), the drain waits its 500ms ceiling and logs a warning.

## Follow-ups

- **#301: shrink the upgrade snapshot** (filed with Les's go-ahead). Per-cell JSON (about 212 bytes/cell) gives 2 GB and 6s + 12–18s in the heavy case. Options:
  - a compact binary or run-length style encoding;
  - encoding outside `s.mu`, which trades against the output loss that #295 fixed.

  Shrinking it also shrinks residual gap (a).

## Fork

- The clone is at `/tmp/lmorchard-x`, branch `vt-scrollback-ring`, based on `cacc71cdcc0f`, with the LFS smudge disabled.
- Commit `7093773` was pushed to `lmorchard/x` `vt-scrollback-ring` with Les's go-ahead. go.mod replaces vt with `github.com/lmorchard/x/vt v0.0.0-20260927070202-7093773bb668`.
- `go mod tidy` also promotes `x/term` and `x/sys` to direct requires. That drift was already on main and has nothing to do with this change, so it's left out of this PR.

## Flake seen during the PR fix round

- One parallel `make check` failed smoke `card layout toggles` and attach-check `dropped connection reconnects`.
- The second case SIGKILLs the server and needs a fresh one bound within the client's unchanged 2s dial ceiling.
- Both passed standalone (attach-check 3/3, smoke 2/2), and two further full `make check` runs were green.
- This is the known parallel-load flake, not this change.
