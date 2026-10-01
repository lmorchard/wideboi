# Plan: Issue 337 Soft-Wrap Selection & Copying

## Proposed Changes

### Phase 1: Protocol Schema & Codec (v20)
1. `internal/protocol/wirepb/wideboi.proto`:
   - `LineData`: add `bool wrapped = 2;`
   - `PaneRow`: add `bool wrapped = 3;`
2. Run `make proto` to regenerate Go and TypeScript code.
3. Bump protocol version to `20` in `internal/protocol/version.go` and `web/src/version.ts`.
4. `internal/protocol/messages.go`:
   - `PaneRow`: add `Wrapped bool`
   - `MsgPaneUpdate`: add `WrappedLines []bool`
5. `internal/protocol/codec.go`:
   - `MarshalServer`: assign `Wrapped: wrapped` on `wirepb.LineData` and `wirepb.PaneRow`.
   - `UnmarshalServer`: decode `update.WrappedLines` (allocating full slice even if all false per review finding), and decode `row.Wrapped` on `PaneRow`.
6. `internal/protocol/pane_patch.go`:
   - `BuildPanePatch`: include `WrappedLines` in row change checks and shift checks.
   - `ApplyPanePatch`: propagate `WrappedLines` across shifts and row overwrites.
7. `internal/protocol/version_guard_test.go`:
   - Record v20 schema hash.

### Phase 2: Server Pane Wrap Computation
1. `internal/server/pane.go`:
   - In `UpdateMessageForOffset`:
     - Allocate `wrappedLines := make([]bool, rows)`.
     - For $y < rows-1$, check if cell at `cols-1` is non-empty/non-space and row $y+1$ has content. If so, set `wrappedLines[y] = true`.
     - Assign `WrappedLines: wrappedLines` in returned `MsgPaneUpdate`.

### Phase 3: TUI Client Selection Copying
1. `internal/client/mouse.go`:
   - In `selectionText`: check `pu.WrappedLines[localY]` if available for the pane. If true, set `wrap = true`.
2. `internal/client/mouse_test.go`:
   - Add unit test verifying drag across soft-wrapped rows joins text without newlines.

### Phase 4: Web Client Selection Copying
1. `web/src/pane-state.ts`:
   - In `PaneStore.applyPatch`: set `wrapped: row.wrapped` on created `LineData`.
   - In `selectionText`: iterate through rows; if `pane.lines[lineY]?.wrapped` is false, append `\n`. If true, do not append `\n`.
2. `web/src/pane-rendering.test.ts`:
   - Add unit tests for `selectionText` with wrapped lines.
3. `web/tests/links-and-clipboard.spec.ts`:
   - Add Playwright browser acceptance test verifying that dragging across soft-wrapped lines copies without intermediate newlines.

## Verification
- `go test ./...`
- `cd web && npm test`
- `cd web && npm run test:browser`
- `python3 scripts/smoke.py`
- `python3 scripts/attachcheck.py`
- `python3 scripts/golden.py`
- `make check`
