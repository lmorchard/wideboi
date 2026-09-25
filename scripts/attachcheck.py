#!/usr/bin/env python3
"""Acceptance checks for the session lifecycle across processes:
`wideboi server`, `wideboi attach`, plain `wideboi` owning a session it
spawned, detach, quit and kill-session.

scripts/smoke.py used to drive an in-process binary and could not see
the socket at all, so nothing it asserted depended on a message
surviving serialisation. (Since #25 a plain wideboi is a client of a
server it spawns, so smoke is on the wire too.) That blind spot shipped
a defect that made `attach` unusable -- protocol.CellData.Style carried
a uv.Style, whose colour fields are interfaces, so the first coloured
cell a child printed failed to gob-encode, the server's write pump
returned, the socket closed, and the attached client exited with status
0 and no message. Every unit test passed throughout.

So these cases run the real pair of processes over a real socket, and
assert on the bytes the *attached client* writes to its pty -- the same
layer smoke.py uses, for the same reason. A case that only checked the
client stayed alive would have passed against that defect too, since
dying quietly is exactly what it did; the assertions here are about
content reaching the screen and surviving a detach.

Never hangs: every wait is bounded and every child is reaped.
"""

import argparse
import json
import os
import signal
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptylib import (
    ALT_SCREEN_ENTER, ALT_SCREEN_EXIT, Drainer, spawn_in_pty, wait_for_exit, force_cleanup,
    descendants, server_child, settle_output, still_alive,
    private_run_dir, run_main, harness_args,
)
# focus_pane_id reads the status line the way the diffing renderer
# actually writes it: the "focus: [pane N" literal appears only in the
# frame that first drew it, and every later change rewrites the bare
# digit at a fixed column. Asserting on the literal alone is flaky here
# -- whether the first frame beats the server's opening snapshot is a
# race, and a reattach usually loses it, painting "pane 0" once before
# the real focus arrives.
from smoke import CUP, EMPTY_SYNC_UPDATE, focus_pane_id

BIN = "./bin/wideboi"
COLS, ROWS = 80, 24

# The hang-up that acknowledges a shutdown comes only after every pane
# has been hung up and its shell has exited, so by the time kill-session
# or C-b q returns the panes are already gone. This is the allowance for
# the kernel to finish tearing them down, not for the hangup itself: a
# client hang-up that came before the panes' would blow through this.
REAPED_BY_ACK = 0.3

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
RUNTIME_DIR = private_run_dir(f"wideboi-attach-{os.getuid()}-")
# A real directory is needed here (unlike smoke's never-created path)
# because a server actually binds inside it, so it has to be removed
# again or every run leaves litter behind. Kept when a case fails: the
# servers' and clients' logs are written beside their sockets.


def runtime_dir() -> str:
    return RUNTIME_DIR


def socket_path() -> str:
    return os.path.join(runtime_dir(), "default.sock")


def bin_env() -> dict:
    """Environment for every wideboi this suite starts, so server,
    attach and the plain-binary probe all agree on one private path."""
    # SHELL and PS1 are pinned for the reason ptylib.spawn_in_pty pins
    # them, which only covers processes started on a pty. Server() is a
    # plain Popen, so without this its panes ran the developer's own
    # login shell: a themed zsh prints no "$", so waiting for prompts
    # timed out, and its line editor sometimes discarded text typed
    # while it was starting -- an intermittent "planted job never
    # appeared" that had nothing to do with wideboi.
    env = {k: v for k, v in os.environ.items() if not k.startswith("WIDEBOI_")}
    return {**env, "WIDEBOI_SOCK": socket_path(),
            "SHELL": "/bin/sh", "PS1": "$ "}


class Server:
    """A `wideboi server` in the background, with its socket bound."""

    def __init__(self, timeout=10.0, args=(), env=None, sock=None):
        self.sock = sock or socket_path()
        if os.path.exists(self.sock):
            os.remove(self.sock)
        self.proc = subprocess.Popen(
            harness_args(BIN, *args, "server"),
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            env=env or bin_env(),
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
    """An attached `wideboi attach` on a pty, with helpers to type.

    plain=True runs a plain `wideboi` instead, which attaches if a server
    answers and otherwise spawns one and owns the session."""

    def __init__(self, startup=PROMPT_WAIT, plain=False, args=(), gate=None, cols=COLS, rows=ROWS):
        argv = harness_args(BIN, *args) if plain else harness_args(BIN, "attach", *args)
        if gate is not None:
            # Held on opening the fifo until something opens its write
            # end, which releases every gated client at once.
            argv = ["/bin/sh", "-c", ': < "$0"; exec "$@"', gate, *argv]
        self.pid, self.fd = spawn_in_pty(argv, cols, rows, True,
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
                f"default.client.log and default.server.log in the runtime "
                f"dir, which is kept when the run fails."
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

    def quit(self, timeout=8.0) -> int | None:
        """Sends C-b q and waits for the client to exit on its own."""
        os.write(self.fd, b"\x02")
        time.sleep(0.4)
        os.write(self.fd, b"q")
        status = wait_for_exit(self.pid, timeout)
        if status is None:
            force_cleanup(self.pid)
        self.drainer.stop()
        return status

    def kill(self) -> None:
        self.drainer.stop()
        force_cleanup(self.pid)


def check_detach_notice(fail, out: bytes) -> None:
    """A detach must say the session is still running, and where, once
    the terminal is back -- otherwise wideboi just vanishes and nothing
    tells you it is still spending CPU in the background."""
    exit_at = out.rfind(ALT_SCREEN_EXIT)
    tail = out[exit_at:] if exit_at >= 0 else b""
    if b"still running" not in tail:
        fail("detaching printed no notice that the session is still running "
             "(or printed it inside the alt screen, where it is wiped)")
    if socket_path().encode() not in tail:
        fail("the detach notice does not name the session's socket")


def case_attach_renders_pane_content(fail):
    """The regression case. A shell prompt is coloured on any normal
    setup, so 'the prompt reached the screen' is also 'a styled cell
    survived the wire'."""
    srv = Server()
    try:
        c = Client()
        c.type(b"\x02w")
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
        c.type(b"\x02w")
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


def case_multibyte_title_does_not_kill_the_connection(fail):
    """#175. U+2733 is E2 9C B3, and 0x9C is also the C1 String
    Terminator: x/ansi cut the OSC after E2, the title became invalid
    UTF-8, protobuf refused the snapshot carrying it, the write pump
    closed the connection, and when that client owned the session the
    session ended. The title text is split in the typed command so the
    command's own echo cannot satisfy either check."""
    srv = Server()
    try:
        c = Client()
        c.type(b"printf '\\033]0;\\342\\234\\263 ti''tle-mk\\007'\r")
        c.type(b"echo title-survived\r")
        if not c.wait_for(lambda out: b"title-survived" in out):
            fail("the connection stopped carrying updates after a multibyte title")
        # The pane header draws the title, so "title-mk" belongs on
        # screen -- but only whole, after its U+2733. A bare occurrence is
        # the sequence's tail printed as text after the false ST.
        c.type(b"\x02w")
        out = c.output()
        whole = "\u2733 title-mk".encode()
        if whole not in out:
            fail("the pane header never showed the title intact")
        if out.count(b"title-mk") != out.count(whole):
            fail("the tail of the title leaked onto the screen as text")
        if wait_for_exit(c.pid, 0.5) is not None:
            fail("the attached client exited after a multibyte title")
        c.kill()
    finally:
        srv.stop()


def case_attached_control_mode_offers_detach(fail):
    """`wideboi attach`'s side of smoke.py's 80-column case: the bar
    must offer detach here too. Asserted on the wire because the
    unit test can only see the string the client builds, not whether
    this client is the one that built it."""
    srv = Server()
    try:
        c = Client()
        c.type(b"\x02", settle=1.0)
        c.type(b"\x02w")
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
        check_detach_notice(fail, first.output())

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


def case_quit_from_an_attached_client_ends_the_session(fail):
    """C-b q says "quit wideboi and close every pane", and from an
    attached client it used to detach instead. It ends the session from
    any client now; d is the only way to leave panes running."""
    srv = Server()
    try:
        c = Client()
        kids = [p for p, _ in descendants(srv.proc.pid)]
        if not kids:
            fail("server has no panes; the reap assertion would be vacuous")
        status = c.quit()
        if status is None:
            fail("C-b q did not exit the attached client")
        elif not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
            fail(f"quit exited badly: status {status}")
        left = still_alive(kids, REAPED_BY_ACK)
        if left:
            fail(f"quit returned while the server's descendants were alive: {left} "
                 f"-- the hang-up came before the reap")
        try:
            srv.proc.wait(timeout=8)
        except subprocess.TimeoutExpired:
            fail("C-b q from an attached client left the server running")
        if os.path.exists(srv.sock):
            fail("quit left the socket file behind")
    finally:
        srv.stop()


def case_signalled_attached_client_restores_and_detaches(fail):
    """A signal to an attached client restores the host terminal, dies by
    that signal, and leaves the session running -- it detaches. Before
    the client armed a guard it died with the terminal still in the alt
    screen."""
    srv = Server()
    try:
        c = Client()
        os.kill(c.pid, signal.SIGTERM)
        status = wait_for_exit(c.pid, 5.0)
        c.drainer.stop()
        if status is None:
            fail("attached client did not exit on SIGTERM")
            force_cleanup(c.pid)
        elif not os.WIFSIGNALED(status) or os.WTERMSIG(status) != signal.SIGTERM:
            fail(f"attached client did not die by SIGTERM: status {status}")
        if not c.drainer.saw(ALT_SCREEN_EXIT):
            fail("signalled attached client left the terminal in the alt screen")
        time.sleep(0.5)
        if not srv.alive():
            fail("signalling an attached client killed the server")
    finally:
        srv.stop()


def owned_session(fail):
    """Starts a plain wideboi with nothing listening, so it spawns and
    owns a server. Returns (client, server pid), or (client, None) after
    failing if the server never showed up."""
    sock = socket_path()
    if os.path.exists(sock):
        os.remove(sock)
    c = Client(plain=True)
    deadline = time.monotonic() + 5.0
    srv = server_child(c.pid)
    while srv is None and time.monotonic() < deadline:
        time.sleep(0.05)
        srv = server_child(c.pid)
    if srv is None:
        fail("plain wideboi never spawned a server")
    return c, srv


def reap_everything(pids):
    """Last-ditch cleanup, so a failing case does not leak into the next."""
    for pid in pids:
        try:
            os.kill(pid, signal.SIGKILL)
        except OSError:
            pass


def case_plain_wideboi_offers_detach(fail):
    """The point of #25: the commonest way to start wideboi is one you
    can detach from."""
    c, srv = owned_session(fail)
    try:
        c.type(b"\x02", settle=1.0)
        if b"d detach" not in c.output():
            fail("plain wideboi's control mode does not offer 'd detach'")
        c.type(b"\x1b", settle=0.5)
    finally:
        kids = [p for p, _ in descendants(c.pid)]
        c.kill()
        reap_everything(kids)


def case_plain_wideboi_detaches_and_the_session_survives(fail):
    """Detaching an owned session gives the ownership up and leaves
    everything running; a later attach sees it, and kill-session ends
    it."""
    c, srv = owned_session(fail)
    if srv is None:
        c.kill()
        return
    kids = []
    try:
        c.type(b"echo owned-marker\r")
        if b"owned-marker" not in c.output():
            fail("marker never rendered before detaching; the rest of this case is meaningless")
        kids = [p for p, _ in descendants(srv)]
        status = c.detach()
        if status is None:
            fail("C-b d did not exit the plain wideboi")
        elif not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
            fail(f"detach exited badly: status {status}")
        check_detach_notice(fail, c.output())
        time.sleep(0.5)
        if still_alive([srv], 0.0) != [srv]:
            fail("detaching the owner killed its server")
            return
        if not os.path.exists(socket_path()):
            fail("the detached session's socket is gone")

        second = Client(startup=SETTLE * 2)
        if not second.wait_for(lambda out: b"owned-marker" in out):
            fail("attaching after the owner detached does not show its output")
        # The reattached client does not own the session: killing it
        # outright must leave the session running.
        second.kill()
        time.sleep(0.5)
        if still_alive([srv], 0.0) != [srv]:
            fail("killing a reattached client ended the session; it re-owned it")
            return

        r = kill_session()
        if r.returncode != 0:
            fail(f"kill-session exited {r.returncode}")
        left = still_alive([srv, *kids], 2.0)
        if left:
            fail(f"kill-session left processes alive: {left}")
        if os.path.exists(socket_path()):
            fail("kill-session left the socket file behind")
    finally:
        reap_everything([srv, *kids])


def case_sigkilled_owner_takes_the_session_with_it(fail):
    """SIGKILL runs no code at all, so the owner cannot say anything.
    The server must notice the owner's connection end with no detach
    before it, and end the session itself."""
    c, srv = owned_session(fail)
    if srv is None:
        c.kill()
        return
    kids = []
    try:
        kids = [p for p, _ in descendants(srv)]
        c.kill()
        # The server's own teardown: the pane grace, with headroom.
        left = still_alive([srv, *kids], 6.0)
        if left:
            fail(f"a SIGKILLed owner left its session running: {left}")
        if os.path.exists(socket_path()):
            fail("the SIGKILLed owner's session left its socket behind")
    finally:
        reap_everything([srv, *kids])


def case_plain_wideboi_attaches_to_a_running_server(fail):
    """With a server already up, a plain wideboi attaches rather than
    starting a second session -- and does not own it, so its death is an
    ordinary detach."""
    srv = Server()
    try:
        c = Client(plain=True)
        if server_child(c.pid) is not None:
            fail("plain wideboi spawned a server despite one already running")
        if focus_pane_id(c.output(), ROWS) is None:
            fail("plain wideboi never drew the running session")
        c.kill()
        time.sleep(0.5)
        if not srv.alive():
            fail("killing a plain wideboi that attached to a running server killed the server")
    finally:
        srv.stop()


def case_two_plain_wideboi_at_once_share_one_session(fail):
    """#86: two plain wideboi on one free socket, released together.
    Both dials fail and both spawn a server; the loser's server finds
    the lock held. The user asked for a wideboi, so the loser attaches
    to the winner's session instead of reporting an error."""
    sock = socket_path()
    if os.path.exists(sock):
        os.remove(sock)
    fifo = os.path.join(runtime_dir(), "go.fifo")
    os.mkfifo(fifo)
    a = b = None
    try:
        a = Client(plain=True, gate=fifo, startup=0.3)
        b = Client(plain=True, gate=fifo, startup=0.3)
        # Opening the write end releases both `: < fifo` at once.
        os.close(os.open(fifo, os.O_WRONLY))
        for name, c in (("first", a), ("second", b)):
            if not c.wait_for(lambda out: focus_pane_id(out, ROWS) is not None):
                fail(f"the {name} plain wideboi never drew a session")
        for name, c in (("first", a), ("second", b)):
            if wait_for_exit(c.pid, 0.1) is not None:
                fail(f"the {name} plain wideboi exited instead of attaching: "
                     f"{c.output()[-300:]!r}")
        # Exactly one spawned server may remain; the loser's exits at once.
        def servers():
            return [s for s in (server_child(a.pid), server_child(b.pid)) if s]
        deadline = time.monotonic() + 3.0
        while len(servers()) != 1 and time.monotonic() < deadline:
            time.sleep(0.05)
        if len(servers()) != 1:
            fail(f"want exactly one server, found {len(servers())}")
            return
        # Type into the winner and look for it on the loser. Not the
        # other way round: the loser's first frame paints before its
        # doomed connection ends, and keys typed while it switches to
        # the winner's socket are lost with the old connection.
        winner, loser = (a, b) if server_child(a.pid) else (b, a)
        winner.type(b"echo race-marker\r")
        if not loser.wait_for(lambda out: b"race-marker" in out):
            fail("the two clients are not in the same session")
        if not os.path.exists(sock):
            fail("the session's socket file is gone")
    finally:
        pids = []
        for c in (a, b):
            if c is not None:
                pids += [p for p, _ in descendants(c.pid)]
                c.kill()
        reap_everything(pids)
        os.remove(fifo)


def case_named_sessions_are_independent(fail):
    """#27: two names are two sessions. ls lists both, and ending one
    leaves the other.

    Named sessions live in $TMPDIR/wideboi-<uid>/, so TMPDIR points at a
    directory private to this case -- a short one under /tmp, because
    sun_path is 104 bytes and the default temp dir under /var/folders
    plus wideboi-<uid>/<name>.sock gets close."""
    tmp = private_run_dir("wb", parent="/tmp")
    env = {k: v for k, v in bin_env().items() if k != "WIDEBOI_SOCK"}
    env["TMPDIR"] = tmp
    sdir = os.path.join(tmp, f"wideboi-{os.getuid()}")
    alpha = beta = None
    try:
        alpha = Server(args=("-L", "alpha"), env=env, sock=os.path.join(sdir, "alpha.sock"))
        beta = Server(args=("-L", "beta"), env=env, sock=os.path.join(sdir, "beta.sock"))
        ls = subprocess.run(harness_args(BIN, "ls"), capture_output=True, timeout=10, env=env)
        if ls.returncode != 0 or ls.stdout.decode().split() != ["alpha", "beta"]:
            fail(f"ls exited {ls.returncode} printing {ls.stdout!r}, want alpha and beta")
        r = subprocess.run(harness_args(BIN, "-L", "alpha", "kill-session"),
                           capture_output=True, timeout=15, env=env)
        if r.returncode != 0:
            fail(f"kill-session -L alpha exited {r.returncode}: "
                 f"{(r.stdout + r.stderr).decode(errors='replace').strip()!r}")
        try:
            alpha.proc.wait(timeout=8)
        except subprocess.TimeoutExpired:
            fail("alpha still running after kill-session -L alpha")
        if not beta.alive():
            fail("ending alpha ended beta")
        ls = subprocess.run(harness_args(BIN, "ls"), capture_output=True, timeout=10, env=env)
        if ls.stdout.decode().split() != ["beta"]:
            fail(f"after the kill, ls printed {ls.stdout!r}, want beta")
    finally:
        for s in (alpha, beta):
            if s is not None:
                s.stop()


def case_second_server_refuses_to_steal_the_socket(fail):
    """Binding over a live server's path orphans its panes invisibly:
    the old process keeps its clients, the new one owns the name."""
    srv = Server()
    try:
        second = subprocess.run(
            harness_args(BIN, "server"), capture_output=True, timeout=10, env=bin_env(),
        )
        if second.returncode != 3:
            fail(f"second server exited {second.returncode}, want 3 (session taken)")
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
    r = subprocess.run(harness_args(BIN, "attach"), capture_output=True, timeout=10,
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
        for pid in srv.leaked:
            try:
                os.kill(pid, signal.SIGKILL)
            except OSError:
                pass


def kill_session() -> subprocess.CompletedProcess:
    return subprocess.run(harness_args(BIN, "kill-session"), capture_output=True,
                          timeout=15, env=bin_env())


def case_kill_session_ends_the_session(fail):
    """kill-session ends the whole session: the server exits, everything
    it spawned is reaped, the socket is removed, and an attached client
    is hung up on and exits cleanly rather than being left staring at a
    dead connection."""
    srv = Server()
    c = None
    try:
        c = Client()
        kids = [p for p, _ in descendants(srv.proc.pid)]
        if not kids:
            fail("server has no panes; the reap assertion would be vacuous")
        r = kill_session()
        if r.returncode != 0:
            fail(f"kill-session exited {r.returncode}: "
                 f"{(r.stdout + r.stderr).decode(errors='replace').strip()!r}")
        left = still_alive(kids, REAPED_BY_ACK)
        if left:
            fail(f"kill-session returned while the server's descendants were alive: {left} "
                 f"-- the hang-up came before the reap")
        try:
            srv.proc.wait(timeout=8)
        except subprocess.TimeoutExpired:
            fail("server still running after kill-session")
        if os.path.exists(srv.sock):
            fail("kill-session left the socket file behind")
        status = wait_for_exit(c.pid, 3.0)
        if status is None:
            fail("attached client kept running after the session ended")
        elif not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
            fail(f"attached client exited badly after kill-session: status {status}")
    finally:
        if c is not None:
            c.kill()
        srv.stop()


def case_kill_session_without_a_server_says_so(fail):
    sock = socket_path()
    if os.path.exists(sock):
        os.remove(sock)
    r = kill_session()
    if r.returncode == 0:
        fail("kill-session succeeded with no server running")
    msg = (r.stdout + r.stderr).decode(errors="replace")
    if "no wideboi server running" not in msg:
        fail(f"kill-session error does not say nothing is running: {msg.strip()!r}")


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


def cursor_col(out: bytes) -> int | None:
    """Column of the last cursor move: the focused pane's cursor, which
    is where the layout put that pane."""
    moves = CUP.findall(out)
    return int(moves[-1][1]) if moves else None


def case_toggle_affects_only_its_own_client(fail):
    """Layout is presentation, and presentation is per-client (#92):
    C-b c in one attached client must leave another alone."""
    srv = Server()
    a = b = None
    try:
        a = Client()
        b = Client()
        a.type(b"\x02n")                  # a third pane, so there is a fan
        settle_output(b.drainer, timeout=SETTLE)
        a_before, b_before = cursor_col(a.output()), cursor_col(b.output())

        a.type(b"\x02c")
        settle_output(b.drainer, timeout=SETTLE)
        a_after, b_after = cursor_col(a.output()), cursor_col(b.output())

        if a_after == a_before:
            fail(f"the toggle left client a's cursor at column {a_before}; "
                 "the layouts coincide at this size and the case is vacuous")
        if b_after != b_before:
            fail(f"toggling in client a moved client b's cursor from column "
                 f"{b_before} to {b_after}; the layout is still session state")
    finally:
        for c in (a, b):
            if c is not None:
                c.kill()
        srv.stop()


def case_focus_affects_only_its_own_client(fail):
    """A focus change and later layout broadcast cannot move another client."""
    srv = Server()
    a = b = None
    try:
        a = Client()
        b = Client()
        if focus_pane_id(a.output(), ROWS) != 1 or focus_pane_id(b.output(), ROWS) != 1:
            fail("clients did not both start on pane 1")
            return
        a.type(b"\x02l")
        if focus_pane_id(a.output(), ROWS) != 2:
            fail("client a did not focus pane 2")
        if focus_pane_id(b.output(), ROWS) != 1:
            fail("client a's focus change moved client b")
        a.type(b"\x02w")  # width change broadcasts a fresh snapshot
        settle_output(b.drainer, timeout=SETTLE)
        if focus_pane_id(a.output(), ROWS) != 2 or focus_pane_id(b.output(), ROWS) != 1:
            fail("the width snapshot changed one client's local focus")
        a.type(b"\x02n")
        settle_output(b.drainer, timeout=SETTLE)
        if focus_pane_id(a.output(), ROWS) != 3 or focus_pane_id(b.output(), ROWS) != 1:
            fail("the new pane did not focus only in its requesting client")
    finally:
        for c in (a, b):
            if c is not None:
                c.kill()
        srv.stop()


def case_attach_layout_flag_is_honoured(fail):
    """An attaching client starts in the layout its own config names.
    Before #92 attach resolved --layout and threw it away."""
    srv = Server()
    clients = []
    try:
        a = Client()
        clients.append(a)
        a.type(b"\x02n")
        scroll = Client(args=["--layout", "scroll"])
        clients.append(scroll)
        cards = Client(args=["--layout", "cards"])
        clients.append(cards)
        # Focus is local to each client. Compare the two layouts while
        # all three clients are looking at the same pane.
        scroll.type(b"\x022")  # new pane 3 is inserted at position 2
        cards.type(b"\x022")
        a_col = cursor_col(a.output())
        scroll_col = cursor_col(scroll.output())
        cards_col = cursor_col(cards.output())

        if cards_col != a_col:
            fail(f"an explicit --layout cards client sits at column {cards_col}, "
                 f"the default client at {a_col}; the comparison below means nothing")
        if scroll_col == a_col:
            fail(f"attach --layout scroll put the cursor at column {scroll_col}, "
                 "the same as the cards client; the flag was ignored")
    finally:
        for c in clients:
            c.kill()
        srv.stop()


def case_reattach_starts_from_the_configured_layout(fail):
    """A toggle belongs to the client that made it and goes when it
    detaches. The next attach starts from config, not from whatever the
    last client left behind -- which is how an accidental C-b c used to
    outlive the terminal it was pressed in."""
    srv = Server()
    c = None
    try:
        a = Client()
        a.type(b"\x02n")
        col0 = cursor_col(a.output())
        a.type(b"\x02c")
        col1 = cursor_col(a.output())
        if col1 == col0:
            fail(f"the toggle left the cursor at column {col0}; the case is vacuous")
        status = a.detach()
        if status is None:
            fail("C-b d did not exit the client")
            return

        c = Client(startup=SETTLE * 2)
        c.type(b"\x022")  # restore the same local focus for the comparison
        col2 = cursor_col(c.output())
        if col2 != col0:
            fail(f"reattached client's cursor is at column {col2}, want the "
                 f"configured layout's {col0} (the toggled layout put it at {col1})")
    finally:
        if c is not None:
            c.kill()
        srv.stop()


def case_dropped_connection_reconnects(fail):
    srv = Server()
    try:
        c = Client(args=("--socket", srv.sock, "attach"))
        try:
            c.wait_for(lambda out: b"[pane 1]" in out)
            
            # Kill the server hard
            srv.proc.kill()
            srv.proc.wait(timeout=1.0)
            
            # Start a new server on the same socket
            srv2 = Server(sock=srv.sock)
            try:
                c.type(b"echo SURVIVED\r")
                c.wait_for(lambda out: b"SURVIVED" in out, timeout=4.0)
                c.detach()
            finally:
                srv2.stop()
        finally:
            c.kill()
    finally:
        srv.stop()

def case_small_client_does_not_shrink_session_and_can_claim_size(fail):
    """A smaller client attaching as a viewer does not shrink the hosted PTYs (#184).
    Claiming size with C-b S explicitly adopts the smaller client's dimensions."""
    srv = Server()
    a = b = None
    try:
        a = Client(cols=80, rows=24)
        time.sleep(0.1)

        def get_col_height():
            raw = subprocess.check_output([BIN, "--socket", socket_path(), "status", "--json"])
            data = json.loads(raw)
            cols = data.get("columns", [])
            return cols[0]["Height"] if cols else None

        h0 = get_col_height()
        if h0 != 22:
            fail(f"initial column height = {h0}, want 22")
            return

        # Smaller client attaches at 50x14
        b = Client(cols=50, rows=14)
        settle_output(b.drainer, timeout=SETTLE)

        h1 = get_col_height()
        if h1 != 22:
            fail(f"viewer attached and shrunk column height to {h1}, want 22")
            return

        # Client b claims size via C-b S
        b.type(b"\x02S")
        settle_output(b.drainer, timeout=SETTLE)
        time.sleep(0.1)

        h2 = get_col_height()
        if h2 != 12:
            fail(f"after claim size, column height = {h2}, want 12 (50x14 availHeight)")
            return
    finally:
        for c in (a, b):
            if c is not None:
                c.kill()
        srv.stop()


CASES = [
    ("dropped connection reconnects", case_dropped_connection_reconnects),
    ("attach renders pane content over the socket", case_attach_renders_pane_content),
    ("attached client emits no bytes while idle", case_attached_client_idle_emits_no_bytes),
    ("attached client presents no empty frames", case_attached_client_presents_no_empty_frames),
    ("styled output does not kill the connection", case_styled_output_does_not_kill_the_connection),
    ("multibyte title does not kill the connection", case_multibyte_title_does_not_kill_the_connection),
    ("attached control mode offers detach", case_attached_control_mode_offers_detach),
    ("detach leaves the session running", case_detach_leaves_the_session_running),
    ("quit from an attached client ends the session", case_quit_from_an_attached_client_ends_the_session),
    ("signalled attached client restores and detaches", case_signalled_attached_client_restores_and_detaches),
    ("a second server refuses to steal the socket", case_second_server_refuses_to_steal_the_socket),
    ("named sessions are independent", case_named_sessions_are_independent),
    ("attach without a server says so", case_attach_without_a_server_says_so),
    ("server reaps its panes on signal", case_server_reaps_its_panes_on_signal),
    ("kill-session ends the session", case_kill_session_ends_the_session),
    ("kill-session without a server says so", case_kill_session_without_a_server_says_so),
    ("plain wideboi offers detach", case_plain_wideboi_offers_detach),
    ("plain wideboi detaches and the session survives", case_plain_wideboi_detaches_and_the_session_survives),
    ("SIGKILLed owner takes the session with it", case_sigkilled_owner_takes_the_session_with_it),
    ("plain wideboi attaches to a running server", case_plain_wideboi_attaches_to_a_running_server),
    ("two plain wideboi at once share one session", case_two_plain_wideboi_at_once_share_one_session),
    ("layout toggle affects only its own client", case_toggle_affects_only_its_own_client),
    ("focus affects only its own client", case_focus_affects_only_its_own_client),
    ("attach honours its own layout flag", case_attach_layout_flag_is_honoured),
    ("reattach starts from the configured layout", case_reattach_starts_from_the_configured_layout),
    ("small client does not shrink session and can claim size", case_small_client_does_not_shrink_session_and_can_claim_size),
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
    run_main(main)
