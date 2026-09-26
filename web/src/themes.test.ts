import { describe, it, expect } from 'vitest';
import { create } from '@bufbuild/protobuf';
import { getTheme, listThemes, getThemeCSSVariables } from './themes';
import { decodeColor } from './colors';
import { ColorDataSchema, ColorKind } from './gen/internal/protocol/wirepb/wideboi_pb';

describe('themes', () => {
  it('contains expected default themes', () => {
    const themes = listThemes();
    expect(themes.length).toBe(8);
    const ids = themes.map(t => t.id);
    expect(ids).toContain('dark');
    expect(ids).toContain('one-dark');
    expect(ids).toContain('nord');
    expect(ids).toContain('dracula');
    expect(ids).toContain('solarized-dark');
    expect(ids).toContain('solarized-light');
    expect(ids).toContain('github-light');
    expect(ids).toContain('monokai');
  });

  it('retrieves default theme for invalid or empty id', () => {
    expect(getTheme('dark').id).toBe('dark');
    expect(getTheme('non-existent').id).toBe('dark');
    expect(getTheme('toString').id).toBe('dark');
    expect(getTheme('constructor').id).toBe('dark');
    expect(getTheme(null).id).toBe('dark');
    expect(getTheme(undefined).id).toBe('dark');
  });

  it('generates css custom properties dictionary', () => {
    const darkVars = getThemeCSSVariables(getTheme('dark'));
    expect(darkVars['--wb-bg-app']).toBe('#1e1e1e');
    expect(darkVars['--wb-focus']).toBe('#007fd4');

    const lightVars = getThemeCSSVariables(getTheme('solarized-light'));
    expect(lightVars['--wb-bg-app']).toBe('#fdf6e3');
    expect(lightVars['--wb-focus']).toBe('#268bd2');
  });

  describe('decodeColor with themes', () => {
    it('uses theme background and foreground when color is undefined or None', () => {
      const dark = getTheme('dark');
      const light = getTheme('solarized-light');

      expect(decodeColor(undefined, true, dark)).toBe(dark.terminal.background);
      expect(decodeColor(undefined, false, dark)).toBe(dark.terminal.foreground);

      expect(decodeColor(undefined, true, light)).toBe('#fdf6e3');
      expect(decodeColor(undefined, false, light)).toBe('#657b83');

      expect(decodeColor(create(ColorDataSchema, { kind: ColorKind.NONE }), true, light)).toBe('#fdf6e3');
    });

    it('maps ANSI 0-15 to theme ansi palette', () => {
      const nord = getTheme('nord');
      const color4 = create(ColorDataSchema, { kind: ColorKind.BASIC, index: 4 });
      expect(decodeColor(color4, false, nord)).toBe(nord.terminal.ansi[4]); // #81a1c1

      const dark = getTheme('dark');
      expect(decodeColor(color4, false, dark)).toBe('#2472c8');
    });

    it('maps ANSI 16-255 to standard 256 color cube', () => {
      const nord = getTheme('nord');
      const color16 = create(ColorDataSchema, { kind: ColorKind.INDEXED, index: 16 });
      expect(decodeColor(color16, false, nord)).toBe('rgb(0,0,0)');
    });

    it('preserves RGBA colors across themes', () => {
      const light = getTheme('github-light');
      const rgba = create(ColorDataSchema, { kind: ColorKind.RGBA, r: 120, g: 150, b: 200, a: 255 });
      expect(decodeColor(rgba, false, light)).toBe('rgba(120, 150, 200, 1)');
    });
  });
});
