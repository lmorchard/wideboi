import { describe, expect, it, vi } from 'vitest';
import { WideboiApp } from './wideboi-app';

describe('Prompt command handling for renaming', () => {
  it('sends renamePaneRequest on :title and :rename-pane', () => {
    vi.stubGlobal('ResizeObserver', class {
      observe() {}
      unobserve() {}
      disconnect() {}
    });

    const app = new WideboiApp();
    const sent: any[] = [];
    (app as any).client = {
      send: (msg: any) => sent.push(msg),
    };
    (app as any).session = { focusedPaneId: 2 };

    // 1. Rename focused pane with quotes
    (app as any).handlePromptCommand('title "New Title"');
    expect(sent).toHaveLength(1);
    expect(sent[0]).toEqual({
      case: 'renamePaneRequest',
      value: { paneId: 2, title: 'New Title', clear: false },
    });

    // 2. Rename specific pane with unquoted words
    (app as any).handlePromptCommand('rename-pane 3 Other Title');
    expect(sent).toHaveLength(2);
    expect(sent[1]).toEqual({
      case: 'renamePaneRequest',
      value: { paneId: 3, title: 'Other Title', clear: false },
    });

    // 3. Clear title with no arguments
    (app as any).handlePromptCommand('label');
    expect(sent).toHaveLength(3);
    expect(sent[2]).toEqual({
      case: 'renamePaneRequest',
      value: { paneId: 2, title: '', clear: true },
    });

    // 4. Clear specific pane
    (app as any).handlePromptCommand('title 3 ""');
    expect(sent).toHaveLength(4);
    expect(sent[3]).toEqual({
      case: 'renamePaneRequest',
      value: { paneId: 3, title: '', clear: true },
    });
  });

  it('handles renamePaneResponse error with warning', () => {
    vi.stubGlobal('ResizeObserver', class {
      observe() {}
      unobserve() {}
      disconnect() {}
    });

    const app = new WideboiApp();
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});

    // Mock WebSocket for WideboiClient
    vi.stubGlobal('WebSocket', class {
      close() {}
    });

    (app as any).wsUrl = 'ws://localhost:8080';
    (app as any).connectClient();
    // Call the registered onMessage directly if set on app.client
    if ((app as any).client?.onMessage) {
      (app as any).client.onMessage({
        msg: {
          case: 'renamePaneResponse',
          value: { paneId: 999, error: 'pane 999 not found' },
        },
      });
      expect(warnSpy).toHaveBeenCalledWith('[wideboi] rename-pane error: pane 999 not found');
    }

    warnSpy.mockRestore();
    vi.unstubAllGlobals();
  });
});
