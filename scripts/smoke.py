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
    if "│".encode() not in out:
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
        return
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
        return
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
