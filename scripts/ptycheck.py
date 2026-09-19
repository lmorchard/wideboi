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
import os
import signal
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptylib import (
    ALT_SCREEN_EXIT, Drainer, spawn_in_pty, descendants, pane_children, still_alive,
    wait_for_exit, force_cleanup, parse_size, parse_signal,
)

ESCAPEE_SLEEP = "987654"
ESCAPEE_CMD = f"nohup sleep {ESCAPEE_SLEEP} >/dev/null 2>&1 &\r"


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


def find_stray_wideboi(binary_path: str, exclude_pid: int) -> list[str]:
    """Looks for any process whose argv[0] is exactly binary_path (the
    absolute path this script exec'd).
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


def run_check(binary: str, cols: int, rows: int, set_winsize: bool, sig: int,
              startup_delay: float, timeout: float, plant: bool) -> bool:
    label = f"{cols}x{rows}" if set_winsize else f"{cols}x{rows} (no winsize set)"
    print(f"--- size={label} signal={signal.Signals(sig).name} ---")

    argv = [os.path.abspath(binary)]
    pid, master_fd = spawn_in_pty(argv, cols, rows, set_winsize)

    drainer = Drainer(master_fd)
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
        # the process is reaped it is normally already drained -- but give
        # it a bounded moment to ensure the drainer has read it.
        deadline = time.monotonic() + 2.0
        saw_alt_exit = False
        while time.monotonic() < deadline:
            if drainer.saw(ALT_SCREEN_EXIT):
                saw_alt_exit = True
                break
            time.sleep(0.05)

        if not saw_alt_exit:
            print("FAIL: never saw the alt-screen exit sequence (ESC[?1049l) on the pty "
                  "-- the process died without restoring the terminal, which is what the "
                  "kernel's default disposition looks like")
            ok = False
        else:
            print("OK: alt-screen exit sequence reached the pty before the process died")
    finally:
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
        drainer.stop()

    leaked = still_alive([t for t, _ in tracked], within=2.0)
    if leaked:
        by_pid = dict(tracked)
        print(f"FAIL: {len(leaked)} process(es) wideboi spawned survived teardown:")
        for lp in leaked:
            print(f"  pid={lp} command={by_pid.get(lp, '?')}")
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
