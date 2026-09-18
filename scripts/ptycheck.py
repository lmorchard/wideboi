#!/usr/bin/env python3
"""Verify wideboi's signal-exit contract inside a real pty, non-interactively.

Runs the built binary as the session leader of a freshly opened pty at a
given size, drains the pty's master side continuously (so the render loop
can never block writing a frame against a full buffer), sends it a signal
after a short startup delay, and asserts the process dies BY that signal
(WIFSIGNALED with the matching WTERMSIG) rather than exiting normally. A
shell observing that death reports status 128+signo; this is what
internal/hostterm's restore-then-re-raise design exists to guarantee.

Also asserts no stray `wideboi` process is left in the process table
afterward -- a leaked pane would show up as a lingering shell, not as a
second wideboi, but a wideboi process outliving the one this script
tracked would mean something (double-fork, a hung child of a panic, etc)
kept a copy of the binary alive past the point this script reaped it.

Exit code is 0 if every assertion holds, non-zero otherwise. This script
never blocks indefinitely: every wait has a bound, and on timeout it
SIGKILLs whatever it started before reporting failure, so a run that finds
a real hang still leaves the process table clean.

Usage:
    scripts/ptycheck.py [--binary PATH] [--size COLSxROWS] [--signal SIG]
                         [--timeout SECONDS] [--startup-delay SECONDS]

    --size 0x0 means "leave the pty's winsize unset" (the no-winsize case
    that panicked before the width/height clamp fix), not a literal
    request for a zero-sized winsize ioctl.
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
import termios
import threading
import time


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


def spawn_in_pty(argv: list[str], cols: int, rows: int, set_winsize: bool) -> tuple[int, int]:
    """Forks argv onto a fresh pty, as the session leader with that pty as
    its controlling terminal. Winsize (if any) is applied to the pty
    before the fork, so the child can never observe an unset-then-set
    race -- it either sees the size from the moment it can ask, or (when
    set_winsize is False) never sees one at all, matching a pty that
    genuinely never had TIOCSWINSZ called on it.

    Returns (child_pid, master_fd) in the calling (parent) process.
    """
    master_fd, slave_fd = pty.openpty()

    if set_winsize:
        winsize = struct.pack("HHHH", rows, cols, 0, 0)
        fcntl.ioctl(slave_fd, termios.TIOCSWINSZ, winsize)

    pid = os.fork()
    if pid == 0:
        # Child: become session leader, acquire the pty as our controlling
        # terminal, wire it to stdin/stdout/stderr, then exec. Anything
        # that goes wrong here must exit immediately via os._exit, never
        # via a normal Python exception path that might try to flush
        # inherited buffers or run atexit handlers twice.
        try:
            os.close(master_fd)
            os.setsid()
            fcntl.ioctl(slave_fd, termios.TIOCSCTTY, 0)
            os.dup2(slave_fd, 0)
            os.dup2(slave_fd, 1)
            os.dup2(slave_fd, 2)
            if slave_fd > 2:
                os.close(slave_fd)
            env = dict(os.environ)
            # Pin the pane shell to /bin/sh: this project's own tests
            # establish that an interactive /bin/sh on a pty ignores
            # SIGTERM, which is the behavior the exit-status contract is
            # built to survive. Leaving $SHELL as whatever the harness
            # happened to inherit would make timing depend on the
            # operator's login shell.
            env["SHELL"] = "/bin/sh"
            os.execvpe(argv[0], argv, env)
        except Exception:
            pass
        os._exit(127)

    os.close(slave_fd)
    return pid, master_fd


def drain(master_fd: int, stop: threading.Event) -> None:
    """Reads and discards output from master_fd until it hits EOF/error or
    stop is set. Runs as a daemon thread so it can never itself hang the
    script's own exit.
    """
    while not stop.is_set():
        try:
            data = os.read(master_fd, 65536)
        except OSError:
            return
        if not data:
            return


def find_stray_wideboi(binary_path: str, exclude_pid: int) -> list[str]:
    """Looks for any process whose argv[0] is exactly binary_path (the
    absolute path this script exec'd). A substring match against the
    whole command line is not safe here: this script's own invocation,
    and its parent shell, both have "wideboi" in their command lines
    too, since that is this project's directory name. Matching argv[0]
    exactly is what actually identifies "another copy of the binary
    this script started."
    """
    try:
        out = subprocess.run(
            ["ps", "-axo", "pid=,command="],
            capture_output=True,
            text=True,
            check=True,
        ).stdout
    except (subprocess.CalledProcessError, FileNotFoundError) as exc:
        return [f"<could not run ps to check for strays: {exc}>"]

    strays = []
    for line in out.splitlines():
        line = line.strip()
        if not line:
            continue
        pid_str, _, command = line.partition(" ")
        argv0 = command.split(" ", 1)[0] if command else ""
        if argv0 != binary_path:
            continue
        try:
            pid = int(pid_str)
        except ValueError:
            continue
        if pid == exclude_pid:
            continue
        strays.append(f"pid={pid} command={command.strip()}")
    return strays


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


def run_check(binary: str, cols: int, rows: int, set_winsize: bool, sig: int,
              startup_delay: float, timeout: float) -> bool:
    label = f"{cols}x{rows}" if set_winsize else f"{cols}x{rows} (no winsize set)"
    print(f"--- size={label} signal={signal.Signals(sig).name} ---")

    argv = [os.path.abspath(binary)]
    pid, master_fd = spawn_in_pty(argv, cols, rows, set_winsize)

    stop = threading.Event()
    drainer = threading.Thread(target=drain, args=(master_fd, stop), daemon=True)
    drainer.start()

    ok = True
    try:
        time.sleep(startup_delay)

        # The process must still be alive at this point -- if it already
        # exited (e.g. the panic this harness was built to catch), there
        # is nothing left to signal, and that is itself a failure.
        got_pid, early_status = os.waitpid(pid, os.WNOHANG)
        if got_pid == pid:
            print(f"FAIL: process exited before being signalled (status={early_status:#x})")
            return False

        os.kill(pid, sig)

        status = wait_for_exit(pid, timeout)
        if status is None:
            print(f"FAIL: process did not exit within {timeout}s of {signal.Signals(sig).name} "
                  f"-- treating as unkillable")
            force_cleanup(pid)
            return False

        if os.WIFSIGNALED(status):
            termsig = os.WTERMSIG(status)
            if termsig == sig:
                print(f"OK: died by signal {termsig} (shell would report status {128 + termsig})")
            else:
                print(f"FAIL: died by signal {termsig}, expected {sig}")
                ok = False
        elif os.WIFEXITED(status):
            print(f"FAIL: exited normally with status {os.WEXITSTATUS(status)}, "
                  f"expected death by signal {sig}")
            ok = False
        else:
            print(f"FAIL: unrecognized wait status {status:#x}")
            ok = False
    finally:
        stop.set()
        try:
            os.close(master_fd)
        except OSError:
            pass
        # Idempotent safety net: if anything above returned early without
        # reaping the child (e.g. an unexpected exception), make sure it
        # is gone before this function returns, so no run of this script
        # can itself be the source of a stray process.
        try:
            os.kill(pid, 0)
        except ProcessLookupError:
            pass
        else:
            force_cleanup(pid)

    strays = find_stray_wideboi(argv[0], exclude_pid=pid)
    if strays:
        print(f"FAIL: {len(strays)} stray wideboi process(es) left behind:")
        for s in strays:
            print(f"  {s}")
        ok = False

    return ok


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--binary", default="./bin/wideboi", help="path to the wideboi binary (default: ./bin/wideboi)")
    parser.add_argument("--size", type=parse_size, default=(80, 24), help="pty size as COLSxROWS (default: 80x24); 0x0 means leave the winsize unset")
    parser.add_argument("--signal", type=parse_signal, default=signal.SIGTERM, help="signal to send (default: SIGTERM)")
    parser.add_argument("--timeout", type=float, default=10.0, help="seconds to wait for exit after signalling (default: 10)")
    parser.add_argument("--startup-delay", type=float, default=0.5, help="seconds to let the process start before signalling (default: 0.5)")
    args = parser.parse_args()

    if not os.path.isfile(args.binary):
        print(f"FAIL: binary not found at {args.binary!r} -- build it first (make build)", file=sys.stderr)
        return 2

    cols, rows = args.size
    set_winsize = not (cols == 0 and rows == 0)

    ok = run_check(args.binary, cols, rows, set_winsize, args.signal,
                    args.startup_delay, args.timeout)
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
