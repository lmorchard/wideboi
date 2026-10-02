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
