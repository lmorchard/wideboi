export interface FontOption {
  id: string;
  name: string;
  family: string;
}

export const AVAILABLE_FONTS: FontOption[] = [
  { id: 'monospace', name: 'System Monospace', family: 'monospace' },
  { id: 'JetBrainsMono Nerd Font Mono', name: 'JetBrains Mono Nerd Font', family: 'JetBrainsMono Nerd Font Mono' },
  { id: 'FiraCode Nerd Font Mono', name: 'Fira Code Nerd Font', family: 'FiraCode Nerd Font Mono' },
  { id: 'CaskaydiaCove Nerd Font Mono', name: 'Cascadia Code Nerd Font', family: 'CaskaydiaCove Nerd Font Mono' },
  { id: 'Hack Nerd Font Mono', name: 'Hack Nerd Font', family: 'Hack Nerd Font Mono' },
  { id: 'MesloLGS Nerd Font Mono', name: 'Meslo LGS Nerd Font', family: 'MesloLGS Nerd Font Mono' },
  { id: 'BlexMono Nerd Font Mono', name: 'IBM Plex Mono Nerd Font', family: 'BlexMono Nerd Font Mono' },
  { id: 'SauceCodePro Nerd Font Mono', name: 'Source Code Pro Nerd Font', family: 'SauceCodePro Nerd Font Mono' },
  { id: 'Inconsolata Nerd Font Mono', name: 'Inconsolata Nerd Font', family: 'Inconsolata Nerd Font Mono' },
  { id: 'RobotoMono Nerd Font Mono', name: 'Roboto Mono Nerd Font', family: 'RobotoMono Nerd Font Mono' },
  { id: 'DejaVuSansM Nerd Font Mono', name: 'DejaVu Sans Mono Nerd Font', family: 'DejaVuSansM Nerd Font Mono' },
  { id: 'VictorMono Nerd Font Mono', name: 'Victor Mono Nerd Font', family: 'VictorMono Nerd Font Mono' },
  { id: 'GeistMono Nerd Font Mono', name: 'Geist Mono Nerd Font', family: 'GeistMono Nerd Font Mono' },
];

export function findFont(id: string): FontOption | undefined {
  return AVAILABLE_FONTS.find(f => f.id === id);
}

export function formatFontSpec(family: string, size: number): string {
  const quotedFamily = family.includes(' ') && !family.startsWith('"') ? `"${family}"` : family;
  return `${size}px ${quotedFamily}, monospace`;
}
