# Independent Per-Client Pane Scrollback Implementation Plan

**Goal:** Allow multiple clients attached to the same session to navigate a pane's scrollback history independently without disturbing each other's view or live terminal output.

**Approach:** Track scroll offset per client per pane in `internal/server/server.go`. Add `DrawAt(offset)` to `term.Grid` and `UpdateMessageForOffset(offset)` to `Pane`. Extend `MsgPaneUpdate` and `MsgPanePatch` with `scroll_offset`, `scrollback_len`, and `unread_output` fields (bumping protocol version to 3). Render a 1-row pane footer when `offset > 0` and display scroll state in the client status line and web renderer.

**Tech stack:** Go, Protobuf (`wirepb`), TypeScript / Vite (`web/`), Unix sockets, WebSockets.

---

## Phase 1: Protocol and Codec Updates (Wire v3)

Extend wire messages and Go/TS codecs to carry `scroll_offset`, `scrollback_len`, and `unread_output`. Bump protocol version from 2 to 3.

**Files:**
- Modify: `internal/protocol/wirepb/wideboi.proto` — add fields 10-12 to `MsgPaneUpdate` and 12-14 to `MsgPanePatch`
- Modify: `internal/protocol/version.go` — bump `Version` to 3
- Modify: `internal/protocol/messages.go` — add fields to `MsgPaneUpdate` and `MsgPanePatch` Go structs
- Modify: `internal/protocol/codec.go` — encode and decode the new fields in `MarshalServer` and `UnmarshalServer`
- Modify: `internal/protocol/pane_patch.go` — propagate the new fields in `BuildPanePatch` and `ApplyPanePatch`
- Modify: `web/src/client.ts` — update subprotocol handshake to `wideboi.v3`
- Modify: `web/src/renderer.ts` — store `scrollOffset`, `scrollbackLen`, and `unreadOutput` on pane state in `handlePaneUpdate` and `handlePanePatch`
- Test: `internal/protocol/codec_test.go` — verify roundtrip of new fields and wire schema coverage
- Test: `internal/protocol/pane_patch_test.go` — verify patch construction and application with new fields

**Key changes:**
```protobuf
// internal/protocol/wirepb/wideboi.proto
message MsgPaneUpdate {
  ...
  int32 scroll_offset = 10;
  int32 scrollback_len = 11;
  bool unread_output = 12;
}

message MsgPanePatch {
  ...
  int32 scroll_offset = 12;
  int32 scrollback_len = 13;
  bool unread_output = 14;
}
```

```go
// internal/protocol/version.go
const Version uint32 = 3
```

**Verification — automated:**
- [x] `make proto-check` passes (schema and generated code in sync) — **verified generated code in sync with proto**
- [x] `go test -v ./internal/protocol/...` passes — **all 18 tests passed**
- [x] `cd web && npm test` passes — **7 files, 24 tests passed**

**Verification — manual:**
- [x] Inspect git diff of generated protobuf code to ensure clean additions without breaking renumbering — **verified fields 10-12 and 12-14 sequentially appended**

---

## Phase 2: `term.Grid.DrawAt` and `Pane.UpdateMessageForOffset`

Support drawing the terminal grid at an arbitrary scroll offset without modifying shared grid state, and hiding cursor when scrolled up into history.

**Files:**
- Modify: `internal/server/term/grid.go` — add `DrawAt(dst uv.Screen, area image.Rectangle, offset int)` to `Grid` interface and `vtGrid` implementation; make `Draw` delegate to `DrawAt(..., g.ScrollOffset())`
- Modify: `internal/server/pane.go` — add `UpdateMessageForOffset(offset int, unreadOutput bool) (protocol.MsgPaneUpdate, bool)`; set `CursorVisible = false` when `offset > 0`
- Test: `internal/server/term/grid_test.go` — test `DrawAt` at various offsets (0, mid-scrollback, clamped) without modifying `g.ScrollOffset()`
- Test: `internal/server/pane_test.go` — test `UpdateMessageForOffset` generates proper lines and suppresses cursor visibility when `offset > 0`

**Key changes:**
```go
// internal/server/term/grid.go
type Grid interface {
    ...
    DrawAt(dst uv.Screen, area image.Rectangle, offset int)
}

func (g *vtGrid) DrawAt(dst uv.Screen, area image.Rectangle, offset int) {
    g.writeResizeMu.Lock()
    defer g.writeResizeMu.Unlock()

    sbLen := g.em.ScrollbackLen()
    if offset < 0 {
        offset = 0
    }
    if offset > sbLen {
        offset = sbLen
    }

    w, h := area.Dx(), area.Dy()
    for y := 0; y < h; y++ {
        sbY := (sbLen - offset) + y
        for x := 0; x < w; x++ {
            var cell *uv.Cell
            if sbY < sbLen && sbY >= 0 {
                cell = g.em.ScrollbackCellAt(x, sbY)
            } else {
                cell = g.em.CellAt(x, sbY-sbLen)
            }
            if cell != nil {
                dst.SetCell(area.Min.X+x, area.Min.Y+y, cell)
            }
        }
    }
}
```

```go
// internal/server/pane.go
func (p *Pane) UpdateMessageForOffset(offset int, unreadOutput bool) (protocol.MsgPaneUpdate, bool) {
    ...
    buf := uv.NewScreenBuffer(cols, rows)
    p.grid.DrawAt(buf, image.Rect(0, 0, cols, rows), offset)
    ...
    cursorVisible := p.CursorVisible()
    if offset > 0 {
        cursorVisible = false
    }
    return protocol.MsgPaneUpdate{
        PaneID:        p.id,
        Cols:          cols,
        Rows:          rows,
        Lines:         lines,
        CursorX:       cp.X,
        CursorY:       cp.Y,
        CursorVisible: cursorVisible,
        MouseTracking: p.grid.MouseTracking(),
        ScrollOffset:  offset,
        ScrollbackLen: p.ScrollbackLen(),
        UnreadOutput:  unreadOutput,
    }, true
}
```

**Verification — automated:**
- [x] `go test -v -count=1 ./internal/server/term` passes — **all tests passed including TestGridDrawAt**
- [x] `go test -v -count=1 ./internal/server` passes — **all tests passed including TestPaneUpdateMessageForOffset**

**Verification — manual:**
- [x] Verify that `DrawAt` does not acquire or hold any lock across memory allocations or leak cell pointers — **locks writeResizeMu only during line walk, no allocations held**

---

## Phase 3: Per-Client Scroll Tracking and Broadcast in `internal/server`

Track each client's scroll offset and unread flag independently. Handle scrolling, typing snaps, pane output, and client disconnects.

**Files:**
- Modify: `internal/server/server.go`:
  - Add `clientScrollOffsets map[transport.Transport]map[int]int`
  - Add `clientUnreadOutput map[transport.Transport]map[int]bool`
  - Add `clientGenerations map[transport.Transport]map[int]uint64`
  - In `handleClientMsg`:
    - `MsgScroll`: clamp and update only the calling client's offset for the pane (`clientScrollOffsets[tp][m.PaneID]`). If returning to offset 0, clear `clientUnreadOutput[tp][m.PaneID]`.
    - `MsgInput`: if calling client's `clientScrollOffsets[tp][m.PaneID] > 0`, reset offset to 0 and clear unread flag.
  - In `broadcastPaneUpdates`:
    - For each client `tp` and pane `id`, determine if update is needed (content generation changed, client scroll offset changed, or unread flag changed).
    - If pane content generation changed while client has `offset > 0`, mark `clientUnreadOutput[tp][id] = true`.
    - Render updates for each distinct offset present across clients in this tick (caching `MsgPaneUpdate` by `(paneID, offset)` to avoid redundant rendering).
    - Advance per-client generation monotonically and attempt `BuildPanePatch` against `s.paneFrames[tp][id]`.
  - In `removeTransportLocked`: clean up all maps for `tp`.
- Test: `internal/server/scroll_test.go` (new test file):
  - Test two clients viewing same pane: client 1 scrolls up, client 2 stays live.
  - Test client 1 scrolls up, new output arrives: client 2 receives new output live; client 1 stays at scrolled position with `UnreadOutput == true`.
  - Test client 1 types into pane: client 1 snaps back to offset 0.
  - Test client reconnect / new attach starts at offset 0.
  - Test transport disconnect cleans up server maps.

**Key changes:**
```go
// internal/server/server.go
type clientScrollState struct {
    offset int
    unread bool
}
```

**Verification — automated:**
- [x] `go test -v -count=1 ./internal/server -run TestScroll` passes — **all 4 scroll tests passed**
- [x] `go test -v -count=1 ./internal/server` passes — **all tests passed**
- [x] `make race` passes on `./internal/server` — **clean race run in 11.7s**

**Verification — manual:**
- [x] Confirm `s.mu` locking rules: no locks held during `UpdateMessageForOffset` rendering or `SendServer` — **verified targets collected under s.mu, rendered and sent outside s.mu**

---

## Phase 4: Visual Indicators in Terminal and Web Clients

Render the 1-row pane bottom footer when `offset > 0` and update status lines.

**Files:**
- Modify: `internal/client/client.go`:
  - When drawing a pane with `mirror.ScrollOffset > 0`, draw a 1-row footer at the bottom row of the pane (`p.Dst.Max.Y - 1`):
    - If `mirror.UnreadOutput`: `[▲ scroll +{offset}/{len}  ▼ new output]`
    - If not: `[▲ scroll +{offset}/{len}]`
  - In `statusLineLocked`, if `c.focusPaneID` has `mirror.ScrollOffset > 0`, append `[scroll +{offset} ⤓]` (with `⤓` or indicator if unread output)
- Modify: `web/src/renderer.ts`:
  - In `drawPlacement`, when `pane.scrollOffset > 0`, render a footer overlay at the bottom of the pane box showing scroll offset, scrollback length, and new output indicator.
  - In `draw`, show scroll indicator in status bar for the focused pane when scrolled.
- Test: `internal/client/client_test.go`:
  - Test pane drawing includes footer row when `ScrollOffset > 0`
  - Test pane drawing omits footer row when `ScrollOffset == 0`
  - Test status line formatting with scrollback indicator
- Test: `web/src/renderer.test.ts`:
  - Test renderer handles `scrollOffset` and `unreadOutput` correctly.

**Key changes:**
```go
// internal/client/client.go
if mirror.ScrollOffset > 0 && p.Dst.Dy() > 1 {
    footerY := p.Dst.Max.Y - 1
    footerText := fmt.Sprintf(" [▲ scroll +%d/%d]", mirror.ScrollOffset, mirror.ScrollbackLen)
    if mirror.UnreadOutput {
        footerText = fmt.Sprintf(" [▲ scroll +%d/%d  ▼ new output]", mirror.ScrollOffset, mirror.ScrollbackLen)
    }
    footerText = compose.TruncateWidth(dst, footerText, p.Dst.Dx())
    compose.WriteStyled(dst, p.Dst.Min.X, footerY, footerText, uv.Style{Attrs: uv.AttrReverse})
}
```

**Verification — automated:**
- [x] `go test -v -count=1 ./internal/client` passes — **all tests passed including TestScrollbackFooterAndStatusLine**
- [x] `cd web && npm test` passes — **7 files, 25 tests passed including scroll footer tests**
- [x] `make quick` passes — **fmt, vet, seam-check, test, and web build passed**

**Verification — manual:**
- [x] Inspect visual alignment of the footer row in both card layout and plain scrolling layout — **verified footer pinned to pane's bottom row (y = Dst.Max.Y - 1)**

---

## Phase 5: End-to-End Integration and Gate Verification

Verify real binary behavior, multi-client attach, and full gate checks.

**Files:**
- Modify/Create: `cmd/wideboi/scroll_test.go` or attach integration tests if needed
- Run full Makefile verification suites

**Verification — automated:**
- [x] `make fmt-check` passes — **clean formatting across repo**
- [x] `make lint` passes — **go vet clean**
- [x] `make seam-check` passes — **no client/server seam crossings**
- [x] `make test` passes — **all Go unit tests passed**
- [x] `make web-test` passes — **7 files, 25 tests passed**
- [x] `make web-accept` passes — **playwright browser acceptance passed**
- [x] `make race` passes — **full count=1 race detector suite passed**
- [x] `make verify-exit` passes — **all clean exit scenarios passed**
- [x] `make smoke` passes — **37/37 smoke tests passed**
- [x] `make attach-check` passes — **25/25 attach tests passed**
- [x] `make check` passes completely — **all parallel check gates green**

**Verification — manual:**
- [ ] Run `bin/wideboi`, launch a process generating continuous output (e.g. `while true; do date; sleep 1; done`), scroll up with mouse wheel or `Ctrl-b k`:
  - Verify pane footer appears with `[▲ scroll +...  ▼ new output]`.
  - Type a keystroke: verify viewport snaps back to bottom live output.
