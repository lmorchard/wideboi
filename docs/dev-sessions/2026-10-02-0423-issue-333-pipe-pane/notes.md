# Notes: Issue 333 (pipe-pane raw PTY stream tapping)

- **Worktree:** `.worktrees/issue-333-pipe-pane`
- **Branch:** `issue-333-pipe-pane`
- **Baseline:** `make quick` passed cleanly on origin/main (`91fceab`).
- **Research:** Investigated PTY master reading in `internal/server/pane.go:178`, queryScanner filtering, transport streaming loops, and pane lifecycle.
- **Brainstorm:** Completed interactive Q&A:
  - Client-driven streaming runs in the foreground until Ctrl-C or pane exit.
  - Drop chunks on overflow (256-chunk buffer) to ensure live pane processes never stall.
  - Taps raw bytes directly from `pty.Master.Read` before `queryScanner`.
  - Registered in both root CLI (`wideboi pipe-pane`) and internal registry (`:pipe-pane`) with `--stop` support.
- **Spec & Plan:** Finalized in `spec.md` and `plan.md`.
- **Execution:**
  - Phase 1: Implemented `AddTap`, `RemoveTap`, `broadcastRawBytes`, and `closeTaps` in `Pane`.
  - Phase 2: Added `MsgPipePaneRequest`/`MsgPipePaneResponse` to wire protocol, protobuf schema, and codecs; bumped `protocol.Version` to 24.
  - Phase 3: Added root CLI command `wideboi pipe-pane` with `-o, --output` and `-a, --append` flags.
  - Phase 4: Registered `pipe-pane` (alias `pipe`) in `internal/commands/registry.go` with `--stop` support.
  - Phase 5: Updated `docs/skills/wideboi-control/SKILL.md` with streaming documentation; added e2e tests.
- **Verification:** Unit tests, race tests, pty smoke tests, golden check, and attachcheck suite all passed cleanly.

## Retrospective

### Recap
Implemented `wideboi pipe-pane` in root CLI (`cmd/wideboi/`) and internal command registry (`internal/commands/`) with streaming output to stdout or file (`-o, --output`), append mode (`-a, --append`), and in-session caller pane fallback (`$WIDEBOI_PANE_ID`). Added non-blocking PTY tap fan-out in `Pane` with drop-on-overflow protection (256-chunk buffer) to prevent slow consumers from stalling live panes. Added `MsgPipePaneRequest`/`MsgPipePaneResponse` wire messages, bumping `protocol.Version` to 24. Documented workflows in `docs/skills/wideboi-control/SKILL.md`.

### Scope Drift & Hardening
- **Connection-scoped lifecycle:** Added per-connection context cancellation in `handleClientConnLoop` so disconnected pipe clients immediately clean up their tap subscriptions on quiet panes.
- **Tap generation identity:** Assigned unique `tapGen` identifiers to background taps in `activePipeTaps` so replacing a tap on a pane does not let the departing goroutine's defer delete the new tap's entry.
- **Socket cancellation unblocking:** Bound blocked `net.Conn` reads to tap context cancellation so `--stop` immediately interrupts quiet socket reads.
- **Terminal tap state:** Added `tapsClosed` flag to `Pane` so kept panes after PTY EOF reject new tap requests immediately rather than waiting on closed PTYs.
- **Disk throughput:** Removed per-chunk `Sync()` to prevent disk I/O bottlenecks from creating tap queue drops.
- **E2E synchronization:** Replaced fixed `sleep` intervals with interactive shell trigger-and-observe synchronization, ensuring tests pass deterministically under high CPU load.

### Surprises
- Reading raw bytes directly from `p.pty.Master.Read` prior to `qs.process` provides authentic child process PTY bytes, cleanly separating terminal device query processing from debug recordings.

### Workflow Friction
- Copilot review provided 11 substantive comments across connection lifecycles, error reporting, and test robustness. Addressing them all before merge resulted in rock-solid streaming tap infrastructure.

### Memory Candidates
- When handling streaming subscriptions over client connections, tie the subscription to a connection-scoped context that cancels on transport disconnect; server-wide contexts will leak subscriptions on quiet panes.
- Avoid per-chunk `Sync()` on streaming file taps; unbuffered OS writes already reach the kernel, and forced disk flushes cause consumer lag and drop-on-overflow data loss.

