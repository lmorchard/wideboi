export const PADDING_CHAR = '\u00a0';
export const PADDING_LEN = 8;
export const CARET_OFFSET = PADDING_LEN / 2; // 4
export const BASELINE = PADDING_CHAR.repeat(PADDING_LEN);

export interface DirectInputElement {
  value: string;
  selectionStart?: number | null;
  selectionEnd?: number | null;
  setSelectionRange?(start: number, end: number): void;
  addEventListener?(type: string, listener: (ev: any) => void): void;
  removeEventListener?(type: string, listener: (ev: any) => void): void;
}

export interface MobileDirectInputCallbacks {
  onKey: (event: KeyboardEvent) => boolean;
  onText: (text: string) => boolean;
  getCtrl: () => boolean;
  resetCtrl: () => void;
  onRevealCursor?: () => void;
}

export class MobileDirectInputController {
  public input: DirectInputElement | null = null;
  public isComposing = false;
  private justComposed = false;
  private callbacks: MobileDirectInputCallbacks;
  private cleanupFns: Array<() => void> = [];

  constructor(callbacks: MobileDirectInputCallbacks) {
    this.callbacks = callbacks;
  }

  attach(input: DirectInputElement): () => void {
    this.detach();
    this.input = input;

    if (input.addEventListener && input.removeEventListener) {
      const add = (type: string, listener: (ev: any) => void) => {
        input.addEventListener!(type, listener);
        this.cleanupFns.push(() => input.removeEventListener!(type, listener));
      };

      add('focus', () => this.handleFocus());
      add('blur', () => this.handleBlur());
      add('click', () => this.handleClick());
      add('keydown', (e) => this.handleKeyDown(e));
      add('beforeinput', (e) => this.handleBeforeInput(e));
      add('input', (e) => this.handleInput(e));
      add('compositionstart', () => this.handleCompositionStart());
      add('compositionend', (e) => this.handleCompositionEnd(e));
    }

    if (typeof document !== 'undefined' && document.activeElement === (input as unknown as Element)) {
      this.resetBaseline();
    }

    return () => this.detach();
  }

  detach() {
    for (const cleanup of this.cleanupFns) {
      cleanup();
    }
    this.cleanupFns = [];
    this.input = null;
    this.isComposing = false;
  }

  resetBaseline() {
    if (!this.input) return;
    this.input.value = BASELINE;
    try {
      this.input.setSelectionRange?.(CARET_OFFSET, CARET_OFFSET);
    } catch {}
  }

  private syncTarget(e?: { target?: any }) {
    if (e?.target) {
      this.input = e.target as DirectInputElement;
    }
  }

  handleFocus(e?: { target?: any }) {
    this.syncTarget(e);
    this.resetBaseline();
  }

  handleBlur(e?: { target?: any }) {
    this.syncTarget(e);
    if (this.input) {
      this.input.value = '';
    }
    this.isComposing = false;
  }

  handleClick(e?: { target?: any }) {
    this.syncTarget(e);
    if (this.input && this.input.value === BASELINE) {
      try {
        this.input.setSelectionRange?.(CARET_OFFSET, CARET_OFFSET);
      } catch {}
    }
  }

  handleCompositionStart(e?: { target?: any }) {
    this.syncTarget(e);
    this.isComposing = true;
  }

  handleCompositionEnd(e: { data?: string; target?: any }) {
    this.syncTarget(e);
    this.isComposing = false;
    this.justComposed = true;
    if (e.data) {
      if (this.callbacks.onText(e.data)) {
        this.callbacks.onRevealCursor?.();
      }
    }
    this.resetBaseline();
  }

  handleKeyDown(e: KeyboardEvent) {
    this.syncTarget(e);
    // When composing or when Android emits "Process" or "Dead", do not handle or clear yet
    if (e.isComposing || this.isComposing || e.key === 'Process' || e.key === 'Dead') {
      return;
    }

    const ctrl = this.callbacks.getCtrl() || e.ctrlKey;
    if (ctrl && e.key && e.key.length === 1) {
      if (e.ctrlKey) {
        if (this.callbacks.onKey(e)) {
          this.callbacks.onRevealCursor?.();
        }
      } else {
        let synthesized: KeyboardEvent;
        try {
          synthesized = new KeyboardEvent('keydown', {
            key: e.key,
            code: e.code,
            ctrlKey: true,
            shiftKey: e.shiftKey,
            altKey: e.altKey,
            metaKey: e.metaKey,
            repeat: e.repeat,
          });
        } catch {
          synthesized = {
            key: e.key,
            code: e.code,
            ctrlKey: true,
            shiftKey: e.shiftKey,
            altKey: e.altKey,
            metaKey: e.metaKey,
            repeat: e.repeat,
          } as KeyboardEvent;
        }
        if (this.callbacks.onKey(synthesized)) {
          this.callbacks.onRevealCursor?.();
        }
      }
      this.callbacks.resetCtrl();
      e.preventDefault?.();
      this.resetBaseline();
      return;
    }

    // Direct Backspace key
    if (e.key === 'Backspace') {
      if (this.callbacks.onKey(e)) {
        this.callbacks.onRevealCursor?.();
      }
      e.preventDefault?.();
      this.resetBaseline();
      return;
    }

    // Direct Named keys
    const isNamed = [
      'Enter', 'Tab', 'Escape', 'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight',
      'Insert', 'Delete', 'PageUp', 'PageDown', 'Home', 'End'
    ].includes(e.key);
    if (isNamed) {
      if (this.callbacks.onKey(e)) {
        this.callbacks.onRevealCursor?.();
      }
      e.preventDefault?.();
      this.resetBaseline();
      return;
    }

    // Printable single character (plain, Shifted, or Alt chord)
    if (!e.metaKey && !e.ctrlKey && e.key && e.key.length === 1) {
      if (this.callbacks.onKey(e)) {
        this.callbacks.onRevealCursor?.();
      }
      e.preventDefault?.();
      this.resetBaseline();
      return;
    }
  }

  private sendTextOrCtrl(text: string) {
    if (this.callbacks.getCtrl() && text.length === 1) {
      let ev: KeyboardEvent;
      const code = 'Key' + text.toUpperCase();
      try {
        ev = new KeyboardEvent('keydown', { key: text, code, ctrlKey: true });
      } catch {
        ev = { key: text, code, ctrlKey: true } as KeyboardEvent;
      }
      if (this.callbacks.onKey(ev)) {
        this.callbacks.onRevealCursor?.();
      }
      this.callbacks.resetCtrl();
      return;
    }
    if (this.callbacks.onText(text)) {
      this.callbacks.onRevealCursor?.();
    }
  }

  handleBeforeInput(e: { inputType?: string; data?: string | null; preventDefault?: () => void; target?: any }) {
    this.syncTarget(e);
    if (this.isComposing) return;
    if (this.justComposed || e.inputType === 'insertFromComposition') {
      this.justComposed = false;
      e.preventDefault?.();
      this.resetBaseline();
      return;
    }

    if (e.inputType === 'deleteContentBackward') {
      let ev: KeyboardEvent;
      try {
        ev = new KeyboardEvent('keydown', { key: 'Backspace', code: 'Backspace' });
      } catch {
        ev = { key: 'Backspace', code: 'Backspace', ctrlKey: false } as KeyboardEvent;
      }
      if (this.callbacks.onKey(ev)) {
        this.callbacks.onRevealCursor?.();
      }
      e.preventDefault?.();
      this.resetBaseline();
      return;
    }

    if (e.inputType === 'deleteContentForward') {
      let ev: KeyboardEvent;
      try {
        ev = new KeyboardEvent('keydown', { key: 'Delete', code: 'Delete' });
      } catch {
        ev = { key: 'Delete', code: 'Delete', ctrlKey: false } as KeyboardEvent;
      }
      if (this.callbacks.onKey(ev)) {
        this.callbacks.onRevealCursor?.();
      }
      e.preventDefault?.();
      this.resetBaseline();
      return;
    }

    if (e.inputType === 'insertText' && e.data) {
      this.sendTextOrCtrl(e.data);
      e.preventDefault?.();
      this.resetBaseline();
      return;
    }
  }

  handleInput(e?: { target?: any }) {
    this.syncTarget(e);
    if (this.isComposing || !this.input) return;
    if (this.justComposed) {
      this.justComposed = false;
      this.resetBaseline();
      return;
    }

    const val = this.input.value;

    // Fallback: character was deleted
    if (val.length < BASELINE.length) {
      let ev: KeyboardEvent;
      try {
        ev = new KeyboardEvent('keydown', { key: 'Backspace', code: 'Backspace' });
      } catch {
        ev = { key: 'Backspace', code: 'Backspace', ctrlKey: false } as KeyboardEvent;
      }
      if (this.callbacks.onKey(ev)) {
        this.callbacks.onRevealCursor?.();
      }
      this.resetBaseline();
      return;
    }

    // Fallback: character(s) were inserted
    const cleaned = val.split(PADDING_CHAR).join('');
    if (cleaned.length > 0) {
      this.sendTextOrCtrl(cleaned);
    }
    this.resetBaseline();
  }
}
