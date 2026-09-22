# Notes: issue #77, drop the dirtyFrames follow-up tick

## Open question from the spec: resolved
A cursor-only change (focus switch, entering or leaving control mode) does make `Draw` return true: `offscreenHostScreen.equal` (`internal/client/client.go:301-306`) compares cursor visibility and position. The existing smoke cases "focus switch moves the cursor" and "control mode is visible and escapable" stayed green 4/4 without the follow-up.

## Deviation from spec: the test
The spec suggested asserting that `present` on an unchanged screen leaves the cursor in place. That's true on main already, so it can't fail first. The new smoke case `no empty frames` counts the exact bytes of an empty synchronized update with the cursor shown, `?2026h ?25h ?25l ?2026l`. Main wrote 4 of them in a startup plus one command; this branch writes none. A real frame can't match the signature, because with the cursor shown `copyToHostScreen` writes its own `?25h` before the bracket.

## Resize
Checked that resize doesn't depend on the follow-up. It was only ever armed by a changed `Draw`, and a host resize reaches `Draw` through the client's cols/rows (staging screen rebuilt, so it compares as changed), not through the tick.

## Copilot review
- **Attach path uncovered (fixed).** Smoke only runs the standalone binary, so the attach loop's identical change had no test. Added `attached client presents no empty frames` to `scripts/attachcheck.py`, reusing smoke's `EMPTY_SYNC_UPDATE`. It passes here and fails (4 empty updates) with an extra `present` put back into the attach loop only. `make check` ×4 green (attach-check 9/9).
- **"Signature needs correction" (documented, not changed).** No detail was given. The real limit: the signature only catches empty frames while the cursor is shown. With it hidden, an empty update is `?2026h ?2026l`, which a legitimate visibility-only frame also produces (`Draw`'s hide lands before the bracket), so widening the match would false-positive. Both new cases run with the cursor shown, which is the state the old follow-up tick was observable in.
