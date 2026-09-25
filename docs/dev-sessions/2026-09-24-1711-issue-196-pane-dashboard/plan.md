# In-App Pane Status Dashboard Implementation Plan

**Goal:** Implement a server-managed status dashboard pane that lists all active terminal panes, can sit alongside terminals persistently, and enables keyboard and mouse navigation.

**Approach:** Build a non-PTY pane on the server backed by an in-memory VT emulator (`term.Grid`) that re-renders upon state changes. Use a new verb (`VerbToggleStatus`) and wire message (`MsgFocusPane`) with protocol version bump so navigation can request client focus changes across both terminal and web clients.

**Tech stack:** Go, Ultraviolet, Protobuf, TypeScript (web client).

---

## Phase 1: Protocol extension & Version Bump

Extend the protobuf wire schema and Go protocol package with `VerbToggleStatus` and `MsgFocusPane`, and bump `protocol.Version` to 5.

**Files:**
- Modify: `internal/protocol/wirepb/wideboi.proto` — add `VERB_TYPE_TOGGLE_STATUS = 13` and `MsgFocusPane`
- Modify: `internal/protocol/messages.go` — add `VerbToggleStatus` and `MsgFocusPane` struct
- Modify: `internal/protocol/codec.go` — encode/decode `MsgFocusPane` and map `VerbToggleStatus`
- Modify: `internal/protocol/version.go` — bump `Version` to 5
- Modify: `web/src/client.ts` — update subprotocol `wideboi.v6`
- Modify: `scripts/wssink/main.go` — update subprotocol `wideboi.v6`
- Test: `internal/protocol/codec_test.go` — update verb count and test roundtrip for `MsgFocusPane`
- Test: `internal/protocol/wire_test.go` — ensure wire schema covers `MsgFocusPane`

**Key changes:**
```protobuf
enum VerbType {
  ...
  VERB_TYPE_TOGGLE_STATUS = 13;
}

message MsgFocusPane {
  int32 pane_id = 1;
}

message ServerMessage {
  oneof msg {
    ...
    MsgFocusPane focus_pane = 7;
  }
}
```

```go
const (
  ...
  VerbToggleStatus VerbType = 13
)

type MsgFocusPane struct {
  PaneID int
}
```

**Verification — automated:**
- [x] `make proto` regenerates protobuf files without errors
- [x] `make proto-check` passes
- [x] `go test -v ./internal/protocol/...` passes

---

## Phase 2: Server-Side Dashboard Pane & Lifecycle

Implement the dashboard rendering, input handling, and server lifecycle in `internal/server`.

**Files:**
- Create: `internal/server/dashboard.go` — dashboard state, table ANSI rendering, key/mouse handling
- Create: `internal/server/dashboard_test.go` — unit tests for dashboard logic, rendering, and navigation
- Modify: `internal/server/pane.go` — support non-PTY dashboard pane
- Modify: `internal/server/server.go` — track `statusPaneID`, handle `VerbToggleStatus`, forward `MsgFocusPane`, reap/shutdown on lone dashboard

**Key changes:**
- `Dashboard` struct manages selection index, builds formatted table:
  - Header: `PANE ID  STATUS    TITLE                 CWD`
  - Rows: `> [1]     ● idle    bash                  /Users/les/...`
  - Highlights selected row.
- `Dashboard.HandleKey(ev uv.KeyEvent) (focusPaneID int, handled bool)`:
  - `j` / `down`: increment selection, redraw grid
  - `k` / `up`: decrement selection, redraw grid
  - `Enter`: return selected pane's ID
- `Dashboard.HandleMouse(ev uv.MouseEvent) (focusPaneID int, handled bool)`:
  - compute row from click Y coordinate, update selection, return selected pane's ID
- In `internal/server/server.go`:
  - `handleClientMsg`: when `VerbToggleStatus` arrives, spawn or focus dashboard pane.
  - When `MsgInput` or `MsgMouse` on dashboard pane yields `focusPaneID > 0`, send `MsgFocusPane{PaneID: focusPaneID}` to `tp`.
  - When terminal pane exits in `onPaneExit`, if only dashboard panes remain, initiate `s.Close()`.

**Verification — automated:**
- [x] `go test -v ./internal/server -run TestDashboard` passes
- [x] `go test -v ./internal/server` passes

---

## Phase 3: Client Handling & Keybindings

Implement `MsgFocusPane` client handling and register `<prefix> s` keybinding.

**Files:**
- Modify: `internal/client/client.go` — handle `MsgFocusPane`
- Modify: `internal/keys/keys.go` — register `ActionNameToggleStatus` (`Key: "s"`)
- Modify: `cmd/wideboi/router.go` — ensure `VerbToggleStatus` routes properly
- Test: `internal/client/client_test.go` — test `MsgFocusPane` updates `c.focusPaneID`
- Test: `internal/client/help_test.go` — assert `TestHelpOverlayFitsAt80x24` still passes with new binding
- Test: `cmd/wideboi/router_test.go` — test `s` key routes to `VerbToggleStatus`

**Key changes:**
```go
// in client.go:
case protocol.MsgFocusPane:
    c.strip.FocusPaneID(m.PaneID)
    c.focusPaneID = c.strip.FocusedPaneID()
    c.updatePlacementsLocked()
```

```go
// in keys.go:
{
    ActionName: ActionNameToggleStatus,
    Key: "s",
    Action: ActionVerb,
    Verb: protocol.VerbToggleStatus,
    Long: "open/focus the pane status dashboard",
}
```

**Verification — automated:**
- [x] `go test -v ./internal/client/...` passes
- [x] `go test -v ./internal/keys/...` passes
- [x] `go test -v ./cmd/wideboi/...` passes

---

## Phase 4: Integration, Documentation & Verification

Update documentation, verify end-to-end integration and run smoke and attach test suites.

**Files:**
- Modify: `README.md` — document `<prefix> s`
- Modify: `config.example.toml` — document `toggle_status` action
- Modify: `docs/dev-sessions/2026-09-24-1711-issue-196-pane-dashboard/notes.md` — dev session notes

**Verification — automated:**
- [x] `make lint` / `go vet ./...` passes
- [x] `make check` passes completely (including smoke, golden, attachcheck, playwright)
- [x] `git status` clean (except untracked dev session docs)

**Verification — manual:**
- [ ] Run binary interactively, verify `<prefix> s` opens status dashboard to the right of focused pane, `j`/`k` move selection, `Enter` jumps to selected pane, and `x` kills the dashboard pane.
