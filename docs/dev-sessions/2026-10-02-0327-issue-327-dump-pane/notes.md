# Notes: Issue 327 (dump-pane CLI command)

- **Worktree:** `.worktrees/issue-327-dump-pane`
- **Branch:** `issue-327-dump-pane`
- **Baseline:** `make quick` passed cleanly on origin/main (`07145ec`).
- **Research:** Completed codebase exploration covering CLI dispatch, existing `capture`, terminal cell buffers & ANSI styling in ultraviolet, wire protocol & protobuf codecs, and session/pane targeting.
- **Brainstorm:** Completed interactive Q&A:
  - `--scrollback` includes scrollback + screen, with optional line limits.
  - `capture` is an alias to `dump-pane`.
  - `-o, --output` overwrites files.
- **Spec:** Finalized in `spec.md`.
- **Execution:**
  - Phase 1: Implemented `DumpText` on `term.Grid` and `Pane` with pagination, line limits, and ANSI SGR styling.
  - Phase 2: Added `MsgDumpPaneRequest` / `MsgDumpPaneResponse` to wire protocol, protobuf schema, and codecs; bumped `protocol.Version` to 23.
  - Phase 3: Added root CLI command `wideboi dump-pane` with all flags and argument preprocessing; aliased `capture` to `dump-pane`.
  - Phase 4: Registered `dump-pane` (and aliases `dump`, `capture`) in `internal/commands/registry.go`.
  - Phase 5: Updated `docs/skills/wideboi-control/SKILL.md` with paging and inspection documentation; added e2e tests.
- **Verification:** Unit tests, race tests, pty smoke tests, and attachcheck suite all passed cleanly.
