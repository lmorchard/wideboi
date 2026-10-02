# Mobile Web Virtual Keyboard Hardening Spec

**Goal:** Harden virtual keyboard input handling on mobile browsers (Android Chrome / GBoard, Firefox Android, iOS Safari) in wideboi's mobile direct input mode, ensuring Backspace reliably deletes on empty buffers, `Process` keys do not swallow typed characters, and IME composition functions smoothly.

**Source:** https://github.com/lmorchard/wideboi/issues/331

## Current state

- On mobile (`narrowMedia.matches`), `wideboi-app.ts` disables direct canvas focus and routes input through `.mobile-input-bar`:
  - `draft` mode: `<input class="mobile-draft-input">` buffers draft text for review before sending.
  - `direct` mode: `<input class="mobile-direct-input">` forwards keystrokes individually.
- In `direct` mode, `handleMobileDirectKey` and `handleMobileDirectInput` currently clear the `<input>` element to `input.value = ''` immediately after every event (`web/src/wideboi-app.ts:1213-1240`).
- **GBoard / Firefox Android Backspace Bug:** When the `<input>` element is completely empty, soft keyboards (notably GBoard on Android) emit no `keydown`, `keypress`, or `beforeinput` events when Backspace is tapped. Users cannot delete text in direct mode.
- **Android `Process` Key & Composition:** On Android Chrome, soft keyboard taps emit `keydown` with `key === "Process"` and `isComposing === true`. `sendKeyboardInput` (`web/src/input.ts:20`) ignores `key === 'Process'`, but `handleMobileDirectKey` resets `input.value = ''` on `keydown`, clearing the input before `@input` can read the typed character, dropping input.

## Desired end state

### 1. Padded Baseline Buffer for Direct Input
- Introduce `MobileDirectInputController` in `web/src/mobile-direct-input.ts`:
  - `BASELINE = "\u00a0".repeat(8)` (8 non-breaking spaces)
  - `CARET_OFFSET = 4` (caret centered in baseline)
- On `focus`: Set `input.value = BASELINE` and selection to `CARET_OFFSET`.
- On `blur`: Clear `input.value = ''` so the placeholder (`placeholder="Direct terminal keys…"`) is displayed when idle.

### 2. Reliable Backspace and Delete Detection
- Listen to `beforeinput` on the direct input element:
  - If `e.inputType === 'deleteContentBackward'`: Dispatch Backspace (`code 127`), `e.preventDefault()`, reset input to baseline with centered caret.
  - If `e.inputType === 'deleteContentForward'`: Dispatch Delete (`code 0x110008`), `e.preventDefault()`, reset input to baseline.
- Fallback in `input` event:
  - If a deletion occurred without `beforeinput` prevention (`input.value.length < BASELINE.length`), detect deletion, dispatch Backspace, and reset to baseline.

### 3. Android `Process` Key and IME Handling
- On `keydown`:
  - If `e.key === 'Process'` or `e.isComposing`: Do NOT call `e.preventDefault()` and do NOT clear `input.value`. Allow the browser to proceed to `beforeinput` / `input`.
  - If `ctrlKey` or `this.mobileCtrl` with length 1: Dispatch Ctrl key, `e.preventDefault()`, reset to baseline.
  - If named key (`Enter`, `Tab`, `Escape`, `Arrow*`, `Backspace`): Dispatch named key, `e.preventDefault()`, reset to baseline.
- On `beforeinput`:
  - If `e.inputType === 'insertText' && e.data`: Dispatch text via `sendTextInput(client, paneId, e.data)`, `e.preventDefault()`, reset to baseline.
- On `input`:
  - If text was inserted, extract non-baseline characters, dispatch via `sendTextInput`, reset to baseline.
- On `compositionstart` / `compositionend`:
  - Track composition state; on `compositionend`, dispatch committed `e.data` via `sendTextInput` and reset to baseline.

### 4. Integration & Testing
- Integrate `MobileDirectInputController` into `web/src/wideboi-app.ts`.
- Comprehensive Vitest unit test suite in `web/src/mobile-direct-input.test.ts`.

## Design decisions

- **Decision:** Use Zellij's padded baseline buffer (`\u00a0` x 8 with centered caret).
  - **Why:** Guarantees there are characters to both the left and right of the caret at all times. Tapping Backspace on Android soft keyboards always has a character to delete, reliably triggering `deleteContentBackward` / length shrinking.
  - **Rejected:** Zero-width spaces `\u200b` (Android keyboards often collapse or skip zero-width spaces; `\u00a0` is recognized as a physical character by all mobile engines).

- **Decision:** Dedicated `MobileDirectInputController` module.
  - **Why:** Isolates the complex mobile browser event matrix (`keydown`, `beforeinput`, `input`, `composition*`, caret management) into a standalone, testable class without cluttering the 2200-line `wideboi-app.ts`.

- **Decision:** Reset to empty on `blur`.
  - **Why:** Restores the HTML input `placeholder="Direct terminal keys…"` when the user taps away from the direct input field.

## Patterns to follow

- Mobile input handling in `web/src/wideboi-app.ts:1213-1240`.
- Terminal input dispatch in `web/src/input.ts:19-48`.
- Component lifecycle and event listening in Lit (`web/src/wideboi-app.ts:697-810`).

## What we're NOT doing

- Not changing Desktop canvas keyboard handling (which uses document listeners).
- Not altering Draft mode (`mobile-draft-input` is already an unmanaged text input that uses standard typing).
- Not modifying server-side Go code or wire protocols (pure web client improvement).

## Open questions

*(None remaining — all resolved during brainstorm)*
