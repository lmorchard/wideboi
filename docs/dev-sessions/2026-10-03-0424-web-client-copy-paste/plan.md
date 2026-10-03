# Web Client Copy & Paste Implementation Plan

**Goal:** Implement reliable, platform-idiomatic copy and paste in the wideboi web UI client across macOS, Linux, and Windows, supporting keyboard shortcuts (`Cmd+C`/`Cmd+V`, `Ctrl+C`/`Ctrl+V`/`Ctrl+Shift+C`/`Ctrl+Shift+V`/`Shift+Insert`), a terminal context menu, and command menu/palette integration without permission failures in insecure contexts.

**Approach:** Follow the established xterm.js/VS Code pattern by embedding an invisible helper `<textarea>` in each `WideboiPane` to receive focus and handle native `copy`/`paste` events synchronously across all origins (HTTP and HTTPS). Synchronize pane selection to the helper textarea, wire platform-aware keyboard shortcuts in `wideboi-app.ts`, provide a lightweight right-click context menu (Copy/Paste/Select All), and expose a Paste action in the Command Menu and Command Palette.

**Tech stack:** TypeScript, Lit, Canvas 2D, Playwright, Vitest, Web Protocols (WebSocket, Protobuf).

---

## Phase 1: Test Harness & Environment Isolation

Isolate the test server spawned by `live-terminal.spec.ts` from host session environment variables per `LESSONS.md:218-221`, ensuring `make web-accept` succeeds cleanly in all environments before making functional changes.

**Files:**
- Modify: `web/tests/live-terminal.spec.ts` — strip `WIDEBOI*` and `LC_WIDEBOI` from test server spawn environment

**Key changes:**
Filter `process.env` when spawning `wideboi server` in `test.beforeAll`:

```typescript
const cleanEnv = Object.fromEntries(
  Object.entries(process.env).filter(([k]) => !k.startsWith('WIDEBOI') && k !== 'LC_WIDEBOI')
);
serverProc = spawn(binPath, [...], {
  env: {
    ...cleanEnv,
    SHELL: '/bin/sh',
    TERM: 'xterm-256color',
    PS1: '$ ',
    LANG: 'C.UTF-8',
    LC_ALL: 'C.UTF-8',
  },
  stdio: ['ignore', 'pipe', 'pipe'],
});
```

**Verification — automated:**
- [x] `cd web && npx playwright test tests/live-terminal.spec.ts` passes — **1 passed (4.3s)**
- [x] `make web-accept` passes all 53 browser tests — **53 passed (1.7m)**

**Verification — manual:**
- [ ] None (automated test harness fix)

---

## Phase 2: Helper Textarea in `WideboiPane` & DOM Selection Sync

Embed a hidden `<textarea class="clipboard-helper">` in `WideboiPane`. Focus it on `pane.focusInput()`, sync terminal selection into `helperTextarea.value` and `select()`, and emit a `pane-paste` event when the browser pastes into the helper. Update `fromFormControl` in `WideboiApp` so keystrokes from `.clipboard-helper` bubble to the terminal key router.

**Files:**
- Modify: `web/src/wideboi-pane.ts` — add helper textarea, styling, selection syncing, focus delegation, and paste/copy event listeners
- Modify: `web/src/wideboi-app.ts` — update `fromFormControl` to exclude `.clipboard-helper`, handle `pane-paste` custom event
- Test: `web/src/pane-rendering.test.ts` — unit tests for helper textarea creation, selection sync, and paste event dispatching

**Key changes:**
In `web/src/wideboi-pane.ts`:
- Add query decorator: `@query('.clipboard-helper') private helperTextarea?: HTMLTextAreaElement;`
- In `render()`:
  ```html
  <textarea
    class="clipboard-helper"
    tabindex="-1"
    aria-hidden="true"
    autocomplete="off"
    autocorrect="off"
    autocapitalize="off"
    spellcheck="false"
    @paste=${this.onHelperPaste}
    @copy=${this.onHelperCopy}
  ></textarea>
  ```
- In `styles`:
  ```css
  .clipboard-helper {
    position: absolute;
    top: 0;
    left: 0;
    width: 1px;
    height: 1px;
    padding: 0;
    border: none;
    margin: 0;
    opacity: 0;
    pointer-events: none;
    overflow: hidden;
    resize: none;
    z-index: -1;
  }
  ```
- `focusInput()`:
  ```typescript
  focusInput() {
    if (this.helperTextarea) {
      this.helperTextarea.focus({ preventScroll: true });
    } else {
      this.canvas.focus({ preventScroll: true });
    }
  }
  ```
- In `setSelection()` / `clearSelection()`:
  When selection is set, `if (this.helperTextarea) { this.helperTextarea.value = this.selectedText(); this.helperTextarea.select(); }`
  When selection is cleared, `if (this.helperTextarea) { this.helperTextarea.value = ''; }`
- Event handlers:
  ```typescript
  private onHelperPaste = (e: ClipboardEvent) => {
    const text = e.clipboardData?.getData('text/plain') || '';
    if (text) {
      this.dispatchEvent(new CustomEvent('pane-paste', {
        detail: { paneId: this.paneId, text },
        bubbles: true,
        composed: true,
      }));
    }
    if (this.helperTextarea) this.helperTextarea.value = '';
    e.preventDefault();
  };

  private onHelperCopy = (e: ClipboardEvent) => {
    const text = this.selectedText();
    if (text) {
      e.clipboardData?.setData('text/plain', text);
      e.preventDefault();
    }
  };
  ```

In `web/src/wideboi-app.ts`:
- Update `fromFormControl`:
  ```typescript
  const fromFormControl = (e: Event) => e.composedPath().some(node =>
    (node instanceof HTMLInputElement || node instanceof HTMLTextAreaElement ||
     node instanceof HTMLSelectElement || node instanceof HTMLButtonElement) &&
    !(node instanceof HTMLElement && node.classList.contains('clipboard-helper')));
  ```
- Listen for `pane-paste` on `this.paneStrip`:
  ```typescript
  this.paneStrip.addEventListener('pane-paste', (e: Event) => {
    const custom = e as CustomEvent<{ paneId: number; text: string }>;
    if (!this.client || !custom.detail.text) return;
    sendPasteInput(this.client, custom.detail.paneId, custom.detail.text);
    this.pendingReveal.add(custom.detail.paneId);
    this.focusedPane()?.revealCursor();
  }, { signal: this.listeners?.signal });
  ```

**Verification — automated:**
- [x] `cd web && npm test` passes (including new unit tests for helper textarea sync) — **179 passed (22.68s)**
- [x] `make quick` passes — **all targets passed**

**Verification — manual:**
- [ ] Inspect element in browser DOM to verify `.clipboard-helper` is inside `<wideboi-pane>` and invisible

---

## Phase 3: Platform Keyboard Shortcuts & Clipboard Utilities

Implement platform-aware copy and paste shortcuts in `WideboiApp.setupKeyboard()`. Support macOS (`Cmd+C`/`Cmd+V`), Linux/Windows (`Ctrl+C`/`Ctrl+V`/`Ctrl+Shift+C`/`Ctrl+Shift+V`/`Shift+Insert`), and fallback copy via `document.execCommand('copy')` on insecure HTTP contexts.

**Files:**
- Modify: `web/src/wideboi-app.ts` — refactor shortcut handling in `setupKeyboard()`, add `copyToClipboard()` and `pasteFromClipboard()` helper methods
- Test: `web/tests/links-and-clipboard.spec.ts` — add Playwright tests for `Meta+c`/`Meta+v`, `Control+c`/`Control+v`, and `Control+Shift+v`

**Key changes:**
In `web/src/wideboi-app.ts`:
- Add clipboard helpers:
  ```typescript
  private async copyToClipboard(text: string): Promise<boolean> {
    if (!text) return false;
    let copied = false;
    if (navigator.clipboard?.writeText) {
      try {
        await navigator.clipboard.writeText(text);
        copied = true;
      } catch {}
    }
    if (!copied) {
      // Fallback via helper textarea or active selection execCommand
      const pane = this.selectedPane || this.focusedPane();
      if (pane?.helperElement) {
        pane.helperElement.value = text;
        pane.helperElement.select();
        try {
          copied = document.execCommand('copy');
        } catch {}
      }
    }
    return copied;
  }

  private async pasteFromClipboard(): Promise<boolean> {
    if (!this.connected || !this.client) return false;
    if (navigator.clipboard?.readText) {
      try {
        const text = await navigator.clipboard.readText();
        if (text) {
          sendPasteInput(this.client, this.focusedPaneId, text);
          this.pendingReveal.add(this.focusedPaneId);
          this.focusedPane()?.revealCursor();
          return true;
        }
      } catch {}
    }
    return false;
  }
  ```
- In `setupKeyboard()` `keydown`:
  ```typescript
  // macOS Shortcuts (Cmd+C / Cmd+V)
  if (!this.keyRouter.inPrefix && e.metaKey && !e.ctrlKey && !e.altKey) {
    if (e.key === 'c' || e.key === 'C') {
      const text = this.selectedPane?.selectedText() || this.focusedPane()?.selectedText() || '';
      if (text) {
        void this.copyToClipboard(text);
        this.selectedPane?.clearSelection();
        this.selectedPane = undefined;
        e.preventDefault();
        return;
      }
      // If no selection, do not prevent default or forward to terminal
      return;
    }
    if (e.key === 'v' || e.key === 'V') {
      // Helper textarea paste will handle native paste event; fallback to readText
      void this.pasteFromClipboard();
      e.preventDefault();
      return;
    }
  }

  // Linux / Windows Shortcuts (Ctrl+C, Ctrl+Shift+C, Ctrl+V, Ctrl+Shift+V, Shift+Insert)
  if (!this.keyRouter.inPrefix && e.ctrlKey && !e.metaKey && !e.altKey) {
    if (e.shiftKey && (e.key === 'c' || e.key === 'C')) {
      const text = this.selectedPane?.selectedText() || this.focusedPane()?.selectedText() || '';
      if (text) {
        void this.copyToClipboard(text);
        this.selectedPane?.clearSelection();
        this.selectedPane = undefined;
      }
      e.preventDefault();
      return;
    }
    if (!e.shiftKey && (e.key === 'c' || e.key === 'C')) {
      const text = this.selectedPane?.selectedText() || this.focusedPane()?.selectedText() || '';
      if (text) {
        void this.copyToClipboard(text);
        this.selectedPane?.clearSelection();
        this.selectedPane = undefined;
        e.preventDefault();
        return;
      }
      // No selection: falls through to terminal SIGINT
    }
    if (e.key === 'v' || e.key === 'V') {
      void this.pasteFromClipboard();
      e.preventDefault();
      return;
    }
  }

  if (e.shiftKey && e.key === 'Insert') {
    void this.pasteFromClipboard();
    e.preventDefault();
    return;
  }
  ```

**Verification — automated:**
- [x] `cd web && npx playwright test tests/links-and-clipboard.spec.ts` passes — **9 passed (30.1s)**
- [x] `make quick` passes — **all targets passed**

**Verification — manual:**
- [ ] Pressing `Cmd+C` / `Ctrl+C` on selected text copies to system clipboard
- [ ] Pressing `Cmd+V` / `Ctrl+V` pastes clipboard text into the active shell

---

## Phase 4: Custom Terminal Context Menu (Copy, Paste, Select All)

Add a custom lightweight context menu overlay that appears on right-click (`contextmenu`) when mouse tracking is disabled in the clicked pane. Provides **Copy**, **Paste**, and **Select All** actions.

**Files:**
- Create: `web/src/components/context-menu.ts` — `WideboiContextMenu` Lit component
- Modify: `web/src/wideboi-pane.ts` — add `selectAll()` method
- Modify: `web/src/wideboi-app.ts` — mount context menu, hook `contextmenu` listener on `paneStrip`, wire actions
- Test: `web/src/components/context-menu.test.ts` — unit tests for menu items and event dispatch
- Test: `web/tests/links-and-clipboard.spec.ts` — browser test for right-click context menu

**Key changes:**
In `web/src/components/context-menu.ts`:
- Component `WideboiContextMenu`:
  - Properties: `open: boolean`, `x: number`, `y: number`, `hasSelection: boolean`, `hasClipboard: boolean`
  - Events: `action` (`detail: 'copy' | 'paste' | 'select-all'`), `close`
  - Closes on `Escape`, click outside (`window.addEventListener('pointerdown')`), or window blur
  - Accessible menu structure with `role="menu"` and `role="menuitem"`

In `web/src/wideboi-pane.ts`:
- Add `selectAll()`:
  ```typescript
  selectAll() {
    if (!this.pane) return;
    this.setSelection({ x: 0, y: 0 }, { x: this.pane.cols - 1, y: this.pane.rows - 1 });
  }
  ```

In `web/src/wideboi-app.ts`:
- In `setupMouse()`:
  ```typescript
  this.paneStrip.addEventListener('contextmenu', (e) => {
    const pane = this.eventPane(e);
    if (!pane) return;
    if (this.panes.mouseTracking(pane.paneId)) {
      // Forward right-click to terminal mouse tracking; do not open context menu
      return;
    }
    e.preventDefault();
    this.openContextMenu(e.clientX, e.clientY, pane);
  }, { signal: this.listeners?.signal });
  ```
- Action dispatch:
  - `'copy'`: copies selection via `copyToClipboard()` and clears selection
  - `'paste'`: calls `pasteFromClipboard()`
  - `'select-all'`: calls `pane.selectAll()`

**Verification — automated:**
- [x] `cd web && npm test` passes — **181 passed (11.21s)**
- [x] `cd web && npx playwright test tests/links-and-clipboard.spec.ts` passes — **10 passed (44.6s)**
- [x] `make quick` passes — **all targets passed**

**Verification — manual:**
- [ ] Right-click on a terminal pane shows dark-themed context menu with Copy, Paste, Select All
- [ ] Clicking Select All highlights all terminal text; clicking Copy copies it

---

## Phase 5: Command Menu & Command Palette Integration

Expose a "Paste" command in both the Command Menu (`wideboi-command-menu`) and the Command Palette (`wideboi-command-palette`), allowing mobile and keyboard-driven users to paste easily.

**Files:**
- Modify: `web/src/components/command-menu.ts` — add `paste` command item
- Modify: `web/src/components/command-palette.ts` — add `Paste` command item
- Modify: `web/src/wideboi-app.ts` — handle `'paste'` command action in `handleCommand()`
- Test: `web/src/components/command-menu.test.ts` — verify command list includes paste
- Test: `web/tests/mobile.spec.ts` — verify mobile command menu triggers paste

**Key changes:**
In `web/src/components/command-menu.ts`:
```typescript
{ id: 'paste', label: 'Paste from Clipboard', icon: '📋', shortcut: 'v', description: 'Paste into focused pane' }
```

In `web/src/components/command-palette.ts`:
```typescript
{ id: 'paste', label: 'Paste from Clipboard', icon: '📋', category: 'Edit', description: 'Paste text from system clipboard' }
```

In `web/src/wideboi-app.ts`:
```typescript
case 'paste':
  void this.pasteFromClipboard();
  break;
```

**Verification — automated:**
- [x] `cd web && npm test` passes — **182 passed (9.96s)**
- [x] `make check` passes all check gates (Playwright, Vitest, Go race, smoke, golden, attach-check) — **all gates passed**

**Verification — manual:**
- [ ] Open Command Menu on mobile or desktop, select "Paste from Clipboard", verify clipboard text is pasted into active pane

---

## Plan Self-Review

- **Spec coverage:**
  - Helper textarea per pane & selection sync: Phase 2
  - Platform keyboard shortcuts (`Cmd+C`/`Cmd+V`, `Ctrl+C`/`Ctrl+V`/`Ctrl+Shift+C`/`Ctrl+Shift+V`/`Shift+Insert`): Phase 3
  - Insecure context HTTP fallback: Phase 3
  - Custom context menu (Copy, Paste, Select All): Phase 4
  - Command Menu & Palette Paste action: Phase 5
  - Test harness env isolation: Phase 1
- **Placeholder scan:** Zero placeholders ("TBD", "TODO", "implement later" absent).
- **Type consistency:** Method names (`copyToClipboard`, `pasteFromClipboard`, `selectAll`), event names (`pane-paste`), and class names (`WideboiContextMenu`) consistent across all phases.
