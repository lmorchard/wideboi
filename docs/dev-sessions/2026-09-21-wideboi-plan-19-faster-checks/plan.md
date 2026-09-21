# Plan 19 — Faster checks Implementation Plan

**Goal:** Replace the fixed sleeps in the pty suites with waits on observed
output, and stop 24 smoke cases paying a 2s teardown that one case asserts.

**Approach:** One shared wait helper in `ptylib`, reused by `smoke.py` and
`attachcheck.py`, with existing `settle=`/`startup=` values becoming timeouts.

**Tech stack:** Python (stdlib only), Go, `make`.

**Commit per phase:** `Phase N: <name>`.

**Baseline to beat**, measured before any change: `smoke` 158.4s,
`attach-check` 53.5s, whole `make check` ~252s.

---

## Phase 1: Wait for the screen to settle, not for the clock

**Test first** in the only sense available here: the suites *are* the test.
Phase 1 is done when all 25 smoke cases still pass and `smoke` is materially
faster. Any case that breaks is the signal the spec's open question was
looking for.

**Files:**
- Modify: `scripts/ptylib.py` — add `settle_output`.
- Modify: `scripts/smoke.py` — `Session.type` and `Session.__init__` use it.

**Key changes:**

```python
# Cursor show/hide is emitted every frame regardless of whether
# anything changed -- cmd/wideboi/main.go renders unconditionally on a
# 16ms tick and the renderer wraps each frame in a hide/show pair. At
# ~62 pairs a second nothing is ever quiet, so waiting for silence
# waits forever. Excluded from the change metric only: output() is
# untouched, so cursor_visible and every existing assertion still see
# these bytes.
_FRAME_NOISE = re.compile(rb"\x1b\[\?25[hl]")


def settle_output(drainer, timeout: float, quiet: float = 0.10,
                  poll: float = 0.01) -> bool:
    """Waits until the drained output has been unchanged for `quiet`
    seconds, or `timeout` elapses. Returns True if it settled.

    The timeout is a ceiling, not a duration: callers pass what used to
    be a fixed sleep, and get that as the worst case rather than the
    every-case. A loaded CI runner takes longer; a fast local run takes
    ~0.15s.
    """
    def size() -> int:
        return len(_FRAME_NOISE.sub(b"", drainer.output()))

    deadline = time.monotonic() + timeout
    last, last_change = size(), time.monotonic()
    while time.monotonic() < deadline:
        time.sleep(poll)
        n = size()
        if n != last:
            last, last_change = n, time.monotonic()
        elif time.monotonic() - last_change >= quiet:
            return True
    return False
```

`Session.type` keeps its signature so no case changes:

```python
def type(self, text: str, settle=0.8):
    os.write(self.fd, text.encode())
    settle_output(self.drainer, timeout=settle)
```

`Session.__init__` likewise — `startup=1.2` becomes the ceiling, and the
measured need is 0.154s.

**Verification — automated:**
- [x] `python3 scripts/smoke.py` — all 25 cases pass
- [x] `smoke` target timed against the 158.4s baseline — **92.5s, a 42% cut**
- [x] `env -u TERM python3 scripts/smoke.py` passes — **green**
- [x] run **three times**, all green, to catch a case that settles early by luck — **ran four; 25/25 each, timings within 0.2s of each other**
- [x] `make check` passes — **201.5s against a 252s baseline**

**Verification — manual:**
- [x] Skim the diff: no case's assertions changed, only the waiting. — **confirmed; `smoke.py` changes are the import line, `__init__` and `type`.**

### The first quiet window was too tight, and the suite said so

`quiet=0.10` gave **78.1s** and looked like a win, then failed 1–2 cases per
run across three runs — three different cases, never the same one twice:

```
control mode is visible and escapable / leaving control mode did not restore the cursor
ctrl repeat moves two columns         / focus did not reach pane 1 after C-h C-h: got pane 4
help overlay opens and any key dismisses / could not get back to typing after dismissing help
```

Multi-step operations have gaps wider than 100 ms between bursts — wideboi
processes a keystroke, renders, then handles the next on a later tick — so the
waiter returned between them. **The first run passed 25/25, which is exactly
how a flaky suite presents.**

`quiet=0.25` is stable over four consecutive runs and costs ~14s against
`0.10`. Slower than the best number, and the best number was wrong. Recorded
because the temptation to tune this back down will recur, and the way to
judge it is repeat runs, not one green.

---

## Phase 2: Stop 24 cases paying for a teardown one case asserts

`quit_and_reap` costs 2.04s, and none of it is the panes — `still_alive`
returns in 0.000s. It is wideboi burning its full grace period because an
interactive `/bin/sh` ignores SIGTERM. Correct behaviour, asserted by
`verify-exit` and by `case_quit_restores_and_reaps`, and paid for by 24 cases
that assert neither.

**Files:**
- Modify: `scripts/smoke.py` — add `Session.close`, switch cleanup-only cases.

**Key changes:**

```python
def close(self) -> None:
    """Tears the session down without asserting anything about how.

    quit_and_reap sends SIGTERM and waits for the ordinary teardown
    path, which takes ~2s: an interactive /bin/sh ignores SIGTERM, so
    pane teardown always burns its grace before escalating. That is
    the contract verify-exit and case_quit_restores_and_reaps exist to
    assert, and every other case was paying 2s to re-verify it.

    Children are reaped explicitly rather than trusting a SIGKILL'd
    wideboi to have done it -- trading 47 seconds for a process leak
    would be a bad deal.
    """
    kids = [p for p, _ in pane_children(self.pid)]
    force_cleanup(self.pid)
    for kid in kids:
        try:
            os.kill(kid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    still_alive(kids, 1.0)
    self.drainer.stop()
```

Every `s.quit_and_reap()` whose return value is unused, and which is not in
`case_quit_restores_and_reaps`, becomes `s.close()`.

**A leak check, so this cannot quietly regress:** the runner counts stray
`wideboi` processes after the suite finishes and fails if any survive. The
whole risk of this phase is trading time for leaked processes, and nothing
currently notices.

**Verification — automated:**
- [x] `python3 scripts/smoke.py` — all 25 pass — **three consecutive runs**
- [x] `case_quit_restores_and_reaps` still asserts `s.leaked` and still uses `quit_and_reap` — **the only remaining caller; 28 cleanup-only sites swapped to `close()`**
- [!] `ps -axo command | grep -c '[b]in/wideboi'` is 0 after a full run — **replaced with a better check.** A global "is any wideboi running" test false-positives on a stray from an earlier run, another terminal, or a developer poking at the binary — and it did, exactly once, on a leftover from one of my own probe scripts. The guard now tracks the pids *this run* spawned and reports only those.
- [x] `smoke` timed against the Phase 1 number — **44.0s, from 92.5s; 158.4s at baseline**
- [x] `make check` passes — **136.4s, from 201.5s; 252s at baseline**

**Verification — manual:**
- [x] Run the suite twice back to back. — **ran three; 44.0 / 44.1 / 44.0s, no drift and no strays, so `close()` is reaping rather than leaving zombies.**

**The guard was proved to fail**, not just to pass: spawning a session and
deliberately not closing it makes `strays()` report `[(6754, './bin/wideboi')]`.
A leak check nobody has watched fail is not a leak check.

---

## Phase 3: The same treatment for attach-check

`attach-check` is 53.5s and has the same shape: a `Client` with its own
`type(settle=SETTLE)` and startup sleeps.

**Files:**
- Modify: `scripts/attachcheck.py` — use `settle_output`; fast teardown where
  teardown is not the assertion.

**Key changes:** mechanical — `Client.type` calls `settle_output` with the
existing settle as timeout. Cases that assert on detach/reattach semantics
(`case_detach_leaves_the_session_running`, `case_server_reaps_its_panes_on_signal`)
keep whatever waiting they assert on.

**Note the socket:** `attachcheck` binds a fixed `default.sock`, so its cases
are order-dependent and must stay serial. Nothing here changes that; it is the
obstacle parallelism would have to solve, and is recorded in #51.

**Verification — automated:**
- [x] `python3 scripts/attachcheck.py` — all 7 pass
- [x] timed against the 53.5s baseline — **7.5s, a 7x cut**
- [x] run twice back to back, both green — **ran three: 7.6 / 7.6 / 7.7s, all 7/7. The fixed socket path was the risk and it held.**
- [x] `make check` passes — **90.8s, and 90.9s with `TERM` unset**

**`PROMPT_WAIT = 8.0` was waiting for something that no longer happens.** Its
comment justified the value as covering "whatever `$SHELL` is on the
developer's machine, and a themed zsh takes multiple seconds to reach its
first prompt" — but `ptylib` pins `SHELL=/bin/sh` now, so that has not been
the risk for some time. Kept as a ceiling anyway, since a cold CI runner
still is one and an unused ceiling costs nothing.

---

## Phase 4: Record the numbers and the finding

**Files:**
- Modify: `docs/LESSONS.md` — only if the execution earns an entry.
- Comment on #51 with before/after.
- New issue for the idle render trickle.

**Key changes:**

The idle-render issue is the substantive one, and is a product defect rather
than a test concern: wideboi emits **1,134 B/s while completely idle**, ~62
`ESC[?25l`/`ESC[?25h` pairs a second, because `main.go` renders and flushes
every 16 ms tick regardless of whether anything changed. Over SSH that is
1.1 KB/s for a static screen. It is also what made naive idle-detection
impossible, which is how it was found.

**Verification — automated:**
- [x] `make check` passes — **90.8s, and 90.9s with `TERM` unset**
- [x] final timings recorded in `notes.md` and on #51 — **done**

**Verification — manual:**
- [x] The new issue names a reproduction, not just a symptom. — **#52 carries the 2-second sample and the sequence counts, points at the two unconditional `Render`/`Flush` call sites, and notes that `_FRAME_NOISE` should be deleted when it is fixed.**

No `LESSONS.md` entry. The two candidates here — "a fast number that fails on
the second run is not a fast number" and "scope a leak check to what you
started" — are both instances of lessons the file already carries about
verifying rather than assuming. Adding near-duplicates dilutes the ones that
were paid for.

---

## Spec coverage check

| Spec requirement | Phase |
| --- | --- |
| `make check` well under a minute | 1, 2, 3 |
| Waits bounded by timeout, satisfied by observation | 1 |
| Teardown assertions unchanged | 2 |
| No new dependency | all |
| attach-check gets the same treatment | 3 |
| Idle render trickle filed, not fixed | 4 |
