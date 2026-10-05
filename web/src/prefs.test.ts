import { beforeEach, describe, expect, it } from 'vitest';
import { getPref, setPref } from './prefs';
import { DEFAULT_MACROS } from './macros';

const mockStorage = (() => {
  let store: Record<string, string> = {};
  return {
    getItem: (key: string) => store[key] ?? null,
    setItem: (key: string, value: string) => {
      store[key] = String(value);
    },
    removeItem: (key: string) => {
      delete store[key];
    },
    clear: () => {
      store = {};
    },
  };
})();

describe('preferences module', () => {
  beforeEach(() => {
    mockStorage.clear();
    (globalThis as unknown as { localStorage: typeof mockStorage }).localStorage = mockStorage;
  });

  it('returns default preferences when storage is empty', () => {
    expect(getPref('theme')).toBe('dark');
    expect(getPref('prefix')).toBe('ctrl+b');
    expect(getPref('macros')).toEqual(DEFAULT_MACROS);
    expect(getPref('fontSize')).toBe(14);
    expect(getPref('fontFamily')).toBe('monospace');
  });

  it('reads from legacy keys if modern key is not present', () => {
    mockStorage.setItem('wideboi.theme', 'monokai');
    mockStorage.setItem('wideboi.prefix', 'ctrl+a');
    mockStorage.setItem(
      'wideboi.macros',
      JSON.stringify([{ name: 'Custom', steps: [{ text: 'ls' }] }]),
    );

    expect(getPref('theme')).toBe('monokai');
    expect(getPref('prefix')).toBe('ctrl+a');
    expect(getPref('macros')).toEqual([{ name: 'Custom', steps: [{ text: 'ls' }] }]);
  });

  it('prefers modern key over legacy key when both exist', () => {
    mockStorage.setItem('wideboi.theme', 'monokai');
    mockStorage.setItem('wideboi:theme', 'nord');

    expect(getPref('theme')).toBe('nord');
  });

  it('writes preferences to modern keys', () => {
    setPref('theme', 'solarized');
    setPref('prefix', 'ctrl+space');
    setPref('fontSize', 18);
    setPref('fontFamily', 'Hack');
    setPref('macros', [{ name: 'Test', steps: [{ text: 'pwd' }] }]);

    expect(mockStorage.getItem('wideboi:theme')).toBe('solarized');
    expect(mockStorage.getItem('wideboi:prefix')).toBe('ctrl+space');
    expect(mockStorage.getItem('wideboi:fontSize')).toBe('18');
    expect(mockStorage.getItem('wideboi:fontFamily')).toBe('Hack');
    expect(mockStorage.getItem('wideboi:macros')).toBe(
      JSON.stringify([{ name: 'Test', steps: [{ text: 'pwd' }] }]),
    );

    expect(getPref('fontSize')).toBe(18);
    expect(getPref('fontFamily')).toBe('Hack');
  });

  it('lets panes copy by default, and respects a stored opt-out', () => {
    expect(getPref('paneClipboard')).toBe(true);
    setPref('paneClipboard', false);
    expect(mockStorage.getItem('wideboi:paneClipboard')).toBe('false');
    expect(getPref('paneClipboard')).toBe(false);
    setPref('paneClipboard', true);
    expect(getPref('paneClipboard')).toBe(true);
  });

  it('handles invalid macros JSON gracefully', () => {
    mockStorage.setItem('wideboi:macros', 'not-valid-json');
    expect(getPref('macros')).toEqual(DEFAULT_MACROS);
  });

  it('handles invalid font size gracefully', () => {
    mockStorage.setItem('wideboi:fontSize', 'invalid-number');
    expect(getPref('fontSize')).toBe(14);
  });

  it('handles storage.getItem throwing SecurityError gracefully', () => {
    const throwingStorage = {
      getItem: () => { throw new Error('SecurityError'); },
      setItem: () => {},
      removeItem: () => {},
      clear: () => {},
    };
    (globalThis as unknown as { localStorage: typeof throwingStorage }).localStorage = throwingStorage;
    expect(getPref('theme')).toBe('dark');
    expect(getPref('prefix')).toBe('ctrl+b');
    expect(getPref('fontSize')).toBe(14);
  });
});
