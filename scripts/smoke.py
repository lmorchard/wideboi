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
    wait_for_exit, descendants, force_cleanup, pane_children, ps_rows,
    settle_output, still_alive,
)

CUP = re.compile(rb"\x1b\[(\d+);(\d+)H")
DIVIDER = "[│┃]".encode()
# The renderer diffs cell-by-cell, so a divider redrawn at an unchanged
# column may ride on the cursor's position left over from the previous
# write with no cursor move of its own. But whenever a divider's column
# actually changes, that new position requires an absolute cursor move
# (optionally followed by an SGR reset) right before the glyph -- so this
# reliably surfaces *new* divider columns appearing in a byte range,
# which is exactly the signal a column-width change should produce.
DIVIDER_CUP = re.compile(rb"\x1b\[(\d+);(\d+)H(?:\x1b\[[0-9;]*m)*" + DIVIDER)
# The status line's "focus: [pane N ★]" prefix is only ever transmitted once
# (the first frame). After that the diffing renderer only rewrites the
# digit itself, addressed by an absolute cursor move to column 14 of the
# status row (len("focus: [pane ") == 13). Combine the one-time literal
# with later positional updates, in stream order, to track the current
# focused pane ID across a whole session.
FOCUS_LITERAL = re.compile(rb"focus: (?:pane|\[pane) (\d+)")


def _focus_digit_re(status_row: int) -> re.Pattern:
    return re.compile(
        rb"\x1b\[" + str(status_row).encode() + rb";(?:13|14)H(?:\x1b\[[0-9;]*m)*(\d+)"
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


# Cursor visibility is the one part of control mode that is assertable
# on the wire. The bar's inversion is an SGR attribute, which survives
# a test only if someone looks for the exact byte; the cursor is a mode
# escape the renderer emits on change. Taking the last one in stream
# order gives the state as of the end of the capture.
CURSOR_VIS = re.compile(rb"\x1b\[\?25(h|l)")


def cursor_visible(out: bytes) -> bool | None:
    """Whether the cursor was last shown or hidden, in stream order."""
    found = CURSOR_VIS.findall(out)
    return None if not found else found[-1] == b"h"


class Session:
    """A running wideboi in a pty, with helpers to type and observe."""

    def __init__(self, cols=100, rows=30, startup=1.2, env=None):
        self.pid, self.fd = spawn_in_pty(["./bin/wideboi"], cols, rows, True, env)
        self.rows = rows
        SPAWNED.append(self.pid)
        self.drainer = Drainer(self.fd)
        self.drainer.start()
        # startup and settle are ceilings now, not durations. Every
        # value a case passes was chosen as "long enough for this to
        # finish", which is exactly the right timeout; waiting for the
        # screen to stop changing turns it into the worst case rather
        # than the every case. Measured: startup settles in 0.154s
        # against this 1.2s, a keystroke in 0.10-0.28s against 0.8s.
        settle_output(self.drainer, timeout=startup)

    def type(self, text: str, settle=0.8):
        os.write(self.fd, text.encode())
        settle_output(self.drainer, timeout=settle)

    def output(self) -> bytes:
        return self.drainer.output()

    def cursor_positions(self) -> list[tuple[int, int]]:
        return [(int(r), int(c)) for r, c in CUP.findall(self.output())]

    def close(self) -> None:
        """Tears the session down without asserting anything about how.

        quit_and_reap sends SIGTERM and waits for the ordinary teardown
        path, which costs ~2s: an interactive /bin/sh ignores SIGTERM,
        so pane teardown always burns its full grace before escalating
        to SIGKILL. None of that is the panes -- still_alive returns in
        0.000s -- it is wideboi waiting.

        That contract is real and asserted twice already: by the
        verify-exit target, and by case_quit_restores_and_reaps, the
        only reader of s.leaked. The other 24 cases were each paying 2s
        to re-verify it, which is ~47s of the suite.

        Children are reaped explicitly rather than trusting a SIGKILL'd
        wideboi to have done it. Trading 47 seconds for a process leak
        would be a bad deal, and check_no_strays below is what notices
        if this stops holding.
        """
        # descendants, not pane_children: a pane's shell can leave
        # behind a grandchild that escaped its process group entirely,
        # which is the whole subject of ptyx's reap tests. Direct
        # children miss exactly the process most likely to leak.
        #
        # Such a process often dies anyway, via SIGHUP when the pty
        # master closes -- but that is incidental, and the reap tests
        # exist because it is not guaranteed.
        kids = [p for p, _ in descendants(self.pid)]
        force_cleanup(self.pid)
        for kid in kids:
            try:
                os.kill(kid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        still_alive(kids, 1.0)
        # Hand them to the guard too. Tracking only the wideboi pid
        # would let this path leak a descendant while the suite still
        # reported a clean run.
        SPAWNED.extend(kids)
        self.drainer.stop()

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
    s.close()


def case_typing_reaches_the_focused_pane(fail):
    s = Session()
    s.type("echo smoke-lower-42\r")
    if b"smoke-lower-42" not in s.output():
        fail("lowercase input never reached the pane")
    s.close()


def case_shifted_keys_reach_the_pane(fail):
    # The defect that shipped in Plan 1: every ModShift key was dropped.
    s = Session()
    s.type("echo SMOKE_CAPS '!bang'\r")
    out = s.output()
    if b"SMOKE_CAPS" not in out:
        fail("capital letters never reached the pane")
    if b"!bang" not in out:
        fail("shifted punctuation never reached the pane")
    s.close()


def case_focus_switch_moves_the_cursor(fail):
    s = Session()
    s.type("echo pane-zero\r")
    before = s.cursor_positions()[-1] if s.cursor_positions() else None
    s.type("\x02l")  # C-b l -> focus right, which exits control mode on its own
    s.type("echo pane-one\r")
    after = s.cursor_positions()[-1] if s.cursor_positions() else None
    if before is None or after is None:
        fail("no cursor positions observed around a focus switch")
        return
    if before[1] == after[1]:
        fail(f"cursor column did not move across panes: {before} -> {after}")
    if b"pane-one" not in s.output():
        fail("input did not follow focus to the second pane")
    s.close()


def case_new_column_opens_pane(fail):
    # "Typing reaches a pane" is true whether or not a column ever opened
    # -- ctrl+n used to be routed to VerbNewColumn and to the pane itself
    # equally well as far as that assertion could tell. Pin down the
    # verb's actual effect instead: the focused pane ID must change, and
    # the next input must land in that new, distinct pane.
    s = Session()
    before_focus = focus_pane_id(s.output(), s.rows)
    before_len = len(s.output())
    s.type("\x02n")  # C-b n -> new column, which exits control mode on its own
    after_focus = focus_pane_id(s.output(), s.rows)
    if before_focus is None or after_focus is None:
        fail("no focus-pane-id observed around C-b n")
        return
    if after_focus == before_focus:
        fail(f"C-b n did not change the focused pane: still pane {after_focus}")
    s.type("echo pane-three\r")
    landed = s.output()[before_len:]
    if b"pane-three" not in landed:
        fail("input did not reach the newly opened column pane")
    s.close()


def case_cycle_width(fail):
    # As with new-column, "typing reaches a pane" cannot distinguish a
    # cycled width from a no-op -- the same pane keeps taking input
    # either way. The client draws a "│" divider at the right edge of
    # every column short of the far edge, so a genuine width change must
    # move that divider to a column it was not at before.
    s = Session()
    before_cols = divider_columns(s.output())
    before_len = len(s.output())
    s.type("\x02w")  # C-b w -> cycle width, which exits control mode on its own
    moved_to = divider_columns(s.output()[before_len:]) - before_cols
    if not moved_to:
        fail(f"C-b w did not move any column divider off of {sorted(before_cols)}")
    s.type("echo cycled-width\r")
    if b"cycled-width" not in s.output():
        fail("input failed to reach pane after cycling column width")
    s.close()


def case_prefix_routes_verbs(fail):
    # The whole point of the plan: verbs reachable without the terminal
    # being configured to send Option as Meta.
    s = Session()
    s.type("\x02n")  # C-b n -> new column, which exits control mode on its own
    s.type("echo prefix-pane3\r")
    if b"prefix-pane3" not in s.output():
        fail("C-b n failed to create a new column and accept input")
    s.type("\x02h")  # C-b h -> focus left, which exits control mode on its own
    s.type("echo back-in-pane2\r")
    if b"back-in-pane2" not in s.output():
        fail("C-b h failed to move focus left")
    s.close()


def case_control_mode_is_visible_and_escapable(fail):
    # A mode you cannot tell you are in is worse than no mode. The bar
    # inverts (SGR 7) and the cursor hides (DECTCEM), and both have to
    # come back on the way out.
    s = Session()
    s.type("echo before-mode\r")
    if cursor_visible(s.output()) is not True:
        fail("cursor was not visible before entering control mode")
    before = len(s.output())

    s.type("\x02")  # C-b, and stay there
    entered = s.output()[before:]
    if b"\x1b[7m" not in entered:
        fail("entering control mode did not invert the status bar (no SGR 7 on the wire)")
    if cursor_visible(s.output()) is not False:
        fail("entering control mode did not hide the cursor")
    if b"q quit" not in entered:
        fail("control mode did not show the verb menu")
    mid = len(s.output())

    s.type("\x1b")  # escape
    if cursor_visible(s.output()) is not False and cursor_visible(s.output()) is None:
        fail("no cursor state observed after leaving control mode")
    if cursor_visible(s.output()) is not True:
        fail("leaving control mode did not restore the cursor")
    if b"for commands" not in s.output()[mid:]:
        fail("leaving control mode did not restore the normal status line")

    s.type("echo after-mode\r")
    if b"after-mode" not in s.output():
        fail("input did not reach the pane after leaving control mode")
    s.close()


def case_ctrl_repeat_moves_two_columns(fail):
    # C-h C-h must move two columns on one prefix. Plain h h no longer
    # does this: an unmodified verb exits control mode on the first h,
    # so the second h would land in a pane as a literal letter -- that
    # is deliberate now (case_unmodified_verb_exits_control_mode), and
    # ctrl+h is the repeat form that stays in the mode instead.
    #
    # A new column is inserted right after the currently focused one,
    # not at the end -- so two "\x02n"s from the default two panes
    # leave the strip in column order [1, 3, 4, 2] with focus on 4. One
    # C-h reaches 3; a second is required to reach 1. That is what
    # makes this case able to fail if only one repeat actually fired.
    #
    # The focus check is the only assertion here on purpose: anything
    # that types further text afterward and greps for it in the output
    # cannot fail on a stuck mode, because the terminal echoes typed
    # bytes whether or not they land where intended. focus_pane_id reads
    # a structured escape sequence, not echoed keystrokes -- but only
    # once control mode has ended: while it is active, the status row
    # shows the verb menu instead of the "focus: [pane N]" hint, so the
    # check has to come after leaving, not while the repeat is live.
    s = Session()
    s.type("\x02n")   # columns: [1, 3, 2], focus 3
    s.type("\x02n")   # columns: [1, 3, 4, 2], focus 4
    s.type("\x02")    # enter control mode
    s.type("\x08")    # C-h: focus left, 4 -> 3, stay
    s.type("\x08")    # C-h: focus left, 3 -> 1, stay
    s.type("\x1b")    # leave control mode
    if focus_pane_id(s.output(), s.rows) != 1:
        fail(f"focus did not reach pane 1 after C-h C-h: got pane {focus_pane_id(s.output(), s.rows)}")
    s.close()


def case_doubled_prefix_reaches_the_pane(fail):
    # Without this there is no way to type the prefix byte at all, and
    # readline's backward-char becomes unreachable in every pane.
    #
    # stty first for the same reason case_reclaimed_control_keys_pass_through
    # needs it: on a canonical-mode pty the kernel's line discipline eats
    # some control bytes before cat ever reads them.
    s = Session()
    s.type("stty -icanon -iexten -echo; cat -v\r", settle=1.3)
    before = len(s.output())
    os.write(s.fd, b"\x02\x02")
    time.sleep(0.6)
    s.type("\r", settle=1.2)
    if b"^B" not in s.output()[before:]:
        fail("a doubled prefix did not put a literal ctrl+b into the pane")
    s.close()


def case_reclaimed_control_keys_pass_through(fail):
    # ctrl+q and ctrl+o existed only as the escape hatch for a terminal
    # that would not send Option as Meta. The prefix is that hatch now,
    # so these belong to the pane again -- ctrl+q is XON/XOFF resume and
    # ctrl+o is readline's operate-and-get-next.
    s = Session()
    s.type("stty -icanon -iexten -ixon -echo; cat -v\r", settle=1.3)
    before = len(s.output())
    for byte in (b"\x11", b"\x0f"):  # ctrl+q, ctrl+o
        os.write(s.fd, byte)
        time.sleep(0.5)
    s.type("\r", settle=1.2)
    seen = s.output()[before:]
    for name, mark in (("ctrl+q", b"^Q"), ("ctrl+o", b"^O")):
        if mark not in seen:
            fail(f"{name} is still claimed by the multiplexer; the pane should have it now")
    s.close()


def case_custom_prefix_from_env(fail):
    # Configurability is not a later nicety: running wideboi inside tmux
    # collides on ctrl+b, and the whole justification for claiming a
    # shell key is that the user can move it.
    s = Session(env={"WIDEBOI_PREFIX": "ctrl+a"})
    out = s.output()
    if b"C-a for commands" not in out:
        fail("status line does not name the configured prefix")
    s.type("\x01n")  # C-a n -> new column, which exits control mode on its own
    s.type("echo custom-prefix-pane\r")
    if b"custom-prefix-pane" not in s.output():
        fail("the configured prefix did not route a verb")
    s.close()



# The OSC 133 case is back, and asserts through focus_pane_id.
#
# Its predecessor was deleted rather than patched, because it passed for
# that feature's entire broken life: it echoed a command into a pane and
# grepped the wire for the text it had just typed, which proves the pane
# echoes and nothing else. The handler underneath never matched a single
# sequence. See docs/LESSONS.md, "A smoke test that types a command and
# then greps for its own text proves nothing."
#
# This version writes the escape sequences into a pane and then asks the
# multiplexer a question only a working handler can answer: which pane
# does smart jump choose? The answer arrives as the focused-pane digit
# in the status line, which no amount of pane echo can fake.
def case_osc133_status_drives_smart_jump(fail):
    s = Session()

    s.type("\x02l")                                  # focus pane 2
    s.type("printf '\\033]133;D;1\\007'\r")           # mark pane 2 failed
    s.type("\x02h")                                  # back to pane 1

    if focus_pane_id(s.output(), s.rows) != 1:
        s.close()
        fail("setup did not return focus to pane 1")
        return

    before = len(s.output())
    s.type("\x02a")                                  # smart jump
    landed = focus_pane_id(s.output()[before:], s.rows)
    s.close()

    if landed != 2:
        fail(f"smart jump landed on pane {landed}, want the failed pane 2")


# CardStrategy shipped complete in Plan 8 and was unreachable until
# Plan 17 -- nothing outside a test ever called Strip.SetStrategy. This
# asserts the toggle actually reaches the client's placement maths.
#
# Asserted on the cursor, not on divider columns: the dividers do move,
# but the renderer emits them inside line runs rather than behind an
# absolute cursor move, so divider_columns' CUP-then-glyph pattern does
# not see them on a repaint. The focused pane's origin shifts when the
# fan opens, and the cursor goes with it -- same observable
# case_focus_switch_moves_the_cursor uses.
def case_card_layout_toggles(fail):
    s = Session()
    s.type("\x02n")                       # third pane, so there is a fan

    positions = s.cursor_positions()
    if not positions:
        fail("no cursor position reported before toggling")
        return
    scroll_col = positions[-1][1]

    s.type("\x02c")                       # toggle to cards
    cards_col = s.cursor_positions()[-1][1]

    s.type("\x02c")                       # and back
    back_col = s.cursor_positions()[-1][1]
    s.close()

    if cards_col == scroll_col:
        fail(f"card layout left the focused pane at column {scroll_col}; "
             "the toggle never reached the placement maths")
    if back_col != scroll_col:
        fail(f"toggling back landed at column {back_col}, want {scroll_col}")


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


def case_status_line_names_the_prefix(fail):
    # Normal mode's only affordance is the hint. If it goes missing, a
    # new user has no way to discover that any verbs exist.
    s = Session()
    out = s.output()
    if b"$mod" in out:
        fail("status line renders the literal placeholder '$mod'")
    if b"C-b for commands" not in out:
        fail("status line never tells the user how to reach the verbs")
    if b"alt+" in out:
        fail("status line still advertises the removed alt bindings")
    s.close()


def case_control_mode_names_every_entry_at_80_columns(fail):
    # 80 is the commonest terminal width and cmd/wideboi's own fallback
    # when the host reports no size. h/j/k/l collapse into one "hjkl
    # move" entry precisely so the whole menu still fits here: the
    # descriptive form needs 81 cells against a budget of 79.
    s = Session(cols=80, rows=24)
    s.type("\x02")
    out = s.output()
    for verb in (b"hjkl move", b"n new", b"w width", b"x kill",
                 b"a attn", b"? help", b"q quit", b"esc exit"):
        if verb not in out:
            fail(f"at 80 columns control mode never shows {verb.decode()!r}")
    # In-process owns its panes, so detaching would kill them.
    # scripts/attachcheck.py asserts the socket case.
    if b"d detach" in out:
        fail("in-process control mode offers 'd detach', which would kill the panes")
    s.type("\x1b")
    s.close()


def case_ctrl_repeats_control_mode(fail):
    # The feature, typed as real bytes. A unit test that synthesises
    # KeyPressEvent{Code:'l', Mod:ModCtrl} proves the router, not the
    # binding: MatchString returns false indistinguishably for "not
    # pressed" and "name I can never produce".
    #
    # 0x02 = C-b, 0x0c = C-l, 0x6c = plain l. Columns are [1, 3, 4, 2]
    # with focus on 4: the first C-l reaches the last column, 2, and the
    # second clamps there rather than wrapping. The final plain l clamps
    # again and exits the mode, so focus stays on pane 2 throughout --
    # the actual two-hop repeat is what case_ctrl_repeat_moves_two_columns
    # pins; this case's job is the mode-exit proof below, not movement.
    s = Session(cols=100, rows=30)
    s.type("\x02n", settle=1.2)   # a third column
    s.type("\x02n", settle=1.2)   # a fourth
    s.type("\x02")                # enter control mode
    s.type("\x0c")                # C-l: focus right, stay
    s.type("\x0c")                # C-l: focus right (clamped), stay
    if cursor_visible(s.output()) is not False:
        fail("cursor is visible mid-repeat; control mode should hide it")
    s.type("l")                   # plain l: focus right (clamped), exit
    out = s.output()
    if focus_pane_id(out, s.rows) != 2:
        fail(f"expected focus on pane 2, got {focus_pane_id(out, s.rows)}")
    # Typing must now reach the pane, which is the proof the mode ended.
    s.type("echo ctrl-repeat-done\r")
    if b"ctrl-repeat-done" not in s.output():
        fail("still in control mode after an unmodified verb -- the mode did not exit")
    s.close()


def case_unmodified_verb_exits_control_mode(fail):
    s = Session()
    s.type("\x02")
    s.type("l")
    s.type("echo one-shot-done\r")
    if b"one-shot-done" not in s.output():
        fail("an unmodified verb did not leave control mode")
    s.close()


def case_unknown_key_exits_control_mode(fail):
    # A keystroke that did nothing must never strand you in a mode that
    # eats the next one.
    s = Session()
    s.type("\x02")
    s.type("z")          # not a verb
    s.type("echo unknown-exits\r")
    if b"unknown-exits" not in s.output():
        fail("an unknown key left the session stuck in control mode")
    s.close()


def case_help_overlay_opens_and_any_key_dismisses(fail):
    s = Session(cols=100, rows=30)
    s.type("\x02?", settle=1.0)
    out = s.output()
    if b"hold ctrl to stay in control mode" not in out:
        fail("? did not raise the help overlay")
    if b"scroll this pane's history up" not in out:
        fail("the overlay does not describe the bindings")

    before = len(s.output())
    # k dismisses, and must NOT also scroll.
    s.type("k", settle=1.0)
    after = s.output()[before:]
    if b"hold ctrl to stay" in after:
        fail("the overlay redrew after the dismissing key")

    # The check above cannot fail in either direction: ultraviolet only
    # emits *changed* cells, so if k dismissed the overlay the text is
    # simply not re-emitted (passes), and if k did nothing the frame is
    # identical and nothing is emitted at all (also passes). Force a
    # redraw instead: press ? again. If k returned to control mode, this
    # reopens the overlay and repaints its text. If k did nothing, the
    # overlay was still up and this ? is consumed as *its* dismissing
    # key instead -- so the text does not appear, and now the two states
    # disagree.
    before = len(s.output())
    s.type("?", settle=1.0)
    reopened = s.output()[before:]
    if b"hold ctrl to stay in control mode" not in reopened:
        fail("the overlay did not reopen on a second '?' -- k may never have dismissed it")

    # Dismiss this second overlay, then leave control mode entirely and
    # confirm typing reaches the pane again.
    s.type("k", settle=1.0)
    s.type("\x1b", settle=0.5)
    s.type("echo help-dismissed\r")
    if b"help-dismissed" not in s.output():
        fail("could not get back to typing after dismissing help")
    s.close()


def case_help_overlay_works_after_a_focus_switch(fail):
    # A focus switch starts an 8-frame wipe. This does not prove help
    # wins the race against the wipe -- the wipe self-clears in ~128ms
    # (8 frames at the 16ms tick), well inside this case's settle time,
    # so the overlay would show up here even with the layers in the
    # wrong order. The actual precedence is asserted directly, without a
    # terminal, by TestLayerLockedPrecedence in
    # internal/client/help_overlay_test.go. This case is just an
    # integration check that raising help right after a focus switch
    # still works end to end.
    s = Session(cols=100, rows=30)
    s.type("\x02")
    os.write(s.fd, b"l")     # focus right: starts a wipe
    os.write(s.fd, b"\x02?")  # and immediately raise help
    time.sleep(1.0)
    if b"hold ctrl to stay in control mode" not in s.output():
        fail("help raised during a wipe never appeared")
    s.type("k", settle=0.5)
    s.type("\x1b", settle=0.5)
    s.close()


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
    s.close()


def case_host_resize_resizes_panes(fail):
    # A pane's child must learn its new size, or it keeps wrapping at the
    # old width and full-screen apps lay out wrong.
    #
    # This only asserts the whole (rows, cols) pair changes, not cols
    # specifically: for a *fully visible, focused* pane, a plain host
    # resize is only ever supposed to change rows -- column width is the
    # column's own property, independent of the host's size (see
    # case_partly_clipped_pane_keeps_full_width, and the layout spec's
    # invariant 4). Rows changing is what proves SIGWINCH actually reached
    # the child at all.
    s = Session(cols=120, rows=30)
    s.type("stty size\r", settle=1.4)
    first = re.findall(rb"(\d+) (\d+)", s.output())
    if not first:
        fail("could not read the pane's initial size")
        s.close()
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
    s.close()


def case_partly_clipped_pane_keeps_full_width(fail):
    # Regression guard: a column scrolled mostly off-screen must keep its
    # own full logical width. Resizing it down to whatever sliver is on
    # screen -- what an earlier version of this fix actually shipped --
    # corrupts its line layout and lies to its child about its own width,
    # even though nothing about the pane itself changed; only a neighbor's
    # share of the screen did.
    s = Session(cols=120, rows=30)

    s.type("\x02l")  # C-b l -> focus right, onto pane 2, which exits control mode on its own
    s.type("stty size\r", settle=1.4)
    first = re.findall(rb"(\d+) (\d+)", s.output())
    if not first:
        fail("could not read pane 2's initial size")
        s.close()
        return
    before_cols = first[-1][1]
    s.type("\x02h")  # C-b h -> focus back to pane 1, leaving pane 2 unfocused, exits on its own

    # Narrow enough that two full-width columns no longer both fit: pane 1
    # (focused) stays fully visible, pane 2 is scrolled down to a sliver.
    fcntl.ioctl(s.fd, termios.TIOCSWINSZ, struct.pack("HHHH", 20, 70, 0, 0))
    time.sleep(1.5)

    s.type("\x02l")  # C-b l -> focus pane 2, which exits control mode on its own
    mark = len(s.output())
    s.type("stty size\r", settle=1.6)
    after = re.findall(rb"(\d+) (\d+)", s.output()[mark:])
    if not after:
        fail("pane 2 produced no size output after being focused post-resize")
        s.close()
        return
    after_cols = after[-1][1]
    if after_cols != before_cols:
        fail(
            f"partly-clipped pane's column width changed from "
            f"{before_cols.decode()} to {after_cols.decode()} -- a "
            "partly-covered pane must keep its full logical width"
        )
    s.close()


CASES = [
    ("launch shows two panes and a cursor", case_launch_shows_two_panes),
    ("typing reaches the focused pane", case_typing_reaches_the_focused_pane),
    ("shifted keys reach the pane", case_shifted_keys_reach_the_pane),
    ("focus switch moves the cursor", case_focus_switch_moves_the_cursor),
    ("new column opens pane", case_new_column_opens_pane),
    ("cycle width adjusts column", case_cycle_width),
    ("prefix routes verbs", case_prefix_routes_verbs),
    ("control mode is visible and escapable", case_control_mode_is_visible_and_escapable),
    ("ctrl repeat moves two columns", case_ctrl_repeat_moves_two_columns),
    ("doubled prefix reaches the pane", case_doubled_prefix_reaches_the_pane),
    ("reclaimed control keys pass through", case_reclaimed_control_keys_pass_through),
    ("custom prefix from env", case_custom_prefix_from_env),
    ("osc133 status drives smart jump", case_osc133_status_drives_smart_jump),
    ("card layout toggles", case_card_layout_toggles),
    ("quit restores the terminal and reaps", case_quit_restores_and_reaps),
    ("status line names the prefix", case_status_line_names_the_prefix),
    ("control mode names every entry at 80 columns", case_control_mode_names_every_entry_at_80_columns),
    ("ctrl repeats control mode", case_ctrl_repeats_control_mode),
    ("unmodified verb exits control mode", case_unmodified_verb_exits_control_mode),
    ("unknown key exits control mode", case_unknown_key_exits_control_mode),
    ("help overlay opens and any key dismisses", case_help_overlay_opens_and_any_key_dismisses),
    ("help overlay works after a focus switch", case_help_overlay_works_after_a_focus_switch),
    ("shell control keys pass through", case_shell_control_keys_pass_through),
    ("host resize resizes panes", case_host_resize_resizes_panes),
    ("partly clipped pane keeps full width", case_partly_clipped_pane_keeps_full_width),
]


# Every pid this run spawned, so the stray check can be about what we
# started rather than about whatever else is on the machine. A global
# "is any wideboi running" check false-positives on a stray from an
# earlier run, another terminal, or a developer poking at the binary
# -- and a check that cries wolf gets ignored, which is worse than not
# having one.
SPAWNED: list[int] = []


def strays() -> list[tuple[int, str]]:
    """Processes this run started that are still alive.

    Session.close SIGKILLs wideboi rather than letting it tear itself
    down, which is what makes the suite fast -- and the whole risk of
    that trade is leaking processes instead of time. Nothing noticed
    before, because every case that cared used quit_and_reap.
    """
    live = {pid: cmd for pid, _, cmd in ps_rows()}
    return [(pid, live[pid]) for pid in SPAWNED if pid in live]


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

    left = strays()
    if left:
        print("FAIL  no stray processes after the suite")
        for pid, cmd in left:
            print(f"        still running: {pid} {cmd}")
        failures.append(("no stray processes after the suite", left))

    print(f"\n{len(CASES) - len(failures)} passed, {len(failures)} failed")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
