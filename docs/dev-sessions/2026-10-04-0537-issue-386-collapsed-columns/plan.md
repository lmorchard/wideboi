# Plan: Collapsed Columns (Issue #386)

**Goal:** Implement collapsed columns across layout engine, wire protocol, server, client, and web interface.

**Tech stack:** Go, protobuf, TypeScript/Lit.

---

## Phase 1: Protocol & Schema Updates

Add `collapsed` to `ColumnData` and `VERB_TOGGLE_COLLAPSE` to `VerbType`.

**Files:**
- Modify: `internal/protocol/wirepb/wideboi.proto`
- Regenerate bindings: `npx @bufbuild/buf generate`
- Modify: `internal/protocol/messages.go`
- Modify: `internal/protocol/codec.go`
- Modify: `internal/protocol/version.go` (bump to 29)
- Modify: `web/src/version.ts` (bump to 29)
- Modify: `internal/protocol/version_guard_test.go` (update schema hash)

**Verification — automated:**
- [x] `go test -v ./internal/protocol/...` passes — **verified wirepb roundtrips and version guard at 29**

---

## Phase 2: Layout Engine Implementation

Add `Collapsed` column state, partition reordering, and placement calculation in `ScrollStrategy` and `CardStrategy`.

**Files:**
- Modify: `internal/layout/layout.go` — `Column.Collapsed`, partition reordering, `ScrollStrategy.ComputePlacements`
- Modify: `internal/layout/card.go` — `CardStrategy.ComputePlacements` with collapsed columns on the right
- Test: `internal/layout/layout_test.go` — test `ScrollStrategy` placement and boundary move guards
- Test: `internal/layout/card_test.go` — test `CardStrategy` placement with pinned on left and collapsed on right

**Verification — automated:**
- [x] `go test -v ./internal/layout/...` passes — **verified rapid property tests and placement calculations with collapsed columns**

---

## Phase 3: Server, Keys, Commands & Web Integration

Wire `VerbToggleCollapse` into server, keybindings, command palette, and web client.

**Files:**
- Modify: `internal/server/handlers.go` — handle `VerbToggleCollapse`
- Modify: `internal/server/upgrade.go` — restore `Collapsed` columns across upgrades
- Modify: `internal/keys/keys.go` — bind `C` to `VerbToggleCollapse`
- Modify: `internal/commands/registry.go` — register `:collapse`, `:uncollapse`, `:expand`, `:toggle-collapse`
- Modify: `web/src/card-layout.ts` — support `collapsed` cards
- Modify: `web/src/wideboi-app.ts` — handle collapsed column styling
- Test: `internal/server/server_test.go` — test `VerbToggleCollapse` via server
- Test: `web/src/card-layout.test.ts` — test web card layout with collapsed columns

**Verification — automated:**
- [x] `go test -v ./internal/server -run TestVerbToggleCollapse` passes — **verified server verb handling and layout snapshot broadcast**
- [x] `make quick` passes cleanly — **`go vet`, `seam-check`, Go test suite, and 184 Vitest web tests passed**
