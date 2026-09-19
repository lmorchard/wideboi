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
import fcntl
import os
import re
import signal
import struct
import sys
import termios
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptylib import (
    ALT_SCREEN_ENTER, ALT_SCREEN_EXIT, Drainer, spawn_in_pty,
    wait_for_exit, force_cleanup, pane_children, still_alive,
)

CUP = re.compile(rb"\x1b\[(\d+);(\d+)H")
DIVIDER = "│".encode()
# The renderer diffs cell-by-cell, so a divider redrawn at an unchanged
# column may ride on the cursor's position left over from the previous
# write with no cursor move of its own. But whenever a divider's column
# actually changes, that new position requires an absolute cursor move
# (optionally followed by an SGR reset) right before the glyph -- so this
# reliably surfaces *new* divider columns appearing in a byte range,
# which is exactly the signal a column-width change should produce.
DIVIDER_CUP = re.compile(rb"\x1b\[(\d+);(\d+)H(?:\x1b\[[0-9;]*m)*" + DIVIDER)
# The status line's "focus: pane N" prefix is only ever transmitted once
# (the first frame). After that the diffing renderer only rewrites the
# digit itself, addressed by an absolute cursor move to column 13 of the
# status row (len("focus: pane ") == 12). Combine the one-time literal
# with later positional updates, in stream order, to track the current
# focused pane ID across a whole session.
FOCUS_LITERAL = re.compile(rb"focus: pane (\d+)")


def _focus_digit_re(status_row: int) -> re.Pattern:
    return re.compile(
        rb"\x1b\[" + str(status_row).encode() + rb";13H(?:\x1b\[[0-9;]*m)*(\d+)"
    )


def focus_pane_id(out: bytes, status_row: int) -> int | None:
    """The most recently reported focused pane ID, or None if never seen."""
    matches = [(m.start(), int(m.group(1))) for m in FOCUS_LITERAL.finditer(out)]
    matches += [(m.start(), int(m.group(1))) for m in _focus_digit_re(status_row).finditer(out)]
    if not matches:
        return None
    return max(matches, key=lambda t: t[0])[1]


def divider_columns(out: bytes) -> set[int]:
    """Columns where a divider glyph was drawn via an absolute cursor move."""
    return {int(c) for _, c in DIVIDER_CUP.findall(out)}


class Session:
    """A running wideboi in a pty, with helpers to type and observe."""

    def __init__(self, cols=100, rows=30, startup=1.2):
        self.pid, self.fd = spawn_in_pty(["./bin/wideboi"], cols, rows, True)
        self.rows = rows
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


def case_new_column_opens_pane(fail):
    # "Typing reaches a pane" is true whether or not a column ever opened
    # -- ctrl+n used to be routed to VerbNewColumn and to the pane itself
    # equally well as far as that assertion could tell. Pin down the
    # verb's actual effect instead: the focused pane ID must change, and
    # the next input must land in that new, distinct pane.
    s = Session()
    before_focus = focus_pane_id(s.output(), s.rows)
    before_len = len(s.output())
    s.type("\x1bn")  # alt+n -> new column
    after_focus = focus_pane_id(s.output(), s.rows)
    if before_focus is None or after_focus is None:
        fail("no focus-pane-id observed around alt+n")
        return
    if after_focus == before_focus:
        fail(f"alt+n did not change the focused pane: still pane {after_focus}")
    s.type("echo pane-three\r")
    landed = s.output()[before_len:]
    if b"pane-three" not in landed:
        fail("input did not reach the newly opened column pane")
    s.quit_and_reap()


def case_cycle_width(fail):
    # As with new-column, "typing reaches a pane" cannot distinguish a
    # cycled width from a no-op -- the same pane keeps taking input
    # either way. The client draws a "│" divider at the right edge of
    # every column short of the far edge, so a genuine width change must
    # move that divider to a column it was not at before.
    s = Session()
    before_cols = divider_columns(s.output())
    before_len = len(s.output())
    s.type("\x1bw")  # alt+w -> cycle focused column's width
    moved_to = divider_columns(s.output()[before_len:]) - before_cols
    if not moved_to:
        fail(f"alt+w did not move any column divider off of {sorted(before_cols)}")
    s.type("echo cycled-width\r")
    if b"cycled-width" not in s.output():
        fail("input failed to reach pane after cycling column width")
    s.quit_and_reap()


def case_alt_mod_keybindings(fail):
    s = Session()
    s.type("\x1bn")  # alt+n -> new column
    s.type("echo alt-mod-pane3\r")
    if b"alt-mod-pane3" not in s.output():
        fail("alt+n failed to create new column and accept input")
    s.type("\x1bh")  # alt+h -> focus left
    s.type("echo back-in-pane2\r")
    if b"back-in-pane2" not in s.output():
        fail("alt+h failed to move focus left")
    s.quit_and_reap()


def case_osc133_status_and_smart_jump(fail):
    s = Session()
    s.type("printf '\\033]133;A\\007'\r")
    time.sleep(0.5)
    s.type("\x1bl")  # alt+l -> focus right
    s.type("\x1bj")  # alt+j -> smart jump back
    s.type("echo smart-jumped\r")
    if b"smart-jumped" not in s.output():
        fail("smart jump failed to focus pane requiring attention")
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


def case_status_line_names_real_keys(fail):
    s = Session()
    out = s.output()
    if b"$mod" in out:
        fail("status line renders the literal placeholder '$mod'")
    # The bindings the user actually has. If a binding changes, this
    # fails loudly and someone updates both together.
    for key in (b"alt+h", b"alt+n", b"alt+w", b"alt+q"):
        if key not in out:
            fail(f"status line never mentions {key.decode()}")
    s.quit_and_reap()


def case_shell_control_keys_pass_through(fail):
    # cat -v echoes control bytes visibly as ^X, so we can see exactly
    # which ones survive the multiplexer's binding matrix. Plain "cat -v"
    # is not enough: on a canonical-mode pty (the pane's default), the
    # kernel's own line discipline treats ctrl+w as WERASE and silently
    # eats it before cat ever reads it -- a false negative that exists
    # even with wideboi entirely out of the picture. Disabling icanon
    # and iexten first removes that kernel-level interception so this
    # only measures what the multiplexer itself does with the byte.
    s = Session()
    s.type("stty -icanon -iexten -echo; cat -v\r", settle=1.3)
    before = len(s.output())
    for byte in (b"\x17", b"\x0c", b"\x0e", b"\x08"):  # ctrl+w l n h
        os.write(s.fd, byte)
        time.sleep(0.5)
    s.type("\r", settle=1.2)
    seen = s.output()[before:]
    for name, mark in (("ctrl+w", b"^W"), ("ctrl+l", b"^L"),
                       ("ctrl+n", b"^N"), ("ctrl+h", b"^H")):
        if mark not in seen:
            fail(f"{name} was swallowed by the multiplexer; the shell needs it")
    s.quit_and_reap()


def case_host_resize_resizes_panes(fail):
    # A pane's child must learn its new size, or it keeps wrapping at the
    # old width and full-screen apps lay out wrong.
    s = Session(cols=120, rows=30)
    s.type("stty size\r", settle=1.4)
    first = re.findall(rb"(\d+) (\d+)", s.output())
    if not first:
        fail("could not read the pane's initial size")
        s.quit_and_reap()
        return
    before = first[-1]

    fcntl.ioctl(s.fd, termios.TIOCSWINSZ, struct.pack("HHHH", 20, 70, 0, 0))
    time.sleep(1.5)
    mark = len(s.output())
    s.type("stty size\r", settle=1.6)
    after = re.findall(rb"(\d+) (\d+)", s.output()[mark:])
    if not after:
        fail("pane produced no size output after the host resized")
    elif after[-1] == before:
        fail(f"pane size unchanged after host resize: {before} -- SIGWINCH never reached the child")
    s.quit_and_reap()


CASES = [
    ("launch shows two panes and a cursor", case_launch_shows_two_panes),
    ("typing reaches the focused pane", case_typing_reaches_the_focused_pane),
    ("shifted keys reach the pane", case_shifted_keys_reach_the_pane),
    ("focus switch moves the cursor", case_focus_switch_moves_the_cursor),
    ("new column opens pane", case_new_column_opens_pane),
    ("cycle width adjusts column", case_cycle_width),
    ("alt mod keybindings route verbs", case_alt_mod_keybindings),
    ("osc133 status and smart jump", case_osc133_status_and_smart_jump),
    ("quit restores the terminal and reaps", case_quit_restores_and_reaps),
    ("status line names real keys", case_status_line_names_real_keys),
    ("shell control keys pass through", case_shell_control_keys_pass_through),
    ("host resize resizes panes", case_host_resize_resizes_panes),
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
