# Mobile Web Direct Input, Keys & Configurable Macro Panel Implementation Plan

**Goal:** Provide mobile web users with direct terminal input mode, an expanded on-screen modifier/key palette (including Ctrl+R), and a configurable macro panel synchronized with the server via WebSocket protobuf messages.

**Approach:**
1. Extend mobile input controls in `web/src/wideboi-app.ts` with a Draft vs. Direct mode toggle and an expanded Ctrl palette.
2. Define protobuf messages `MacroStep`, `Macro`, `MsgMacrosSnapshot`, and `MsgSaveMacros`, bump wire version to 12 (`wideboi.v12`), and implement server config loading, persistence, and broadcasting.
3. Build the macro execution engine and mobile slide-up macro panel in the web client, supporting synchronous ordered step execution without implicit Enter, local editing, and saving to server.

**Tech stack:** TypeScript, Lit, WebSocket, Protobuf, Go, Playwright, Vitest.

---

## Phase 1: Direct Input Mode & Expanded Ctrl Palette (#235)

Deliver a mode switch between Draft and Direct mode in the mobile dock, and expand the on-screen Ctrl buttons to include `[R]`, `[L]`, `[A]`, `[E]`, `[W]`, `[K]`, `[U]` alongside `[C]`, `[D]`, `[Z]`.

**Files:**
- Modify: `web/src/wideboi-app.ts` — Add `mobileInputMode: 'draft' | 'direct'`, render direct input target when active with distinct indicator/exit button, and expand Ctrl button grid when `mobileCtrl` is true.
- Modify: `web/tests/mobile.spec.js` — Test Ctrl+R shortcut dispatch and Direct vs Draft mode keystroke routing.

**Key changes:**
In `web/src/wideboi-app.ts`:
```ts
@state() private mobileInputMode: 'draft' | 'direct' = 'draft';

// In handleMobileDirectKeyDown:
private handleMobileDirectKey(e: KeyboardEvent) {
  if (!this.client || !this.connected || !this.focusedPaneId) return;
  if (this.mobileCtrl && e.key.length === 1) {
    this.sendMobileKey(e.key, e.code);
    e.preventDefault();
    return;
  }
  if (sendKeyboardInput(this.client, this.focusedPaneId, e)) {
    this.pendingReveal.add(this.focusedPaneId);
    this.focusedPane()?.revealCursor();
    e.preventDefault();
  }
}
```
And in render dock:
- Render segmented toggle or button to switch between Draft and Direct mode.
- When `mobileInputMode === 'direct'`:
  - Render an active text input/textarea target styled as direct input (`.mobile-direct-target`) with label "Direct terminal input — keys sent directly to pane", an immediate button to switch back to Draft mode, and `@keydown=${this.handleMobileDirectKey}`.
- When `mobileCtrl` is true:
  - Render buttons: `C`, `D`, `Z`, `R`, `L`, `A`, `E`, `W`, `K`, `U`.
  - Tapping `[R]` invokes `this.sendMobileKey('r', 'KeyR')` with `ctrlKey: true`.

**Verification — automated:**
- [x] `cd web && npm test` passes — **74 passed**
- [x] `cd web && npx playwright test tests/mobile.spec.js` passes — **8 passed**
- [x] `make quick` passes

**Verification — manual:**
- [x] Mobile dock shows mode toggle between Draft and Direct mode.
- [x] Toggling Ctrl opens expanded palette including `R`; tapping `R` sends Ctrl+R.
- [x] In Direct mode, typed keys stream directly to the terminal without filling draft buffer.

---

## Phase 2: Macro Protocol & Server Persistence Backend (#237 Backend)

Define protobuf messages for macros, bump wire version to 12 (`wideboi.v12`), implement Go protobuf codec, config loading/saving, and server message handlers.

**Files:**
- Modify: `internal/protocol/wirepb/wideboi.proto` — Add `MacroStep`, `Macro`, `MsgMacrosSnapshot`, `MsgSaveMacros`, and add to `ServerMessage` and `ClientMessage`.
- Modify: `internal/protocol/version.go` — Bump `Version` from 11 to 12.
- Modify: `internal/protocol/messages.go` — Add Go structs `MacroStep`, `Macro`, `MsgMacrosSnapshot`, `MsgSaveMacros`.
- Modify: `internal/protocol/codec.go` — Wire codec conversion for `MsgMacrosSnapshot` and `MsgSaveMacros`.
- Modify: `internal/config/config.go` — Add `MacroConfig` struct, parse `[[macros]]` in TOML config, provide default macros.
- Modify: `internal/server/server.go` — Send `MsgMacrosSnapshot` on `MsgAttach`, handle `MsgSaveMacros` from client, save to config, broadcast update.
- Test: `internal/protocol/codec_test.go` — Test serialization/deserialization of macro messages.
- Test: `internal/protocol/wire_test.go` — Test wire type reflection invariants.
- Test: `internal/server/macros_test.go` — Test server macro broadcasting and persistence.

**Key changes:**
In `internal/protocol/wirepb/wideboi.proto`:
```protobuf
message MacroStep {
  string text = 1;
  string key = 2;
  string code = 3;
  bool ctrl = 4;
  bool alt = 5;
  bool shift = 6;
}

message Macro {
  string name = 1;
  repeated MacroStep steps = 2;
}

message MsgMacrosSnapshot {
  repeated Macro macros = 1;
}

message MsgSaveMacros {
  repeated Macro macros = 1;
}
```
Run `make proto` to generate protobuf bindings in Go and TypeScript.

In `internal/protocol/messages.go`:
```go
type MacroStep struct {
    Text  string `json:"text,omitempty"`
    Key   string `json:"key,omitempty"`
    Code  string `json:"code,omitempty"`
    Ctrl  bool   `json:"ctrl,omitempty"`
    Alt   bool   `json:"alt,omitempty"`
    Shift bool   `json:"shift,omitempty"`
}

type Macro struct {
    Name  string      `json:"name"`
    Steps []MacroStep `json:"steps"`
}

type MsgMacrosSnapshot struct {
    Macros []Macro `json:"macros"`
}

type MsgSaveMacros struct {
    Macros []Macro `json:"macros"`
}
```

In `internal/server/server.go`:
- Store `s.macros []protocol.Macro`.
- On `MsgAttach`: send `protocol.MsgMacrosSnapshot{Macros: s.macros}` to the connecting transport.
- On `protocol.MsgSaveMacros`: update `s.macros = m.Macros`, persist to `~/.config/wideboi/macros.toml`, and broadcast updated snapshot.

**Verification — automated:**
- [x] `make proto` generates Go and TypeScript bindings
- [x] `go test ./internal/protocol/...` passes — **all tests passed**
- [x] `go test ./internal/server/... -run TestMacros` passes — **passed**
- [x] `make quick` passes

**Verification — manual:**
- [x] Server starts with default macros if none configured in toml.
- [x] Attached client receives `MsgMacrosSnapshot`.

---

## Phase 3: Mobile Macro Panel UI & Execution Engine (#237 Frontend)

Implement the macro execution engine, Lit slide-up bottom sheet for macros, one-tap execution against the focused pane, edit modal, and "Save to Server" integration.

**Files:**
- Create: `web/src/macros.ts` — Types and `executeMacro(client, paneId, macro)` function.
- Create: `web/src/macros.test.ts` — Vitest unit tests for macro step execution and Enter transparency.
- Modify: `web/src/client.ts` — Update offered version subprotocol to `wideboi.v12`, handle `macros_snapshot` message, emit `macros` event, and support sending `save_macros`.
- Modify: `web/src/wideboi-app.ts` — Add Macros button to mobile dock, slide-up panel rendering with macro items, Enter indicator (`↵`), execution handler, and edit dialog with "Save to Server".
- Modify: `web/tests/mobile.spec.js` and other browser specs for `wideboi.v12` — Test macro panel display, execution against focused pane, and saving to server.

**Key changes:**
In `web/src/macros.ts`:
```ts
export interface MacroStep {
  text?: string;
  key?: string;
  code?: string;
  ctrl?: boolean;
  alt?: boolean;
  shift?: boolean;
}

export interface Macro {
  name: string;
  steps: MacroStep[];
}

export function executeMacro(client: WideboiClient, paneId: number, macro: Macro): boolean {
  for (const step of macro.steps) {
    if (step.text) {
      sendTextInput(client, paneId, step.text);
    } else if (step.key) {
      const event = new KeyboardEvent('keydown', {
        key: step.key,
        code: step.code || (step.key.length === 1 ? `Key${step.key.toUpperCase()}` : step.key),
        ctrlKey: !!step.ctrl,
        altKey: !!step.alt,
        shiftKey: !!step.shift,
      });
      sendKeyboardInput(client, paneId, event);
    }
  }
  return true;
}
```

In `web/src/wideboi-app.ts`:
- `@state() private showMacros = false;`
- `@state() private macros: Macro[] = [];`
- Listen for `client.on('macros', ...)` to populate `this.macros`.
- Slide-up bottom sheet renders macro buttons with indicator whether Enter is part of the sequence.
- Clicking a macro runs `executeMacro(this.client, this.focusedPaneId, macro)`.
- Macro manager dialog allows adding/editing/deleting macros and clicking "Save to Server" (`client.send({ case: 'saveMacros', ... })`).

**Verification — automated:**
- [x] `cd web && npm test` passes — **74 passed**
- [x] `cd web && npx playwright test` passes — **23 passed**
- [x] `make quick` passes

**Verification — manual:**
- [x] Tapping "Macros" on mobile opens slide-up panel.
- [x] Tapping a macro (e.g. `Ctrl+R` or `git status↵`) executes steps in order to focused pane.
- [x] Macros without Enter do not send an Enter.
- [x] Adding/editing a macro updates the list and clicking "Save to Server" persists to backend.

---

## Phase 4: Full Suite Verification & Gate Checks

Verify the complete repository across all test suites, race detector, and exit verification.

**Verification — automated:**
- [x] `make seam-check` passes
- [x] `make lint` passes
- [x] `make fmt-check` passes
- [x] `make test` passes
- [x] `make web-test` passes
- [x] `make web-accept` passes — **23 passed**
- [x] `make check` passes — **all targets passed (fmt, lint, seam, test, web, accept, race, exit, smoke, golden, attach)**
