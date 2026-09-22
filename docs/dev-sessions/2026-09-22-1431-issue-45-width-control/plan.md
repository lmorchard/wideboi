# Width Control & Configurable Presets Implementation Plan

**Goal:** Implement incremental width adjustment (grow/shrink via `p`/`o`) and user-configurable width presets in wideboi.

**Approach:**
- Extend `internal/protocol` with `VerbGrowWidth` and `VerbShrinkWidth`.
- Extend `internal/layout.Strip` with `GrowWidth`, `ShrinkWidth`, and configurable preset cycling via `SetWidthPresets([]int)`.
- Wire verbs in `internal/server` to call layout operations followed by `s.resizePanesLocked()`.
- Add `grow_width` and `shrink_width` actions in `internal/keys`, bound by default to `p` and `o`.
- Add `width_presets` to TOML configuration in `internal/config`, passed down to the server layout strip.

**Tech stack:** Go, standard library, pelletier/go-toml/v2.

---

## Phase 1: Protocol Verbs & Layout Width Operations

Delivers:
- New verbs `protocol.VerbGrowWidth` and `protocol.VerbShrinkWidth` with stringer / wire registrations.
- `Strip.GrowWidth(delta int)`, `Strip.ShrinkWidth(delta int)` (default delta 10, minimum width floor 20).
- Configurable preset cycling in `Strip`: `SetWidthPresets([]int)` and updated `CycleWidth()` respecting presets.

**Files:**
- Modify: `internal/protocol/messages.go` — add `VerbGrowWidth`, `VerbShrinkWidth`
- Modify: `internal/layout/layout.go` — add presets field, `SetWidthPresets`, `GrowWidth`, `ShrinkWidth`, update `CycleWidth`
- Test: `internal/layout/layout_test.go` — tests for grow, shrink, clamp, and configurable preset cycling
- Test: `internal/protocol/wire_test.go` — register new verbs if needed

**Key changes:**
```go
func (s *Strip) GrowWidth(delta int)
func (s *Strip) ShrinkWidth(delta int)
func (s *Strip) SetWidthPresets(presets []int)
```

**Verification — automated:**
- [x] `go test ./internal/protocol/...` passes
- [x] `go test ./internal/layout/...` passes
- [x] `make quick` passes

---

## Phase 2: Server Routing & Pane Resizing

Delivers:
- Server routes `VerbGrowWidth` and `VerbShrinkWidth` to `strip.GrowWidth(10)` and `strip.ShrinkWidth(10)`, calling `s.resizePanesLocked()`.
- Server exposes method or option to configure layout width presets on startup.

**Files:**
- Modify: `internal/server/server.go` — handle `VerbGrowWidth` and `VerbShrinkWidth` in `MsgVerb`, add `SetWidthPresets`
- Test: `internal/server/server_test.go` — test server handling of grow/shrink verbs and pane resizing

**Verification — automated:**
- [x] `go test ./internal/server/...` passes
- [x] `make quick` passes

---

## Phase 3: Keys, Default Bindings & Help Overlay

Delivers:
- `ActionNameGrowWidth` and `ActionNameShrinkWidth` in `internal/keys`.
- Bindings for `p` and `o` in `Bindings` table (with repeat chords `ctrl+p` and `ctrl+o`).
- Updated help overlay tests and status bar budget checks.

**Files:**
- Modify: `internal/keys/keys.go` — add action constants, validActions entries, and bindings for `p` and `o`
- Test: `internal/keys/keys_test.go` — verify key bindings and remapping
- Test: `internal/client/help_test.go` — verify help overlay and status bar budget at 80 cols

**Verification — automated:**
- [x] `go test ./internal/keys/...` passes
- [x] `go test ./internal/client/...` passes
- [x] `make quick` passes

---

## Phase 4: Configuration Support (`width_presets`)

Delivers:
- `WidthPresets []int` in `config.Config` mapped to TOML `width_presets`.
- Validation of presets (must be positive, sorted or sorted on parse, min length).
- Plumbing from `cmd/wideboi` to server strip configuration.

**Files:**
- Modify: `internal/config/config.go` — add `width_presets` field, parsing, and validation
- Modify: `cmd/wideboi/main.go` — pass configured presets to server
- Test: `internal/config/config_test.go` — test valid and invalid `width_presets` TOML configurations

**Verification — automated:**
- [x] `go test ./internal/config/...` passes
- [x] `make check` passes
