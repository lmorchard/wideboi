# In-place upgrade: remaining state gaps (#299) Implementation Plan

**Goal:** Close the #299 gaps. Keypad mode, scroll region and SGR pen should survive an upgrade. Keystrokes during an upgrade should reach the pane. The upgrade reconnect should tolerate a slow restore and report a timeout honestly.

**Approach:**
- Keypad mode rides the existing mode-callback atomics.
- Scroll region and pen need new exported accessors in the `lmorchard/x/vt` fork.
- `MsgInput` for pty panes passes the `upgrading` gate. `execFn` holds `s.mu` through exec and drains pane input queues first.
- The reconnect's handshake ceiling goes to 60s, based on measurement. The dial ceiling stays at 2s.

**Tech stack:** Go, `charmbracelet/x/vt` (fork `lmorchard/x/vt`, branch `vt-scrollback-ring`), ultraviolet.

**Phase order:** The fork phase is last. Phases 1–3 need no fork change, so they stay valuable if the fork push is held up.

**Local fork development:** The fork clone is at `/tmp/lmorchard-x`. The LFS smudge is disabled there; vt doesn't need it. Phase 4 develops against a temporary `replace github.com/charmbracelet/x/vt => /tmp/lmorchard-x/vt`. **Pushing the fork needs Les's explicit go-ahead**, and only then is the replace switched back to a pushed pseudo-version.

---

## Phase 1: Keypad application mode survives an upgrade

This phase records DECNKM (?66) the same way as bracketed paste and cursor keys, snapshots it, and restores it.

**Files:**
- Modify: `internal/server/term/grid.go`
  - Add `keypadApp atomic.Bool` to `vtGrid`, next to `bracketedPaste`/`cursorKeys`.
  - In `trackTerminalMode`, add a case for `ansi.ModeNumericKeypad`.
  - Add `KeypadApp bool \`json:"keypad_app,omitempty"\`` to `GridSnapshot`.
  - Fill it in `ExportSnapshot`.
  - In `RestoreSnapshot`, restore it after cursor keys.
- Test: `internal/server/term/snapshot_test.go`

**Key changes:**

```go
case ansi.ModeNumericKeypad:
	g.keypadApp.Store(on)
```

```go
if snap.KeypadApp {
	g.keypadApp.Store(true)
	_, _ = g.em.Write([]byte("\033[?66h"))
}
```

Check before writing the case: `ansi.ModeNumericKeypad` must be an `ansi.DECMode`, which `trackTerminalMode` requires. Verify in x/ansi v0.11.8. If it isn't, match it the way vt's mode.go declares it.

**Test (write first):** `TestGridSnapshotKeypadRoundTrip`:
- Write `ESC =` to a fresh `NewVT(80,24)`.
- Assert `ExportSnapshot().KeypadApp`.
- Restore into a fresh grid and assert that re-exporting still has `KeypadApp`.
- Also assert **behaviour**: after restore, `SendKey` for keypad Enter (`uv.KeyKpEnter`) produces the application sequence `ESC O M`, read from `grid.Read` in a goroutine, because `SendKey` blocks on the pipe (LESSONS "Probe the pinned terminal dependencies").
- Negative case: `ESC >` after `ESC =` means `KeypadApp` is false.
- Prove it fails first: the struct field won't compile, so first add the field only, watch the assertion fail, then add the tracking.

**Verification — automated:**
- [x] `go test ./internal/server/term -run 'Snapshot' -v` passes — **3/3 PASS**
- [x] The new test fails with the behaviour removed — **with the `?66h` restore dropped: `restored keypad Enter = "\r", want "\x1bOM"`**; before tracking existed: `expected snap.KeypadApp after ESC =`
- [x] `make quick` passes — **exit 0**

**Verification — manual:**
- [x] None. This is covered by the behavioural assertion.

---

## Phase 2: Keystrokes during an upgrade reach the pane

This phase lets `MsgInput` for pty panes through the `upgrading` gate, and has `execFn` drain queued input under `s.mu` right before exec.

**Files:**
- Modify: `internal/server/handlers.go`, in `handleClientMsg`. Replace the blanket drop with:

  ```go
  s.mu.Lock()
  if s.upgrading {
  	// Keystrokes for a pty pane still reach the child: the pty survives
  	// exec (CLOEXEC is cleared) and execFn drains queued keys before
  	// exec. Everything else mutates state being serialized, or is
  	// re-sent by the client on reconnect (MsgAttach carries its size).
  	in, ok := msg.(protocol.MsgInput)
  	if !ok || !s.isPtyPaneLocked(in.PaneID) {
  		s.mu.Unlock()
  		return false
  	}
  }
  ```

  The `switch` then routes it to `handleInputLocked` as usual. That handler's scroll-offset bookkeeping only touches `clientState`, which is never serialized, so it's harmless.

- Modify: `internal/server/handlers.go` (or next to `clientLocked` in `clients.go`). Add:

  ```go
  // isPtyPaneLocked reports whether id is a pane backed by a live pty.
  func (s *Server) isPtyPaneLocked(id int) bool {
  	p, ok := s.panes[id]
  	return ok && p.pty != nil && id != s.statusPaneID
  }
  ```

- **Revised in PR review (#302, Copilot).** The `inputPending` counter below was replaced by an `inputFlush` marker answered with `grid.SendText("")`, and the drain moved before `buildUpgradeStateLocked`. See notes.md. `TestDrainInputWaitsForPtyWrite` was added.
- Modify: `internal/server/pane.go`:
  - Add `inputPending atomic.Int64` to `Pane`, next to `input`.
  - `SendKey`, `SendBytes` and `SendMouse` call `p.inputPending.Add(1)` **before** the channel send, and `Add(-1)` in the `default:` (dropped) branch.
  - The key-writer loop calls `p.inputPending.Add(-1)` after handling each event, deferred per event so a panic doesn't wedge the counter. The loop body is small enough to do it at the end of the case.
  - Add:

  ```go
  // drainInput waits until every queued key, mouse event and paste has
  // been handed to the child, or ceiling passes. It reports whether the
  // queue drained. A pane without a pty has no writer and drains
  // trivially.
  func (p *Pane) drainInput(ceiling time.Duration) bool {
  	if p.pty == nil {
  		return true
  	}
  	deadline := time.Now().Add(ceiling)
  	for p.inputPending.Load() > 0 {
  		if time.Now().After(deadline) {
  			return false
  		}
  		select {
  		case <-p.closed:
  			return true
  		case <-time.After(time.Millisecond):
  		}
  	}
  	return true
  }
  ```

- Modify: `internal/server/upgrade.go`, in `execFn`:
  - Stop unlocking `s.mu` between the encode and the exec.
  - After `f.Close()`, drain: `for _, p := range s.panes { if !p.drainInput(inputDrainCeiling) { slog.Warn("upgrade: pane input did not drain", "pane", p.id) } }`, with `const inputDrainCeiling = 500 * time.Millisecond`.
  - Then exec **while holding `s.mu`**. On exec failure, `rollbackUpgradeLocked` then `s.mu.Unlock()`.
  - Update the comment to say why `s.mu` is held across exec: so no handler can queue input after the drain.
  - Check `s.execSyscall` test overrides: `TestExecFailureDropsClient` must not take `s.mu` inside the override. Read it first.

**Tests (write first):** in `internal/server/upgrade_test.go`.
- `TestInputDuringUpgradeReachesPtyPane`:
  1. Create a server whose pane 1 is `NewPane(1, []string{"/bin/sh", "-c", "read x; echo got:$x; sleep 5"}, 40, 10, "")`, started, with an in-proc transport.
  2. Set `s.upgrading = true` under `s.mu`.
  3. `handleClientMsg(ctx, tp, MsgInput{PaneID: 1, Data: []byte("abc\r")})`.
  4. `waitFor` the grid's `CaptureText` to contain `got:abc`. That is output the program produced, not the tty echo (LESSONS "Test the actual path").
  5. Also send a `Key`-form input path. Use a second pane with `read` and `MsgInput{Key: ...}` for each rune plus Enter, and assert `got:` output. This covers the `p.input` path.
- `TestUpgradeDropsNonInputMessages`: with `upgrading` set, a `MsgResize{Cols: 99, Rows: 9}` leaves `cs.size` unchanged, and a `MsgVerb` for focus leaves focus unchanged. Input for the status/dashboard pane ID is dropped: `MsgInput` for `s.statusPaneID` with a dashboard set doesn't call dashboard handling. Assert that dashboard state (selection) is unchanged.
- `TestDrainInputWaitsForQueuedKeys`:
  - A `cat` pane (`NewPane(1, []string{"/bin/cat"}, ...)`, started).
  - Queue 50 `SendBytes` chunks, call `drainInput(2*time.Second)`, and assert it returns true and `inputPending == 0`.
  - Then a pane with a nil pty returns true immediately.
- `TestExecHoldsLockAfterDrain`: an `s.execSyscall` override that tries `s.mu.TryLock()`, expects false (still held), and returns an error so the rollback path runs. Afterwards `s.mu` is unlocked, which a `TryLock` + `Unlock` in the test confirms, and `s.upgrading` is false.
- Prove each new test fails first: revert the gate change, and `TestInputDuringUpgradeReachesPtyPane` must time out. Drop the lock-hold, and `TestExecHoldsLockAfterDrain` must fail.

**Verification — automated:**
- [x] `go test ./internal/server -run 'Upgrade|DrainInput|InputDuring' -v -count=1` passes — **9/9 PASS** (incl. existing upgrade tests)
- [x] The new tests fail with the behaviour reverted — **stub drain + old gate: `timed out … data input to reach the child`, `s.mu was not held at exec`; gate opened for all: `resize during upgrade was handled`; decrement removed: `drainInput timed out with 50 events pending`**
- [x] The same run repeats green 4× (`-count=4`) — **ok 0.468s**
- [x] `make quick` passes — **exit 0**
- [x] `go test -race ./internal/server -run 'Upgrade|DrainInput|InputDuring' -count=2` passes — **ok 1.758s**

**Verification — manual:**
- [ ] Deferred to the branch self-review: a real `wideboi upgrade-server` while typing in a pane, with the typed text appearing after the upgrade.

---

## Phase 3: Upgrade reconnect tolerates a slow restore and reports honestly

This phase raises the reconnect handshake ceiling to 60s, keeps the dial at 2s, and reports a handshake timeout as a timeout.

**Files:**
- Modify: `internal/transport/handshake.go`. Split the ceiling out:

  ```go
  // Handshake exchanges hellos with the default HandshakeCeiling.
  func Handshake(conn net.Conn) (Hello, error) { return HandshakeWithin(conn, HandshakeCeiling) }

  // HandshakeWithin is Handshake with an explicit ceiling, for a peer
  // that is known to be alive but may be slow to answer (a server
  // restoring state after an in-place upgrade).
  func HandshakeWithin(conn net.Conn, ceiling time.Duration) (Hello, error) {
  	_ = conn.SetDeadline(time.Now().Add(ceiling))
  	... // existing body unchanged
  }
  ```

- Modify: `internal/commands/commands.go`. Add `HandshakeServerWithin(conn, socket, ceiling)`, which mirrors `HandshakeServer`. Have `HandshakeServer` call it with `transport.HandshakeCeiling`.
- Modify: `cmd/wideboi/handshake.go`. Add a `handshakeServerWithin` wrapper, matching the existing wrapper style.
- Modify: `cmd/wideboi/main.go`, reconnect path (~983-1012):
  - Add `const reconnectHandshakeCeiling = 60 * time.Second`, next to `takenCeiling`/`reapCeiling`. Comment it with the measurement: 12.2s `RestoreState` for 8 panes × 10k lines on 2026-09-26, with the socket bound before restore so a live server accepts the dial at once.
  - Use `handshakeServerWithin(reconnectConn, cfg.Socket, reconnectHandshakeCeiling)`.
  - Set `endReason` by cause:

  ```go
  if err := handshakeServerWithin(reconnectConn, cfg.Socket, reconnectHandshakeCeiling); err != nil {
  	var mm *transport.MismatchError
  	if errors.As(err, &mm) {
  		endReason = "reconnected to an incompatible server"
  	} else {
  		endReason = "reconnected server did not answer: " + err.Error()
  	}
  	return err
  }
  ```

  Check that `DescribeHandshakeErr` wraps with `%w`, so `errors.As` finds the `MismatchError` through `HandshakeError`. Read its `Unwrap`.
- Test: `internal/transport/handshake_test.go`.
- Benchmark: `internal/server/upgrade_bench_test.go`. Add `BenchmarkRestoreStateHeavy`: build one styled 120×40 grid with 10,000 scrollback lines, use its snapshot for 8 panes, write the JSON, then time `RestoreState`. Run it with `-bench RestoreStateHeavy -benchtime 1x`. It records the measurement the ceiling rests on and isn't part of `make quick`.

**Tests (write first):**
- `TestHandshakeWithinTimesOut`: `net.Pipe()` with a peer that never writes. `HandshakeWithin(c, 50*time.Millisecond)` returns an error that is **not** a `*MismatchError` and wraps `os.ErrDeadlineExceeded`.
- `TestHandshakeWithinWaitsForSlowPeer`: the peer sleeps 100ms (a slow peer is the point of this test, not a wait for state), then runs `Handshake`. `HandshakeWithin(c, 2*time.Second)` succeeds, while `HandshakeWithin(c2, 20*time.Millisecond)` against the same slow peer shape fails.
- Error labelling in main.go is a small branch with no existing unit seam. Cover it by asserting `errors.As(commands.HandshakeServerWithin(timeoutConn, "sock", 20ms), &mm)` is **false** for a timeout and true for a version mismatch, in `internal/commands`. That test sits beside any existing commands handshake tests. Check `internal/commands` for a `TestMain` with `testenv.Run`: a test that touches no session doesn't need it, but follow the package's convention.

**Verification — automated:**
- [x] `go test ./internal/transport ./internal/commands -run 'Handshake' -v -count=1` passes — **ok / ok**
- [x] The timeout test fails if `HandshakeWithin` ignores its ceiling — **stub: `took 5.001s, ignoring its 50ms ceiling`; `slow peer under a 20ms ceiling succeeded`**
- [x] `go test ./internal/server -run '^$' -bench RestoreStateHeavy -benchtime 1x` runs — **18.35s/op, 2041 MB** (the spike measured 12.2s; recorded in notes.md)
- [x] `make quick` passes — **exit 0**

**Verification — manual:**
- [x] None beyond the Phase 5 end-to-end check.

---

## Phase 4: Scroll region and SGR pen survive an upgrade (vt fork)

This phase adds accessors to the fork and snapshots and restores the region and pen.

**Files (fork, `/tmp/lmorchard-x/vt`):**
- Modify `emulator.go`:

  ```go
  // ScrollRegion returns the active screen's scroll region (DECSTBM
  // margins). It is the full screen unless the application set one.
  func (e *Emulator) ScrollRegion() uv.Rectangle { return e.scr.ScrollRegion() }

  // CursorPen returns the active screen's current SGR pen: the style
  // applied to text written next.
  func (e *Emulator) CursorPen() uv.Style { return e.scr.cursorPen() }
  ```

- Modify `safe_emulator.go`: add `SafeEmulator` wrappers under `se.mu.RLock()`, matching `IsAltScreen` there.
- Test in `emulator_test.go`:
  - After `CSI 5;20 r`, `ScrollRegion()` has `Min.Y == 4` and `Max.Y == 20`.
  - After `CSI 1;31m`, `CursorPen()` has bold attributes and a red foreground.
  - After `CSI 0m`, the pen is zero.
- Commit on `vt-scrollback-ring`: `feat(vt): expose ScrollRegion and CursorPen on Emulator`. **Do not push.**

**Files (wideboi):**
- Modify `go.mod`: temporarily `replace github.com/charmbracelet/x/vt => /tmp/lmorchard-x/vt`, until the push is approved.
- Modify `internal/server/term/grid.go`:
  - `GridSnapshot` gains `ScrollTop int \`json:"scroll_top,omitempty"\``, `ScrollBottom int \`json:"scroll_bottom,omitempty"\`` and `Pen *protocol.StyleData \`json:"pen,omitempty"\``.
    - `ScrollTop`/`ScrollBottom` are 0-based rows, bottom exclusive, and are only set when the region isn't the full screen.
    - `Pen` is only set when it isn't zero.
  - `ExportSnapshot`:

  ```go
  if r := g.em.ScrollRegion(); r.Min.Y != 0 || r.Max.Y != rows {
  	snap.ScrollTop, snap.ScrollBottom = r.Min.Y, r.Max.Y
  }
  if pen := g.em.CursorPen(); !pen.IsZero() {
  	sd := protocol.EncodeStyle(pen)
  	snap.Pen = &sd
  }
  ```

  - `RestoreSnapshot`: insert between step 2 (screen) and step 3 (cursor):

  ```go
  // 2a. Scroll region before the cursor: DECSTBM homes the cursor.
  if snap.ScrollBottom > 0 {
  	_, _ = fmt.Fprintf(g.em, "\033[%d;%dr", snap.ScrollTop+1, snap.ScrollBottom)
  }
  // 2b. Pen before any fed text; SetCell above does not touch it.
  if snap.Pen != nil {
  	pen := snap.Pen.Decode()
  	_, _ = g.em.Write([]byte(pen.String()))
  }
  ```

  Check that `g.em` satisfies `io.Writer` for `Fprintf`. If it doesn't, use `Write([]byte(fmt.Sprintf(...)))`, as step 3 does.
  - Verify that feeding the later mode sequences (`?1049h` is step 0; mouse, `?2004h`, `?1h` and `?66h` come after) doesn't reset the pen or region in vt. Check with the test below, not by assumption.
- Modify `internal/server/term/grid.go` interface: if `Grid` is used through an interface that lacks the new methods, only `vtGrid` needs them. `g.em` is `*vt.SafeEmulator`, so no interface change is needed.
- Docs: add a note above the `replace` in `go.mod`, and a short entry under LESSONS "Probe the pinned terminal dependencies": the fork now carries `ScrollRegion`/`CursorPen` accessors beyond upstream PRs, so removing the fork needs those upstreamed or replaced.

**Tests (write first):** in `internal/server/term/snapshot_test.go`.
- `TestGridSnapshotScrollRegionRoundTrip`:
  1. Write `CSI 5;20 r`, then a CUP to row 10, col 3.
  2. Export and assert Top=4, Bottom=20.
  3. Restore into a fresh 80×24 grid.
  4. Assert the re-export has the same region **and** the cursor at (2, 9), not homed.
  5. Behaviour: write 30 `\n` at row 19 (0-based) of the restored grid. Row 0 is unchanged and rows inside the region scrolled. Seed distinct text on row 0 and row 4 before exporting.
- `TestGridSnapshotPenRoundTrip`:
  1. Write `CSI 1;31m` (no text after it) and export.
  2. Restore into a fresh grid.
  3. Write `X`. The cell at the cursor has bold and a red foreground (compare `protocol.EncodeStyle(cell.Style)` against the snapshot's pen).
  4. Negative case: a default-pen grid gives a nil `Pen`, and after restore, written text has a zero style.
- `TestGridSnapshotRegionPenWithAltScreen`: alt screen + region + pen together restore all three. This guards the ordering claim.
- Prove they fail: drop the restore lines, and each test fails on its behavioural assertion.

**Verification — automated:**
- [x] `cd /tmp/lmorchard-x/vt && go test ./... -run 'ScrollRegion|CursorPen' -count=1` passes — **PASS; the full vt suite also passes**. Fork commit `7093773`, not pushed.
- [x] `go test ./internal/server/term -run Snapshot -v -count=1` passes — **6/6 PASS**
- [x] The new tests fail with the restore lines removed — **region: `restored region = 0-0`, `row 0 above the region = " "`; pen: style mismatch. With DECSTBM moved after the CUP: `restored cursor = (0,0), want (2,9)`**
- [x] `make quick` passes with the local replace — **exit 0**

**Verification — manual:**
- [x] **Checkpoint — approved and pushed (`7093773`); replace → `v0.0.0-20260927070202-7093773bb668`; `make check` 29 passed.** Original: ask Les to approve pushing the fork commit to `lmorchard/x` `vt-scrollback-ring`.** After the push, point the `replace` at `github.com/lmorchard/x/vt <pseudo-version of the pushed commit>` (resolve it with `go list -m github.com/lmorchard/x/vt@<sha>`), run `go mod tidy`, and confirm `make quick` passes with no local path left in go.mod.

---

## Phase 5: Branch verification and end-to-end check

This phase has no new code. It is the gate before `pr`.

**Verification — automated:**
- [x] `make check` passes (the full gate) — **exit 0, 28 passed 0 failed (local vt replace)**
- [x] `make check` passes again — **exit 0, 28 passed**; `-count=4` upgrade tests **ok**. The phase 2 concurrency change must repeat, per CLAUDE.md "run it four times", so run `go test ./internal/server -run 'Upgrade|DrainInput|InputDuring' -count=4` as well.
- [x] `go test ./cmd/wideboi -run TestUpgradeServerE2E -count=3` passes — **3/3 PASS**

**Verification — manual (by agent, in a scratch session with `-L`, never Les's live one):**
- [x] **Scroll region: scripted as `check-upgrade-region.sh`. It PASSes, and FAILs with the region restore disabled (`output below the region: after2…after10`).** Pen and input-during-upgrade aren't visible to `capture` (text only). They're covered by unit tests and left as manual checks for Les in the PR test plan. Original item: Build a binary. Start `wideboi -L up299`. In a pane, run `printf '\e[5;20r\e[1;31m'; cat`, and in another run `vim` or `less` on a long file. Then `wideboi -L up299 upgrade-server ./bin/wideboi`. Afterwards, typed text in the `cat` pane comes out bold red, and scrolling in less/vim stays inside its region. If this can't be driven headless, record it for Les as a manual check in the PR test plan instead.

---

## Out of scope (from spec)

- `clientSizes`.
- Other emulator state (cursor style, DECSC, origin/autowrap, charsets, tab stops, palette, the primary screen under alt screen).
- Replaying non-input messages.
- A reconnect UI.
- Moving `ListenSocket`.
- Shrinking the snapshot. Filed as #301: 2 GB of JSON, 6.3s encode under `s.mu` and 12.2s restore.
- Raising the dial ceiling.
- Upstreaming the accessors.
