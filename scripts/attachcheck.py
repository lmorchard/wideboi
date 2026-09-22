#!/usr/bin/env python3
"""Acceptance checks for `wideboi server` / `wideboi attach` / detach.

scripts/smoke.py drives the in-process binary and cannot see this seam
at all: it never opens a socket, so nothing it asserts depends on a
message surviving serialisation. That blind spot shipped a defect that
made `attach` unusable -- protocol.CellData.Style carried a uv.Style,
whose colour fields are interfaces, so the first coloured cell a child
printed failed to gob-encode, the server's write pump returned, the
socket closed, and the attached client exited with status 0 and no
message. Every unit test passed throughout.

So these cases run the real pair of processes over a real socket, and
assert on the bytes the *attached client* writes to its pty -- the same
layer smoke.py uses, for the same reason. A case that only checked the
client stayed alive would have passed against that defect too, since
dying quietly is exactly what it did; the assertions here are about
content reaching the screen and surviving a detach.

Never hangs: every wait is bounded and every child is reaped.
"""

import argparse
import atexit
import os
import shutil
import signal
import subprocess
import sys
import tempfile
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptylib import (
    ALT_SCREEN_ENTER, Drainer, spawn_in_pty, wait_for_exit, force_cleanup,
    descendants, settle_output, still_alive,
)
# focus_pane_id reads the status line the way the diffing renderer
# actually writes it: the "focus: [pane N" literal appears only in the
# frame that first drew it, and every later change rewrites the bare
# digit at a fixed column. Asserting on the literal alone is flaky here
# -- whether the first frame beats the server's opening snapshot is a
# race, and a reattach usually loses it, painting "pane 0" once before
# the real focus arrives.
from smoke import EMPTY_SYNC_UPDATE, focus_pane_id

BIN = "./bin/wideboi"
COLS, ROWS = 80, 24

# Ceilings, not durations. Both are now the timeout handed to
# settle_output, which returns as soon as the screen stops changing.
#
# PROMPT_WAIT stays generous for a reason that has changed: it used to
# be waiting out whatever $SHELL was on the developer's machine, and a
# themed zsh can take seconds to reach its first prompt. ptylib pins
# SHELL=/bin/sh now, so that is no longer the risk -- but a cold CI
# runner still is, and an unused ceiling costs nothing.
PROMPT_WAIT = 8.0
SETTLE = 1.5


# Per-run, not the per-uid default. The default path is machine-global,
# so two runs of this suite -- or a run alongside the developer's own
# session -- fight over the same name, and a plain wideboi started
# anywhere attaches to whichever server holds it. Cases here assert
# *exclusive* ownership of the path ("a second server refuses to steal
# the socket", "attach without a server says so"), so the path has to
# belong to this run alone. Passed to the binary as WIDEBOI_SOCK.
RUNTIME_DIR = tempfile.mkdtemp(prefix=f"wideboi-attach-{os.getuid()}-")
# A real directory is needed here (unlike smoke's never-created path)
# because a server actually binds inside it, so it has to be removed
# again or every run leaves litter behind.
atexit.register(shutil.rmtree, RUNTIME_DIR, True)


def runtime_dir() -> str:
    return RUNTIME_DIR


def socket_path() -> str:
    return os.path.join(runtime_dir(), "default.sock")


def bin_env() -> dict:
    """Environment for every wideboi this suite starts, so server,
    attach and the plain-binary probe all agree on one private path."""
    return {**os.environ, "WIDEBOI_SOCK": socket_path()}


class Server:
    """A `wideboi server` in the background, with its socket bound."""

    def __init__(self, timeout=10.0):
        self.sock = socket_path()
        if os.path.exists(self.sock):
            os.remove(self.sock)
        self.proc = subprocess.Popen(
            [BIN, "server"],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            env=bin_env(),
        )
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if os.path.exists(self.sock):
                return
            if self.proc.poll() is not None:
                raise RuntimeError(f"server exited early: {self.output()}")
            time.sleep(0.05)
        raise RuntimeError(f"server never bound {self.sock}")

    def alive(self) -> bool:
        return self.proc.poll() is None

    def output(self) -> str:
        try:
            return (self.proc.stdout.read() or b"").decode(errors="replace")
        except Exception:
            return ""

    def stop(self) -> None:
        if self.proc.poll() is None:
            kids = [p for p, _ in descendants(self.proc.pid)]
            self.proc.send_signal(signal.SIGTERM)
            try:
                self.proc.wait(timeout=8)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait(timeout=3)
            self.leaked = still_alive(kids, 2.0)
        if os.path.exists(self.sock):
            os.remove(self.sock)


class Client:
    """An attached `wideboi attach` on a pty, with helpers to type."""

    def __init__(self, startup=PROMPT_WAIT):
        self.pid, self.fd = spawn_in_pty([BIN, "attach"], COLS, ROWS, True,
                                         {"WIDEBOI_SOCK": socket_path()})
        self.drainer = Drainer(self.fd)
        self.drainer.start()
        settle_output(self.drainer, timeout=startup)

    def type(self, text: bytes, settle=SETTLE) -> None:
        # A client that has already exited closes the pty slave, so the
        # write fails with EIO. Say that, rather than letting an opaque
        # OSError stand in for the failure this suite exists to catch:
        # dying quietly mid-session is precisely the defect's signature.
        try:
            os.write(self.fd, text)
        except OSError as exc:
            raise AssertionError(
                f"the attached client is gone -- writing {text!r} to its pty "
                f"failed with {exc}. It exited on its own, which is what a "
                f"transport-level failure looks like from here; check "
                f"client.log and server.log in the runtime dir."
            ) from exc
        settle_output(self.drainer, timeout=settle)

    def output(self) -> bytes:
        return self.drainer.output()

    def wait_for(self, predicate, timeout=10.0, interval=0.1):
        """Polls predicate(self.output()) until it is truthy or timeout.

        Returns the last value. Fixed sleeps are a race here: the
        reattached client paints one frame of `focus: [pane 0]` before
        the server's snapshot lands, and how long that takes depends on
        machine load -- attach-check runs last in `make check`, behind
        verify-exit and the whole smoke suite. Polling to a deadline
        removes the race without hiding a real failure: a reattach that
        genuinely never reports focus still fails, at the deadline.
        """
        deadline = time.monotonic() + timeout
        value = predicate(self.output())
        while not value and time.monotonic() < deadline:
            time.sleep(interval)
            value = predicate(self.output())
        return value

    def detach(self, timeout=6.0) -> int | None:
        """Sends C-b d and waits for the client to exit on its own."""
        os.write(self.fd, b"\x02")
        time.sleep(0.4)
        os.write(self.fd, b"d")
        status = wait_for_exit(self.pid, timeout)
        if status is None:
            force_cleanup(self.pid)
        self.drainer.stop()
        return status

    def kill(self) -> None:
        self.drainer.stop()
        force_cleanup(self.pid)


def case_attach_renders_pane_content(fail):
    """The regression case. A shell prompt is coloured on any normal
    setup, so 'the prompt reached the screen' is also 'a styled cell
    survived the wire'."""
    srv = Server()
    try:
        c = Client()
        out = c.output()
        if ALT_SCREEN_ENTER not in out:
            fail("attached client never entered the alt screen")
        if "│".encode() not in out and "┃".encode() not in out:
            fail("no column divider -- the client drew no layout")
        if focus_pane_id(out, ROWS) is None:
            fail("no status line -- the client drew no chrome")
        c.type(b"echo attach-marker-1\r")
        if b"attach-marker-1" not in c.output():
            fail("typed text never came back from the pane: "
                 "either input or pane updates are not crossing the socket")
        c.kill()
    finally:
        srv.stop()


def case_styled_output_does_not_kill_the_connection(fail):
    """Drives explicit SGR through the pane rather than trusting the
    prompt to be coloured: /bin/sh on a bare CI box has no colour at
    all, and this case must not quietly stop testing anything there.

    Each escape is a different concrete color.Color on the server side
    -- ansi.BasicColor for 31, ansi.IndexedColor for 38;5, color.RGBA
    for 38;2 -- which is the axis the original defect failed along."""
    srv = Server()
    try:
        c = Client()
        c.type(b"printf '\\033[31mRED\\033[m \\033[38;5;200mIDX\\033[m \\033[38;2;1;2;3mRGB\\033[m\\n'\r")
        c.type(b"echo colour-survived\r")
        out = c.output()
        for needle in (b"RED", b"IDX", b"RGB"):
            if needle not in out:
                fail(f"{needle!r} never reached the screen")
        if b"colour-survived" not in out:
            fail("the connection stopped carrying updates after styled output")
        if wait_for_exit(c.pid, 0.5) is not None:
            fail("the attached client exited while styled output was on screen")
        c.kill()
    finally:
        srv.stop()


def case_attached_control_mode_offers_detach(fail):
    """The mirror of smoke.py's in-process case: over a socket the verb
    is real, so the bar must say so. Asserted on the wire because the
    unit test can only see the string the client builds, not whether
    this client is the one that built it."""
    srv = Server()
    try:
        c = Client()
        c.type(b"\x02", settle=1.0)
        out = c.output()
        if b"d detach" not in out:
            fail("attached control mode does not offer 'd detach'")
        for verb in (b"q quit", b"esc exit"):
            if verb not in out:
                fail(f"attached control mode never shows {verb.decode()!r}")
        c.type(b"\x1b", settle=0.5)
        c.kill()
    finally:
        srv.stop()


def case_detach_leaves_the_session_running(fail):
    """C-b d must exit the client cleanly and leave the server, its
    panes, and their scrollback alone -- then a fresh attach must show
    what the first one left behind."""
    srv = Server()
    try:
        first = Client()
        first.type(b"echo survives-detach\r")
        if b"survives-detach" not in first.output():
            fail("marker never rendered before detaching; the rest of this case is meaningless")

        status = first.detach()
        if status is None:
            fail("C-b d did not exit the client")
        elif not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
            fail(f"detach exited badly: status {status}")

        if not srv.alive():
            fail("detaching killed the server: " + srv.output())
            return

        second = Client(startup=SETTLE * 2)
        if not second.wait_for(lambda out: b"survives-detach" in out):
            fail("reattached client does not show the pre-detach output; "
                 "the session was not preserved")
        if not second.wait_for(lambda out: focus_pane_id(out, ROWS) == 1):
            fail("reattached client does not report pane 1 focused")
        second.kill()
    finally:
        srv.stop()


def case_second_server_refuses_to_steal_the_socket(fail):
    """Binding over a live server's path orphans its panes invisibly:
    the old process keeps its clients, the new one owns the name."""
    srv = Server()
    try:
        second = subprocess.run(
            [BIN, "server"], capture_output=True, timeout=10, env=bin_env(),
        )
        if second.returncode == 0:
            fail("a second server started while the first was listening")
        msg = (second.stdout + second.stderr).decode(errors="replace")
        if "already listening" not in msg:
            fail(f"second server failed for the wrong reason: {msg.strip()!r}")
        if not srv.alive():
            fail("the first server died when the second one tried to bind")
    finally:
        srv.stop()


def case_attach_without_a_server_says_so(fail):
    """The failure a user hits most often should name the fix."""
    sock = socket_path()
    if os.path.exists(sock):
        os.remove(sock)
    r = subprocess.run([BIN, "attach"], capture_output=True, timeout=10,
                       env=bin_env())
    if r.returncode == 0:
        fail("attach succeeded with no server running")
    msg = (r.stdout + r.stderr).decode(errors="replace")
    if "wideboi server" not in msg:
        fail(f"error does not tell the user how to start one: {msg.strip()!r}")


def case_server_reaps_its_panes_on_signal(fail):
    """A background server that leaks shells on SIGTERM is worse than a
    foreground one: there is no window left to find them in."""
    srv = Server()
    try:
        c = Client()
        if focus_pane_id(c.output(), ROWS) is None:
            fail("client never attached; teardown assertion would be vacuous")
        c.kill()
    finally:
        srv.stop()
    if getattr(srv, "leaked", None):
        fail(f"server left descendants alive after SIGTERM: {srv.leaked}")


def case_attached_client_idle_emits_no_bytes(fail):
    """Verifies that wideboi attach emits 0 bytes to its pty while completely idle."""
    srv = Server()
    try:
        c = Client()
        if not settle_output(c.drainer, timeout=5.0):
            fail("attached client never settled after startup")
            c.kill()
            return
        initial_len = len(c.output())
        time.sleep(0.5)
        idle_bytes = len(c.output()) - initial_len
        if idle_bytes > 0:
            fail(f"attached client emitted {idle_bytes} bytes over 0.5s while idle")
        c.kill()
    finally:
        srv.stop()


def case_attached_client_presents_no_empty_frames(fail):
    """The attach frame loop is a separate copy of smoke's, so it needs
    its own check that a frame is presented only when it changed (#77).
    See EMPTY_SYNC_UPDATE in smoke.py for the signature."""
    srv = Server()
    try:
        c = Client()
        c.type(b"echo attach-busy-frames\r")
        n = c.output().count(EMPTY_SYNC_UPDATE)
        if n:
            fail(f"{n} empty synchronized updates written -- a frame was presented with nothing changed")
        c.kill()
    finally:
        srv.stop()


CASES = [
    ("attach renders pane content over the socket", case_attach_renders_pane_content),
    ("attached client emits no bytes while idle", case_attached_client_idle_emits_no_bytes),
    ("attached client presents no empty frames", case_attached_client_presents_no_empty_frames),
    ("styled output does not kill the connection", case_styled_output_does_not_kill_the_connection),
    ("attached control mode offers detach", case_attached_control_mode_offers_detach),
    ("detach leaves the session running", case_detach_leaves_the_session_running),
    ("a second server refuses to steal the socket", case_second_server_refuses_to_steal_the_socket),
    ("attach without a server says so", case_attach_without_a_server_says_so),
    ("server reaps its panes on signal", case_server_reaps_its_panes_on_signal),
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

    ran = len(CASES) if not args.only else sum(1 for n, _ in CASES if args.only in n)
    print(f"\n{ran - len(failures)} passed, {len(failures)} failed")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
