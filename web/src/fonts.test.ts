import { describe, expect, it } from 'vitest';
import { AVAILABLE_FONTS, findFont, formatFontSpec } from './fonts';

describe('fonts', () => {
  it('defines available fonts including System Monospace and Nerd Fonts', () => {
    expect(AVAILABLE_FONTS.length).toBeGreaterThanOrEqual(10);
    const mono = findFont('monospace');
    expect(mono).toBeDefined();
    expect(mono?.name).toBe('System Monospace');

    const jb = findFont('JetBrainsMono Nerd Font Mono');
    expect(jb).toBeDefined();
    expect(jb?.name).toContain('JetBrains Mono');

    const cascadia = findFont('CaskaydiaCove Nerd Font Mono');
    expect(cascadia).toBeDefined();
    expect(cascadia?.name).toContain('Cascadia');

    const hack = findFont('Hack Nerd Font Mono');
    expect(hack).toBeDefined();

    const geist = findFont('GeistMono Nerd Font Mono');
    expect(geist).toBeDefined();
  });

  it('formats font spec correctly with and without quotes', () => {
    expect(formatFontSpec('monospace', 14)).toBe('14px monospace, monospace');
    expect(formatFontSpec('JetBrainsMono Nerd Font Mono', 16)).toBe('16px "JetBrainsMono Nerd Font Mono", monospace');
    expect(formatFontSpec('"FiraCode Nerd Font Mono"', 12)).toBe('12px "FiraCode Nerd Font Mono", monospace');
  });

  it('findFont returns undefined for unknown fonts', () => {
    expect(findFont('NonExistentFont')).toBeUndefined();
  });
});
