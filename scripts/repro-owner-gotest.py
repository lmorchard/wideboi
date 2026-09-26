#!/usr/bin/env python3
"""Run the Go test suite inside a pane of an *owned* wideboi session
with a web server, and report whether the session survives it.

Closer to the 2026-09-26 incident than repro-gotest-in-pane.sh: a plain
`wideboi` on a pty owns the server it spawned, the server runs a
websocket listener, and the session lives in the real session dir, so
the tests' auto-cleanup sweeps dial it just as they dialled `default`.
It uses its own uniquely named session and ends only that one.

usage: scripts/repro-owner-gotest.py [go test packages...]
"""
import os
import re
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptylib import Drainer, spawn_in_pty, wait_for_exit, force_cleanup  # noqa: E402

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BIN = os.path.join(ROOT, "bin", "wideboi")
NAME = f"repro-owner-{os.getpid()}"
PKGS = sys.argv[1:] or ["./internal/...", "./cmd/..."]
CEILING = 300.0

os.environ.pop("WIDEBOI_SOCK", None)
os.environ.pop("WIDEBOI_SESSION", None)

argv = [BIN, "-L", NAME, "--websocket", "127.0.0.1:0", "--websocket-token", "repro"]
pid, fd = spawn_in_pty(argv, 200, 50, True)
drainer = Drainer(fd)
drainer.start()
time.sleep(2.0)  # the owner spawns its server and draws its panes

done = f"GOTEST-DONE-{NAME}"
cmd = f"cd {ROOT} && go test -count=1 -cover {' '.join(PKGS)} >/tmp/{NAME}.gotest 2>&1; echo {done}-$?\r"
os.write(fd, cmd.encode())

start = time.monotonic()
outcome = "timed out"
while time.monotonic() - start < CEILING:
    status = wait_for_exit(pid, 1.0)
    if status is not None:
        outcome = f"CLIENT EXITED after {time.monotonic() - start:.0f}s, status {status}"
        break
    # The marker followed by a digit is the real exit status; the echo of
    # the typed command shows it followed by "$?".
    if re.search(re.escape(done).encode() + rb"-\d", drainer.output()):
        outcome = f"session survived; go test done after {time.monotonic() - start:.0f}s"
        break
print(outcome)

if "survived" in outcome or outcome == "timed out":
    subprocess.run([BIN, "-L", NAME, "kill-session"], capture_output=True, timeout=20)
    if wait_for_exit(pid, 10.0) is None:
        force_cleanup(pid)
drainer.stop()

print("--- go test tail")
try:
    with open(f"/tmp/{NAME}.gotest") as f:
        print("".join(f.readlines()[-20:]))
except OSError:
    pass

tmp = os.environ.get("TMPDIR", "/tmp").rstrip("/")
exits_log = os.path.join(tmp, f"wideboi-{os.getuid()}", "exits.log")
print(f"--- {exits_log} (this session, and anything recorded while it ran)")
try:
    with open(exits_log) as f:
        for line in f:
            if f"session={NAME}" in line or "sweep removed" in line:
                print(line.rstrip()[:400])
except OSError as exc:
    print(f"no exits.log: {exc}")
