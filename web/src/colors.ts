import { ColorKind, type ColorData } from './gen/internal/protocol/wirepb/wideboi_pb';
import { DEFAULT_THEMES, type Theme } from './themes';

const CUBE_STEPS = [0, 95, 135, 175, 215, 255];

// Default xterm 256 color palette
const ANSI_COLORS = [
  // 16 basic colors
  ...DEFAULT_THEMES.dark.terminal.ansi,
  // 216 colors (16-231)
  ...Array.from({ length: 216 }).map((_, i) => {
    const r = CUBE_STEPS[Math.floor(i / 36)];
    const g = CUBE_STEPS[Math.floor((i % 36) / 6)];
    const b = CUBE_STEPS[i % 6];
    return `rgb(${r},${g},${b})`;
  }),
  // 24 grayscale (232-255)
  ...Array.from({ length: 24 }).map((_, i) => {
    const v = i * 10 + 8;
    return `rgb(${v},${v},${v})`;
  })
];

export function decodeColor(color: ColorData | undefined, isBg: boolean, theme?: Theme): string {
  const defaultBg = theme?.terminal.background ?? DEFAULT_THEMES.dark.terminal.background;
  const defaultFg = theme?.terminal.foreground ?? DEFAULT_THEMES.dark.terminal.foreground;

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
