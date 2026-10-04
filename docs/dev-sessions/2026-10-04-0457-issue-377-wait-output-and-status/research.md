# Research: Native wait-output and wait-status Commands (Issue #377)

## 1. Context & Motivation

Wideboi provides headless automation via `wideboi-control` (`split`, `send`, `dump-pane`, `wait`, `close`).
Currently, `wideboi wait` only waits for a process to exit. For interactive REPLs and long-running agents that do not exit (e.g. Claude Code, OpenCode, shell sessions), orchestrating agents currently poll using bash sleep loops:

```bash
wait_for() {
  for _ in $(seq 100); do
    wideboi capture "$1" -n 5 | grep -qF -- "$2" && return 0
    sleep 0.1
  done
  return 1
}
```

This causes CPU churn, latency, and race conditions. Herdr demonstrates that native server-side wait primitives (`wait-output` and `wait-status`) eliminate polling loops completely.

## 2. Server Wait Architecture

Wideboi's server already has a waiter system for `wideboi wait` (`s.waiters[paneID][]transport.Transport`), which unblocks when a kept pane exits or is closed.

We can extend this pattern to:
1. `s.outputWaiters[paneID][]outputWaiter`:
   - Checks existing tail output immediately on request.
   - If not matched, registers an output waiter.
   - On new PTY output (`p.onOutput()`), evaluates registered waiters.
   - Once matched, sends `MsgWaitOutputResponse` and removes waiter.
   - If pane closes, notifies with error.
2. `s.statusWaiters[paneID][]statusWaiter`:
   - Checks `p.Status()` immediately against `until` status set.
   - If already matched, returns immediately.
   - If not, registers waiter.
   - On status transitions (`SetExplicitStatus`, heuristic updates, process exits), checks waiters.
   - Once matched, sends `MsgWaitStatusResponse` and removes waiter.

## 3. Protocol & CLI

1. `wideboi wait-output <pane-id> [pattern] [--match <str>] [--regex <pat>] [--lines <N>] [--timeout <dur>]`
   - Emits matched line to stdout, exits 0.
   - Exits 124 on timeout.
2. `wideboi wait-status <pane-id> [status] [--until <s1,s2>] [--timeout <dur>]`
   - Emits matched status to stdout, exits 0.
   - Exits 124 on timeout.
3. Wire protocol version bump: 27 &rarr; 28.
