# Implementation Plan: Issue 328 OSC 8 Hyperlinks

**Goal:** Support explicit OSC 8 terminal hyperlinks across wire protocol, server extraction, TUI client rendering, and Web client link detection.

## Proposed Changes

### Phase 1: Protocol Schema & Codec
1. `internal/protocol/wirepb/wideboi.proto`:
   - Add `uint32 link_id = 4;` to `CellData`.
   - Add `repeated string links = 13;` to `MsgPaneUpdate`.
   - Add `repeated string links = 15;` to `MsgPanePatch`.
2. Run `make proto` to regenerate Go and TypeScript protobuf code.
3. `internal/protocol/version.go`:
   - Bump `Version` to `19`.
4. `web/src/version.ts`:
   - Bump `PROTOCOL_VERSION` to `19`.
5. `internal/protocol/messages.go`:
   - Add `LinkID uint32` to `CellData`.
   - Add `Links []string` to `MsgPaneUpdate` and `MsgPanePatch`.
6. `internal/protocol/codec.go`:
   - Encode/decode `LinkID` in `encodeLine` and `decodeLine`.
   - Marshal/unmarshal `Links` in `MsgPaneUpdate` and `MsgPanePatch`.
7. `internal/protocol/pane_patch.go`:
   - Propagate `Links` in `BuildPanePatch` and `ApplyPanePatch`.
8. `internal/protocol/version_guard_test.go`:
   - Update expected schema hash for v19.

### Phase 2: Server Pane Extraction
1. `internal/server/pane.go`:
   - In `UpdateMessageForOffset`:
     - Deduplicate `c.Link.URL` per update frame into an indexed slice `links []string` and `linkMap map[string]uint32`.
     - Assign 1-indexed `linkID` to `CellData.LinkID`.
     - Attach `Links: links` to `MsgPaneUpdate`.

### Phase 3: TUI Client Hyperlink Propagation
1. `internal/client/client.go`:
   - In `applyPaneUpdateLocked`:
     - When `cell.LinkID > 0` and within bounds of `m.Links`, set `uvCell.Link = uv.NewLink(m.Links[cell.LinkID-1])`.
2. Unit test in `internal/client/screen_test.go`:
   - `TestOSC8HyperlinkPreservedInMirrorAndHostScreen`: verify that cells carrying `LinkID` set `Link.URL` on mirror and host screen cells within pane bounds, and cells outside pane bounds have empty links.

### Phase 4: Web Client Link Detection & Security
1. `web/src/pane-state.ts`:
   - In `PaneStore.applyPatch`: retain links table from patch or base (`links: patch.links?.length ? patch.links : base.links`).
   - In `findUrlAt`:
     - Check if cell has `linkId > 0`.
     - Validate with `isSafeUrl` to reject `javascript:`, `data:`, etc., while allowing `http:`, `https:`, `mailto:`, `ssh:`, `git:`, `gemini:`.
     - Trace start and end horizontal extent of same `linkId`.
2. Web tests in `web/tests/links-and-clipboard.spec.ts` (or `web/src/pane-rendering.test.ts`):
   - Unit test for OSC 8 hyperlink lookup and safe scheme validation in `pane-rendering.test.ts`.
   - Browser acceptance test in `web/tests/links-and-clipboard.spec.ts` testing hover/title on OSC 8 link and rejection of `javascript:`.

## Verification Plan
1. `make proto-check` to verify protobuf generation and wire sync.
2. `go test ./internal/protocol/...`
3. `go test ./internal/server/...`
4. `go test ./internal/client/...`
5. `cd web && npm test`
6. `make quick`
