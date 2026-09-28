# Distinguish bracketed paste from typed text and macro inputs in MsgInput Spec

**Goal:** Distinguish clipboard paste events from raw text, macros, and IME inputs in `MsgInput` so that child shells receive bracketed paste markers only for actual clipboard pastes.

**Source:** GitHub Issue #312

## Current state

- In `internal/server/handlers.go:510-515` (added in #306), the server wraps any `MsgInput.Data` with bracketed paste markers (`\x1b[200~` and `\x1b[201~`) whenever `p.grid.BracketedPaste()` is true.
- `MsgInput.Data` is also used for non-paste inputs:
  1. Macro text steps (`web/src/macros.ts:45`: `sendTextInput(client, paneId, step.text)`).
  2. IME text composition (`web/src/wideboi-app.ts:760`: `compositionend` event).
  3. Mobile direct input and mobile draft send button (`web/src/wideboi-app.ts:1194, 1204`).
  4. Programmatic/test keystrokes (`cmd/wideboi/main_test.go:273`).
- When a child shell enables bracketed paste mode (e.g. bash, zsh, ipython), wrapping these text inputs in bracketed paste markers prevents the shell from executing commands on newlines. For example, a macro `{ text: "git status\n" }` pastes `git status` into the prompt without executing it.
- In `internal/protocol/wirepb/wideboi.proto:216-220`, `message MsgInput` has `int32 pane_id = 1; KeyData key = 2; bytes data = 3;`. It lacks a way to signal whether `data` represents a clipboard paste.

## Desired end state

1. **Protocol schema & version:**
   - `internal/protocol/wirepb/wideboi.proto`: `MsgInput` gains `bool paste = 4;`.
   - `internal/protocol/messages.go`: `MsgInput` gains `Paste bool`.
   - `internal/protocol/codec.go`: `MarshalClient` and `UnmarshalClient` serialize and deserialize `Paste`.
   - Protocol version bumped from 17 to 18:
     - `internal/protocol/version.go`: `Version = 18`.
     - `web/src/version.ts`: `PROTOCOL_VERSION = 18; VERSION_PROTOCOL = "wideboi.v18";`.
     - `internal/protocol/version_guard_test.go`: `wireSchemaHashes[18]` updated with new schema sha256.

2. **Go Client:**
   - `internal/client/client.go`:
     - `cli.SendInput(ctx, data)` sends `protocol.MsgInput{PaneID: focusedID, Data: data, Paste: false}`.
     - `cli.SendPaste(ctx, data)` sends `protocol.MsgInput{PaneID: focusedID, Data: data, Paste: true}`.
   - `cmd/wideboi/main.go:1256`: `handlePaste` calls `cli.SendPaste(ctx, []byte(ev.Content))`.

3. **Web Client:**
   - `web/src/input.ts`:
     - `sendTextInput(sender, paneID, text)` sends `MsgInput` with `paste: false`.
     - `sendPasteInput(sender, paneID, text)` sends `MsgInput` with `paste: true`.
   - `web/src/wideboi-app.ts`:
     - Clipboard paste handlers (lines 716 and 752) call `sendPasteInput`.
     - IME (`compositionend` at line 760), mobile direct input (line 1194), mobile draft (line 1204), and macros (`web/src/macros.ts:45`) continue using `sendTextInput` (`paste: false`).

4. **Server handling:**
   - `internal/server/handlers.go:510-515`:
     Wraps `m.Data` with bracketed paste markers (`\x1b[200~` ... `\x1b[201~`) **only** when `m.Paste && p.grid != nil && p.grid.BracketedPaste()`. Raw text inputs (`!m.Paste`) are forwarded to `p.SendBytes` without markers even if bracketed paste is active on the terminal.

5. **Tests:**
   - Unit tests in `internal/server/paste_test.go` verifying that `MsgInput` with `Paste: true` is wrapped when bracketed paste is on, while `MsgInput` with `Paste: false` is not wrapped.
   - Unit tests in `cmd/wideboi/paste_test.go` verifying `handlePaste` sends `Paste: true`.
   - Vitest tests in `web/src/input.test.ts` verifying `sendPasteInput` emits `{ paneId, data, paste: true }` and `sendTextInput` emits `{ paneId, data, paste: false }`.
   - Wire roundtrip tests in `internal/protocol/wire_test.go`.

## Design decisions

- **Decision: Add `bool paste = 4;` to `MsgInput`**
  - **Why:** Preserves the unified input pipeline (FIFO queueing via `p.input`, non-blocking under `s.mu`, scrollback reset, dashboard routing, and upgrade drain) while cleanly differentiating clipboard paste from typed/injected text. In protobuf, unset booleans default to `false`.
  - **Rejected:** Creating a dedicated `MsgPaste` message. That would duplicate routing and handling logic across client and server without functional benefit.
  - **Rejected:** Client-side bracket wrapping. The client does not track child terminal DEC 2004 mode; keeping mode tracking on the server emulator is clean and prevents unnecessary state broadcasts.

- **Decision: Explicit helper functions (`SendPaste` / `sendPasteInput`)**
  - **Why:** Clear call-site semantics in both Go and TypeScript prevent accidentally omitting or inverting a boolean flag.
  - **Rejected:** Adding a boolean parameter to existing `SendInput` / `sendTextInput` methods.

- **Decision: Protocol version bump (17 -> 18)**
  - **Why:** Enforced by `internal/protocol/version_guard_test.go` for any schema alteration in `wideboi.proto`, ensuring server and client version contracts match.
  - **Rejected:** Suppressing version guard or avoiding proto change.

## Patterns to follow

- Protobuf definitions: `internal/protocol/wirepb/wideboi.proto:216-220`
- Protocol codec mapping: `internal/protocol/codec.go:37-38, 107-108`
- Client input methods: `internal/client/client.go:625-650`
- Web input methods: `web/src/input.ts:19-41`
- Server input handler: `internal/server/handlers.go:500-520`
- Server paste test: `internal/server/paste_test.go:19-63`

## What we're NOT doing

- Not altering `MsgSendInputRequest` / `wideboi send-input` CLI scripting command (which already injects raw bytes).
- Not changing macro definition syntax or data models in preferences.
- Not altering how `term.Grid` detects `ansi.ModeBracketedPaste` (already tested and working).
- Not broadcasting emulator terminal modes to clients over the wire.

## Open questions

None.
