import { describe, it, expect } from 'vitest';
import { create } from '@bufbuild/protobuf';
import { decodeColor } from './colors';
import { DEFAULT_THEMES } from './themes';
import { ColorDataSchema, ColorKind } from './gen/internal/protocol/wirepb/wideboi_pb';

describe('colors', () => {
  describe('xterm 256 color cube', () => {
    it('decodes known cube indices correctly using xterm steps', () => {
      const idx16 = create(ColorDataSchema, { kind: ColorKind.INDEXED, index: 16 });
      const idx17 = create(ColorDataSchema, { kind: ColorKind.INDEXED, index: 17 });
      const idx196 = create(ColorDataSchema, { kind: ColorKind.INDEXED, index: 196 });
      const idx231 = create(ColorDataSchema, { kind: ColorKind.INDEXED, index: 231 });

      expect(decodeColor(idx16, false)).toBe('rgb(0,0,0)');
      expect(decodeColor(idx17, false)).toBe('rgb(0,0,95)');
      expect(decodeColor(idx196, false)).toBe('rgb(255,0,0)');
      expect(decodeColor(idx231, false)).toBe('rgb(255,255,255)');
    });

    it('decodes grayscale ramp (indices 232-255)', () => {
      for (let i = 0; i < 24; i++) {
        const index = 232 + i;
        const v = 8 + i * 10;
        const color = create(ColorDataSchema, { kind: ColorKind.INDEXED, index });
        expect(decodeColor(color, false)).toBe(`rgb(${v},${v},${v})`);
      }
    });
  });

  describe('defaults from themes', () => {
    it('uses DEFAULT_THEMES.dark for default background and foreground', () => {
      expect(decodeColor(undefined, true)).toBe(DEFAULT_THEMES.dark.terminal.background);
      expect(decodeColor(undefined, false)).toBe(DEFAULT_THEMES.dark.terminal.foreground);
    });
  });
});
