import { describe, it, expect, vi } from 'vitest';
import { executeMacro, macroEndsWithEnter, type Macro } from './macros';
import type { WideboiClient } from './client';

describe('macros', () => {
  it('detects whether a macro ends with Enter', () => {
    const withEnter: Macro = {
      name: 'Status',
      steps: [
        { text: 'git status' },
        { key: 'Enter', code: 'Enter' },
      ],
    };
    const withoutEnter: Macro = {
      name: 'Search',
      steps: [{ key: 'r', ctrl: true }],
    };
    const textOnly: Macro = {
      name: 'Text',
      steps: [{ text: 'some command' }],
    };
    expect(macroEndsWithEnter(withEnter)).toBe(true);
    expect(macroEndsWithEnter(withoutEnter)).toBe(false);
    expect(macroEndsWithEnter(textOnly)).toBe(false);
  });

  it('executes steps in exact order to the target pane without implicit Enter', () => {
    const sentMessages: any[] = [];
    const mockClient = {
      send: vi.fn((msg) => {
        sentMessages.push(msg);
      }),
    } as unknown as WideboiClient;

    const macro: Macro = {
      name: 'Deploy',
      steps: [
        { text: 'npm run test' },
        { key: 'Enter', code: 'Enter' },
        { key: 'r', code: 'KeyR', ctrl: true },
      ],
    };

    const targetPaneId = 7;
    const ok = executeMacro(mockClient, targetPaneId, macro);
    expect(ok).toBe(true);

    expect(sentMessages).toHaveLength(3);

    // Step 1: text sent without implicit Enter
    expect(sentMessages[0].case).toBe('input');
    expect(sentMessages[0].value.paneId).toBe(7);
    expect(new TextDecoder().decode(sentMessages[0].value.data)).toBe('npm run test');

    // Step 2: Enter key
    expect(sentMessages[1].case).toBe('input');
    expect(sentMessages[1].value.paneId).toBe(7);
    expect(sentMessages[1].value.key.code).toBe(13); // Enter code

    // Step 3: Ctrl+R key
    expect(sentMessages[2].case).toBe('input');
    expect(sentMessages[2].value.paneId).toBe(7);
    expect(sentMessages[2].value.key.code).toBe(114); // 'r'
    expect(sentMessages[2].value.key.mod & 4).toBe(4); // Ctrl
  });

  it('handles empty macros gracefully', () => {
    const mockClient = { send: vi.fn() } as unknown as WideboiClient;
    expect(executeMacro(mockClient, 1, { name: 'Empty', steps: [] })).toBe(false);
  });
});
