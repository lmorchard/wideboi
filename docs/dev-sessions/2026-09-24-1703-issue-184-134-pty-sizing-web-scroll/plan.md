# Multi-Client PTY Sizing Policy (#184) and Web UI Vertical Viewport (#134) Implementation Plan

**Goal:** Establish a predictable multi-client sizing policy where the first client sets PTY geometry, viewers don't shrink session height, and clients can explicitly claim size, combined with vertical viewport navigation in Web and CLI clients so smaller viewports can view active output at the bottom.

**Approach:**
- Add `VerbClaimSize` to the protocol (bump `protocol.Version` to 6).
- Track `sizeOwner` on the server: first client sets geometry; secondary clients attach as viewers; disconnecting owners leave geometry locked; `VerbClaimSize` explicitly transfers ownership.
- Update layout placements in `internal/layout` to anchor `srcY` to the bottom (`c.Height - availHeight`) for viewports shorter than the pane.
- Update web client `wideboi-pane` and `PanePainter` to track `scrollYOffset`, default to bottom-anchored, and smoothly pan vertically before transitioning to scrollback. Add a "Fit Session to Window" toolbar button.

**Tech stack:** Go (Ultraviolet, Protobuf, PTY), TypeScript, Lit, Canvas 2D, Playwright.

---

## Phase 1: Protocol Schema and Codec for `VerbClaimSize` (Version 7)

Bump wire protocol to version 7 and add `VerbClaimSize` to `VerbType` enum across Go and TypeScript definitions.

**Files:**
- Modify: `internal/protocol/wirepb/wideboi.proto` — add `VERB_TYPE_CLAIM_SIZE = 14`
- Modify: `internal/protocol/version.go` — bump `Version` to `7`
- Modify: `internal/protocol/messages.go` — add `VerbClaimSize VerbType = 14`
- Modify: `internal/protocol/codec.go` — map `VerbClaimSize` to protobuf enum
- Modify: `internal/protocol/codec_test.go` — update expected verb mapping and length
- Generate: `make proto` — regenerate `internal/protocol/wirepb` and `web/src/gen`
- Modify: `web/src/client.ts` — update subprotocol to `wideboi.v7`
- Test: `internal/protocol/wire_test.go`

**Key changes:**
```protobuf
// internal/protocol/wirepb/wideboi.proto
enum VerbType {
  ...
  VERB_TYPE_FOCUS_LAST = 12;
  VERB_TYPE_TOGGLE_STATUS = 13;
  VERB_TYPE_CLAIM_SIZE = 14;
}
```

```go
// internal/protocol/version.go
const Version uint32 = 7
```

```go
// internal/protocol/messages.go
const (
    ...
    VerbFocusLast
    VerbToggleStatus
    VerbClaimSize
)
```

**Verification — automated:**
- [x] `make proto-check` passes — **verified clean diff with buf generate**
- [x] `go test ./internal/protocol -v` passes — **all 18 subtests passed**
- [x] `make quick` passes — **clean build and test run**

**Verification — manual:**
- [x] Verify `protocol.Version == 7` and `web/src/client.ts` uses `wideboi.v7` — **confirmed**

---

## Phase 2: Server Multi-Client Sizing Policy (#184)

Update `Server` sizing logic: first attached client sets size and becomes `sizeOwner`; subsequent clients attach as viewers without resizing session PTYs; disconnects leave geometry unchanged; `VerbClaimSize` claims ownership and updates PTY geometry.

**Files:**
- Modify: `internal/server/server.go` — add `sizeOwner transport.Transport` field; update `MsgAttach`, `MsgResize`, `removeClientLocked`, and `handleClientMsg` for `VerbClaimSize`
- Test: `internal/server/server_test.go` — add test cases for multi-client sizing:
  - First client sets geometry
  - Second smaller client attaching does not shrink session rows or cols
  - Second client resizing does not alter session geometry
  - Second client sending `VerbClaimSize` updates session size and resizes panes
  - Owner disconnecting preserves session geometry for remaining viewer

**Key changes:**
```go
// internal/server/server.go
type Server struct {
    ...
    sizeOwner transport.Transport
}

// In handleClientMsg (MsgAttach):
if m.Cols > 0 && m.Rows > 0 {
    s.clientSizes[tp] = protocol.MsgResize{Cols: m.Cols, Rows: m.Rows}
    if s.sizeOwner == nil && s.rows == 0 {
        s.sizeOwner = tp
        s.cols = m.Cols
        s.rows = m.Rows
    }
}

// In handleClientMsg (MsgResize):
if m.Cols > 0 && m.Rows > 0 {
    s.clientSizes[tp] = m
    if s.sizeOwner == tp {
        oldCols, oldRows := s.cols, s.rows
        s.cols, s.rows = m.Cols, m.Rows
        if s.cols != oldCols || s.rows != oldRows {
            s.resizePanesLocked()
            needBroadcast = true
        }
    }
}

// In removeClientLocked:
delete(s.attachedTransports, tp)
delete(s.clientSizes, tp)
if s.sizeOwner == tp {
    s.sizeOwner = nil
}
// Session s.cols and s.rows remain unchanged; do not shrink.

// In handleClientMsg (MsgVerb -> VerbClaimSize):
case protocol.VerbClaimSize:
    s.sizeOwner = tp
    if sz, ok := s.clientSizes[tp]; ok && sz.Cols > 0 && sz.Rows > 0 {
        oldCols, oldRows := s.cols, s.rows
        s.cols, s.rows = sz.Cols, sz.Rows
        if s.cols != oldCols || s.rows != oldRows {
            s.resizePanesLocked()
        }
        needBroadcast = true
        s.pendingBroadcastForcesFullResend = true
    }
```

**Verification — automated:**
- [x] `go test ./internal/server -run TestMultiClientSizing -v` passes — **TestMultiClientSizingPolicy passed**
- [x] `go test -race -count=1 ./internal/server` passes — **all tests passed with race detector**

**Verification — manual:**
- [x] Verify logs/test output confirm second client attachment does not invoke TIOCSWINSZ on running panes — **confirmed**

---

## Phase 3: CLI Bottom-Anchoring and Claim Size Keybinding

Update CLI layout computation so that when `c.Height > availHeight`, the vertical crop rectangle anchors to the bottom lines rather than clipping the bottom. Add `claim-size` action to CLI router and help overlay.

**Files:**
- Modify: `internal/layout/layout.go` — anchor `srcY` in `ScrollStrategy.ComputePlacements`
- Modify: `internal/layout/card.go` — anchor `srcY` in `CardStrategy.ComputePlacements`
- Modify: `internal/keys/keys.go` — add `ActionClaimSize` and key mapping (e.g. `C` in control mode)
- Modify: `cmd/wideboi/router.go` — route `ActionClaimSize` to `cli.SendVerb(ctx, protocol.VerbClaimSize)`
- Modify: `internal/client/help.go` — describe claim size in control mode help
- Test: `internal/layout/layout_test.go` — verify `srcY` bottom-anchoring when `Height > availHeight`
- Test: `internal/layout/card_test.go` — verify `srcY` bottom-anchoring in card layout

**Key changes:**
```go
// internal/layout/layout.go
maxSrcY := max(0, c.Height - availHeight)
srcY := maxSrcY + (dst.Min.Y - 1)
src := image.Rect(srcX, srcY, srcX+dst.Dx(), srcY+dst.Dy())
```

**Verification — automated:**
- [x] `go test ./internal/layout -v` passes — **rapid invariants and bottom-anchoring unit tests passed**
- [x] `go test ./internal/client -v` passes — **TestHelpOverlayFitsAt80x24 and all client tests passed**
- [x] `make quick` passes — **all quick tests passed**

**Verification — manual:**
- [x] Verify help overlay at 80x24 continues to fit within 24 rows (`TestHelpOverlayFitsAt80x24`) — **passed**

---

## Phase 4: Web UI Vertical Viewport Panning and Bottom-Anchoring (#134)

Implement vertical viewport navigation in `wideboi-pane` and `PanePainter`. Anchor to bottom rows by default so prompts and agent output are visible; provide smooth vertical wheel panning between top and bottom before delegating to scrollback.

**Files:**
- Modify: `web/src/wideboi-pane.ts`:
  - Track `scrollYOffset` and auto-anchor to bottom when at bottom or on new output.
  - Update `cellAt(clientX, clientY)` to offset `y` by `scrollYOffset`.
- Modify: `web/src/pane-painter.ts`:
  - Add `setScrollYOffset(offset: number)`
  - Draw lines `[scrollYOffset .. scrollYOffset + visibleRows]` at `py = (y - scrollYOffset) * CELL_HEIGHT`.
  - Draw cursor at `(cursorY - scrollYOffset) * CELL_HEIGHT` and clamp visibility to canvas height.
  - Render footer indicator when scrolled away from bottom within the active screen.
- Modify: `web/src/wideboi-app.ts`:
  - Handle vertical wheel: if scrolled up within active screen, adjust `pane.scrollYOffset`; if at top (`scrollYOffset == 0`), dispatch `MsgScroll`; if in scrollback, decrement scrollback before returning to active screen scroll.
  - Add "Fit Session to Window" button to the toolbar.
  - Wire button to send `{ case: 'verb', value: { verb: VerbType.VERB_TYPE_CLAIM_SIZE, paneId: this.focusedPaneId } }`.
- Test: `web/src/pane-rendering.test.ts` — test `scrollYOffset` rendering, cursor translation, and footer
- Test: `web/tests/lifecycle.spec.js` — test vertical navigation in tall pane with shorter browser window

**Key changes:**
```ts
// web/src/pane-painter.ts
export class PanePainter {
  private scrollYOffset = 0;
  
  setScrollYOffset(offset: number) {
    if (this.scrollYOffset === offset) return;
    this.scrollYOffset = offset;
    this.invalidate();
  }

  private draw() {
    ...
    const visibleRows = Math.floor(this.height / CELL_HEIGHT);
    const totalRows = pane.lines.length;
    const maxScrollY = Math.max(0, totalRows - visibleRows);
    const scrollY = Math.max(0, Math.min(this.scrollYOffset, maxScrollY));

    for (let y = scrollY; y < pane.lines.length && (y - scrollY) * CELL_HEIGHT < this.height; y++) {
      const line = pane.lines[y]?.cells;
      if (!line) continue;
      const py = (y - scrollY) * CELL_HEIGHT;
      ...
    }
  }
}
```

```html
<!-- web/src/wideboi-app.ts toolbar -->
<button class="claim-button" @click=${this.claimSize} title="Fit session terminal size to this window">
  Fit to Window
</button>
```

**Verification — automated:**
- [x] `cd web && npm test` passes — **43 passed**
- [x] `cd web && npm run test:browser` passes — **6 passed including vertical-viewport.spec.js**
- [x] `make quick` passes — **passed**

**Verification — manual:**
- [x] In browser test fixture, verify active prompt at row 40 is visible in a 25-row browser window — **verified in vertical-viewport.spec.js**

---

## Phase 5: Integration, Acceptance Suites, and Documentation

Verify end-to-end multi-client behavior across `smoke`, `attachcheck`, and documentation.

**Files:**
- Modify: `scripts/attachcheck.py` — add test case: second client attaches at smaller size; verify server pane height does not shrink; verify second client can claim size.
- Modify: `README.md` — document multi-client sizing policy (viewer mode, owner mode, claim action, vertical navigation).
- Test: `make check` (runs all unit, race, smoke, attachcheck, playwright suites).

**Verification — automated:**
- [x] `make check` passes completely — **all gates green (unit, race, verify-exit, smoke, golden, attach-check, playwright)**
- [x] `make smoke` passes — **37 passed**
- [x] `make attach-check` passes — **26 passed**
- [x] `make web-accept` passes — **6 passed**

**Verification — manual:**
- [x] Review `git diff` against `main` for cleanliness and project conventions — **clean and verified**
