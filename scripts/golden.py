#!/usr/bin/env python3
"""Capture and compare a golden snapshot of wideboi's wire output.

Table tests cannot fail for behaviour nobody implemented. A committed
transcript can: a reader notices what is absent, and afterwards every
change shows as a diff.

Escape sequences are decoded to readable names so the golden file is
reviewable by a human rather than a wall of hex.
"""

import argparse
import os
import re
import signal
import sys
import tempfile
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptylib import Drainer, spawn_in_pty, wait_for_exit, force_cleanup

GOLDEN = os.path.join("testdata", "golden", "startup.txt")

# Sequence name and whether count is expected to be exact (one-off) or present (per-frame).
NAMES = [
    (rb"\x1b\[\?1049h", "ALT_SCREEN_ENTER", True),
    (rb"\x1b\[\?1049l", "ALT_SCREEN_EXIT", True),
    (rb"\x1b\[\?25h", "CURSOR_SHOW", False),
    (rb"\x1b\[\?25l", "CURSOR_HIDE", False),
    (rb"\x1b\[(\d+);(\d+)H", "CURSOR_TO(r,c)", False),
    (rb"\x1b\[2J", "CLEAR_SCREEN", True),
    (rb"\x1b\[0?m", "SGR_RESET", False),
    # Per-frame since cmd/wideboi/present.go brackets every frame (#72):
    # the count is how many frames startup took, which is timing.
    (rb"\x1b\[\?2026h", "SYNC_BEGIN", False),
    (rb"\x1b\[\?2026l", "SYNC_END", False),
]


def summarize(raw: bytes) -> str:
    """Reduce a byte stream to an ordered, stable inventory of what it did."""
    seen = []
    for pattern, name, exact in NAMES:
        count = len(re.findall(pattern, raw))
        if count:
            if exact:
                seen.append(f"{name}: {count}")
            else:
                seen.append(f"{name}: present")
    printable = re.sub(rb"\x1b\[[0-9;?]*[a-zA-Z]", b" ", raw)
    printable = printable.replace(b"\r", b" ").replace(b"\n", b" ").replace(b"\x1b", b" ")
    text = printable.decode("utf-8", "replace")
    words = sorted({w for w in re.findall(r"[A-Za-z0-9_+-]{3,}", text)})
    return (
        "# wideboi startup wire snapshot\n"
        "# Regenerate: make golden. Review diffs; do not blind-accept.\n"
        "\n## escape sequences\n" + "\n".join(seen) +
        "\n\n## words rendered\n" + "\n".join(words) + "\n"
    )


def capture() -> str:
    # Private to this capture: a plain wideboi attaches to whatever
    # answers its socket, so a shared path would record someone else's
    # session. Nothing answers here, so it spawns a server that binds
    # the path and removes it again when the SIGTERM below ends the
    # session. See smoke.py's RUNTIME_DIR.
    sock = os.path.join(tempfile.gettempdir(), f"wideboi-golden-{os.getpid()}.sock")
    pid, fd = spawn_in_pty(["./bin/wideboi"], 100, 30, True,
                           {"WIDEBOI_SOCK": sock})
    d = Drainer(fd)
    d.start()
    time.sleep(2.0)
    os.kill(pid, signal.SIGTERM)
    if wait_for_exit(pid, 8.0) is None:
        force_cleanup(pid)
    d.stop()
    return summarize(d.output())


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--update", action="store_true", help="rewrite the golden file")
    args = ap.parse_args()

    got = capture()
    if args.update:
        os.makedirs(os.path.dirname(GOLDEN), exist_ok=True)
        with open(GOLDEN, "w") as f:
            f.write(got)
        print(f"wrote {GOLDEN}")
        return 0

    if not os.path.exists(GOLDEN):
        print(f"FAIL: {GOLDEN} missing. Run: make golden")
        return 1
    with open(GOLDEN) as f:
        want = f.read()
    if got != want:
        print("FAIL: wire output differs from the golden snapshot.")
        import difflib
        for line in difflib.unified_diff(
            want.splitlines(), got.splitlines(),
            fromfile="golden", tofile="actual", lineterm="",
        ):
            print("  " + line)
        print("\nIf the change is intended: make golden")
        return 1
    print("OK    wire output matches the golden snapshot")
    return 0


if __name__ == "__main__":
    sys.exit(main())
