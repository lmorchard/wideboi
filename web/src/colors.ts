import { ColorKind, type ColorData } from './gen/internal/protocol/wirepb/wideboi_pb';
import type { Theme } from './themes';

// Default xterm 256 color palette
const ANSI_COLORS = [
  // 16 basic colors
  '#000000', '#cd3131', '#0dbc79', '#e5e510', '#2472c8', '#bc3fbc', '#11a8cd', '#e5e5e5',
  '#666666', '#f14c4c', '#23d18b', '#f5f543', '#3b8eea', '#d670d6', '#29b8db', '#e5e5e5',
  // 216 colors (16-231)
  ...Array.from({ length: 216 }).map((_, i) => {
    const r = Math.floor(i / 36) * 51;
    const g = Math.floor((i % 36) / 6) * 51;
    const b = (i % 6) * 51;
    return `rgb(${r},${g},${b})`;
  }),
  // 24 grayscale (232-255)
  ...Array.from({ length: 24 }).map((_, i) => {
    const v = i * 10 + 8;
    return `rgb(${v},${v},${v})`;
  })
];

export function decodeColor(color: ColorData | undefined, isBg: boolean, theme?: Theme): string {
  const defaultBg = theme?.terminal.background ?? '#1e1e1e';
  const defaultFg = theme?.terminal.foreground ?? '#d4d4d4';

  if (!color) return isBg ? defaultBg : defaultFg;

  switch (color.kind) {
    case ColorKind.NONE:
      return isBg ? defaultBg : defaultFg;
    case ColorKind.BASIC:
    case ColorKind.INDEXED: {
      if (theme && color.index < 16) {
        return theme.terminal.ansi[color.index] || (isBg ? defaultBg : defaultFg);
      }
      return ANSI_COLORS[color.index] || (isBg ? defaultBg : defaultFg);
    }
    case ColorKind.RGBA:
      return `rgba(${color.r}, ${color.g}, ${color.b}, ${color.a / 255})`;
    default:
      return isBg ? defaultBg : defaultFg;
  }
}
