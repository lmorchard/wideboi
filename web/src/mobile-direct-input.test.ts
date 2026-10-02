import { describe, it, expect, vi, beforeEach } from 'vitest';
import {
  MobileDirectInputController,
  BASELINE,
  CARET_OFFSET,
  DirectInputElement,
} from './mobile-direct-input';

describe('MobileDirectInputController', () => {
  let mockInput: DirectInputElement;
  let keysSent: Array<{ key: string; code: string; ctrlKey?: boolean }>;
  let textsSent: string[];
  let revealsSent: number;
  let ctrlActive = false;
  let controller: MobileDirectInputController;

  beforeEach(() => {
    mockInput = {
      value: '',
      selectionStart: 0,
      selectionEnd: 0,
      setSelectionRange: vi.fn((start: number, end: number) => {
        mockInput.selectionStart = start;
        mockInput.selectionEnd = end;
      }),
    };
    keysSent = [];
    textsSent = [];
    revealsSent = 0;
    ctrlActive = false;

    controller = new MobileDirectInputController({
      onKey: (e) => {
        keysSent.push({ key: e.key, code: e.code, ctrlKey: Boolean(e.ctrlKey) });
        return true;
      },
      onText: (text) => {
        textsSent.push(text);
        return true;
      },
      getCtrl: () => ctrlActive,
      resetCtrl: () => {
        ctrlActive = false;
      },
      onRevealCursor: () => {
        revealsSent++;
      },
    });
    controller.attach(mockInput);
  });

  it('sets padded baseline on focus and clears on blur', () => {
    expect(mockInput.value).toBe('');

    controller.handleFocus();
    expect(mockInput.value).toBe(BASELINE);
    expect(mockInput.setSelectionRange).toHaveBeenCalledWith(CARET_OFFSET, CARET_OFFSET);

    controller.handleBlur();
    expect(mockInput.value).toBe('');
  });

  it('handles standard keydown events for named keys', () => {
    controller.handleFocus();

    const enterEvent = { key: 'Enter', code: 'Enter', preventDefault: vi.fn() } as unknown as KeyboardEvent;
    controller.handleKeyDown(enterEvent);
    expect(enterEvent.preventDefault).toHaveBeenCalled();
    expect(keysSent).toEqual([{ key: 'Enter', code: 'Enter', ctrlKey: false }]);
    expect(mockInput.value).toBe(BASELINE);
    expect(revealsSent).toBe(1);

    const escEvent = { key: 'Escape', code: 'Escape', preventDefault: vi.fn() } as unknown as KeyboardEvent;
    controller.handleKeyDown(escEvent);
    expect(escEvent.preventDefault).toHaveBeenCalled();
    expect(keysSent[1]).toEqual({ key: 'Escape', code: 'Escape', ctrlKey: false });
  });

  it('handles printable character via keydown', () => {
    controller.handleFocus();

    const keyEvent = { key: 'a', code: 'KeyA', preventDefault: vi.fn() } as unknown as KeyboardEvent;
    controller.handleKeyDown(keyEvent);
    expect(keyEvent.preventDefault).toHaveBeenCalled();
    expect(keysSent).toEqual([{ key: 'a', code: 'KeyA', ctrlKey: false }]);
    expect(mockInput.value).toBe(BASELINE);
    expect(revealsSent).toBe(1);
  });

  it('handles GBoard Backspace via beforeinput deleteContentBackward', () => {
    controller.handleFocus();

    const beforeInput = { inputType: 'deleteContentBackward', preventDefault: vi.fn() };
    controller.handleBeforeInput(beforeInput);

    expect(beforeInput.preventDefault).toHaveBeenCalled();
    expect(keysSent).toEqual([{ key: 'Backspace', code: 'Backspace', ctrlKey: false }]);
    expect(mockInput.value).toBe(BASELINE);
    expect(revealsSent).toBe(1);
  });

  it('handles GBoard Backspace fallback via input event when character deleted', () => {
    controller.handleFocus();

    // Value shrinks because one baseline character was deleted by soft keyboard
    mockInput.value = BASELINE.slice(1);
    controller.handleInput();

    expect(keysSent).toEqual([{ key: 'Backspace', code: 'Backspace', ctrlKey: false }]);
    expect(mockInput.value).toBe(BASELINE);
    expect(revealsSent).toBe(1);
  });

  it('handles Delete key via beforeinput deleteContentForward', () => {
    controller.handleFocus();

    const beforeInput = { inputType: 'deleteContentForward', preventDefault: vi.fn() };
    controller.handleBeforeInput(beforeInput);

    expect(beforeInput.preventDefault).toHaveBeenCalled();
    expect(keysSent).toEqual([{ key: 'Delete', code: 'Delete', ctrlKey: false }]);
    expect(mockInput.value).toBe(BASELINE);
    expect(revealsSent).toBe(1);
  });

  it('bypasses Android Process key on keydown and captures typed character via beforeinput', () => {
    controller.handleFocus();

    const processKey = { key: 'Process', code: 'KeyB', preventDefault: vi.fn() } as unknown as KeyboardEvent;
    controller.handleKeyDown(processKey);
    // Should NOT be prevented or sent as a key
    expect(processKey.preventDefault).not.toHaveBeenCalled();
    expect(keysSent.length).toBe(0);
    expect(mockInput.value).toBe(BASELINE);

    const beforeInput = { inputType: 'insertText', data: 'b', preventDefault: vi.fn() };
    controller.handleBeforeInput(beforeInput);

    expect(beforeInput.preventDefault).toHaveBeenCalled();
    expect(textsSent).toEqual(['b']);
    expect(mockInput.value).toBe(BASELINE);
    expect(revealsSent).toBe(1);
  });

  it('bypasses Android Process key on keydown and captures typed character via input event fallback', () => {
    controller.handleFocus();

    const processKey = { key: 'Process', code: 'KeyC', preventDefault: vi.fn() } as unknown as KeyboardEvent;
    controller.handleKeyDown(processKey);
    expect(keysSent.length).toBe(0);

    // Browser inserts 'c' into the input value
    mockInput.value = BASELINE.slice(0, CARET_OFFSET) + 'c' + BASELINE.slice(CARET_OFFSET);
    controller.handleInput();

    expect(textsSent).toEqual(['c']);
    expect(mockInput.value).toBe(BASELINE);
    expect(revealsSent).toBe(1);
  });

  it('handles IME composition events without sending intermediate data', () => {
    controller.handleFocus();

    controller.handleCompositionStart();

    // Keydown during composition is ignored
    const keyEvent = { key: 'Process', isComposing: true } as unknown as KeyboardEvent;
    controller.handleKeyDown(keyEvent);
    expect(keysSent.length).toBe(0);

    // beforeinput during composition is ignored
    const beforeInput = { inputType: 'insertCompositionText', data: 'k' };
    controller.handleBeforeInput(beforeInput);
    expect(textsSent.length).toBe(0);

    // compositionend commits the finalized text
    controller.handleCompositionEnd({ data: '漢字' });
    expect(textsSent).toEqual(['漢字']);
    expect(mockInput.value).toBe(BASELINE);
    expect(revealsSent).toBe(1);

    // Follow-up beforeinput with insertFromComposition or insertText does not duplicate
    const postBeforeInput = { inputType: 'insertFromComposition', data: '漢字', preventDefault: vi.fn() };
    controller.handleBeforeInput(postBeforeInput);
    expect(textsSent).toEqual(['漢字']); // unchanged

    controller.handleInput();
    expect(textsSent).toEqual(['漢字']); // unchanged
  });

  it('handles mobile Ctrl modifier button combination', () => {
    controller.handleFocus();
    ctrlActive = true;

    const keyEvent = { key: 'c', code: 'KeyC', preventDefault: vi.fn() } as unknown as KeyboardEvent;
    controller.handleKeyDown(keyEvent);

    expect(keyEvent.preventDefault).toHaveBeenCalled();
    expect(keysSent).toEqual([{ key: 'c', code: 'KeyC', ctrlKey: true }]);
    expect(ctrlActive).toBe(false); // resetCtrl called
    expect(mockInput.value).toBe(BASELINE);
    expect(revealsSent).toBe(1);
  });

  it('preserves modifiers on hardware Ctrl keydown combinations', () => {
    controller.handleFocus();

    const chordEvent = {
      key: 'C',
      code: 'KeyC',
      ctrlKey: true,
      shiftKey: true,
      altKey: true,
      preventDefault: vi.fn(),
    } as unknown as KeyboardEvent;
    controller.handleKeyDown(chordEvent);

    expect(chordEvent.preventDefault).toHaveBeenCalled();
    expect(keysSent).toEqual([{ key: 'C', code: 'KeyC', ctrlKey: true }]);
  });

  it('forwards printable Alt chords with altKey preserved', () => {
    controller.handleFocus();

    const altEvent = {
      key: 'b',
      code: 'KeyB',
      altKey: true,
      ctrlKey: false,
      metaKey: false,
      preventDefault: vi.fn(),
    } as unknown as KeyboardEvent;
    controller.handleKeyDown(altEvent);

    expect(altEvent.preventDefault).toHaveBeenCalled();
    expect(keysSent).toEqual([{ key: 'b', code: 'KeyB', ctrlKey: false }]);
  });

  it('synthesizes Ctrl chord when on-screen Ctrl is active and Android sends Process + beforeinput', () => {
    controller.handleFocus();
    ctrlActive = true;

    const processKey = { key: 'Process', code: 'KeyC', preventDefault: vi.fn() } as unknown as KeyboardEvent;
    controller.handleKeyDown(processKey);
    expect(keysSent.length).toBe(0);

    const beforeInput = { inputType: 'insertText', data: 'c', preventDefault: vi.fn() };
    controller.handleBeforeInput(beforeInput);

    expect(beforeInput.preventDefault).toHaveBeenCalled();
    expect(keysSent).toEqual([{ key: 'c', code: 'KeyC', ctrlKey: true }]);
    expect(ctrlActive).toBe(false);
    expect(mockInput.value).toBe(BASELINE);
  });
});

