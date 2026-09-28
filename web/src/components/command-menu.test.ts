import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { WideboiCommandMenu } from './command-menu';

class TestWideboiCommandMenu extends WideboiCommandMenu {
  override performUpdate() {}
  override createRenderRoot(): HTMLElement | DocumentFragment {
    return { appendChild: vi.fn(), querySelectorAll: () => [] } as unknown as HTMLElement;
  }
}

describe('WideboiCommandMenu', () => {
  let listeners: Record<string, EventListenerOrEventListenerObject[]> = {};

  beforeEach(() => {
    listeners = {};
    const mockWindow = {
      addEventListener: (type: string, listener: EventListenerOrEventListenerObject) => {
        listeners[type] = listeners[type] || [];
        listeners[type].push(listener);
      },
      removeEventListener: (type: string, listener: EventListenerOrEventListenerObject) => {
        if (listeners[type]) {
          listeners[type] = listeners[type].filter(l => l !== listener);
        }
      },
    };
    const mockDocument = {
      activeElement: null,
      createElement: () => ({ textContent: '', appendChild: vi.fn() }),
      createComment: () => ({}),
    };

    vi.stubGlobal('window', mockWindow);
    vi.stubGlobal('document', mockDocument);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('ignores keydown events when open is false', () => {
    const menu = new TestWideboiCommandMenu();
    menu.open = false;

    let closeDispatched = false;
    menu.addEventListener('close', () => {
      closeDispatched = true;
    });

    const preventDefault = vi.fn();
    const stopPropagation = vi.fn();
    const escapeEvent = {
      key: 'Escape',
      preventDefault,
      stopPropagation,
    } as unknown as KeyboardEvent;

    // Call handleKeyDown via window event listener when connected
    menu.connectedCallback();
    const keydownListener = listeners['keydown']?.[0] as (e: KeyboardEvent) => void;
    expect(keydownListener).toBeDefined();

    keydownListener(escapeEvent);

    expect(closeDispatched).toBe(false);
    expect(preventDefault).not.toHaveBeenCalled();
    expect(stopPropagation).not.toHaveBeenCalled();

    const tabEvent = {
      key: 'Tab',
      shiftKey: false,
      preventDefault,
      stopPropagation,
    } as unknown as KeyboardEvent;

    keydownListener(tabEvent);

    expect(preventDefault).not.toHaveBeenCalled();
    expect(stopPropagation).not.toHaveBeenCalled();

    menu.disconnectedCallback();
  });

  it('handles Escape keydown when open is true', () => {
    const menu = new TestWideboiCommandMenu();
    menu.open = true;

    let closeDispatched = false;
    menu.addEventListener('close', () => {
      closeDispatched = true;
    });

    const preventDefault = vi.fn();
    const stopPropagation = vi.fn();
    const escapeEvent = {
      key: 'Escape',
      preventDefault,
      stopPropagation,
    } as unknown as KeyboardEvent;

    menu.connectedCallback();
    const keydownListener = listeners['keydown']?.[0] as (e: KeyboardEvent) => void;
    expect(keydownListener).toBeDefined();

    keydownListener(escapeEvent);

    expect(closeDispatched).toBe(true);
    expect(preventDefault).toHaveBeenCalled();
    expect(stopPropagation).toHaveBeenCalled();

    menu.disconnectedCallback();
  });

  it('removes window keydown listener on disconnect', () => {
    const menu = new TestWideboiCommandMenu();
    menu.connectedCallback();
    expect(listeners['keydown']?.length).toBe(1);

    menu.disconnectedCallback();
    expect(listeners['keydown']?.length).toBe(0);
  });
});
