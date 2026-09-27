# Spec: Web Honours User Key Bindings and Width Presets

## Context & Problem
Issue #270:
The browser re-implements behaviour that is configurable in Go, so users who customise get different behaviour on the web:
1. `web/src/key-router.ts` hardcodes verb keys; Go's are a rebindable table (`internal/keys/keys.go`, `config.Load` -> `keys.BuildBindings`).
2. `cycleWidth` (`wideboi-app.ts`) hardcodes `[40, 60, 80]`, `20`, `4096`; Go has `layout.DefaultWidthPresets`, `MinColumnWidth`, `MaxColumnWidth` and the user's `width_presets`.
3. The help table (`wideboi-app.ts`) is static and has drifted from reality (omits `s`, `:`, `Space`).
4. `web/src/card-layout.ts` is a parallel port of `internal/layout/card.go` with minimal tests; Go's edge clamping and window slide rules have subtle differences.

## Goals
- Ship server configuration (width presets, column width limits, key bindings) to web client on attach via `MsgConfigSnapshot`.
- Bump `protocol.Version` to 15 (and `web/src/version.ts`) as wire schema changes.
- Build `KeyRouter` dynamically from the active bindings table with default fallbacks.
- Build the web shortcuts help table dynamically from `KeyRouter.helpEntries`.
- Have `cycleWidth` honour the server's width presets, min column width, and max column width.
- Align `web/src/card-layout.ts` with `internal/layout/card.go` and verify against Go-generated golden test fixtures in vitest.

## Non-Goals
- Changing the Go client's key router or terminal rendering.
- Adding arbitrary macro execution in prefix mode outside existing actions.

## Protocol Changes
- Wire schema:
  - Add `KeyBindingData` message to `wideboi.proto`.
  - Add `MsgConfigSnapshot` message to `wideboi.proto` containing `width_presets`, `min_column_width`, `max_column_width`, and `bindings`.
  - Add `MsgConfigSnapshot config_snapshot = 18;` to `ServerMessage.msg` oneof.
- Bump `protocol.Version` from 14 to 15.
- Update `web/src/version.ts` to 15.
- Update `internal/protocol/version_guard_test.go` with new schema sha256 hash.
- Add `MsgConfigSnapshot` handling to `internal/protocol/codec.go`.
