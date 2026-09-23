# Notes — #85 change-only pane updates

## Session log

- **Start.** The worktree baseline `make quick` was green. The project-board move failed: the `gh` token lacks the
  `project` scope (`gh auth refresh -s project`), so board moves are skipped this session.
- **Brainstorm.** Les chose a generation counter and per-client delivery tracking (both recommended). The
  research turned up one constraint the issue didn't mention: the client prunes or blanks mirrors on
  a snapshot (`client.go:188-200`), so `broadcastLayout` must keep forcing full pane updates.
- **Plan.** Writing the code out surfaced a race: overlapping `broadcastPaneUpdates` rounds could deliver an old
  render last but record the newer generation. The fix is `paneSendMu`. Added to the spec.
- **Phase 1** (`4ecbde2`). Straightforward. All four sabotages failed for the right reason.
- **Phase 2.**
  - The wire test's name overflowed darwin's 104-byte unix socket path under `t.TempDir()`. Used the existing
    `os.MkdirTemp("", "wb")` pattern.
  - **Finding: the planned typing check could not fail.** With `vtGrid.Write`'s bump removed, the wire test
    still passed. Typing into an idle pane changes its status from Idle to Working, and
    `broadcastLayoutIfStatusChanged` then forces a resend of every pane. Any keystroke into an idle pane
    reaches the client through the status path, whatever the generation says.
  - Reworked the test: the first key wakes the pane, then it waits for a quiet stretch inside the 3s decay. The
    second key must then arrive with no snapshot in front of it. Re-sabotaged: red with `typing "y" produced no
    MsgPaneUpdate`.
  - Aside: the status-driven forced resend also hides a missing bump in manual testing whenever the pane was
    idle. Keep that in mind when checking a new mutation path by hand.

## For later (not fixed here)

- `vtGrid.Draw`'s doc comment (`grid.go`, above `Draw`) and the `writeResizeMu` field comment both describe an
  offset-0 "fast path" through `g.em.Draw` that the body no longer has.
