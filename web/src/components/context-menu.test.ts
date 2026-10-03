import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { WideboiContextMenu } from './context-menu';

class TestWideboiContextMenu extends WideboiContextMenu {
  override performUpdate() {}
  override createRenderRoot(): HTMLElement | DocumentFragment {
    return { appendChild: vi.fn(), querySelectorAll: () => [], querySelector: () => null } as unknown as HTMLElement;
  }
}

describe('WideboiContextMenu', () => {
  let listeners: Record<string, EventListenerOrEventListenerObject[]> = {};

  beforeEach(() => {
    listeners = {};
    const mockWindow = {
      innerWidth: 1024,
      innerHeight: 768,
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

  it('ignores keydown when closed and handles Escape when open', () => {
    const menu = new TestWideboiContextMenu();
    menu.open = false;

    let closeCount = 0;
    menu.addEventListener('close', () => {
      closeCount++;
    });

    menu.connectedCallback();
    const keydownListener = listeners['keydown']?.[0] as (e: KeyboardEvent) => void;

    const stopPropagation = vi.fn();
    const preventDefault = vi.fn();

    keydownListener({ key: 'Escape', stopPropagation, preventDefault } as unknown as KeyboardEvent);
    expect(closeCount).toBe(0);

    menu.open = true;
    keydownListener({ key: 'Escape', stopPropagation, preventDefault } as unknown as KeyboardEvent);
    expect(closeCount).toBe(1);
    expect(stopPropagation).toHaveBeenCalled();
    expect(preventDefault).toHaveBeenCalled();

    menu.disconnectedCallback();
  });

  it('dispatches action event with paneId and action name when triggered', () => {
    const menu = new TestWideboiContextMenu();
    menu.paneId = 7;
    menu.open = true;

    let actionDetail: any = null;
    let closeFired = false;

    menu.addEventListener('action', (e: any) => {
      actionDetail = e.detail;
    });
    menu.addEventListener('close', () => {
      closeFired = true;
    });

    (menu as any).triggerAction('paste');
    expect(actionDetail).toEqual({ action: 'paste', paneId: 7 });
    expect(closeFired).toBe(true);
  });
});
