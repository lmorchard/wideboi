# Implementation Plan: Web Honours User Key Bindings and Width Presets

## Phase 1: Protocol & Server Configuration Dispatch
- [ ] Add `KeyBindingData` and `MsgConfigSnapshot` to `internal/protocol/wirepb/wideboi.proto`.
- [ ] Add `config_snapshot` field (tag 18) to `ServerMessage` oneof.
- [ ] Run `make proto` to generate Go and TypeScript protobuf stubs.
- [ ] Bump `protocol.Version` to 15 in `internal/protocol/version.go` and `web/src/version.ts`.
- [ ] Add `KeyBinding` and `MsgConfigSnapshot` types to `internal/protocol/messages.go`.
- [ ] Implement serialization/deserialization in `internal/protocol/codec.go`.
- [ ] Add round-trip tests in `internal/protocol/wire_test.go` and update `internal/protocol/version_guard_test.go` hash.
- [ ] Add `ToProtocol` helper in `internal/keys/keys.go`.
- [ ] Add `SetBindings` to `server.Server` and send `MsgConfigSnapshot` on client attach.
- [ ] Wire `srv.SetBindings(keys.ToProtocol(bindings))` in `cmd/wideboi/main.go`.
- [ ] Verify Go tests pass (`go test -count=1 ./...`).

## Phase 2: Web KeyRouter & Dynamic Help Table & Width Presets
- [ ] Update `web/src/key-router.ts` to support table-driven routing from `KeyBindingData[]` (with default fallback table matching Go's `keys.Bindings`).
- [ ] Implement `helpEntries` getter on `KeyRouter` formatting key labels (e.g. `h / ←`) and descriptions.
- [ ] Update `web/src/wideboi-app.ts`:
  - Handle `configSnapshot` message in `client.onMessage`.
  - Update `widthPresets`, `minColumnWidth`, `maxColumnWidth`, and `keyRouter.setBindings(...)`.
  - Update `cycleWidth` to use `widthPresets`, `minColumnWidth`, and `maxColumnWidth`.
  - Update help overlay rendering to map over `this.keyRouter.helpEntries`.
- [ ] Add unit tests in `web/src/key-router.test.ts` for custom bindings, router actions, and help entry generation.

## Phase 3: Card Layout Golden Tests & Edge Alignment
- [ ] Fix `web/src/card-layout.ts` window edge bounds to mirror Go's `lo := max(focus-margin, 0)` and `hi := min(focus+margin, numCols-1)`.
- [ ] Create Go golden fixture generator / test `internal/layout/card_golden_test.go` that runs diverse layout scenarios and outputs `web/src/fixtures/card-layout-golden.json`.
- [ ] Add vitest test in `web/src/card-layout.test.ts` that iterates through the golden test cases and asserts identical placements and window bounds.
- [ ] Run all checks (`make check`).
