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
    ALT_SCREEN_ENTER, ALT_SCREEN_EXIT, Drainer, spawn_in_pty, wait_for_exit, force_cleanup,
    descendants, server_child, settle_output, still_alive,
)
# focus_pane_id reads the status line the way the diffing renderer
# actually writes it: the "focus: [pane N" literal appears only in the
# frame that first drew it, and every later change rewrites the bare
# digit at a fixed column. Asserting on the literal alone is flaky here
# -- whether the first frame beats the server's opening snapshot is a
# race, and a reattach usually loses it, painting "pane 0" once before
# the real focus arrives.
from smoke import EMPTY_SYNC_UPDATE, focus_pane_id, prompts_seen

BIN = "./bin/wideboi"
COLS, ROWS = 80, 24

# The hang-up that acknowledges a shutdown comes only after the reap, so
# by the time kill-session or C-b q returns the panes are already gone.
# This is the allowance for the kernel to finish tearing them down, not
# for reaping; the reap takes seconds, so a hang-up that came before it
# blows through this.
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
    # SHELL and PS1 are pinned for the reason ptylib.spawn_in_pty pins
    # them, which only covers processes started on a pty. Server() is a
    # plain Popen, so without this its panes ran the developer's own
    # login shell: a themed zsh prints no "$", so waiting for prompts
    # timed out, and its line editor sometimes discarded text typed
    # while it was starting -- an intermittent "planted job never
    # appeared" that had nothing to do with wideboi.
    return {**os.environ, "WIDEBOI_SOCK": socket_path(),
            "SHELL": "/bin/sh", "PS1": "$ "}


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
    """An attached `wideboi attach` on a pty, with helpers to type.

    plain=True runs a plain `wideboi` instead, which attaches if a server
    answers and otherwise spawns one and owns the session."""

    def __init__(self, startup=PROMPT_WAIT, plain=False):
        argv = [BIN] if plain else [BIN, "attach"]
        self.pid, self.fd = spawn_in_pty(argv, COLS, ROWS, True,
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
    """`wideboi attach`'s side of smoke.py's 80-column case: the bar
    must offer detach here too. Asserted on the wire because the
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
    before it, and end the session itself -- escapees included."""
    c, srv = owned_session(fail)
    if srv is None:
        c.kill()
        return
    kids = []
    try:
        escapee = plant_escapee(c, srv, "987652")
        if escapee is None:
            fail("planted job never appeared; the leak assertion would be vacuous")
        kids = [p for p, _ in descendants(srv)]
        c.kill()
        # The server's own teardown: the pane grace plus the kill
        # residual, with headroom.
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


def plant_escapee(c, root_pid, tag, within=5.0):
    """Types a nohup'd background job into the focused pane and waits for
    it to appear under root_pid. Returns its pid, or None.

    The pane shells alone cannot show a skipped teardown: they exit on
    their own when their pty master closes. This job is in its own
    process group and ignores SIGHUP, so only the server's reaper gets
    it -- the same trick scripts/ptycheck.py uses.
    """
    # The focused shell must be at a prompt first. Attaching settles on
    # the client's own chrome, drawn well before a pane shell has
    # exec'd, and text typed into a shell that is still starting up can
    # be discarded. One prompt, not two: at 80x24 in the card layout
    # only the focused pane's is ever drawn, and waiting for a second
    # burned the whole ceiling every time.
    deadline = time.monotonic() + within
    while prompts_seen(c.output()) < 1 and time.monotonic() < deadline:
        time.sleep(0.05)
    c.type(f"nohup sleep {tag} >/dev/null 2>&1 &\r".encode())
    deadline = time.monotonic() + within
    while time.monotonic() < deadline:
        for pid, cmd in descendants(root_pid):
            if f"sleep {tag}" in cmd:
                return pid
        time.sleep(0.1)
    return None


def case_server_reaps_its_panes_on_signal(fail):
    """A background server that leaks shells on SIGTERM is worse than a
    foreground one: there is no window left to find them in."""
    srv = Server()
    escapee = None
    try:
        c = Client()
        if focus_pane_id(c.output(), ROWS) is None:
            fail("client never attached; teardown assertion would be vacuous")
        escapee = plant_escapee(c, srv.proc.pid, "987653")
        if escapee is None:
            fail("planted job never appeared under the server; the leak "
                 "assertion would be vacuous")
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
    return subprocess.run([BIN, "kill-session"], capture_output=True,
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


CASES = [
    ("attach renders pane content over the socket", case_attach_renders_pane_content),
    ("attached client emits no bytes while idle", case_attached_client_idle_emits_no_bytes),
    ("attached client presents no empty frames", case_attached_client_presents_no_empty_frames),
    ("styled output does not kill the connection", case_styled_output_does_not_kill_the_connection),
    ("attached control mode offers detach", case_attached_control_mode_offers_detach),
    ("detach leaves the session running", case_detach_leaves_the_session_running),
    ("quit from an attached client ends the session", case_quit_from_an_attached_client_ends_the_session),
    ("signalled attached client restores and detaches", case_signalled_attached_client_restores_and_detaches),
    ("a second server refuses to steal the socket", case_second_server_refuses_to_steal_the_socket),
    ("attach without a server says so", case_attach_without_a_server_says_so),
    ("server reaps its panes on signal", case_server_reaps_its_panes_on_signal),
    ("kill-session ends the session", case_kill_session_ends_the_session),
    ("kill-session without a server says so", case_kill_session_without_a_server_says_so),
    ("plain wideboi offers detach", case_plain_wideboi_offers_detach),
    ("plain wideboi detaches and the session survives", case_plain_wideboi_detaches_and_the_session_survives),
    ("SIGKILLed owner takes the session with it", case_sigkilled_owner_takes_the_session_with_it),
    ("plain wideboi attaches to a running server", case_plain_wideboi_attaches_to_a_running_server),
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
