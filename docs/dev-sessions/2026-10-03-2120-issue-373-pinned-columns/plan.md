# Plan: Pinned Columns (Issue #373)

**Goal:** Implement pinned columns anchored to the left of the screen in both scroll and card layout modes, supported by protocol updates, server verbs, config, and commands.

**Approach:** Extend `layout.Column` and `protocol.ColumnData` with `Pinned bool`. Update `ScrollStrategy` and `CardStrategy` to partition pinned vs unpinned columns and render unpinned content in `viewportWidth - pinnedWidth`. Add `VerbTogglePin` to protocol and server, and add `:pin-pane`/`:unpin-pane` commands and `<prefix> P` binding.

**Tech stack:** Go, protobuf wire protocol, Ultraviolet VT layout.

---

## Phase 1: Protocol & Schema Updates

Update protobuf schema and protocol messages.

**Files:**
- Modify: `internal/protocol/wirepb/wideboi.proto` — add `VERB_TYPE_TOGGLE_PIN = 15;` and `bool pinned = 4;` to `ColumnData`
- Regenerate bindings: `npx @bufbuild/buf generate`
- Modify: `internal/protocol/messages.go` — add `VerbTogglePin`, add `Pinned bool` to `ColumnData`
- Modify: `internal/protocol/codec.go` — marshal/unmarshal `Pinned` and `VerbTogglePin`
- Modify: `internal/protocol/codec_test.go` — test `VerbTogglePin` in codec map
- Modify: `internal/protocol/version.go` — bump to 25

**Verification — automated:**
- [x] `go test -v ./internal/protocol/...` passes — **verified wirepb roundtrips, version guard at 25, and web client protocol version**

---

## Phase 2: Layout Engine Support (Scroll & Card Strategies)

Implement pinned column partitioning and placements in `internal/layout/`.

**Files:**
- Modify: `internal/layout/layout.go` — `Column.Pinned`, `PinColumn`, `UnpinColumn`, `TogglePinColumn`, `reorderPinnedColumns`, `ScrollStrategy.ComputePlacements`
- Modify: `internal/layout/card.go` — `CardStrategy.ComputePlacements`
- Test: `internal/layout/layout_test.go`
- Test: `internal/layout/card_test.go`

**Key changes:**
- `ScrollStrategy.ComputePlacements`: places pinned columns at `0..pinnedWidth`, scrolls unpinned columns in `[pinnedWidth, viewportWidth]`.
- `CardStrategy.ComputePlacements`: places pinned columns at `0..pinnedWidth`, fans unpinned columns in `[pinnedWidth, viewportWidth]`.

**Verification — automated:**
- [x] `go test -v ./internal/layout/...` passes — **all 39 tests passed, including pinned scroll and card tests**

---

## Phase 3: Configuration, Server Verbs & Commands

Expose pinning via config, server verbs, commands, and keybindings.

**Files:**
- Modify: `internal/config/config.go` — `StartupPane.Pinned`
- Modify: `cmd/wideboi/main.go` — forward `Pinned` to server startup
- Modify: `internal/server/server.go` — `StartupPane.Pinned`
- Modify: `internal/server/handlers.go` — handle `spec.Pinned` in attach, handle `VerbTogglePin`
- Modify: `internal/commands/registry.go` — add `:pin-pane`, `:unpin-pane`, `:toggle-pin`
- Modify: `internal/keys/keys.go` — add `ActionNameTogglePin` bound to `"P"`
- Test: `internal/server/server_test.go` — test startup pinning and `VerbTogglePin`

**Verification — automated:**
- [x] `go test -v ./internal/server/...` passes — **verified TestStartupPinnedColumns and TestVerbTogglePin**
- [x] `make quick` passes cleanly — **`go vet`, `seam-check`, Go test suite, and 182 Vitest web tests passed**
