# Notes: Issue 328 OSC 8 Hyperlinks

- Worktree: `.worktrees/issue-328-osc8-hyperlinks`
- Branch: `issue-328-osc8-hyperlinks`
- Status: Completed implementation and full verification pass.

## Decisions & Learnings
- Wire Protocol: Bumped to v19. Added `uint32 link_id` to `CellData` (0 = no link, 1-indexed into links table), and `repeated string links` to `MsgPaneUpdate` and `MsgPanePatch`.
- Server Extraction: `internal/server/pane.go` extracts `c.Link.URL` from `uv.Cell` when generating update messages, maintaining stable `LinkID`s across scrolling updates via `p.linkMap` and `p.links` on `Pane`.
- TUI Client: `internal/client/client.go` decodes `cell.LinkID` into `uvCell.Link = uv.NewLink(...)`. Ultraviolet diffing handles OSC 8 escape sequence emission and pane bounds clipping.
- Web Client: `<wideboi-pane>` links are resolved via `findUrlAt` in `web/src/pane-state.ts`, supporting hover pointer cursor, tooltips, and click-to-open.
- Security: `isSafeUrl` restricts OSC 8 links to allowed protocols (`http:`, `https:`, `mailto:`, `ssh:`, `git:`, `gemini:`), rejecting `javascript:`, `data:`, etc.
- Copilot Review Findings Addressed:
  - Sanitized URLs in `internal/protocol/codec.go` with `validUTF8` before protobuf serialization.
  - Made empty link tables in `ApplyPanePatch` and `web/src/pane-state.ts` unconditionally clear/replace prior URLs.
  - Preserved `LinkID` across scrolling in `internal/server/pane.go` so `BuildPanePatch` shift detection succeeds on consecutive scrolls (verified by new unit test `TestPaneUpdatePreservesLinkIDAcrossScroll`).
- Testing: Verified unit tests, Playwright acceptance suite, `smoke.py`, `attachcheck.py`, `golden.py`, `seam-check`, and `go vet`. CI checks pass 100%.
