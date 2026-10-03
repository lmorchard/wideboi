# Plan: Detect Interrupted or Hung Working Agents via Inactivity Timeout (Issue #383)

**Goal:** Detect when a working pane has stopped producing PTY output for an extended period (default 30s) without completing, flag it as `StatusInterrupted` (`?`), and automatically resume active status as soon as new output arrives.

**Approach:** Add `StatusInterrupted` to `protocol.PaneStatus` and wirepb schema. Add `workingInactivityTimeout` check in `vtGrid.Status()` that transitions working panes with silent PTYs to `StatusInterrupted`. Update dashboard and client presentation.

**Tech stack:** Go, protobuf wire protocol, Ultraviolet VT emulator.

---

## Phase 1: Protocol & Schema Updates

Add `StatusInterrupted` to protocol enums and wire schema.

**Files:**
- Modify: `internal/protocol/wirepb/wideboi.proto` — add `PANE_STATUS_INTERRUPTED = 5;`
- Regenerate bindings: `npx @bufbuild/buf generate`
- Modify: `internal/protocol/messages.go` — add `StatusInterrupted`
- Modify: `internal/protocol/codec.go` — map `StatusInterrupted`
- Modify: `internal/protocol/codec_test.go` — add `StatusInterrupted` to enum test
- Modify: `internal/protocol/version_guard_test.go` — update wire schema hash
- Test: `internal/protocol/pane_status_test.go` — verify string/json representation

**Verification — automated:**
- [x] `go test -v ./internal/protocol/...` passes — **verified wirepb roundtrips and version guard at 25**

---

## Phase 2: Inactivity Tracking in Terminal Grid

Implement `workingInactivityTimeout` and auto-resumption in `internal/server/term/`.

**Files:**
- Modify: `internal/server/term/grid.go` — add `workingInactivityTimeout` field and setter, update `Status()`
- Modify: `internal/server/term/heuristic.go` — add `DefaultWorkingInactivityTimeout` constant
- Test: `internal/server/term/heuristic_test.go` — add inactivity timeout and resumption tests

**Key changes:**
- If status evaluates to `StatusWorking` and `time.Since(*lastWriteTime) > workingInactivityTimeout`, return `StatusInterrupted`.
- New `Write()` resets `lastWriteTime`, immediately resuming `StatusWorking`.

**Verification — automated:**
- [x] `go test -v ./internal/server/term -run TestWorkingInactivity` passes — **verified timeout, auto-resume, and authoritative OSC inactivity**

---

## Phase 3: Dashboard & Client Presentation

Update dashboard, status bar, and smart jump to reflect `StatusInterrupted`.

**Files:**
- Modify: `internal/server/dashboard.go` — format glyph `?`, format string `"interrupted"`, priority sorting, summary banner
- Modify: `internal/client/theme.go` — badge components for `StatusInterrupted` (`?`)
- Modify: `internal/client/client.go` — smart jump ranking for `StatusInterrupted`
- Modify: `web/src/wideboi-app.ts` & `web/src/components/mobile-bar.ts` — glyph `?` and styling
- Test: `internal/server/dashboard_test.go` — test interrupted formatting and sorting
- Test: `internal/client/client_test.go` — test smart jump targeting interrupted panes

**Verification — automated:**
- [x] `go test -v ./internal/server -run TestDashboard` passes — **verified interrupted sorting and banner**
- [x] `go test -v ./internal/client -run TestSmartJump` passes — **verified smart jump targets interrupted panes**
- [x] `make quick` passes cleanly — **`go vet`, `seam-check`, Go test suite, and 183 Vitest web tests passed**
