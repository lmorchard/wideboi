#!/usr/bin/env python3
"""Shared pty plumbing for wideboi's out-of-process checks.

Not a test in itself. scripts/ptycheck.py asserts the signal-exit contract
with it; scripts/smoke.py drives user journeys with it.

The one rule that matters here: ALWAYS drain the master. wideboi's render
loop writes frames to the pty, and a full buffer blocks that write, which
looks exactly like a hang in the thing under test.
"""

from __future__ import annotations

import argparse
import fcntl
import os
import pty
import re
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import threading
import time

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


def settle_output(drainer: "Drainer", timeout: float, quiet: float = 0.25,
                  poll: float = 0.01) -> bool:
    """Waits until the drained output has been unchanged for `quiet`
    seconds, or `timeout` elapses. Returns whether it settled.

    The timeout is a ceiling, not a duration. Callers pass what used to
    be a fixed sleep and get it as the worst case instead of the every
    case: measured, a keystroke settles in 0.10-0.28s against a fixed
    0.8s, and startup in 0.154s against a fixed 1.2s. A loaded CI
    runner takes longer rather than failing, which is the other half of
    the point -- two CI failures this week were fixed sleeps being too
    short on a slower machine.
    """
    def size() -> int:
        return len(drainer.output())

    deadline = time.monotonic() + timeout
    last, last_change = size(), time.monotonic()
    while time.monotonic() < deadline:
        time.sleep(poll)
        n = size()
        if n != last:
            last, last_change = n, time.monotonic()
        elif last > 0 and time.monotonic() - last_change >= quiet:
            # last > 0 matters at startup: a process that has not
            # produced its first byte yet is not "settled", it has not
            # begun. Without this the very first call returns after one
            # quiet window against an empty buffer, and the caller
            # asserts on a screen that was never drawn -- which on a
            # cold runner is exactly when it would happen, and exactly
            # what the generous startup ceiling was there to prevent.
            return True
    return False


def parse_size(text: str) -> tuple[int, int]:
    m = re.fullmatch(r"(\d+)x(\d+)", text)
    if not m:
        raise argparse.ArgumentTypeError(f"size must be COLSxROWS, got {text!r}")
    return int(m.group(1)), int(m.group(2))


def parse_signal(text: str) -> int:
    name = text.upper()
    if not name.startswith("SIG"):
        name = "SIG" + name
    try:
        return signal.Signals[name].value
    except KeyError:
        pass
    try:
        return int(text)
    except ValueError:
        raise argparse.ArgumentTypeError(f"unrecognized signal {text!r}")


def spawn_in_pty(argv: list[str], cols: int, rows: int, set_winsize: bool,
                 env: dict[str, str] | None = None) -> tuple[int, int]:
    """Forks argv onto a fresh pty, as the session leader with that pty as
    its controlling terminal. Winsize (if any) is applied to the pty
    before the fork, so the child can never observe an unset-then-set
    race -- it either sees the size from the moment it can ask, or (when
    set_winsize is False) never sees one at all, matching a pty that
    genuinely never had TIOCSWINSZ called on it.

    env, if given, overlays the inherited environment in the child.

    Returns (child_pid, master_fd) in the calling (parent) process.
    """
    master_fd, slave_fd = pty.openpty()

    if set_winsize:
        winsize = struct.pack("HHHH", rows, cols, 0, 0)
        fcntl.ioctl(slave_fd, termios.TIOCSWINSZ, winsize)

    pid = os.fork()
    if pid == 0:
        try:
            os.close(master_fd)
            os.setsid()
            fcntl.ioctl(slave_fd, termios.TIOCSCTTY, 0)
            os.dup2(slave_fd, 0)
            os.dup2(slave_fd, 1)
            os.dup2(slave_fd, 2)
            if slave_fd > 2:
                os.close(slave_fd)
            # Pinned for the same reason SHELL/TERM/PS1 are below: the
            # assertions depend on it, so it cannot be whatever the
            # person -- or the shell -- running the suite happens to
            # hand us.
            #
            # POSIX requires a shell to set SIGINT and SIGQUIT to
            # SIG_IGN for an asynchronous list, so everything spawned
            # from a `cmd &` inherits them ignored, and that includes
            # every target under `make -j`. ptycheck asserts a process
            # "died by" the signal it was sent, which is not something
            # that can happen under a disposition where the signal does
            # nothing -- so the disposition has to be pinned, not the
            # assertion relaxed.
            #
            # This has to be after the fork and before execvpe: it must
            # not touch the harness process's own handlers.
            for _sig in (signal.SIGINT, signal.SIGQUIT,
                         signal.SIGTERM, signal.SIGHUP):
                signal.signal(_sig, signal.SIG_DFL)
            child_env = dict(os.environ)
            child_env["SHELL"] = "/bin/sh"
            # Pinned for the same reason SHELL is: the assertions
            # depend on it, so it cannot be whatever the person
            # running the suite happens to have.
            #
            # A GitHub runner sets no TERM at all, and ultraviolet
            # then emits a plainer stream -- the status bar still
            # renders its text but without the SGR 7 that makes the
            # inversion assertable, and the divider draws without the
            # absolute cursor move divider_columns matches on. Two
            # smoke cases failed on the first CI run for exactly that,
            # and reproduce locally under `env -u TERM`.
            #
            # Matches what internal/server/ptyx gives the panes.
            child_env["TERM"] = "xterm-256color"
            # And PS1, for the third time the same reason.
            #
            # /bin/sh is bash on macOS and dash on Linux, and their
            # default prompts differ -- "sh-3.2$" against a bare "$".
            # The golden snapshot records every word wideboi renders,
            # so the macOS prompt was baked into it as the token
            # "sh-3" and CI failed on its absence. The prompt is the
            # child's output, not wideboi's chrome, and the snapshot
            # exists to pin the chrome.
            child_env["PS1"] = "$ "
            # And the layout, for the fourth. Several assertions and the
            # golden snapshot expect the built-in default (cards) -- the
            # status line's layout tag, and attachcheck's comparisons
            # between clients -- so neither WIDEBOI_LAYOUT nor a
            # developer's own config file may choose it. Dropping the
            # variable and pointing XDG_CONFIG_HOME at a directory that
            # never exists (config.Load ignores a missing default file)
            # keeps the real default under test, rather than a pinned
            # copy of it. A case that wants another layout, or a config,
            # passes --layout or -c explicitly.
            child_env.pop("WIDEBOI_LAYOUT", None)
            child_env["XDG_CONFIG_HOME"] = os.path.join(
                tempfile.gettempdir(), f"wideboi-harness-no-config-{os.getpid()}")
            if env:
                child_env.update(env)
            os.execvpe(argv[0], argv, child_env)
        except Exception:
            pass
        os._exit(127)

    os.close(slave_fd)
    return pid, master_fd


def ps_rows() -> list[tuple[int, int, str]]:
    """Returns (pid, ppid, command) for every visible process."""
    try:
        out = subprocess.run(
            ["ps", "-axo", "pid=,ppid=,command="],
            capture_output=True,
            text=True,
            check=True,
        ).stdout
    except (subprocess.CalledProcessError, FileNotFoundError):
        return []

    rows = []
    for line in out.splitlines():
        fields = line.split(None, 2)
        if len(fields) < 2:
            continue
        try:
            rows.append((int(fields[0]), int(fields[1]), fields[2] if len(fields) > 2 else ""))
        except ValueError:
            continue
    return rows


def descendants(pid: int) -> list[tuple[int, str]]:
    """Returns (pid, command) for every process descended from pid."""
    rows = ps_rows()
    kids: dict[int, list[int]] = {}
    cmds: dict[int, str] = {}
    for p, pp, cmd in rows:
        kids.setdefault(pp, []).append(p)
        cmds[p] = cmd

    out: list[tuple[int, str]] = []
    seen = {pid}
    stack = list(kids.get(pid, []))
    while stack:
        cur = stack.pop()
        if cur in seen:
            continue
        seen.add(cur)
        out.append((cur, cmds.get(cur, "")))
        stack.extend(kids.get(cur, []))
    return out


def pane_children(pid: int) -> list[tuple[int, str]]:
    """Returns wideboi's direct children -- its pane shells."""
    return [(p, cmd) for p, pp, cmd in ps_rows() if pp == pid]


def server_child(pid: int) -> int | None:
    """The `wideboi server` a plain wideboi spawned, or None if it has
    not spawned one (yet). A plain wideboi owns its session through that
    server, so the pane shells are *its* children -- pid's grandchildren.
    """
    for p, pp, cmd in ps_rows():
        if pp == pid and " server" in cmd:
            return p
    return None


def still_alive(pids: list[int], within: float) -> list[int]:
    """Polls until none of pids are left or within elapses, then returns
    whatever is still there. Exit is not synchronous, so a bare one-shot
    check would be flaky.
    """
    deadline = time.monotonic() + within
    while True:
        alive = []
        for pid in pids:
            try:
                os.kill(pid, 0)
            except ProcessLookupError:
                continue
            except PermissionError:
                pass
            alive.append(pid)
        if not alive or time.monotonic() >= deadline:
            return alive
        time.sleep(0.05)


def wait_for_exit(pid: int, timeout: float) -> int | None:
    """Polls for pid's exit with WNOHANG, bounded by timeout. Returns the
    raw wait status, or None on timeout. Never blocks past timeout.
    """
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        got_pid, status = os.waitpid(pid, os.WNOHANG)
        if got_pid == pid:
            return status
        time.sleep(0.05)
    return None


def force_cleanup(pid: int) -> None:
    """Best-effort: SIGKILL pid and reap it, bounded, so a run that
    discovers a hang still doesn't leave a stray process behind.
    """
    try:
        os.kill(pid, signal.SIGKILL)
    except ProcessLookupError:
        return
    wait_for_exit(pid, timeout=3.0)
