import { describe, expect, it } from 'vitest';
import { KeyRouter } from './key-router';
import { VerbType } from './gen/internal/protocol/wirepb/wideboi_pb';

function keyEvent(key: string, code: string, mods: Partial<KeyboardEvent> = {}): KeyboardEvent {
  return {
    key, code,
    shiftKey: false, altKey: false, ctrlKey: false, metaKey: false,
    isComposing: false, repeat: false,
    preventDefault: () => {},
    ...mods,
  } as unknown as KeyboardEvent;
}

describe('KeyRouter', () => {
  it('forwards normal keys when not in prefix mode', () => {
    const router = new KeyRouter();
    expect(router.handle(keyEvent('a', 'KeyA'))).toEqual({ type: 'forward' });
    expect(router.inPrefix).toBe(false);
  });

  it('enters prefix mode on configured prefix and ignores key', () => {
    const router = new KeyRouter('ctrl+b');
    // Shifted keys like Ctrl+Shift+B should NOT trigger prefix mode
    expect(router.handle(keyEvent('B', 'KeyB', { ctrlKey: true, shiftKey: true }))).toEqual({ type: 'forward' });
    expect(router.inPrefix).toBe(false);

    expect(router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }))).toEqual({ type: 'ignore' });
    expect(router.inPrefix).toBe(true);
  });

  it('doubled prefix sends literal key and exits prefix mode', () => {
    const router = new KeyRouter('ctrl+b');
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.inPrefix).toBe(true);
    expect(router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }))).toEqual({ type: 'send_literal_key' });
    expect(router.inPrefix).toBe(false);
  });

  it('exits prefix mode on Escape or Ctrl+C', () => {
    const router = new KeyRouter('ctrl+b');
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('Escape', 'Escape'))).toEqual({ type: 'ignore' });
    expect(router.inPrefix).toBe(false);

    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('c', 'KeyC', { ctrlKey: true }))).toEqual({ type: 'ignore' });
    expect(router.inPrefix).toBe(false);
  });

  it('routes verbs and handles repeat mode with Ctrl', () => {
    const router = new KeyRouter('ctrl+b');

    // Without Ctrl: exits prefix mode
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('h', 'KeyH'))).toEqual({ type: 'verb', verb: VerbType.FOCUS_LEFT });
    expect(router.inPrefix).toBe(false);

    // With Ctrl held on repeatable action: stays in prefix mode
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('h', 'KeyH', { ctrlKey: true }))).toEqual({ type: 'verb', verb: VerbType.FOCUS_LEFT });
    expect(router.inPrefix).toBe(true);
    expect(router.handle(keyEvent('l', 'KeyL', { ctrlKey: true }))).toEqual({ type: 'verb', verb: VerbType.FOCUS_RIGHT });
    expect(router.inPrefix).toBe(true);

    // Non-repeatable verb exits even with Ctrl held
    expect(router.handle(keyEvent('x', 'KeyX', { ctrlKey: true }))).toEqual({ type: 'verb', verb: VerbType.KILL_PANE });
    expect(router.inPrefix).toBe(false);
  });

  it('routes web-specific actions: cards layout, column jumps, help', () => {
    const router = new KeyRouter('ctrl+b');

    // 's' triggers TOGGLE_STATUS
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('s', 'KeyS'))).toEqual({ type: 'verb', verb: VerbType.TOGGLE_STATUS });
    expect(router.inPrefix).toBe(false);

    // 'c' toggles cards layout
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('c', 'KeyC'))).toEqual({ type: 'toggle_cards' });
    expect(router.inPrefix).toBe(false);

    // '?' toggles help overlay
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('?', 'Slash', { shiftKey: true }))).toEqual({ type: 'toggle_help' });
    expect(router.inPrefix).toBe(false);

    // ',' toggles settings overlay
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent(',', 'Comma'))).toEqual({ type: 'toggle_settings' });
    expect(router.inPrefix).toBe(false);

    // '/' triggers search
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('/', 'Slash'))).toEqual({ type: 'search' });
    expect(router.inPrefix).toBe(false);

    // '1'-'9' and '0' focus columns
    for (let i = 1; i <= 9; i++) {
      router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
      expect(router.handle(keyEvent(String(i), `Digit${i}`))).toEqual({ type: 'focus_column', column: i });
      expect(router.inPrefix).toBe(false);
    }
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('0', 'Digit0'))).toEqual({ type: 'focus_column', column: 0 });
    expect(router.inPrefix).toBe(false);
  });

  it('routes scroll actions', () => {
    const router = new KeyRouter('ctrl+b');
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('j', 'KeyJ'))).toEqual({ type: 'scroll', delta: -10 });
    expect(router.inPrefix).toBe(false);

    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('k', 'KeyK'))).toEqual({ type: 'scroll', delta: 10 });
    expect(router.inPrefix).toBe(false);
  });

  it('keeps shifted pan separate from lowercase focus and toggles follow', () => {
    const router = new KeyRouter('ctrl+b');
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('H', 'KeyH', { shiftKey: true }))).toEqual({ type: 'pan', direction: -1 });
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('L', 'KeyL', { shiftKey: true }))).toEqual({ type: 'pan', direction: 1 });
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('f', 'KeyF'))).toEqual({ type: 'toggle_follow_pty' });
  });

  it('supports custom prefix like ctrl+a or ctrl+space', () => {
    const router = new KeyRouter('ctrl+a');
    expect(router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }))).toEqual({ type: 'forward' });
    expect(router.inPrefix).toBe(false);

    expect(router.handle(keyEvent('a', 'KeyA', { ctrlKey: true }))).toEqual({ type: 'ignore' });
    expect(router.inPrefix).toBe(true);

    router.setPrefix('ctrl+space');
    expect(router.handle(keyEvent(' ', 'Space', { ctrlKey: true }))).toEqual({ type: 'ignore' });
    expect(router.inPrefix).toBe(true);
  });

  it('supports prompt and palette commands in prefix mode', () => {
    const router = new KeyRouter('ctrl+b');
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent(':', 'Semicolon'))).toEqual({ type: 'prompt' });
    expect(router.inPrefix).toBe(false);

    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent(' ', 'Space'))).toEqual({ type: 'palette' });
    expect(router.inPrefix).toBe(false);
  });

  it('ignores bare modifier keydown events without resetting prefix mode', () => {
    const router = new KeyRouter('ctrl+b');
    expect(router.handle(keyEvent('Control', 'ControlLeft', { ctrlKey: true }))).toEqual({ type: 'ignore' });
    expect(router.inPrefix).toBe(false);

    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.inPrefix).toBe(true);

    // Pressing Control alone should not drop out of prefix mode
    expect(router.handle(keyEvent('Control', 'ControlLeft', { ctrlKey: true }))).toEqual({ type: 'ignore' });
    expect(router.inPrefix).toBe(true);

    // Subsequent chord key still matches doubled prefix
    expect(router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }))).toEqual({ type: 'send_literal_key' });
    expect(router.inPrefix).toBe(false);
  });

  it('honours custom key bindings provided via setBindings', () => {
    const router = new KeyRouter('ctrl+b');
    router.setBindings([
      {
        actionName: 'focus_left',
        key: 'a',
        aliases: [],
        action: 1, // Action.VERB
        verb: VerbType.FOCUS_LEFT,
        long: 'custom focus left',
      },
      {
        actionName: 'cycle_width',
        key: 'W',
        aliases: [],
        action: 1,
        verb: VerbType.CYCLE_WIDTH,
        long: 'custom cycle width',
      },
    ]);

    // Old key 'h' no longer triggers focus_left
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('h', 'KeyH'))).toEqual({ type: 'ignore' });
    expect(router.inPrefix).toBe(false);

    // New key 'a' triggers focus_left
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('a', 'KeyA'))).toEqual({ type: 'verb', verb: VerbType.FOCUS_LEFT });
    expect(router.inPrefix).toBe(false);

    // Capital 'W' triggers cycle_width
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('W', 'KeyW', { shiftKey: true }))).toEqual({ type: 'verb', verb: VerbType.CYCLE_WIDTH });
    expect(router.inPrefix).toBe(false);
  });

  it('generates dynamic help entries including prompt, palette, and settings', () => {
    const router = new KeyRouter('ctrl+b');
    const entries = router.helpEntries;

    // Check presence of actions previously omitted from hardcoded help table
    const promptEntry = entries.find(e => e.keys.includes(':'));
    expect(promptEntry).toBeDefined();

    const paletteEntry = entries.find(e => e.keys.includes('Space'));
    expect(paletteEntry).toBeDefined();

    const statusEntry = entries.find(e => e.keys.includes('s'));
    expect(statusEntry).toBeDefined();

    const settingsEntry = entries.find(e => e.keys.includes(','));
    expect(settingsEntry).toBeDefined();

    const cancelEntry = entries.find(e => e.keys.includes('Esc') && e.keys.includes('Ctrl+C'));
    expect(cancelEntry).toBeDefined();
  });

  it('routes quit and detach actions', () => {
    const router = new KeyRouter('ctrl+b');
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('q', 'KeyQ'))).toEqual({ type: 'quit' });
    expect(router.inPrefix).toBe(false);

    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('d', 'KeyD'))).toEqual({ type: 'detach' });
    expect(router.inPrefix).toBe(false);
  });

  it('prioritizes configured comma binding over settings fallback', () => {
    const router = new KeyRouter('ctrl+b');
    // Default: unbound comma opens settings
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent(',', 'Comma'))).toEqual({ type: 'toggle_settings' });

    // Configured: comma bound to toggle_cards
    router.setBindings([
      {
        actionName: 'toggle_cards',
        key: ',',
        aliases: [],
        action: 8, // Action.TOGGLE_LAYOUT
      },
    ]);
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent(',', 'Comma'))).toEqual({ type: 'toggle_cards' });
    expect(router.inPrefix).toBe(false);
  });

  it('prioritizes configured escape binding over cancel fallback', () => {
    const router = new KeyRouter('ctrl+b');
    router.setBindings([
      {
        actionName: 'focus_last',
        key: 'esc',
        aliases: [],
        action: 1, // Action.VERB
        verb: VerbType.FOCUS_LAST,
      },
    ]);
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.handle(keyEvent('Escape', 'Escape'))).toEqual({ type: 'verb', verb: VerbType.FOCUS_LAST });
    expect(router.inPrefix).toBe(false);
  });
});
