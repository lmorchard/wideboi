# Mobile Web Virtual Keyboard Hardening Implementation Plan

**Goal:** Implement `MobileDirectInputController` in `web/src/mobile-direct-input.ts` to harden mobile virtual keyboard input (GBoard backspace, `Process` keys, and IME composition) using a padded baseline buffer, and integrate it into `web/src/wideboi-app.ts`.

**Approach:** Maintain a centered-caret baseline buffer of 8 non-breaking spaces (`\u00a0`). Listen to `beforeinput` (`deleteContentBackward`, `insertText`) and `input` events alongside `keydown` to capture Android soft-keyboard events that omit `keydown` or send `key: "Process"`. Reset the buffer on blur to display the placeholder.

**Tech stack:** TypeScript, Lit, Vitest, Playwright.

---

## Phase 1: `MobileDirectInputController` Module & Unit Tests

Implement the padded baseline buffer controller and unit test all mobile event sequences.

**Files:**
- Create: `web/src/mobile-direct-input.ts` — `MobileDirectInputController` class handling baseline setup, `keydown`, `beforeinput`, `input`, `composition*`, focus, and blur.
- Create: `web/src/mobile-direct-input.test.ts` — Comprehensive unit test suite covering:
  - Standard key events (Enter, Tab, Escape, Arrows)
  - GBoard Backspace via `beforeinput` (`deleteContentBackward`) on padded baseline
  - GBoard Backspace fallback via `input` event length reduction
  - Android `Process` key followed by `beforeinput` / `input`
  - Normal text insertion (`insertText`)
  - IME composition (`compositionstart`, `compositionend`)
  - Focus baseline setup and blur placeholder restore
  - Control key combinations

**Key changes:**
In `web/src/mobile-direct-input.ts`:
```ts
export const PADDING_CHAR = '\u00a0';
export const PADDING_LEN = 8;
export const CARET_OFFSET = PADDING_LEN / 2;
export const BASELINE = PADDING_CHAR.repeat(PADDING_LEN);

export interface MobileDirectInputCallbacks {
  onKey: (event: KeyboardEvent) => boolean;
  onText: (text: string) => boolean;
  getCtrl: () => boolean;
  resetCtrl: () => void;
  onRevealCursor?: () => void;
}

export class MobileDirectInputController {
  private input: HTMLInputElement | null = null;
  private isComposing = false;
  private callbacks: MobileDirectInputCallbacks;

  constructor(callbacks: MobileDirectInputCallbacks) {
    this.callbacks = callbacks;
  }

  attach(input: HTMLInputElement): () => void {
    this.input = input;
    // Bind listeners: focus, blur, keydown, beforeinput, input, compositionstart, compositionend
    // Return cleanup function
  }

  resetBaseline() {
    if (!this.input) return;
    this.input.value = BASELINE;
    try {
      this.input.setSelectionRange(CARET_OFFSET, CARET_OFFSET);
    } catch {}
  }
}
```

**Verification — automated:**
- [ ] `cd web && npx vitest run src/mobile-direct-input.test.ts` passes
- [ ] `make quick` passes

---

## Phase 2: Integration into `wideboi-app.ts` & End-to-End Verification

Integrate `MobileDirectInputController` into `wideboi-app.ts` for the `.mobile-direct-input` field.

**Files:**
- Modify: `web/src/wideboi-app.ts` — Initialize and attach `MobileDirectInputController` on `.mobile-direct-input`.
- Modify: `web/tests/mobile.spec.ts` — Verify direct input typing and backspacing on mobile viewport.

**Key changes:**
In `web/src/wideboi-app.ts`:
```ts
private mobileDirectController = new MobileDirectInputController({
  onKey: (e) => {
    if (!this.client || !this.focusedPaneId) return false;
    return sendKeyboardInput(this.client, this.focusedPaneId, e);
  },
  onText: (text) => {
    if (!this.client || !this.focusedPaneId) return false;
    return sendTextInput(this.client, this.focusedPaneId, text);
  },
  getCtrl: () => this.mobileCtrl,
  resetCtrl: () => { this.mobileCtrl = false; },
  onRevealCursor: () => {
    this.pendingReveal.add(this.focusedPaneId);
    this.focusedPane()?.revealCursor();
  },
});
```

**Verification — automated:**
- [ ] `cd web && npm test` passes
- [ ] `make quick` passes
- [ ] `make check` passes
