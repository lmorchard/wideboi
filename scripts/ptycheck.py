#!/usr/bin/env python3
"""Verify wideboi's signal-exit contract inside a real pty, non-interactively.

Runs the built binary as the session leader of a freshly opened pty at a
given size, drains the pty's master side continuously (so the render loop
can never block writing a frame against a full buffer), snapshots the
pane children it spawned, sends it a signal after a short startup delay,
and then asserts all of:

 1. The process died BY that signal (WIFSIGNALED with the matching
    WTERMSIG) rather than exiting normally. A shell observing that death
    reports status 128+signo.

    On its own this assertion is weak: the kernel's DEFAULT disposition
    for SIGTERM/SIGINT/SIGHUP satisfies it too, so an unarmed binary
    (/bin/cat, or wideboi with guard.Arm deleted) passes it. Assertion 2
    is what gives it teeth.

 2. The alt-screen exit sequence (ESC[?1049l) reached the pty before the
    process died. Nothing emits that except wideboi's own shutdown path,
    so together with 1 this is what actually demonstrates
    internal/hostterm's restore-then-re-raise design: the terminal was
    restored first, and only then did the process die by the signal.

 3. Every process wideboi had spawned before the signal is gone
    afterwards -- the pane shells AND a background job deliberately
    planted in one of them. This is the leaked-pane check, and it is
    invisible to the stray-binary scan below, because a leaked pane is
    a lingering SHELL, not a second wideboi.

    The planted job is what gives this assertion teeth, and it is worth
    being explicit about why. Snapshotting the pane shells alone is not
    enough: when wideboi exits, its pty masters close, the shells read
    EOF and exit on their own, so they are reaped whether or not
    teardown ran. Verified empirically -- with closePanes deleted from
    the shutdown path, a pane-shells-only assertion still passed. So
    the harness types `nohup sleep ... &` into the focused pane first.
    That job is in its own process group and ignores SIGHUP, so nothing
    but ptyx.Kill's descendant walk reaches it, and it survives to be
    counted if teardown is skipped. It is exactly the escapee the
    three-route reaper in internal/server/ptyx exists for.

 4. No other process is running this binary. Not a pane-leak check -- a
    leaked pane is a shell -- but a wideboi outliving the one this script
    reaped would mean something (double-fork, a hung child of a panic)
    kept a copy of the binary alive.

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


ALT_SCREEN_EXIT = b"\x1b[?1049l"


def drain(master_fd: int, stop: threading.Event, saw_restore: threading.Event) -> None:
    """Reads output from master_fd until it hits EOF/error or stop is set,
    setting saw_restore the moment the alt-screen exit sequence appears.

    Output is discarded rather than accumulated -- an agent multiplexer
    can emit unbounded frames -- but a short carry of the previous
    chunk's tail is kept so the sequence is still found when it straddles
    a read boundary. Runs as a daemon thread so it can never itself hang
    the script's own exit.
    """
    carry = b""
    while not stop.is_set():
        try:
            data = os.read(master_fd, 65536)
        except OSError:
            return
        if not data:
            return
        if not saw_restore.is_set() and ALT_SCREEN_EXIT in carry + data:
            saw_restore.set()
        carry = data[-(len(ALT_SCREEN_EXIT) - 1):]


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


ESCAPEE_SLEEP = "987654"
ESCAPEE_CMD = f"nohup sleep {ESCAPEE_SLEEP} >/dev/null 2>&1 &\r"


def descendants(pid: int) -> list[tuple[int, str]]:
    """Returns (pid, command) for every process descended from pid.

    ptyx.Spawn gives each pane its own session via Setsid, which changes
    the child's session and process group but NOT its parent, so the
    pane shells are direct children of wideboi and their jobs are
    grandchildren. The walk has to happen while wideboi is alive: once
    it dies, anything that outlives it reparents to launchd/init and
    there is no longer any link back.
    """
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


def plant_escapee(master_fd: int, pid: int, within: float) -> tuple[int, str] | None:
    """Types a nohup'd background job into wideboi's focused pane and
    waits for it to show up in the process tree.

    Writing to the master is how a real user types: wideboi decodes the
    bytes into key events and forwards them to the focused pane. Returns
    the planted process, or None if it never appeared.
    """
    os.write(master_fd, ESCAPEE_CMD.encode())
    deadline = time.monotonic() + within
    while time.monotonic() < deadline:
        for cpid, cmd in descendants(pid):
            if f"sleep {ESCAPEE_SLEEP}" in cmd:
                return (cpid, cmd)
        time.sleep(0.1)
    return None


def pane_children(pid: int) -> list[tuple[int, str]]:
    """Returns wideboi's direct children -- its pane shells.

    ptyx.Spawn gives each pane its own session via Setsid, which changes
    the child's session and process group but NOT its parent, so every
    pane shell is still a direct child of the wideboi process while it
    lives. That is what makes this snapshot possible, and it has to be
    taken while they are alive: once wideboi dies, a leaked pane
    reparents to init/launchd and there is no longer any link back.
    """
    return [(p, cmd) for p, pp, cmd in ps_rows() if pp == pid]


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
              startup_delay: float, timeout: float, plant: bool) -> bool:
    label = f"{cols}x{rows}" if set_winsize else f"{cols}x{rows} (no winsize set)"
    print(f"--- size={label} signal={signal.Signals(sig).name} ---")

    argv = [os.path.abspath(binary)]
    pid, master_fd = spawn_in_pty(argv, cols, rows, set_winsize)

    stop = threading.Event()
    saw_restore = threading.Event()
    drainer = threading.Thread(target=drain, args=(master_fd, stop, saw_restore), daemon=True)
    drainer.start()

    ok = True
    tracked: list[tuple[int, str]] = []
    try:
        time.sleep(startup_delay)

        # The process must still be alive at this point -- if it already
        # exited (e.g. the panic this harness was built to catch), there
        # is nothing left to signal, and that is itself a failure.
        got_pid, early_status = os.waitpid(pid, os.WNOHANG)
        if got_pid == pid:
            print(f"FAIL: process exited before being signalled (status={early_status:#x})")
            return False

        # Snapshot what wideboi has spawned, while it is still alive to
        # be walked. This has to happen before the signal: afterwards
        # anything leaked has reparented and is unfindable.
        shells = pane_children(pid)
        if len(shells) < 2:
            print(f"FAIL: expected 2 pane children before signalling, found {len(shells)}: {shells}")
            return False

        if plant:
            escapee = plant_escapee(master_fd, pid, within=5.0)
            if escapee is None:
                print(f"FAIL: planted job (sleep {ESCAPEE_SLEEP}) never appeared in wideboi's "
                      f"process tree -- without it the leaked-pane assertion is vacuous, "
                      f"because pane shells exit on their own when the pty master closes")
                return False
            tracked = shells + [escapee]
        else:
            tracked = shells

        print(f"     tracking {len(tracked)} pid(s): {[t for t, _ in tracked]}")

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

        # The restore is written just before the re-raise, so by the time
        # the process is reaped it is normally already drained -- but the
        # drainer is a separate thread reading a pipe, so give it a
        # bounded moment rather than racing it.
        if not saw_restore.wait(timeout=2.0):
            print("FAIL: never saw the alt-screen exit sequence (ESC[?1049l) on the pty "
                  "-- the process died without restoring the terminal, which is what the "
                  "kernel's default disposition looks like")
            ok = False
        else:
            print("OK: alt-screen exit sequence reached the pty before the process died")
    finally:
        stop.set()
        # Reap before closing, not after. Two reasons, and they point the
        # same way. The safety net: if anything above returned early
        # without reaping the child (an assertion that bails before
        # signalling, an unexpected exception), it must not become a
        # stray. The deadlock: on macOS, close() on a pty master blocks
        # while another thread is parked in read() on it, and the only
        # thing that wakes that reader is the child exiting. Closing
        # first with a live child hangs this script indefinitely.
        try:
            os.kill(pid, 0)
        except ProcessLookupError:
            pass
        else:
            force_cleanup(pid)
        try:
            os.close(master_fd)
        except OSError:
            pass

    leaked = still_alive([t for t, _ in tracked], within=2.0)
    if leaked:
        by_pid = dict(tracked)
        print(f"FAIL: {len(leaked)} process(es) wideboi spawned survived teardown:")
        for lp in leaked:
            print(f"  pid={lp} command={by_pid.get(lp, '?')}")
        # This script must never be the reason something is left running,
        # even when the thing it is testing is.
        for lp in leaked:
            try:
                os.kill(lp, signal.SIGKILL)
            except OSError:
                pass
        ok = False
    elif tracked:
        print(f"OK: all {len(tracked)} process(es) wideboi spawned were reaped")

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
    parser.add_argument("--no-escapee", action="store_true", help="skip planting the nohup'd background job; the leaked-pane assertion becomes much weaker (see module docstring)")
    args = parser.parse_args()

    if not os.path.isfile(args.binary):
        print(f"FAIL: binary not found at {args.binary!r} -- build it first (make build)", file=sys.stderr)
        return 2

    cols, rows = args.size
    set_winsize = not (cols == 0 and rows == 0)

    ok = run_check(args.binary, cols, rows, set_winsize, args.signal,
                    args.startup_delay, args.timeout, not args.no_escapee)
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
