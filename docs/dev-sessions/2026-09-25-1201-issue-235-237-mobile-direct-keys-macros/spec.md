# Mobile Web Direct Input, Keys & Configurable Macro Panel Spec

**Goal:** Enable mobile/narrow web users to reliably send arbitrary terminal keys, modifier combinations (including Ctrl+R), and sequenced input macros to focused terminal panes with clear draft/direct mode separation and server-backed macro configuration.

**Source:** GitHub issues #235 and #237

## Current state

- Mobile detection (`web/src/wideboi-app.ts:18, 464`) activates when viewport width is `<= 480px`.
- Mobile dock (`web/src/wideboi-app.ts:1514-1539`) displays a single compose textarea (`.mobile-compose`) and terminal key buttons (`.mobile-keys`).
- Key transmission paths (`web/src/input.ts:18-40`):
  - `sendKeyboardInput`: Encodes `KeyboardEvent` into Protobuf `KeyData` with bitmask modifiers (`mod: Shift=1, Alt=2, Ctrl=4`).
  - `sendTextInput`: Encodes arbitrary string into UTF-8 bytes (`Uint8Array`) without implicit newline.
- Limitations in mobile UI today:
  - Compose textarea intercepts typing into a buffer (`mobileDraft`), requiring a click on "Send".
  - Ctrl button only provides hardcoded shortcuts `[C]`, `[D]`, and `[Z]`. Shortcuts like `Ctrl+R` (reverse history search), `Ctrl+A`, `Ctrl+E`, `Ctrl+L`, `Ctrl+W` cannot be triggered from the on-screen controls.
  - No continuous "Direct Input" mode where hardware or software keyboard events stream straight to the shell/editor without editing the draft buffer.
  - No macro sequence support or server synchronization for custom input sequences.

## Desired end state

### 1. Direct Input Mode & Expanded Modifier Row (#235)
- **Mode Toggle:** Mobile dock provides an explicit toggle between **Draft Mode** (default) and **Direct Mode**.
  - **Draft Mode:** Textarea buffers input; typing does not affect the terminal until "Send" is pressed. Paste and IME composition stay local to the draft. Existing shortcuts (`Escape`, `Tab`, empty-draft arrows) continue to forward directly to the terminal.
  - **Direct Mode:** An active input capture target immediately dispatches all keyboard events directly to the focused pane via `sendKeyboardInput` (or `sendTextInput` for printable input). A distinct visual indicator (e.g. amber/accent badge "Direct Terminal Input") clarifies that keystrokes go straight to the pane. A prominent "Draft Mode" button allows immediate return to compose mode.
- **Expanded On-Screen Ctrl Palette:**
  - Toggling `Ctrl` reveals an expanded, compact palette of common terminal keys: `C`, `D`, `Z`, `R`, `L`, `A`, `E`, `W`, `K`, `U`.
  - Tapping `[R]` while `Ctrl` is active sends `Ctrl+R` to the focused pane.
  - In both Draft and Direct modes, toggling `Ctrl` on and pressing any key (from software or hardware keyboard) sends `Ctrl+<key>` to the terminal pane.

### 2. Configurable Macro Panel (#237)
- **Macro Definition:**
  - Name (string) and ordered array of steps.
  - Each step is either:
    - Text step (`{ text: string }`): sent verbatim as UTF-8 bytes via `sendTextInput`. Crucially, **never gains an implicit Enter**.
    - Key step (`{ key: string, code?: string, ctrl?: boolean, alt?: boolean, shift?: boolean }`): sent as terminal key event via `sendKeyboardInput`.
- **Macro Execution:**
  - Executing a macro captures `focusedPaneId` at start and dispatches each step in sequence to that specific pane.
  - Running a macro does not alter PTY dimensions, client layout, or other clients' view state.
  - Clear visual indicator in the macro list showing whether the macro ends with Enter (`↵`).
- **Macro UI:**
  - A "Macros" button in the mobile dock opens a slide-up sheet/panel.
  - Displays list of configured macros with one-tap execution.
  - Includes an "Edit" interface to add, edit, reorder, or delete macros locally.
- **Server Persistence & Wire Protocol:**
  - Server loads default and configured macros from `wideboi.toml` / user configuration directory.
  - Protobuf wire messages:
    - `MacroStep`, `Macro`, `MsgMacrosSnapshot` (server to client upon attach or update).
    - `MsgSaveMacros` (client to server when saving updated macros).
  - Version bump: `protocol.Version` incremented from 11 to 12, and WebSocket subprotocol updated to `wideboi.v12`.
  - Client can override macros locally (cached in `localStorage`), and click "Save to Server" to persist them back to the server.

## Design decisions

- **Decision:** Explicit toggle between Draft and Direct mode in the mobile dock.
  - **Why:** Prevents accidental command execution or broken terminal state while typing on mobile, while giving terminal power users and hardware keyboard users immediate, unbuffered interactive control.
  - **Rejected:** Implicit modal switching based on key detection (causes race conditions and typing errors with mobile auto-correct/autocomplete).
- **Decision:** Expand on-screen Ctrl buttons with `[R]`, `[L]`, `[A]`, `[E]`, `[W]`, `[K]`, `[U]` in addition to `[C]`, `[D]`, `[Z]`.
  - **Why:** `Ctrl+R` shell history search is a primary requirement in #235 and mobile software keyboards do not provide a physical Ctrl key.
  - **Rejected:** Free-form text dialog for every shortcut (adds excessive tapping friction for common keystrokes).
- **Decision:** Synchronize macros over WebSocket Protobuf messages with server-side config persistence and client-side `localStorage` caching.
  - **Why:** Keeps the client-server transport consistent across all wideboi features, allows macros defined on the server (`wideboi.toml`) to appear automatically on mobile, and lets mobile users save customizations back to the server for future sessions.
  - **Rejected:** HTTP REST API (unnecessary second transport layer when WebSocket is already active and authenticated).
- **Decision:** Synchronous ordered execution of macro steps without delay loops.
  - **Why:** Meets the initial acceptance criteria in #237 while keeping macro execution deterministic and non-blocking.
  - **Rejected:** Arbitrary sleep/delay scripting in v1 (adds unnecessary execution state complexity).

## Patterns to follow

- Key and text transmission: `sendKeyboardInput` and `sendTextInput` in `web/src/input.ts:18-40`.
- Mobile dock layout and state: `web/src/wideboi-app.ts:1514-1539`.
- Protobuf message schema definition: `internal/protocol/wirepb/wideboi.proto:110-180` and `web/src/protobuf.ts`.
- Server message routing and broadcast: `internal/server/server.go:530-580`.
- Mobile tests: `web/tests/mobile.spec.js:53-150`.
- PTY wire and version bump rules: `docs/LESSONS.md:406-422`.

## What we're NOT doing

- We are not creating a new PTY pane for macros (macros run as an in-app UI sheet in the web client).
- We are not adding complex conditionals, scripting languages, or asynchronous wait-for-output triggers to macros.
- We are not changing desktop terminal keyboard routing or modifying existing prefix chord bindings.
- We are not removing Draft mode or forcing mobile users into direct typing by default.

## Open questions

- None. All requirements and architectural choices resolved during research and Q&A.
