# wideboi Plan 2 — Testing Infrastructure and Screen Reflow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a wire-level acceptance harness, then make pane resize non-destructive by reflowing the visible screen inside our own emulator adapter.

**Architecture:** Two halves that do not touch each other. First, testing infrastructure that asserts on the bytes wideboi writes to the host pty rather than on our internal cell buffer — the layer where Plan 1's two escaped defects were invisible. Second, an adapter-level reflow: on `Grid.Resize`, read the screen's cells out, rejoin soft-wrapped runs, resize, re-split at the new width, and write back with `SetCell`. Scrollback is deliberately untouched.

**Tech Stack:** Go 1.27.1 · `charmbracelet/x/vt` (pinned) · `charmbracelet/ultraviolet` · Python 3 stdlib (`pty`, `termios`) for the harness

**Spec:** `docs/dev-sessions/2026-09-18-wideboi-plan-2-layout-seam/spec.md`

**Parent spec:** `../2026-09-18-wideboi-v1-foundations/spec.md` — still the binding architecture document.

## Scope, and why the seam is not in this plan

The Plan 2 spec covers milestones 5–7 plus reflow plus testing. That is five subsystems; Plan 1 needed six fix rounds for less. This plan takes the first two:

| Plan | Scope | Deliverable |
| --- | --- | --- |
| **2 (this)** | Testing infrastructure, screen reflow | Resize stops destroying text; a harness that asserts on the wire |
| **3** | Client/server seam, then the layout core | Scrolling columns behind a transport boundary |
| **4** | Input routing, `$mod` keybindings, the verb set | The real multiplexer |

The seam is deferred **exactly one plan, and no further.** The parent spec warns that "collapsing a seam is easy and cutting one into a monolith is not," and Plan 1's spec said milestone 6 must not slip past 7. The cost of cutting a seam scales with how many places touch pane state — and **nothing in this plan adds a consumer of pane state.** Reflow lives inside the `Grid` adapter; the harness is an external process. Layout wiring is what would add consumers, which is why layout sits behind the seam in Plan 3 rather than ahead of it.

If Plan 3 is tempted to add layout before the seam, that is the mistake this table exists to prevent.

## Global Constraints

- Module path: `github.com/lmorchard/wideboi`. Go 1.27.1.
- Platforms: macOS and Linux only. No Windows, no build tags for it.
- `github.com/charmbracelet/x/vt` is pinned to `v0.0.0-20260913004009-c615ff2f7805`. It has no tagged release; never `@latest`. Do not move it.
- Geometry parameters are declared as stdlib `image.Rectangle` / `image.Point`. `uv.Rectangle` and `uv.Position` are type aliases for these.
- Every exit path must restore the host terminal and reap child processes — panic, signal, early return.
- `internal/client` must not import `internal/server`. `make seam-check` enforces this with three Plan 1 temporaries allowlisted; **do not add a fourth**.
- `make check` must not modify the working tree. Verify with `git status` after running it.
- Each task ends with a commit.

### Verified facts — trust these over intuition

Established empirically against the pinned libraries. Re-deriving them wastes time:

- **`uv.Buffer.Resize` truncates**: narrowing does `b.Lines[i] = b.Lines[i][:width]`; widening appends blanks. Text is destroyed.
- **`type Line []Cell`** — a bare slice. There is **no soft-wrap metadata anywhere**, which is why correct reflow is impossible and a heuristic is required.
- **`Resize` touches only the visible screen.** Scrollback (`ScrollbackCellAt`, `ScrollbackLen`) is left intact at its original width. Measured directly.
- **`Emulator.Draw` paints only `Touched()` lines**, and `Screen.Resize` clears `Touched`, so a pane renders blank after a resize. Writing cells back with `SetCell` re-touches them, which fixes this as a side effect.
- **Writing a wide glyph's placeholder cell explicitly blanks the whole glyph** — it trips `uv.Line.Set`'s partial-overwrite protection. Advance by each cell's own `Width`; never write placeholder slots.
- **There is no public cursor setter.** `setCursor` is unexported. A CUP escape through `Write` is the only route, and it must land before real PTY output resumes.
- `IsAltScreen()` is available and reliable.
- Reflow costs: **0.83 ms** for a 24-row screen; 177 ms if scrollback is rebuilt (which this plan does not do).

### Reference prototype

`/tmp/reflow-spike/main.go` is a **throwaway spike**, not production code and not a template to copy. It proves the mechanism and contains the wide-glyph fix. Read it for orientation; do not lift its structure. If it is gone, the facts above are sufficient.

---

### Task 1: Extract the pty plumbing into a shared module

`scripts/ptycheck.py` is 498 lines and owns both the pty mechanics and the exit-contract assertions. Task 2 needs the mechanics without the assertions. Extract first, so Task 2's diff is new behaviour rather than a move plus new behaviour tangled together.

**Files:**
- Create: `scripts/ptylib.py`
- Modify: `scripts/ptycheck.py`

**Interfaces:**
- Consumes: nothing.
- Produces, in `scripts/ptylib.py`:
  - `ALT_SCREEN_ENTER: bytes` (`b"\x1b[?1049h"`) and `ALT_SCREEN_EXIT: bytes` (`b"\x1b[?1049l"`)
  - `spawn_in_pty(argv: list[str], cols: int, rows: int, set_winsize: bool) -> tuple[int, int]` — returns `(pid, master_fd)`
  - `Drainer` — a class wrapping the background read of the master. `Drainer(master_fd)`, `.start()`, `.stop()`, `.output() -> bytes` (everything read so far), `.saw(needle: bytes) -> bool`
  - `ps_rows() -> list[tuple[int, int, str]]`, `descendants(pid) -> list[tuple[int, str]]`, `pane_children(pid) -> list[tuple[int, str]]`, `still_alive(pids, within) -> list[int]`
  - `wait_for_exit(pid: int, timeout: float) -> int | None`, `force_cleanup(pid: int) -> None`
  - `parse_size(text) -> tuple[int, int]`, `parse_signal(text) -> int`

`Drainer` replaces `ptycheck.py`'s free-function `drain` plus its two `threading.Event`s. The events are an awkward interface for Task 2, which needs to read accumulated output at arbitrary points, not just test one flag.

- [ ] **Step 1: Read the existing implementation**

Run: `sed -n '60,350p' scripts/ptycheck.py`

Note how `drain`, `spawn_in_pty` and the `ps`-based helpers work. This task moves them; it does not redesign them beyond the `Drainer` wrapper.

- [ ] **Step 2: Create `scripts/ptylib.py`**

Move the listed functions verbatim, then wrap the drain logic in `Drainer`:

```python
#!/usr/bin/env python3
"""Shared pty plumbing for wideboi's out-of-process checks.

Not a test in itself. scripts/ptycheck.py asserts the signal-exit contract
with it; scripts/smoke.py drives user journeys with it.

The one rule that matters here: ALWAYS drain the master. wideboi's render
loop writes frames to the pty, and a full buffer blocks that write, which
looks exactly like a hang in the thing under test.
"""

import os
import threading

ALT_SCREEN_ENTER = b"\x1b[?1049h"
ALT_SCREEN_EXIT = b"\x1b[?1049l"


class Drainer:
    """Continuously reads a pty master on a background thread.

    Accumulates everything read so callers can assert against the whole
    stream after the fact, not only against a flag sampled live.
    """

    def __init__(self, master_fd: int) -> None:
        self._fd = master_fd
        self._buf = bytearray()
        self._lock = threading.Lock()
        self._stop = threading.Event()
        self._thread: threading.Thread | None = None

    def start(self) -> None:
        self._thread = threading.Thread(target=self._run, daemon=True)
        self._thread.start()

    def _run(self) -> None:
        while not self._stop.is_set():
            try:
                data = os.read(self._fd, 65536)
            except OSError:
                return
            if not data:
                return
            with self._lock:
                self._buf.extend(data)

    def stop(self) -> None:
        self._stop.set()
        if self._thread is not None:
            self._thread.join(timeout=1.0)

    def output(self) -> bytes:
        with self._lock:
            return bytes(self._buf)

    def saw(self, needle: bytes) -> bool:
        return needle in self.output()
```

Then append the moved functions — `spawn_in_pty`, `ps_rows`, `descendants`, `pane_children`, `still_alive`, `wait_for_exit`, `force_cleanup`, `parse_size`, `parse_signal` — unchanged from `ptycheck.py`.

Accumulating the whole stream removes the carry-buffer boundary bug the previous drain worked around. Do not carry that logic over.

- [ ] **Step 3: Rewrite `ptycheck.py` to use it**

Delete the moved definitions from `ptycheck.py` and add `from ptylib import ...` at the top, plus the `sys.path` line so it resolves when run from the repo root:

```python
import sys, os
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptylib import (
    ALT_SCREEN_EXIT, Drainer, spawn_in_pty, pane_children, still_alive,
    wait_for_exit, force_cleanup, parse_size, parse_signal,
)
```

Replace the drain-thread setup in `run_check` with a `Drainer`, and replace the `saw_restore` event check with `drainer.saw(ALT_SCREEN_EXIT)`.

**This is a refactor. Assertions and output text must not change.**

- [ ] **Step 4: Verify behaviour is identical**

Run: `make verify-exit`
Expected: the same 6 cases × 3 assertions, 18 OK / 0 FAIL, same wording as before.

Then confirm the failure path still works:

Run: `python3 scripts/ptycheck.py --binary /bin/cat --signal SIGTERM`
Expected: assertion 1 passes (the kernel's default disposition kills `/bin/cat`), assertion 2 **fails** — no alt-screen exit. Non-zero exit. This is the documented weakness of assertion 1 and must survive the refactor.

- [ ] **Step 5: Commit**

```bash
git add scripts/ptylib.py scripts/ptycheck.py
git commit -m "refactor(scripts): extract shared pty plumbing into ptylib

Task 2's smoke runner needs the mechanics without the exit-contract
assertions. Drainer replaces the free-function drain plus its events,
accumulating the whole stream so callers can assert after the fact."
```

---

### Task 2: `make smoke` — a scripted acceptance runner

The harness that asserts on the wire. Its cases come from the spec's user-journey list, and every future feature lands with a case here.

**Files:**
- Create: `scripts/smoke.py`
- Modify: `Makefile`

**Interfaces:**
- Consumes: everything `scripts/ptylib.py` produces (Task 1).
- Produces: `make smoke`, and a `Case` structure other tasks extend.

- [ ] **Step 1: Write `scripts/smoke.py`**

```python
#!/usr/bin/env python3
"""Scripted acceptance checks for wideboi, asserted on the wire.

Every assertion here is about the bytes wideboi writes to the host pty.
That is deliberate. Plan 1's rendering tests asserted against our own
cell buffer, and two user-visible defects hid there: the cursor is not a
cell, so its absence was unassertable, and no test sent a keystroke far
enough to notice that every shifted key was dropped.

Cases derive from the spec's user-journey list. Add one per feature.
"""

import argparse
import os
import re
import signal
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptylib import (
    ALT_SCREEN_ENTER, ALT_SCREEN_EXIT, Drainer, spawn_in_pty,
    wait_for_exit, force_cleanup, pane_children, still_alive,
)

CUP = re.compile(rb"\x1b\[(\d+);(\d+)H")


class Session:
    """A running wideboi in a pty, with helpers to type and observe."""

    def __init__(self, cols=100, rows=30, startup=1.2):
        self.pid, self.fd = spawn_in_pty(["./bin/wideboi"], cols, rows, True)
        self.drainer = Drainer(self.fd)
        self.drainer.start()
        time.sleep(startup)

    def type(self, text: str, settle=0.8):
        os.write(self.fd, text.encode())
        time.sleep(settle)

    def output(self) -> bytes:
        return self.drainer.output()

    def cursor_positions(self) -> list[tuple[int, int]]:
        return [(int(r), int(c)) for r, c in CUP.findall(self.output())]

    def quit_and_reap(self, sig=signal.SIGTERM, timeout=8.0) -> int | None:
        kids = [p for p, _ in pane_children(self.pid)]
        os.kill(self.pid, sig)
        status = wait_for_exit(self.pid, timeout)
        if status is None:
            force_cleanup(self.pid)
        self.drainer.stop()
        self.leaked = still_alive(kids, 2.0)
        return status


def case_launch_shows_two_panes(fail):
    s = Session()
    out = s.output()
    if ALT_SCREEN_ENTER not in out:
        fail("never entered the alt screen")
    if b"\u2502" not in out:
        fail("no column divider in the first frames")
    if not s.cursor_positions():
        fail("no cursor positioning emitted -- is the cursor being rendered?")
    s.quit_and_reap()


def case_typing_reaches_the_focused_pane(fail):
    s = Session()
    s.type("echo smoke-lower-42\r")
    if b"smoke-lower-42" not in s.output():
        fail("lowercase input never reached the pane")
    s.quit_and_reap()


def case_shifted_keys_reach_the_pane(fail):
    # The defect that shipped in Plan 1: every ModShift key was dropped.
    s = Session()
    s.type("echo SMOKE_CAPS '!bang'\r")
    out = s.output()
    if b"SMOKE_CAPS" not in out:
        fail("capital letters never reached the pane")
    if b"!bang" not in out:
        fail("shifted punctuation never reached the pane")
    s.quit_and_reap()


def case_focus_switch_moves_the_cursor(fail):
    s = Session()
    s.type("echo pane-zero\r")
    before = s.cursor_positions()[-1] if s.cursor_positions() else None
    s.type("\x0f")  # ctrl+o
    s.type("echo pane-one\r")
    after = s.cursor_positions()[-1] if s.cursor_positions() else None
    if before is None or after is None:
        fail("no cursor positions observed around a focus switch")
    if before[1] == after[1]:
        fail(f"cursor column did not move across panes: {before} -> {after}")
    if b"pane-one" not in s.output():
        fail("input did not follow focus to the second pane")
    s.quit_and_reap()


def case_quit_restores_and_reaps(fail):
    s = Session()
    s.type("echo before-quit\r")
    status = s.quit_and_reap()
    if status is None:
        fail("did not exit after a signal")
    if not os.WIFSIGNALED(status):
        fail(f"exited normally (code {os.WEXITSTATUS(status)}) instead of by signal")
    if not s.drainer.saw(ALT_SCREEN_EXIT):
        fail("never left the alt screen -- the terminal would be stranded")
    if s.leaked:
        fail(f"leaked pane processes: {s.leaked}")


CASES = [
    ("launch shows two panes and a cursor", case_launch_shows_two_panes),
    ("typing reaches the focused pane", case_typing_reaches_the_focused_pane),
    ("shifted keys reach the pane", case_shifted_keys_reach_the_pane),
    ("focus switch moves the cursor", case_focus_switch_moves_the_cursor),
    ("quit restores the terminal and reaps", case_quit_restores_and_reaps),
]


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--only", help="substring filter on case names")
    args = ap.parse_args()

    failures = []
    for name, fn in CASES:
        if args.only and args.only not in name:
            continue
        problems = []
        try:
            fn(problems.append)
        except Exception as exc:  # a crashed case is a failed case
            problems.append(f"raised {exc!r}")
        if problems:
            failures.append((name, problems))
            print(f"FAIL  {name}")
            for p in problems:
                print(f"        {p}")
        else:
            print(f"OK    {name}")

    print(f"\n{len(CASES) - len(failures)} passed, {len(failures)} failed")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
```

- [ ] **Step 2: Verify every case can fail**

A case that has never been seen red is not a check. For each of the five, break the thing it guards, observe FAIL, restore, observe OK. The cheapest breakages:

| Case | Break it by |
| --- | --- |
| launch shows two panes and a cursor | comment out the `scr.SetCursorPosition`/`ShowCursor` call in `cmd/wideboi/main.go` |
| typing reaches the focused pane | make `Pane.SendKey` return immediately without forwarding |
| shifted keys reach the pane | in `vtGrid`, route everything through `SendKey` (the pre-fix behaviour) |
| focus switch moves the cursor | make the `ctrl+o` case a no-op |
| quit restores the terminal and reaps | delete `closePanes` from the guard closure |

Record the observed FAIL output for each in your report. **Restore every breakage** and confirm `git diff` is empty before continuing.

- [ ] **Step 3: Wire it into the Makefile**

```makefile
# Scripted acceptance checks, asserted on the bytes wideboi writes to the
# pty. See scripts/smoke.py for why the wire is the right layer.
smoke: build
	python3 scripts/smoke.py
```

Add `smoke` to `check`'s prerequisites, after `verify-exit`.

- [ ] **Step 4: Run the gate**

Run: `make check`
Expected: green, including `5 passed, 0 failed` from smoke.

Run: `git status --porcelain`
Expected: empty. `check` must not modify the tree.

- [ ] **Step 5: Commit**

```bash
git add scripts/smoke.py Makefile
git commit -m "test(smoke): scripted acceptance checks asserted on the wire

Five cases from the spec's user-journey list, each verified red by
breaking what it guards. Two of them cover defects that shipped in
Plan 1 and were invisible to cell-buffer assertions: dropped shifted
keys, and no cursor."
```

---

### Task 3: Exhaustive key-encoding coverage

Plan 1's key table had seven hand-picked cases and the bug was in case eight. A table test can only fail on rows you wrote. The input space here is small and finite, so enumerate it rather than sampling.

**Files:**
- Modify: `internal/server/term/grid_test.go`

**Interfaces:**
- Consumes: `term.NewVT(cols, rows int) Grid`, `Grid.SendKey`, `Grid.SendText`, `Grid.Read`.
- Produces: no new API.

- [ ] **Step 1: Write the failing test**

Append to `internal/server/term/grid_test.go`:

```go
// drainOne starts a reader and returns the first chunk SendKey produces,
// or "" if nothing arrives. SendKey blocks on an io.Pipe until something
// reads, so the reader must exist before the send.
func drainOne(t *testing.T, g term.Grid, send func()) string {
	t.Helper()
	out := make(chan string, 1)
	go func() {
		buf := make([]byte, 64)
		n, err := g.Read(buf)
		if n > 0 {
			out <- string(buf[:n])
			return
		}
		if err != nil {
			out <- ""
		}
	}()
	send()
	select {
	case got := <-out:
		return got
	case <-time.After(2 * time.Second):
		return ""
	}
}

// Every printable character must reach the child, with or without shift.
// This is an exhaustive sweep of a small finite space, not a sample: the
// defect that shipped in Plan 1 was that ModShift produced no bytes at
// all, and a seven-row table missed it.
func TestEveryPrintableKeyProducesBytes(t *testing.T) {
	for r := rune(0x20); r <= rune(0x7e); r++ {
		for _, mod := range []uv.KeyMod{0, uv.ModShift} {
			name := fmt.Sprintf("%q_mod%d", r, mod)
			t.Run(name, func(t *testing.T) {
				g := term.NewVT(20, 3)
				defer g.Close()
				k := uv.KeyPressEvent{Code: r, Text: string(r), Mod: mod}
				got := drainOne(t, g, func() { g.SendKey(uv.KeyEvent(k)) })
				if got == "" {
					t.Fatalf("no bytes produced for %q with mod %d", r, mod)
				}
				if got != string(r) {
					t.Errorf("got %q, want %q", got, string(r))
				}
			})
		}
	}
}

// For printable input the encode path must be the identity function.
// This needs no enumeration of cases and catches future encoding gaps
// that no hand-written table would anticipate.
func TestPrintableInputRoundTrips(t *testing.T) {
	const sample = "The Quick Brown Fox! @#$%^&*() 0123456789 {}[]|\\;:'\",.<>/?"
	for _, r := range sample {
		g := term.NewVT(20, 3)
		k := uv.KeyPressEvent{Code: r, Text: string(r)}
		got := drainOne(t, g, func() { g.SendKey(uv.KeyEvent(k)) })
		if got != string(r) {
			t.Errorf("round trip failed for %q: got %q", r, got)
		}
		g.Close()
	}
}
```

Add `"fmt"` and `"time"` to the test file's imports if not already present.

- [ ] **Step 2: Run to see the current state**

Run: `go test ./internal/server/term/ -run 'TestEveryPrintableKey|TestPrintableInputRoundTrips' 2>&1 | tail -20`

Expected: **PASS.** The shift fix in `9bd3139` already routes printable text through `SendText`, so these should be green on arrival. That is the point — they are the regression net that fix lacked.

If anything fails, do not weaken the assertion. Report which characters fail and what they produce; an unexpected red here is a real finding.

- [ ] **Step 3: Prove the net catches the original defect**

Temporarily change `vtGrid.SendKey` to call `g.em.SendKey(k)` unconditionally (the pre-fix behaviour).

Run: `go test ./internal/server/term/ -run TestEveryPrintableKey 2>&1 | tail -20`
Expected: ~95 subtests FAIL with "no bytes produced ... with mod 1".

Restore the fix, re-run, confirm green, and confirm `git diff internal/server/term/grid.go` is empty.

- [ ] **Step 4: Commit**

```bash
git add internal/server/term/grid_test.go
git commit -m "test(term): exhaustive key coverage and a round-trip property

Enumerates printable ASCII x shift rather than sampling seven cases, and
adds identity round-tripping which needs no enumeration at all. Verified
to catch the ModShift defect that shipped in Plan 1."
```

---

### Task 4: Golden wire snapshot

Table tests cannot catch omitted behaviour — you cannot write a case for a feature you forgot. A committed snapshot of the actual byte stream can, because a human reads it once and notices what is missing.

**Files:**
- Create: `scripts/golden.py`, `testdata/golden/startup.txt`
- Modify: `Makefile`

**Interfaces:**
- Consumes: `scripts/ptylib.py`.
- Produces: `make golden` (regenerate) and a `golden` case inside `make smoke`.

- [ ] **Step 1: Write the snapshotter**

```python
#!/usr/bin/env python3
"""Capture and compare a golden snapshot of wideboi's wire output.

Table tests cannot fail for behaviour nobody implemented. A committed
transcript can: a reader notices what is absent, and afterwards every
change shows as a diff.

Escape sequences are decoded to readable names so the golden file is
reviewable by a human rather than a wall of hex.
"""

import argparse
import os
import re
import signal
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptylib import Drainer, spawn_in_pty, wait_for_exit, force_cleanup

GOLDEN = os.path.join("testdata", "golden", "startup.txt")

NAMES = [
    (rb"\x1b\[\?1049h", "ALT_SCREEN_ENTER"),
    (rb"\x1b\[\?1049l", "ALT_SCREEN_EXIT"),
    (rb"\x1b\[\?25h", "CURSOR_SHOW"),
    (rb"\x1b\[\?25l", "CURSOR_HIDE"),
    (rb"\x1b\[(\d+);(\d+)H", "CURSOR_TO(r,c)"),
    (rb"\x1b\[2J", "CLEAR_SCREEN"),
    (rb"\x1b\[0?m", "SGR_RESET"),
    (rb"\x1b\[\?2026h", "SYNC_BEGIN"),
    (rb"\x1b\[\?2026l", "SYNC_END"),
]


def summarize(raw: bytes) -> str:
    """Reduce a byte stream to an ordered, stable inventory of what it did."""
    seen = []
    for pattern, name in NAMES:
        count = len(re.findall(pattern, raw))
        if count:
            seen.append(f"{name}: {count}")
    printable = re.sub(rb"\x1b\[[0-9;?]*[a-zA-Z]", b"", raw)
    printable = printable.replace(b"\r", b"").replace(b"\x1b", b"")
    text = printable.decode("utf-8", "replace")
    words = sorted({w for w in re.findall(r"[A-Za-z0-9_+-]{3,}", text)})
    return (
        "# wideboi startup wire snapshot\n"
        "# Regenerate: make golden. Review diffs; do not blind-accept.\n"
        "\n## escape sequences\n" + "\n".join(seen) +
        "\n\n## words rendered\n" + "\n".join(words) + "\n"
    )


def capture() -> str:
    pid, fd = spawn_in_pty(["./bin/wideboi"], 100, 30, True)
    d = Drainer(fd)
    d.start()
    time.sleep(1.5)
    os.kill(pid, signal.SIGTERM)
    if wait_for_exit(pid, 8.0) is None:
        force_cleanup(pid)
    d.stop()
    return summarize(d.output())


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--update", action="store_true", help="rewrite the golden file")
    args = ap.parse_args()

    got = capture()
    if args.update:
        os.makedirs(os.path.dirname(GOLDEN), exist_ok=True)
        with open(GOLDEN, "w") as f:
            f.write(got)
        print(f"wrote {GOLDEN}")
        return 0

    if not os.path.exists(GOLDEN):
        print(f"FAIL: {GOLDEN} missing. Run: make golden")
        return 1
    with open(GOLDEN) as f:
        want = f.read()
    if got != want:
        print("FAIL: wire output differs from the golden snapshot.")
        import difflib
        for line in difflib.unified_diff(
            want.splitlines(), got.splitlines(),
            fromfile="golden", tofile="actual", lineterm="",
        ):
            print("  " + line)
        print("\nIf the change is intended: make golden")
        return 1
    print("OK    wire output matches the golden snapshot")
    return 0


if __name__ == "__main__":
    sys.exit(main())
```

The snapshot is a *summary*, not raw bytes: sequence counts plus the set of words rendered. Raw bytes would churn on every timing difference and nobody would read the diff.

- [ ] **Step 2: Generate and read it**

```bash
make build
python3 scripts/golden.py --update
cat testdata/golden/startup.txt
```

**Read the file.** This is the step that has value — it is the one moment where absent behaviour is visible. Confirm it shows alt-screen enter and exit, cursor show, cursor positioning, and the words from the status line. Note anything conspicuously missing in your report.

- [ ] **Step 3: Verify it detects a change**

Temporarily comment out the `ShowCursor` call in `cmd/wideboi/main.go`, rebuild, and run `python3 scripts/golden.py`.
Expected: FAIL with `CURSOR_SHOW` missing from the diff.

Restore, rebuild, re-run, confirm OK and `git diff cmd/` is empty.

- [ ] **Step 4: Wire into the Makefile**

```makefile
# Regenerate the golden wire snapshot. Review the diff before committing.
golden: build
	python3 scripts/golden.py --update
```

Add a golden comparison to `smoke`:

```makefile
smoke: build
	python3 scripts/smoke.py
	python3 scripts/golden.py
```

- [ ] **Step 5: Run the gate and commit**

```bash
make check
git status --porcelain   # must be empty
git add scripts/golden.py testdata/golden/startup.txt Makefile
git commit -m "test(golden): committed snapshot of wideboi's wire output

Catches omitted behaviour, which table tests structurally cannot: no
test could fail for the missing cursor in Plan 1 because the cursor is
not a cell. A summary rather than raw bytes, so the diff is reviewable."
```

---

### Task 5: The reflow algorithm, as a pure function

The whole algorithm, with no emulator involved — pure input to output, so every edge case is a cheap unit test. Task 6 wires it in.

**Files:**
- Create: `internal/server/term/reflow.go`, `internal/server/term/reflow_unit_test.go`

**Interfaces:**
- Consumes: `uv.Cell`.
- Produces:
  - `type Row []*uv.Cell` — one screen row; `nil` entries are empty cells.
  - `func Reflow(rows []Row, oldWidth, newWidth int) []Row` — rejoins soft-wrapped runs and re-splits at `newWidth`. Returns exactly `len(rows)` rows, each `newWidth` wide, bottom-anchored (content that no longer fits scrolls off the *top*, matching terminal behaviour).

**The used-vs-filler rule, which is the whole difficulty.** The spike's naive version treated a blank row as a full-width logical line and pushed live content off the screen, silently. The rule:

- A row's **used width** is one past its last non-empty cell; a wholly empty row has used width 0.
- A row **continues** into the next only if its used width equals `oldWidth` **and** the next row has used width greater than 0.
- Trailing wholly-empty rows are **filler**, not content. They are dropped before reflow and regenerated after.

- [ ] **Step 1: Write the failing tests**

```go
package term

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// rowsFrom builds Rows from strings, padding each to width with nil.
func rowsFrom(width int, lines ...string) []Row {
	out := make([]Row, len(lines))
	for i, s := range lines {
		r := make(Row, width)
		for x, ch := range []rune(s) {
			if x >= width {
				break
			}
			if ch != ' ' {
				r[x] = &uv.Cell{Content: string(ch), Width: 1}
			}
		}
		out[i] = r
	}
	return out
}

func render(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		var b strings.Builder
		for _, c := range r {
			if c == nil || c.Content == "" {
				b.WriteString(" ")
			} else {
				b.WriteString(c.Content)
			}
		}
		out[i] = strings.TrimRight(b.String(), " ")
	}
	return out
}

func assertRows(t *testing.T, got []Row, want []string) {
	t.Helper()
	g := render(got)
	if len(g) != len(want) {
		t.Fatalf("got %d rows, want %d:\n  got  %q\n  want %q", len(g), len(want), g, want)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Errorf("row %d:\n  got  %q\n  want %q", i, g[i], want[i])
		}
	}
}

func TestReflowNarrowWrapsRatherThanTruncating(t *testing.T) {
	in := rowsFrom(10, "abcdefghij", "klmno", "", "")
	got := Reflow(in, 10, 5)
	assertRows(t, got, []string{"abcde", "fghij", "klmno", ""})
}

func TestReflowWidenRejoins(t *testing.T) {
	in := rowsFrom(5, "abcde", "fghij", "klmno", "")
	got := Reflow(in, 5, 10)
	assertRows(t, got, []string{"abcdefghij", "klmno", "", ""})
}

func TestReflowRoundTripRecoversText(t *testing.T) {
	in := rowsFrom(20, "abcdefghijklmnopqrst", "", "", "")
	got := Reflow(Reflow(in, 20, 10), 10, 20)
	assertRows(t, got, []string{"abcdefghijklmnopqrst", "", "", ""})
}

// The regression for the spike's worst finding: a blank row between
// content must not be priced as a full-width logical line, and live
// content must not be pushed off the top.
func TestReflowDoesNotEvictLiveContentViaBlankRows(t *testing.T) {
	in := rowsFrom(20, "hello", "", "world", "")
	got := Reflow(in, 20, 10)
	assertRows(t, got, []string{"hello", "", "world", ""})
}

func TestReflowKeepsShortLinesIntact(t *testing.T) {
	in := rowsFrom(20, "one", "two", "three", "")
	got := Reflow(in, 20, 10)
	assertRows(t, got, []string{"one", "two", "three", ""})
}

// A hard break landing at exactly the old width is indistinguishable
// from a soft wrap without metadata the library does not keep. This
// test PINS the accepted wrong answer so a future change is a decision
// rather than an accident.
func TestReflowJoinsHardBreakAtExactWidth_AcceptedLimitation(t *testing.T) {
	in := rowsFrom(5, "abcde", "fgh", "", "")
	got := Reflow(in, 5, 10)
	assertRows(t, got, []string{"abcdefgh", "", "", ""})
}

// Content that no longer fits scrolls off the TOP, as a terminal does.
func TestReflowOverflowDropsFromTheTop(t *testing.T) {
	in := rowsFrom(10, "aaaaaaaaaa", "bbbbbbbbbb", "cc", "")
	got := Reflow(in, 10, 5)
	if len(got) != 4 {
		t.Fatalf("got %d rows, want 4", len(got))
	}
	last := render(got)[3]
	if last != "cc" {
		t.Errorf("bottom row = %q, want %q -- overflow must drop from the top", last, "cc")
	}
}

func TestReflowPreservesStyleAndWideGlyphs(t *testing.T) {
	in := make([]Row, 2)
	in[0] = make(Row, 6)
	in[0][0] = &uv.Cell{Content: "中", Width: 2}
	in[0][2] = &uv.Cell{Content: "x", Width: 1, Style: uv.Style{Attrs: uv.AttrBold}}
	in[1] = make(Row, 6)

	got := Reflow(in, 6, 8)
	if got[0][0] == nil || got[0][0].Content != "中" || got[0][0].Width != 2 {
		t.Errorf("wide glyph not preserved: %+v", got[0][0])
	}
	if got[0][2] == nil || got[0][2].Style.Attrs != uv.AttrBold {
		t.Errorf("style not preserved: %+v", got[0][2])
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/server/term/ -run TestReflow 2>&1 | tail -10`
Expected: build failure — `undefined: Reflow`, `undefined: Row`.

- [ ] **Step 3: Implement**

Create `internal/server/term/reflow.go`:

```go
package term

import uv "github.com/charmbracelet/ultraviolet"

// Row is one row of screen cells. A nil entry is an empty cell.
type Row []*uv.Cell

// usedWidth returns one past the last non-empty cell, or 0 if the row is
// wholly empty. It is what distinguishes real content from the blank
// remainder of the screen, and getting it wrong evicts live content.
func usedWidth(r Row) int {
	for x := len(r) - 1; x >= 0; x-- {
		if c := r[x]; c != nil && c.Content != "" && c.Content != " " {
			return x + 1
		}
	}
	return 0
}

// Reflow rejoins soft-wrapped runs and re-splits them at newWidth.
//
// Soft wraps are not recorded anywhere in the cell data, so this uses the
// standard heuristic: a row that filled its full width, and is followed by
// a row with content, probably wrapped. A hard line break landing at
// exactly oldWidth is therefore joined with the line below it. That is
// wrong, accepted, and pinned by a test.
//
// The result always has len(rows) rows of newWidth each. Content that no
// longer fits is dropped from the TOP, as a terminal scrolls.
func Reflow(rows []Row, oldWidth, newWidth int) []Row {
	height := len(rows)
	if height == 0 || newWidth <= 0 {
		return rows
	}

	// Drop trailing blank rows: they are the unused remainder of the
	// screen, not content, and treating them as content is what pushed
	// live text off the top in the spike.
	content := height
	for content > 0 && usedWidth(rows[content-1]) == 0 {
		content--
	}

	// Rejoin wrapped runs into logical lines.
	var logical []Row
	var cur Row
	for y := 0; y < content; y++ {
		u := usedWidth(rows[y])
		cur = append(cur, rows[y][:u]...)
		continues := u == oldWidth && y+1 < content && usedWidth(rows[y+1]) > 0
		if !continues {
			logical = append(logical, cur)
			cur = nil
		}
	}
	if cur != nil {
		logical = append(logical, cur)
	}

	// Re-split at the new width, advancing by each cell's own Width so a
	// wide glyph is never severed from its placeholder slot.
	var out []Row
	for _, line := range logical {
		if len(line) == 0 {
			out = append(out, make(Row, newWidth))
			continue
		}
		row := make(Row, newWidth)
		x := 0
		for i := 0; i < len(line); {
			c := line[i]
			w := 1
			if c != nil && c.Width > 1 {
				w = c.Width
			}
			if x+w > newWidth {
				out = append(out, row)
				row = make(Row, newWidth)
				x = 0
			}
			row[x] = c
			x += w
			i += w
		}
		out = append(out, row)
	}

	// Pad or trim to the original height, dropping from the top.
	for len(out) < height {
		out = append(out, make(Row, newWidth))
	}
	if len(out) > height {
		out = out[len(out)-height:]
	}
	return out
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/server/term/ -run TestReflow -v 2>&1 | tail -25`
Expected: all eight PASS.

If `TestReflowDoesNotEvictLiveContentViaBlankRows` fails, the used-vs-filler logic is wrong — that is the case the spike got wrong, and it is the one that matters most. Debug it rather than adjusting the expectation.

- [ ] **Step 5: Commit**

```bash
git add internal/server/term/reflow.go internal/server/term/reflow_unit_test.go
git commit -m "feat(term): reflow algorithm as a pure function

Rejoins soft-wrapped runs and re-splits at a new width, using the
full-width heuristic since the cell data records no wrap state. The
used-vs-filler row rule is load bearing: the spike's naive version
priced blank rows as full-width logical lines and silently pushed live
content off the screen."
```

---

### Task 6: Wire reflow into `Grid.Resize`

**Files:**
- Modify: `internal/server/term/grid.go`, `internal/server/term/reflow_test.go`
- Modify: `docs/dev-sessions/2026-09-18-wideboi-v1-foundations/spec.md`

**Interfaces:**
- Consumes: `Reflow(rows []Row, oldWidth, newWidth int) []Row`, `Row` (Task 5).
- Produces: `Grid.Resize` becomes non-destructive on the primary screen. No signature change.

- [ ] **Step 1: Un-skip the existing tests**

`internal/server/term/reflow_test.go` holds two `t.Skip`'d tests written in Plan 1 to document the defect. Remove both `t.Skip` lines and update the comments to say these now assert required behaviour rather than a documented defect.

Run: `go test ./internal/server/term/ -run 'TestReflowRecovers|TestNarrowWraps' 2>&1 | tail -10`
Expected: both FAIL — the defect is still present because `Resize` passes straight through.

- [ ] **Step 2: Implement**

Replace `vtGrid.Resize` in `internal/server/term/grid.go`:

```go
// Resize changes the emulator's dimensions, reflowing the visible screen
// so narrowing does not destroy text.
//
// The pinned x/vt truncates on narrow (uv.Buffer.Resize does
// Lines[i][:width]) and its Draw paints only Touched lines while
// Screen.Resize clears Touched, so a plain resize both loses text and
// renders blank. Reading the cells out, reflowing, and writing them back
// with SetCell fixes both: SetCell re-touches every line it writes.
//
// Deliberately NOT handled:
//
//   - Scrollback. Measured: Resize leaves it untouched at its original
//     width, so there is nothing to repair. It will need reflowing for
//     DISPLAY when scroll-back navigation lands, which is a presentation
//     concern and non-destructive to defer.
//   - The alternate screen. A full-screen app repaints itself from its
//     own model on SIGWINCH, so reflowing it would be wasted work on
//     data the app is about to overwrite.
//   - The cursor. x/vt exposes no public setter, so it is left where the
//     resize put it; SIGWINCH makes most programs reposition themselves.
func (g *vtGrid) Resize(cols, rows int) {
	oldCols, oldRows := g.em.Width(), g.em.Height()
	if cols == oldCols && rows == oldRows {
		return
	}
	if g.em.IsAltScreen() {
		g.em.Resize(cols, rows)
		return
	}

	before := make([]Row, oldRows)
	for y := 0; y < oldRows; y++ {
		r := make(Row, oldCols)
		for x := 0; x < oldCols; x++ {
			r[x] = g.em.CellAt(x, y)
		}
		before[y] = r
	}

	g.em.Resize(cols, rows)

	after := Reflow(before, oldCols, cols)
	for y := 0; y < rows && y < len(after); y++ {
		x := 0
		for x < cols {
			c := after[y][x]
			if c == nil {
				g.em.SetCell(x, y, nil)
				x++
				continue
			}
			g.em.SetCell(x, y, c)
			// Never write a wide glyph's placeholder slot: it trips
			// uv.Line.Set's partial-overwrite protection and blanks the
			// whole glyph.
			if c.Width > 1 {
				x += c.Width
			} else {
				x++
			}
		}
	}
}
```

- [ ] **Step 3: Run the tests**

Run: `go test ./internal/server/term/ -v 2>&1 | tail -30`
Expected: everything green, including the two previously-skipped tests and all of Task 5's.

- [ ] **Step 4: Verify the blank-after-resize defect is gone**

Add to `internal/server/term/reflow_test.go`:

```go
// Screen.Resize clears Touched, so a plain resize renders blank until new
// output arrives. Writing cells back re-touches them.
func TestResizeLeavesLinesDrawable(t *testing.T) {
	g := term.NewVT(20, 4)
	defer g.Close()
	io.WriteString(g, "hello")
	g.Resize(10, 4)

	s := compose.NewSurface(10, 4)
	g.Draw(s, s.Bounds())
	if got := compose.Text(s, s.Bounds())[0]; !strings.Contains(got, "hello") {
		t.Errorf("screen blank after resize: got %q", got)
	}
}
```

Run: `go test ./internal/server/term/ -run TestResizeLeavesLinesDrawable -v`
Expected: PASS.

- [ ] **Step 5: Update the spec's open question**

In `../2026-09-18-wideboi-v1-foundations/spec.md`, the Open Questions entry recording that `Emulator.Resize` truncates is now half-stale: the upstream defect is unchanged, but wideboi no longer suffers it on the visible screen. Rewrite that bullet to say the upstream behaviour is unchanged, that `term.Reflow` compensates for the primary screen as of this commit, and that scrollback remains un-reflowed by design pending scroll-back navigation.

Also update `Grid.Resize`'s interface doc comment in `grid.go` — it currently warns callers off using the method, and that warning is now wrong.

- [ ] **Step 6: Run the full gate and commit**

```bash
make check
git status --porcelain   # must be empty
git add internal/server/term/ docs/
git commit -m "feat(term): reflow the visible screen on resize

Narrowing no longer destroys text on the primary screen, and the
blank-after-resize defect goes with it since SetCell re-touches every
line. Alt screen is skipped (the app repaints itself) and scrollback is
untouched (measured: Resize does not damage it).

Un-skips the two reflow tests written in Plan 1 to document the defect."
```

---

## Inherited items this plan does NOT take, and where they go

The spec lists eight items carried over from Plan 1 as "due in Plan 2". This
plan takes one — the emulator reflow. The rest are re-assigned here rather than
dropped, because a carry-forward nobody re-homes is a silent discard:

| Item | Goes to | Why not here |
| --- | --- | --- |
| Emulator reflow + blank-after-resize | **Plan 2, Tasks 5–6** | — |
| Host-window resize unimplemented | Plan 3 | Needs the layout core to decide new pane sizes. Reflow (Task 6) is its precondition and lands here. |
| No tests in `internal/client` | Plan 3 | The package is being split across the seam; testing it first would test code about to be dismantled. |
| Wedge: a child that stops reading stdin freezes rendering | Plan 3 | The fix is a bounded write on the PTY master, which trades dropped child-bound bytes. It belongs with the seam, where input routing is already being reworked. |
| Leak: root exits before `Kill`, escapees never signalled | Plan 3 | Needs a descendant snapshot maintained while the root lives — i.e. server-owned pane lifecycle, which the seam introduces. |
| `ps -axo` portability on Linux | **Unscheduled — needs a machine, not a plan** | No Linux host is available. The anti-leak guarantee degrades silently if `Descendants` returns a short list there. Run `make check` once on Linux before Plan 3 adds more teardown surface. |
| `compose.Text` / `WriteString` ignore `Cell.Width` | Plan 4 | Comes due with status glyphs (`»`, `✓`). |
| `seam-check` allowlist still has three entries | Plan 3 | Two are retired when `client.Pane` is split; the third is a test-only import resolved by moving `compose.Text` to a shared test helper. |

## Done criteria

- `make check` passes and does not modify the tree. It now runs: `fmt-check lint seam-check test verify-exit smoke`.
- `make smoke` reports 5 passed, plus a matching golden snapshot.
- Every smoke case has been observed failing when the thing it guards is broken.
- Narrowing a pane no longer destroys visible text; widening recovers it.
- The two reflow tests from Plan 1 are un-skipped and green.
- `internal/client` still imports `internal/server` only via the three allowlisted Plan 1 temporaries — no fourth.

## What Plan 3 needs from this

- `make smoke` is the place every new verb gets an acceptance case. Plan 3 adds cases for focus movement, column creation, and scrolling.
- `term.Reflow` is pure and independent of the emulator, so it moves across the client/server seam without modification.
- The golden snapshot will change when the layout changes. That is a review prompt, not a failure — regenerate with `make golden` and read the diff.
- **Cut the seam before wiring layout.** Nothing in Plan 2 added a consumer of pane state; layout wiring would. That is the whole reason the seam is Plan 3's first work and not its last.
