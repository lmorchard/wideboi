import { describe, expect, it, vi } from 'vitest';
import { SearchController, type SearchHost } from './search-controller';

function mockHost(): SearchHost & {
  scrolls: any[];
  requests: number[];
  focusedTerminal: boolean;
  focusedInput: boolean;
} {
  return {
    scrolls: [],
    requests: [],
    focusedTerminal: false,
    focusedInput: false,
    sendScroll(cmd) {
      this.scrolls.push(cmd);
    },
    requestHistory(paneId) {
      this.requests.push(paneId);
    },
    focusTerminal() {
      this.focusedTerminal = true;
    },
    focusSearchInput() {
      this.focusedInput = true;
    },
  };
}

describe('SearchController', () => {
  it('starts search session and focuses input', () => {
    const host = mockHost();
    const ctrl = new SearchController(host);

    expect(ctrl.active).toBe(false);
    ctrl.start(3, 10, 100);

    expect(ctrl.active).toBe(true);
    expect(ctrl.paneId).toBe(3);
    expect(ctrl.state?.priorOffset).toBe(10);
    expect(host.focusedInput).toBe(true);
  });

  it('updates query and commits search request', () => {
    const host = mockHost();
    const ctrl = new SearchController(host);
    ctrl.start(2, 0, 50);

    ctrl.setQuery('test');
    expect(ctrl.state?.query).toBe('test');
    expect(ctrl.state?.status).toBe('input');

    ctrl.commit();
    expect(ctrl.state?.status).toBe('searching');
    expect(host.requests).toEqual([2]);
  });

  it('cancels search and sends restoring scroll command', () => {
    const host = mockHost();
    const ctrl = new SearchController(host);
    ctrl.start(5, 25, 200);

    ctrl.cancel();
    expect(ctrl.active).toBe(false);
    expect(host.focusedTerminal).toBe(true);
    expect(host.scrolls).toHaveLength(1);
    expect(host.scrolls[0].offset).toBe(25);
  });

  it('jumps to live and sends offset 0', () => {
    const host = mockHost();
    const ctrl = new SearchController(host);
    ctrl.start(5, 25, 200);

    ctrl.live();
    expect(ctrl.active).toBe(false);
    expect(host.focusedTerminal).toBe(true);
    expect(host.scrolls[0].offset).toBe(0);
  });

  it('handles input keydown for Enter, Escape, Ctrl+G', () => {
    const host = mockHost();
    const ctrl = new SearchController(host);
    ctrl.start(1, 0, 100);
    ctrl.setQuery('findme');

    const enterEvent = { key: 'Enter', preventDefault: vi.fn() } as unknown as KeyboardEvent;
    expect(ctrl.handleInputKeydown(enterEvent)).toBe(true);
    expect(enterEvent.preventDefault).toHaveBeenCalled();
    expect(host.requests).toEqual([1]);

    const escEvent = { key: 'Escape', preventDefault: vi.fn() } as unknown as KeyboardEvent;
    expect(ctrl.handleInputKeydown(escEvent)).toBe(true);
    expect(ctrl.active).toBe(false);
  });

  it('handles terminal keydown for navigation n / N', () => {
    const host = mockHost();
    const ctrl = new SearchController(host);
    ctrl.start(1, 0, 100);
    ctrl.setQuery('findme');

    const nEvent = { key: 'n', ctrlKey: false, altKey: false, metaKey: false, preventDefault: vi.fn() } as unknown as KeyboardEvent;
    expect(ctrl.handleTerminalKeydown(nEvent)).toBe(true);
    expect(nEvent.preventDefault).toHaveBeenCalled();
    expect(host.requests).toEqual([1]);

    const shiftNEvent = { key: 'N', ctrlKey: false, altKey: false, metaKey: false, preventDefault: vi.fn() } as unknown as KeyboardEvent;
    expect(ctrl.handleTerminalKeydown(shiftNEvent)).toBe(true);
    expect(shiftNEvent.preventDefault).toHaveBeenCalled();
    expect(host.requests).toEqual([1, 1]);
  });
});
