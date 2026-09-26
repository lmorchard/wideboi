import { beforeEach, describe, expect, it } from 'vitest';
import { termSettings } from './pane-state';
import { AVAILABLE_FONTS, findFont } from './fonts';
import { KeyRouter } from './key-router';

function keyEvent(key: string, code: string, mods: Partial<KeyboardEvent> = {}): KeyboardEvent {
  return {
    key, code,
    shiftKey: false, altKey: false, ctrlKey: false, metaKey: false,
    isComposing: false, repeat: false,
    preventDefault: () => {},
    ...mods,
  } as unknown as KeyboardEvent;
}

const storage: Record<string, string> = {};
const mockLocalStorage = {
  getItem: (key: string) => storage[key] ?? null,
  setItem: (key: string, val: string) => { storage[key] = String(val); },
  removeItem: (key: string) => { delete storage[key]; },
  clear: () => { Object.keys(storage).forEach(k => delete storage[k]); },
};

if (typeof globalThis.localStorage === 'undefined') {
  (globalThis as any).localStorage = mockLocalStorage;
}

describe('Settings and Font Configuration', () => {
  beforeEach(() => {
    localStorage.clear();
    termSettings.fontSize = 14;
    termSettings.fontFamily = 'monospace';
  });

  it('provides all expected Nerd Fonts in AVAILABLE_FONTS', () => {
    const fontIds = AVAILABLE_FONTS.map(f => f.id);
    expect(fontIds).toContain('monospace');
    expect(fontIds).toContain('JetBrainsMono Nerd Font Mono');
    expect(fontIds).toContain('FiraCode Nerd Font Mono');
    expect(fontIds).toContain('CaskaydiaCove Nerd Font Mono');
    expect(fontIds).toContain('Hack Nerd Font Mono');
    expect(fontIds).toContain('MesloLGS Nerd Font Mono');
    expect(fontIds).toContain('BlexMono Nerd Font Mono');
    expect(fontIds).toContain('SauceCodePro Nerd Font Mono');
    expect(fontIds).toContain('Inconsolata Nerd Font Mono');
    expect(fontIds).toContain('RobotoMono Nerd Font Mono');
    expect(fontIds).toContain('DejaVuSansM Nerd Font Mono');
    expect(fontIds).toContain('VictorMono Nerd Font Mono');
    expect(fontIds).toContain('GeistMono Nerd Font Mono');
  });

  it('persists and restores font settings via localStorage', () => {
    termSettings.fontSize = 18;
    termSettings.fontFamily = 'Hack Nerd Font Mono';
    termSettings.save();

    expect(localStorage.getItem('wideboi:fontSize')).toBe('18');
    expect(localStorage.getItem('wideboi:fontFamily')).toBe('Hack Nerd Font Mono');
    expect(termSettings.font).toBe('18px "Hack Nerd Font Mono", monospace');
    expect(termSettings.cellHeight).toBe(18 * 1.2);
  });

  it('handles font selection changes', () => {
    const font = findFont('CaskaydiaCove Nerd Font Mono');
    expect(font).toBeDefined();
    if (font) {
      termSettings.fontFamily = font.id;
      termSettings.save();
      expect(termSettings.font).toContain('CaskaydiaCove Nerd Font Mono');
    }
  });

  it('routes prefix + comma to toggle_settings in KeyRouter', () => {
    const router = new KeyRouter('ctrl+b');
    router.handle(keyEvent('b', 'KeyB', { ctrlKey: true }));
    expect(router.inPrefix).toBe(true);
    expect(router.handle(keyEvent(',', 'Comma'))).toEqual({ type: 'toggle_settings' });
    expect(router.inPrefix).toBe(false);
  });
});
