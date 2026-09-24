#!/usr/bin/env python3
"""Reproducible live-traffic scenarios against a real wideboi server (#179).

A measurement run, not a gate: numbers are machine-dependent and a run
takes a minute or more. `make traffic` runs it; `--profile` adds pprof
files for the server and every terminal client.

Each scenario gets a fresh private server with a generated config (one
startup pane of a fixed width, a WebSocket listener on a free port),
some terminal clients on ptys (`wideboi attach`) and some WebSocket
clients (scripts/wssink, which also estimates permessage-deflate). The
server's counters (the `status --traffic` report) and the sinks'
summaries are both baselined once everything has attached and gone
quiet, so attach snapshots are kept out of the workload's numbers.

Once the baseline is taken the server is only asked over a sink's own
WebSocket (wssink SIGUSR2), never with the CLI: a CLI connection closing
force-resends every pane in full to every client (server.dropClient ->
broadcastLayout), which would land in the window being measured.

Self-check: every scenario must see at least one pane update per client
after the baseline, and each checks that its workload actually ran, not
just that traffic happened: typing gets an update for at least half its
keystrokes, `scroll` sees a marker the shell prints only after `seq`
finishes, `tui` sees a line from deep in its file that only paging in
vim puts on screen, and `scroll-paced` and `tui` see shift patches.
(Not the `scroll` burst: sustained output goes out as full updates, see
scroll_paced.) The sinks' per-kind payload bytes must also add up to the
server's pane payload bytes for the same client. Otherwise the run exits
nonzero naming the scenario, so a broken harness cannot report an empty
or meaningless table as a result.

Never hangs: every wait is bounded, and every child is reaped or killed.
"""

from __future__ import annotations

import argparse
import json
import os
import platform
import queue
import shutil
import signal
import socket
import subprocess
import sys
import threading
import time
from dataclasses import dataclass, field

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from ptylib import (  # noqa: E402
    Drainer, force_cleanup, private_run_dir, run_main, settle_output,
    spawn_in_pty, wait_for_exit,
)

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BIN = os.path.join(ROOT, "bin", "wideboi")
TOKEN = "traffic"
TYPED = b"the quick brown fox jumps over the lazy dog "
TYPE_HZ = 15

# Ceilings, not durations (see ptylib.settle_output).
SERVER_BIND = 10.0
PROMPT_WAIT = 8.0
ATTACH_WAIT = 10.0
SCROLL_CEILING = 60.0
EXIT_WAIT = 15.0
QUIESCE_CEILING = 20.0
# A pane reads as working while it writes and flips back to idle after
# term.DefaultIdleTimeout (3s) of silence. Each flip is a layout
# broadcast, which force-resends every pane in full to every client. So
# "quiet" means longer than that: before the baseline, so the startup
# prompt's idle flip is not counted as workload; after the workload, so
# the workload's own idle flip is.
IDLE_QUIET = 4.0

# A terminal client's pane area is its window less this much chrome:
# layout.AvailHeight takes 2 rows (status bar and border), and the
# column border takes 2 columns. Only the rows matter to the server,
# since a startup pane's width is fixed by config; the extra columns
# keep the whole pane on screen.
CHROME_COLS, CHROME_ROWS = 2, 2


class ScenarioFailed(Exception):
    pass


@dataclass
class Scenario:
    name: str
    cols: int
    rows: int
    sockets: int
    websockets: int
    workload: object  # callable(Run, args)
    needs: str | None = None  # executable the workload needs
    min_shifts: int = 0
    verify: object = None  # callable(Run, delta) -> [problem], workload-specific


@dataclass
class Sink:
    proc: subprocess.Popen
    lines: "queue.Queue[str]" = field(default_factory=queue.Queue)

    def start_reader(self) -> None:
        def pump():
            for line in self.proc.stdout:
                self.lines.put(line)
        threading.Thread(target=pump, daemon=True).start()

    def snapshot(self, timeout: float = 5.0) -> dict:
        """Asks for the running summary (SIGUSR1) and returns it."""
        self.proc.send_signal(signal.SIGUSR1)
        line = self._next(timeout, "SIGUSR1 summary")
        if "Traffic" in line:
            raise ScenarioFailed(f"wssink pid {self.proc.pid}: wanted a summary, got a traffic reply")
        return line

    def server_traffic(self, timeout: float = 5.0) -> tuple[dict, int]:
        """Asks the server for its traffic report over this sink's own
        connection (SIGUSR2). Returns (report, reply payload bytes)."""
        self.proc.send_signal(signal.SIGUSR2)
        line = self._next(timeout, "SIGUSR2 traffic reply")
        if "Traffic" not in line:
            raise ScenarioFailed(f"wssink pid {self.proc.pid}: wanted a traffic reply, got {line}")
        return line["Traffic"], line["ReplyBytes"]

    def finish(self, timeout: float = 5.0) -> dict:
        self.proc.send_signal(signal.SIGTERM)
        summary = self._next(timeout, "final summary")
        try:
            self.proc.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            self.proc.kill()
            self.proc.wait(timeout=3)
        return summary

    def _next(self, timeout: float, what: str) -> dict:
        try:
            line = self.lines.get(timeout=timeout)
        except queue.Empty:
            raise ScenarioFailed(f"wssink pid {self.proc.pid} printed no {what} "
                                 f"within {timeout}s (exit={self.proc.poll()})")
        return json.loads(line)


class Run:
    """One scenario's server, terminal clients and sinks."""

    def __init__(self, sc: Scenario, args, wssink: str) -> None:
        self.sc = sc
        self.args = args
        self.dir = private_run_dir(f"wideboi-traffic-{sc.name}-")
        self.sock = os.path.join(self.dir, "default.sock")
        self.cfg = os.path.join(self.dir, "traffic.toml")
        self.port = free_port()
        self.pty_cols = sc.cols + CHROME_COLS
        self.pty_rows = sc.rows + CHROME_ROWS
        with open(self.cfg, "w") as f:
            f.write(f'websocket = "127.0.0.1:{self.port}"\n'
                    f'websocket_token = "{TOKEN}"\n'
                    f'[[startup]]\nwidth = {sc.cols}\n')
        self.overlay = {
            "WIDEBOI_SOCK": self.sock,
            "WIDEBOI_TRAFFIC_TIMING": "1",
            "SHELL": "/bin/sh",
            "PS1": "$ ",
            "TERM": "xterm-256color",
            "XDG_CONFIG_HOME": os.path.join(self.dir, "no-config"),
        }
        if args.profile:
            prefix = os.path.join(os.path.abspath(args.outdir), sc.name)
            self.overlay["WIDEBOI_CPUPROFILE"] = prefix
            self.overlay["WIDEBOI_MEMPROFILE"] = prefix
        self.wssink = wssink
        self.server: subprocess.Popen | None = None
        self.clients: list[tuple[int, int, Drainer]] = []
        self.sinks: list[Sink] = []
        self.pane: dict = {}
        self.keystrokes = 0  # set by typing
        self.output = b""    # the first client's pty output, kept past teardown

    # -- environment -------------------------------------------------

    def env(self) -> dict:
        """The server's and CLI's environment. Pty clients get the same
        overlay on top of ptylib's pinned environment."""
        base = {k: v for k, v in os.environ.items() if not k.startswith("WIDEBOI_")}
        return {**base, **self.overlay}

    def cli(self, *argv: str, timeout: float = 5.0) -> str:
        out = subprocess.run([BIN, "-c", self.cfg, *argv], env=self.env(),
                             capture_output=True, text=True, timeout=timeout)
        if out.returncode != 0:
            raise ScenarioFailed(f"wideboi {' '.join(argv)} exited {out.returncode}: "
                                 f"{out.stderr.strip()}")
        return out.stdout

    def traffic(self) -> dict:
        return json.loads(self.cli("status", "--traffic", "--json"))

    def layout(self) -> dict:
        return json.loads(self.cli("status", "--json"))

    # -- lifecycle ---------------------------------------------------

    def start(self) -> None:
        log = open(os.path.join(self.dir, "server.out"), "wb")
        self.server = subprocess.Popen([BIN, "-c", self.cfg, "server"], env=self.env(),
                                       stdout=log, stderr=subprocess.STDOUT)
        log.close()
        wait_until(lambda: os.path.exists(self.sock) or self.server.poll() is not None,
                   SERVER_BIND, "server to bind its socket")
        if self.server.poll() is not None:
            raise ScenarioFailed(f"server exited early ({self.server.returncode}); "
                                 f"see {self.dir}/server.out")
        wait_until(lambda: port_open(self.port), SERVER_BIND,
                   f"WebSocket listener on 127.0.0.1:{self.port}")

        for _ in range(self.sc.sockets):
            pid, fd = spawn_in_pty([BIN, "-c", self.cfg, "attach"],
                                   self.pty_cols, self.pty_rows, True, self.overlay)
            d = Drainer(fd)
            d.start()
            self.clients.append((pid, fd, d))
        # The first attach spawns the startup pane; wait for its prompt.
        first = self.clients[0][2]
        # "$", not "$ ": the diffing renderer need not draw a trailing
        # blank cell, and waiting on the space timed out intermittently.
        wait_until(lambda: b"$" in first.output(), PROMPT_WAIT, "the pane's first prompt")

        port = self.port
        # One at a time: ClientIDs follow attach order, so the last sink
        # -- the one asked for traffic reports -- is the highest
        # websocket ClientID, and its report replies can be subtracted.
        for i in range(self.sc.websockets):
            with open(os.path.join(self.dir, "wssink.err"), "ab") as err:
                proc = subprocess.Popen(
                    [self.wssink, "-url", f"ws://127.0.0.1:{port}/ws", "-token", TOKEN,
                     "-cols", str(self.pty_cols), "-rows", str(self.pty_rows)],
                    stdout=subprocess.PIPE, stderr=err, text=True)
            sink = Sink(proc)
            sink.start_reader()
            self.sinks.append(sink)
            want = {"socket": self.sc.sockets, "websocket": i + 1}

            def attached() -> bool:
                if sink.proc.poll() is not None:
                    raise ScenarioFailed(f"wssink exited early ({sink.proc.returncode}); "
                                         f"see {self.dir}/wssink.err")
                return transport_counts(self.traffic()) == want

            wait_until(attached, ATTACH_WAIT, f"clients to attach ({want})")
        self.pane = self.layout()["columns"][0]
        # Every CLI query above (status, status --traffic) force-resends
        # each pane to every client when its connection closes
        # (server.dropClient -> broadcastLayout). From here on the
        # server is only asked over a sink's own connection.
        settle_output(first, timeout=PROMPT_WAIT, quiet=0.5)
        self.quiesce()

    @property
    def reporter(self) -> Sink:
        return self.sinks[-1]

    def type_to(self, data: bytes) -> None:
        os.write(self.clients[0][1], data)

    def settle(self, timeout: float, quiet: float = 0.5) -> bool:
        return settle_output(self.clients[0][2], timeout=timeout, quiet=quiet)

    def quiesce(self, quiet: float = IDLE_QUIET, ceiling: float = QUIESCE_CEILING) -> None:
        """Waits until the reporting sink's pane-update count has stopped
        moving for `quiet` seconds. The pty going quiet is not enough: a
        status or title change force-resends every pane
        (server.broadcastLayout) some time after output stops, and an
        unchanged full repaints nothing on a terminal client's pty. The
        sink is asked by signal, so polling opens no connection."""
        def updates() -> int:
            s = self.reporter.snapshot()
            return s["PaneUpdates"] + s["RowPatches"] + s["ShiftPatches"]
        deadline = time.monotonic() + ceiling
        last, since = updates(), time.monotonic()
        while time.monotonic() < deadline:
            time.sleep(0.1)
            n = updates()
            if n != last:
                last, since = n, time.monotonic()
            elif time.monotonic() - since >= quiet:
                return
        print(f"traffic: {self.sc.name}: updates still arriving after {ceiling}s; "
              f"measuring anyway", file=sys.stderr)

    def teardown(self) -> dict:
        """Stops everything and waits for it to exit. Returns sink finals."""
        finals = []
        for s in self.sinks:
            try:
                finals.append(s.finish())
            except ScenarioFailed as exc:
                finals.append({"error": str(exc)})
                if s.proc.poll() is None:
                    s.proc.kill()
        # Popped as they go, so kill_all never signals a reaped pid.
        while self.clients:
            pid, fd, d = self.clients.pop(0)
            try:
                mark = len(d.output())
                os.write(fd, b"\x02")  # the default prefix, C-b
                # Wait for control mode's status bar, which offers
                # "d detach" to a socket client, rather than a fixed pause.
                # If it never shows, send d anyway and let the exit wait
                # below decide.
                try:
                    wait_until(lambda: b"d detach" in d.output()[mark:], 2.0,
                               "control mode")
                except ScenarioFailed:
                    pass
                os.write(fd, b"d")     # detach
            except OSError:
                pass
            if wait_for_exit(pid, EXIT_WAIT) is None:
                print(f"traffic: {self.sc.name}: client {pid} did not detach; killing",
                      file=sys.stderr)
                force_cleanup(pid)
            d.stop()
            os.close(fd)
        if self.server is not None and self.server.poll() is None:
            try:
                self.cli("kill-session")
            except (ScenarioFailed, subprocess.TimeoutExpired) as exc:
                print(f"traffic: {self.sc.name}: kill-session: {exc}", file=sys.stderr)
            # kill-session returns before the server has written its
            # profiles; they are only complete once it has exited.
            try:
                self.server.wait(timeout=EXIT_WAIT)
            except subprocess.TimeoutExpired:
                print(f"traffic: {self.sc.name}: server did not exit; killing",
                      file=sys.stderr)
                force_cleanup(self.server.pid)
        return finals

    def kill_all(self) -> None:
        """Backstop after a failure: nothing survives the scenario."""
        for s in self.sinks:
            if s.proc.poll() is None:
                s.proc.kill()
        for pid, fd, d in self.clients:
            d.stop()
            force_cleanup(pid)
            try:
                os.close(fd)
            except OSError:
                pass
        self.clients.clear()
        if self.server is not None and self.server.poll() is None:
            force_cleanup(self.server.pid)


# -- workloads ---------------------------------------------------------

def typing(run: Run, args) -> None:
    deadline = time.monotonic() + args.seconds
    i = 0
    next_at = time.monotonic()
    while time.monotonic() < deadline:
        run.type_to(TYPED[i % len(TYPED):i % len(TYPED) + 1])
        i += 1
        next_at += 1.0 / TYPE_HZ
        time.sleep(max(0.0, next_at - time.monotonic()))
    run.keystrokes = i
    run.settle(timeout=5.0)


def typing_ran(run: Run, delta: dict) -> list[str]:
    # One keystroke every 1/15 s against a 33 ms frame: each echo is its
    # own update. Half leaves room for a coalesced frame or two.
    want = run.keystrokes // 2
    return [f"client {c['client_id']} ({c['transport']}) got {c['updates']} updates "
            f"for {run.keystrokes} keystrokes, want >= {want}"
            for c in delta["clients"] if c["updates"] < want]


# Printed by the shell only once seq has exited successfully. The
# arithmetic keeps it out of the typed command line, which the shell
# echoes: "seq-done-42" can only come from running it.
SCROLL_DONE = b"seq-done-42"


def scroll(run: Run, args) -> None:
    run.type_to(b"seq 1 200000 && echo seq-done-$((6*7))\r")
    if not run.settle(timeout=SCROLL_CEILING, quiet=1.0):
        raise ScenarioFailed(f"seq output did not settle within {SCROLL_CEILING}s")


def scroll_ran(run: Run, delta: dict) -> list[str]:
    if SCROLL_DONE not in run.output:
        return [f"{SCROLL_DONE.decode()} never reached the screen: seq did not finish"]
    return []


def scroll_paced(run: Run, args) -> None:
    # One line at typing pace, so the pane is still between frames. The
    # seq burst above cannot exercise shift patches: when a pane changes
    # while it is being rendered, broadcastPaneUpdates drops that
    # client's baseline, so sustained output goes out as full updates.
    path = os.path.join(run.dir, "paced.sh")
    count = max(1, int(args.seconds * TYPE_HZ))
    with open(path, "w") as f:
        f.write(f"i=0\nwhile [ $i -lt {count} ]; do i=$((i+1)); "
                f"echo \"paced line $i\"; sleep {1.0 / TYPE_HZ:.3f}; done\n")
    run.type_to(f"sh {path}\r".encode())
    if not run.settle(timeout=args.seconds * 3 + 10, quiet=1.0):
        raise ScenarioFailed("paced output did not settle")


# Lines from TUI_DEEP on carry TUI_MARKER; lines before it are lowercase.
# The first page holds ~22 lines and the 30 Ctrl-F pages reach ~630, so
# the marker reaches the screen only if vim ran and paged. Uppercase
# against lowercase and spaces means every cell of the marker differs
# from what it replaces, so the diffing renderer writes it whole.
TUI_DEEP = 300
TUI_MARKER = b"DEEPZONEQX"


def tui(run: Run, args) -> None:
    path = os.path.join(run.dir, "lines.txt")
    with open(path, "w") as f:
        for n in range(1, 5001):
            text = (TUI_MARKER.decode() + " sed do eiusmod tempor incididunt"
                    if n >= TUI_DEEP else "lorem ipsum dolor sit amet, consectetur")
            f.write(f"{n:5d} {text} adipiscing elit\n")
    run.type_to(f"vim -u NONE -N -n {path}\r".encode())
    run.settle(timeout=PROMPT_WAIT)
    paced(run, b"\x06", 30, 5)    # Ctrl-F, a page at a time
    paced(run, b"j", 150, 30)
    run.type_to(b":q!\r")
    run.settle(timeout=5.0)


def tui_ran(run: Run, delta: dict) -> list[str]:
    if TUI_MARKER not in run.output:
        return [f"{TUI_MARKER.decode()} (line {TUI_DEEP}+) never reached the screen: "
                f"vim did not run or did not page"]
    return []


def paced(run: Run, key: bytes, count: int, hz: float) -> None:
    next_at = time.monotonic()
    for _ in range(count):
        run.type_to(key)
        next_at += 1.0 / hz
        time.sleep(max(0.0, next_at - time.monotonic()))


SCENARIOS = [
    Scenario("typing", 80, 24, 1, 1, typing, verify=typing_ran),
    Scenario("scroll", 80, 24, 1, 1, scroll, verify=scroll_ran),
    Scenario("scroll-paced", 80, 24, 1, 1, scroll_paced, min_shifts=1),
    Scenario("tui", 80, 24, 1, 1, tui, needs="vim", min_shifts=1, verify=tui_ran),
    Scenario("large", 160, 48, 2, 2, typing, verify=typing_ran),
]


# -- measurement -------------------------------------------------------

COUNTERS = ["full_updates", "row_patches", "shift_patches", "changed_rows", "resync_requests",
            "send_failures", "messages", "payload_bytes", "pane_payload_bytes", "wire_bytes"]
SINK_COUNTERS = ["Messages", "PaneUpdates", "RowPatches", "ShiftPatches", "Resyncs",
                 "PayloadBytes", "DeflateNoCtxBytes", "DeflateNoCtxNanos",
                 "DeflateBytes", "DeflateNanos", "DecodeApplyNanos",
                 "FullBytes", "RowPatchBytes", "ShiftPatchBytes"]


def sub_timing(a: dict, b: dict) -> dict:
    return {"count": a["count"] - b["count"], "total_nanos": a["total_nanos"] - b["total_nanos"]}


def client_delta(after: dict, before: dict | None) -> dict:
    before = before or {k: 0 for k in COUNTERS} | {"encode": {"count": 0, "total_nanos": 0}}
    d = {k: after[k] - before[k] for k in COUNTERS}
    d["client_id"], d["transport"] = after["client_id"], after["transport"]
    d["encode"] = sub_timing(after["encode"], before["encode"])
    d["updates"] = d["full_updates"] + d["row_patches"] + d["shift_patches"]
    return d


def traffic_delta(after: dict, before: dict) -> dict:
    prev = {c["client_id"]: c for c in before["clients"]}
    return {
        "clients": [client_delta(c, prev.get(c["client_id"])) for c in after["clients"]],
        "render": sub_timing(after["render"], before["render"]),
        "build_patch": sub_timing(after["build_patch"], before["build_patch"]),
    }


def discount_reply(delta: dict, reply_bytes: int) -> None:
    """Takes the baseline report's own reply out of the window. The
    server builds a report before sending it, so the baseline's reply is
    counted after the baseline, against the reporting sink: the highest
    websocket ClientID (see Run.start). The reply is one unmasked
    WebSocket frame, so its header is 2 bytes, or 4 past 125 bytes."""
    ws = [c for c in delta["clients"] if c["transport"] == "websocket"]
    if not ws:
        return
    c = max(ws, key=lambda c: c["client_id"])
    header = 2 if reply_bytes < 126 else 4 if reply_bytes < 65536 else 10
    c["messages"] -= 1
    c["payload_bytes"] -= reply_bytes
    c["wire_bytes"] -= reply_bytes + header


def sink_delta(final: dict, base: dict) -> dict:
    d = {k: final[k] - base[k] for k in SINK_COUNTERS}
    d["Updates"] = d["PaneUpdates"] + d["RowPatches"] + d["ShiftPatches"]
    d["Patches"] = d["RowPatches"] + d["ShiftPatches"]
    d["PatchBytes"] = d["RowPatchBytes"] + d["ShiftPatchBytes"]
    return d


def self_check(sc: Scenario, run: Run, delta: dict, sinks: list[dict]) -> list[str]:
    problems = []
    got = transport_counts({"clients": delta["clients"]})
    want = {"socket": sc.sockets, "websocket": sc.websockets}
    if got != want:
        problems.append(f"clients after the workload {got}, want {want}")
    for c in delta["clients"]:
        if c["updates"] < 1:
            problems.append(f"client {c['client_id']} ({c['transport']}) got no pane updates")
        if c["shift_patches"] < sc.min_shifts:
            problems.append(f"client {c['client_id']} ({c['transport']}) got "
                            f"{c['shift_patches']} shift patches, want >= {sc.min_shifts}")
    for i, s in enumerate(sinks):
        if s["Updates"] < 1:
            problems.append(f"wssink {i} applied no pane updates")
    # Sinks attach one at a time, so sink i is the i-th websocket
    # ClientID (see Run.start). Its per-kind bytes are only worth
    # reporting if they account for what the server says it sent.
    ws = sorted((c for c in delta["clients"] if c["transport"] == "websocket"),
                key=lambda c: c["client_id"])
    for i, (c, s) in enumerate(zip(ws, sinks)):
        kinds = s["FullBytes"] + s["PatchBytes"]
        if kinds != c["pane_payload_bytes"]:
            problems.append(f"wssink {i} pane bytes by kind sum to {kinds}, but the server "
                            f"sent client {c['client_id']} {c['pane_payload_bytes']}")
    if sc.verify is not None:
        problems.extend(sc.verify(run, delta))
    return problems


# -- report ------------------------------------------------------------

def avg_us(t: dict) -> str:
    return f"{t['total_nanos'] / t['count'] / 1000:.1f}" if t["count"] else "-"


def per(n: float, d: float) -> str:
    return f"{n / d:.0f}" if d else "-"


def table(rows: list[list[str]]) -> str:
    widths = [max(len(r[i]) for r in rows) for i in range(len(rows[0]))]
    return "\n".join("  ".join(c.rjust(w) if i else c.ljust(w)
                               for i, (c, w) in enumerate(zip(r, widths)))
                     for r in rows)


def report(res: dict) -> str:
    secs = res["workload_seconds"]
    d = res["delta"]
    out = [f"== {res['name']}: pane {res['pane']['cols']}x{res['pane']['rows']} "
           f"(client pty {res['pty']['cols']}x{res['pty']['rows']}), "
           f"{secs:.1f}s active workload + {res['window_seconds'] - secs:.1f}s quiet tail"]
    rows = [["CLIENT", "TRANSPORT", "UPD/S", "FULL", "ROW", "SHIFT", "ROWS", "RESYNC",
             "PANE PAYLOAD B", "PAYLOAD B", "WIRE B", "WIRE OVH%", "ENC us"]]
    for c in d["clients"]:
        rows.append([str(c["client_id"]), c["transport"],
                     f"{c['updates'] / secs:.1f}" if secs > 0 else "-",
                     str(c["full_updates"]), str(c["row_patches"]), str(c["shift_patches"]),
                     str(c["changed_rows"]), str(c["resync_requests"]),
                     str(c["pane_payload_bytes"]), str(c["payload_bytes"]),
                     str(c["wire_bytes"]),
                     (f"{100 * (c['wire_bytes'] - c['payload_bytes']) / c['payload_bytes']:.1f}"
                      if c["payload_bytes"] else "-"),
                     avg_us(c["encode"])])
    out.append(table(rows))
    out.append(f"server: render avg {avg_us(d['render'])} us (n={d['render']['count']}), "
               f"build patch avg {avg_us(d['build_patch'])} us (n={d['build_patch']['count']})")
    if res["sinks"]:
        srows = [["WSSINK", "UPD", "RESYNC", "FULL", "B/FULL", "PATCH", "B/PATCH",
                  "PAYLOAD B", "DEFLATE AS-IS B", "SAVED%", "us/MSG",
                  "DEFLATE CTX B", "SAVED%", "us/MSG", "DECODE+APPLY us/MSG"]]
        for i, s in enumerate(res["sinks"]):
            p = s["PayloadBytes"]
            m = s["Messages"]
            srows.append([str(i), str(s["Updates"]), str(s["Resyncs"]),
                          str(s["PaneUpdates"]), per(s["FullBytes"], s["PaneUpdates"]),
                          str(s["Patches"]), per(s["PatchBytes"], s["Patches"]), str(p),
                          str(s["DeflateNoCtxBytes"]),
                          f"{100 * (1 - s['DeflateNoCtxBytes'] / p):.1f}" if p else "-",
                          f"{s['DeflateNoCtxNanos'] / m / 1000:.1f}" if m else "-",
                          str(s["DeflateBytes"]),
                          f"{100 * (1 - s['DeflateBytes'] / p):.1f}" if p else "-",
                          f"{s['DeflateNanos'] / m / 1000:.1f}" if m else "-",
                          f"{s['DecodeApplyNanos'] / m / 1000:.1f}" if m else "-"])
        out.append(table(srows))
    return "\n".join(out)


LEGEND = """\
PANE PAYLOAD B: protobuf envelope bytes of pane updates and patches.
PAYLOAD B: protobuf envelope bytes of every message (frame headers excluded).
WIRE B: bytes written to the connection (socket length prefix or WebSocket
  frame headers included). WIRE OVH% = (wire - payload) / payload.
DEFLATE AS-IS: estimate of permessage-deflate as gorilla/websocket v1.5.3
  would send it with EnableCompression (no context takeover, level 1).
DEFLATE CTX: estimate with context takeover (a shared window, level 1).
  Both are estimates on payload bytes; frame headers excluded.
FULL/B/FULL and PATCH/B/PATCH (wssink): full updates and patches (row +
  shift) as that sink received them, with payload bytes per message of each
  kind. The server does not split its per-client bytes by kind; every client
  gets the same pane stream here, and the sink's kinds are checked to sum to
  the server's PANE PAYLOAD B for its client.
UPD/S is over the active workload: from its start to the first client's
  last pty output, excluding the settle's trailing quiet. Counts include the
  quiet tail after it, which holds the pane's idle flip (a forced full per
  client, see IDLE_QUIET).
All counts are after the baseline taken once every client had attached
and the session had gone quiet."""


# -- plumbing ----------------------------------------------------------

def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def port_open(port: int) -> bool:
    try:
        with socket.create_connection(("127.0.0.1", port), timeout=0.2):
            return True
    except OSError:
        return False


def wait_until(pred, timeout: float, what: str, interval: float = 0.05) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if pred():
            return
        time.sleep(interval)
    if not pred():
        raise ScenarioFailed(f"timed out after {timeout}s waiting for {what}")


def transport_counts(stats: dict) -> dict:
    counts = {"socket": 0, "websocket": 0}
    for c in stats.get("clients") or []:
        counts[c["transport"]] = counts.get(c["transport"], 0) + 1
    return counts


def run_scenario(sc: Scenario, args, wssink: str) -> dict:
    run = Run(sc, args, wssink)
    try:
        run.start()
        pane = run.pane
        before, reply_bytes = run.reporter.server_traffic()
        sink_base = [s.snapshot() for s in run.sinks]
        t0 = time.monotonic()
        sc.workload(run, args)
        settled = time.monotonic() - t0
        # The workload returns after its output has been quiet for a
        # settle window; rates over that would understate a burst. The
        # active part ends at the first client's last pty output.
        last = run.clients[0][2].last_read_at()
        workload = last - t0 if last is not None and last > t0 else settled
        run.quiesce()
        after, _ = run.reporter.server_traffic()
        sink_mid = [s.snapshot() for s in run.sinks]
        window = time.monotonic() - t0
        run.output = run.clients[0][2].output()
        finals = run.teardown()
    except BaseException:
        run.kill_all()
        raise
    delta = traffic_delta(after, before)
    discount_reply(delta, reply_bytes)
    sinks = [sink_delta(m, b) for m, b in zip(sink_mid, sink_base)]
    res = {
        "name": sc.name,
        "pane": {"cols": pane["Width"], "rows": pane["Height"]},
        "pty": {"cols": run.pty_cols, "rows": run.pty_rows},
        "workload_seconds": workload,
        "settled_seconds": settled,
        "window_seconds": window,
        "before": before, "after": after, "delta": delta,
        "sink_baseline": sink_base, "sink_after": sink_mid, "sink_final": finals,
        "sinks": sinks,
    }
    if (pane["Width"], pane["Height"]) != (sc.cols, sc.rows):
        print(f"traffic: {sc.name}: pane is {pane['Width']}x{pane['Height']}, "
              f"wanted {sc.cols}x{sc.rows}", file=sys.stderr)
    if args.profile:
        prefix = run.overlay["WIDEBOI_CPUPROFILE"]
        d, base = os.path.split(prefix)
        res["profiles"] = sorted(os.path.join(d, f) for f in os.listdir(d)
                                 if f.startswith(base + "."))
    problems = self_check(sc, run, delta, sinks)
    if problems:
        res["problems"] = problems
    return res


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--seconds", type=float, default=10.0,
                    help="workload duration for typing, scroll-paced and large (default 10)")
    ap.add_argument("--only", action="append", choices=[s.name for s in SCENARIOS],
                    help="run only this scenario (repeatable)")
    ap.add_argument("--outdir", default=os.path.join(ROOT, "tmp", "traffic"))
    ap.add_argument("--profile", action="store_true",
                    help="write pprof CPU and alloc profiles to <outdir>/<scenario>.*")
    args = ap.parse_args()

    if not os.access(BIN, os.X_OK):
        print(f"traffic: {BIN} not found; run `make build`", file=sys.stderr)
        return 2
    os.makedirs(args.outdir, exist_ok=True)
    build_dir = private_run_dir("wideboi-traffic-build-")
    wssink = os.path.join(build_dir, "wssink")
    # Built once up front, so compile time stays out of the measurement.
    subprocess.run(["go", "build", "-o", wssink, "./scripts/wssink"], cwd=ROOT, check=True)

    started = time.strftime("%Y%m%d-%H%M%S")
    results, failed = [], []
    for sc in SCENARIOS:
        if args.only and sc.name not in args.only:
            continue
        if sc.needs and shutil.which(sc.needs) is None:
            print(f"traffic: {sc.name}: skipped, {sc.needs} not found\n")
            results.append({"name": sc.name, "skipped": f"{sc.needs} not found"})
            continue
        print(f"traffic: running {sc.name}...", file=sys.stderr, flush=True)
        try:
            res = run_scenario(sc, args, wssink)
        except ScenarioFailed as exc:
            failed.append(sc.name)
            print(f"traffic: {sc.name}: FAILED: {exc}", file=sys.stderr)
            results.append({"name": sc.name, "error": str(exc)})
            continue
        results.append(res)
        print(report(res) + "\n", flush=True)
        if "problems" in res:
            failed.append(sc.name)
            for p in res["problems"]:
                print(f"traffic: {sc.name}: FAILED: {p}", file=sys.stderr)

    print(LEGEND)
    doc = {
        "started": started,
        "argv": sys.argv[1:],
        "machine": {"platform": platform.platform(), "machine": platform.machine(),
                    "processor": platform.processor(), "cpus": os.cpu_count()},
        "scenarios": results,
    }
    path = os.path.join(args.outdir, f"traffic-{started}.json")
    with open(path, "w") as f:
        json.dump(doc, f, indent=2)
    print(f"\nraw results: {path}")
    if failed:
        print(f"traffic: failed scenarios: {', '.join(failed)}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    run_main(main)
