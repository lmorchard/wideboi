import type { ColorData } from './protocol';

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

export function decodeColor(color: ColorData | undefined, isBg: boolean): string {
  if (!color) return isBg ? '#1e1e1e' : '#d4d4d4'; // Default VSCode-ish theme

  switch (color.Kind) {
    case 0: // COLOR_NONE
      return isBg ? '#1e1e1e' : '#d4d4d4';
    case 1: // COLOR_BASIC
    case 2: // COLOR_INDEXED
      return ANSI_COLORS[color.Index] || (isBg ? '#1e1e1e' : '#d4d4d4');
    case 3: // COLOR_RGBA
      return `rgba(${color.R}, ${color.G}, ${color.B}, ${color.A / 255})`;
    default:
      return isBg ? '#1e1e1e' : '#d4d4d4';
  }
}
