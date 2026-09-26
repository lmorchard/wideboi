export interface TerminalTheme {
  background: string;
  foreground: string;
  cursor: string;
  cursorText: string;
  selection: string;
  scrollFooterBg: string;
  scrollFooterFg: string;
  ansi: [
    string, string, string, string, string, string, string, string,
    string, string, string, string, string, string, string, string
  ];
}

export interface UITheme {
  bgApp: string;
  bgToolbar: string;
  bgPane: string;
  bgInput: string;
  bgButton: string;
  bgButtonHover: string;
  fgPrimary: string;
  fgMuted: string;
  border: string;
  borderDivider: string;
  focus: string;
  cardLabelBg: string;
  cardLabelFg: string;
  cardBorder: string;
  cardBorderFocus: string;
}

export interface Theme {
  id: string;
  name: string;
  isDark: boolean;
  terminal: TerminalTheme;
  ui: UITheme;
}

export const DEFAULT_THEMES: Record<string, Theme> = {
  dark: {
    id: 'dark',
    name: 'Default Dark',
    isDark: true,
    terminal: {
      background: '#1e1e1e',
      foreground: '#d4d4d4',
      cursor: '#d4d4d4',
      cursorText: '#1e1e1e',
      selection: 'rgba(100, 160, 220, 0.45)',
      scrollFooterBg: '#333333',
      scrollFooterFg: '#ffffff',
      ansi: [
        '#000000', '#cd3131', '#0dbc79', '#e5e510', '#2472c8', '#bc3fbc', '#11a8cd', '#e5e5e5',
        '#666666', '#f14c4c', '#23d18b', '#f5f543', '#3b8eea', '#d670d6', '#29b8db', '#e5e5e5',
      ],
    },
    ui: {
      bgApp: '#1e1e1e',
      bgToolbar: '#252526',
      bgPane: '#1e1e1e',
      bgInput: '#1e1e1e',
      bgButton: '#3c3c3c',
      bgButtonHover: '#4c4c4c',
      fgPrimary: '#cccccc',
      fgMuted: '#888888',
      border: '#3c3c3c',
      borderDivider: '#555555',
      focus: '#007fd4',
      cardLabelBg: '#303030',
      cardLabelFg: '#dddddd',
      cardBorder: '#b8b8b8',
      cardBorderFocus: '#0e9aff',
    },
  },
  'one-dark': {
    id: 'one-dark',
    name: 'One Dark',
    isDark: true,
    terminal: {
      background: '#282c34',
      foreground: '#abb2bf',
      cursor: '#528bff',
      cursorText: '#282c34',
      selection: 'rgba(62, 68, 81, 0.65)',
      scrollFooterBg: '#21252b',
      scrollFooterFg: '#abb2bf',
      ansi: [
        '#282c34', '#e06c75', '#98c379', '#e5c07b', '#61afef', '#c678dd', '#56b6c2', '#abb2bf',
        '#5c6370', '#be5046', '#98c379', '#d19a66', '#61afef', '#c678dd', '#56b6c2', '#ffffff',
      ],
    },
    ui: {
      bgApp: '#282c34',
      bgToolbar: '#21252b',
      bgPane: '#282c34',
      bgInput: '#1e2227',
      bgButton: '#353b45',
      bgButtonHover: '#3e4451',
      fgPrimary: '#abb2bf',
      fgMuted: '#5c6370',
      border: '#3e4451',
      borderDivider: '#4b5263',
      focus: '#61afef',
      cardLabelBg: '#21252b',
      cardLabelFg: '#abb2bf',
      cardBorder: '#5c6370',
      cardBorderFocus: '#61afef',
    },
  },
  nord: {
    id: 'nord',
    name: 'Nord',
    isDark: true,
    terminal: {
      background: '#2e3440',
      foreground: '#d8dee9',
      cursor: '#d8dee9',
      cursorText: '#2e3440',
      selection: 'rgba(67, 76, 94, 0.65)',
      scrollFooterBg: '#3b4252',
      scrollFooterFg: '#eceff4',
      ansi: [
        '#3b4252', '#bf616a', '#a3be8c', '#ebcb8b', '#81a1c1', '#b48ead', '#88c0d0', '#e5e9f0',
        '#4c566a', '#bf616a', '#a3be8c', '#ebcb8b', '#81a1c1', '#b48ead', '#8fbcbb', '#eceff4',
      ],
    },
    ui: {
      bgApp: '#2e3440',
      bgToolbar: '#272c36',
      bgPane: '#2e3440',
      bgInput: '#2e3440',
      bgButton: '#434c5e',
      bgButtonHover: '#4c566a',
      fgPrimary: '#d8dee9',
      fgMuted: '#7b88a1',
      border: '#434c5e',
      borderDivider: '#4c566a',
      focus: '#88c0d0',
      cardLabelBg: '#3b4252',
      cardLabelFg: '#eceff4',
      cardBorder: '#4c566a',
      cardBorderFocus: '#88c0d0',
    },
  },
  dracula: {
    id: 'dracula',
    name: 'Dracula',
    isDark: true,
    terminal: {
      background: '#282a36',
      foreground: '#f8f8f2',
      cursor: '#f8f8f2',
      cursorText: '#282a36',
      selection: 'rgba(68, 71, 90, 0.7)',
      scrollFooterBg: '#44475a',
      scrollFooterFg: '#f8f8f2',
      ansi: [
        '#21222c', '#ff5555', '#50fa7b', '#f1fa8c', '#bd93f9', '#ff79c6', '#8be9fd', '#f8f8f2',
        '#6272a4', '#ff6e6e', '#69ff94', '#ffffa5', '#d6acff', '#ff92df', '#a4ffff', '#ffffff',
      ],
    },
    ui: {
      bgApp: '#282a36',
      bgToolbar: '#1e1f29',
      bgPane: '#282a36',
      bgInput: '#282a36',
      bgButton: '#44475a',
      bgButtonHover: '#6272a4',
      fgPrimary: '#f8f8f2',
      fgMuted: '#6272a4',
      border: '#44475a',
      borderDivider: '#6272a4',
      focus: '#bd93f9',
      cardLabelBg: '#1e1f29',
      cardLabelFg: '#f8f8f2',
      cardBorder: '#6272a4',
      cardBorderFocus: '#bd93f9',
    },
  },
  'solarized-dark': {
    id: 'solarized-dark',
    name: 'Solarized Dark',
    isDark: true,
    terminal: {
      background: '#002b36',
      foreground: '#839496',
      cursor: '#93a1a1',
      cursorText: '#002b36',
      selection: 'rgba(7, 54, 66, 0.85)',
      scrollFooterBg: '#073642',
      scrollFooterFg: '#93a1a1',
      ansi: [
        '#073642', '#dc322f', '#859900', '#b58900', '#268bd2', '#d33682', '#2aa198', '#eee8d5',
        '#002b36', '#cb4b16', '#586e75', '#657b83', '#839496', '#6c71c4', '#93a1a1', '#fdf6e3',
      ],
    },
    ui: {
      bgApp: '#002b36',
      bgToolbar: '#073642',
      bgPane: '#002b36',
      bgInput: '#002b36',
      bgButton: '#0a4250',
      bgButtonHover: '#0f5263',
      fgPrimary: '#93a1a1',
      fgMuted: '#586e75',
      border: '#0a4250',
      borderDivider: '#586e75',
      focus: '#268bd2',
      cardLabelBg: '#073642',
      cardLabelFg: '#93a1a1',
      cardBorder: '#586e75',
      cardBorderFocus: '#268bd2',
    },
  },
  'solarized-light': {
    id: 'solarized-light',
    name: 'Solarized Light',
    isDark: false,
    terminal: {
      background: '#fdf6e3',
      foreground: '#657b83',
      cursor: '#586e75',
      cursorText: '#fdf6e3',
      selection: 'rgba(238, 232, 213, 0.9)',
      scrollFooterBg: '#eee8d5',
      scrollFooterFg: '#586e75',
      ansi: [
        '#073642', '#dc322f', '#859900', '#b58900', '#268bd2', '#d33682', '#2aa198', '#eee8d5',
        '#002b36', '#cb4b16', '#586e75', '#657b83', '#839496', '#6c71c4', '#93a1a1', '#fdf6e3',
      ],
    },
    ui: {
      bgApp: '#fdf6e3',
      bgToolbar: '#eee8d5',
      bgPane: '#fdf6e3',
      bgInput: '#fdf6e3',
      bgButton: '#e0dacf',
      bgButtonHover: '#d3ccbf',
      fgPrimary: '#586e75',
      fgMuted: '#93a1a1',
      border: '#d3ccbf',
      borderDivider: '#93a1a1',
      focus: '#268bd2',
      cardLabelBg: '#eee8d5',
      cardLabelFg: '#586e75',
      cardBorder: '#93a1a1',
      cardBorderFocus: '#268bd2',
    },
  },
  'github-light': {
    id: 'github-light',
    name: 'GitHub Light',
    isDark: false,
    terminal: {
      background: '#ffffff',
      foreground: '#24292f',
      cursor: '#24292f',
      cursorText: '#ffffff',
      selection: 'rgba(9, 105, 218, 0.15)',
      scrollFooterBg: '#f6f8fa',
      scrollFooterFg: '#24292f',
      ansi: [
        '#24292f', '#cf222e', '#1a7f37', '#9a6700', '#0969da', '#8250df', '#1b7c83', '#6e7781',
        '#57606a', '#a40e26', '#116329', '#6e4b00', '#0550ae', '#6639ba', '#0a5860', '#24292f',
      ],
    },
    ui: {
      bgApp: '#f6f8fa',
      bgToolbar: '#eaeef2',
      bgPane: '#ffffff',
      bgInput: '#ffffff',
      bgButton: '#e4e8ec',
      bgButtonHover: '#d0d7de',
      fgPrimary: '#24292f',
      fgMuted: '#57606a',
      border: '#d0d7de',
      borderDivider: '#afb8c1',
      focus: '#0969da',
      cardLabelBg: '#eaeef2',
      cardLabelFg: '#24292f',
      cardBorder: '#afb8c1',
      cardBorderFocus: '#0969da',
    },
  },
  monokai: {
    id: 'monokai',
    name: 'Monokai',
    isDark: true,
    terminal: {
      background: '#272822',
      foreground: '#f8f8f2',
      cursor: '#f8f8f0',
      cursorText: '#272822',
      selection: 'rgba(73, 72, 62, 0.8)',
      scrollFooterBg: '#3e3d32',
      scrollFooterFg: '#f8f8f2',
      ansi: [
        '#272822', '#f92672', '#a6e22e', '#f4bf75', '#66d9ef', '#ae81ff', '#a1efe4', '#f8f8f2',
        '#75715e', '#f92672', '#a6e22e', '#e6db74', '#66d9ef', '#ae81ff', '#a1efe4', '#f9f8f5',
      ],
    },
    ui: {
      bgApp: '#272822',
      bgToolbar: '#1e1f1c',
      bgPane: '#272822',
      bgInput: '#272822',
      bgButton: '#3e3d32',
      bgButtonHover: '#49483e',
      fgPrimary: '#f8f8f2',
      fgMuted: '#75715e',
      border: '#3e3d32',
      borderDivider: '#75715e',
      focus: '#66d9ef',
      cardLabelBg: '#1e1f1c',
      cardLabelFg: '#f8f8f2',
      cardBorder: '#75715e',
      cardBorderFocus: '#66d9ef',
    },
  },
};

export function getTheme(id?: string | null): Theme {
  if (id && Object.prototype.hasOwnProperty.call(DEFAULT_THEMES, id)) {
    return DEFAULT_THEMES[id];
  }
  return DEFAULT_THEMES.dark;
}

export function listThemes(): Theme[] {
  return Object.values(DEFAULT_THEMES);
}

export function getThemeCSSVariables(theme: Theme): Record<string, string> {
  return {
    '--wb-bg-app': theme.ui.bgApp,
    '--wb-bg-toolbar': theme.ui.bgToolbar,
    '--wb-bg-pane': theme.ui.bgPane,
    '--wb-bg-input': theme.ui.bgInput,
    '--wb-bg-btn': theme.ui.bgButton,
    '--wb-bg-btn-hover': theme.ui.bgButtonHover,
    '--wb-fg-primary': theme.ui.fgPrimary,
    '--wb-fg-muted': theme.ui.fgMuted,
    '--wb-border': theme.ui.border,
    '--wb-border-divider': theme.ui.borderDivider,
    '--wb-focus': theme.ui.focus,
    '--wb-card-label-bg': theme.ui.cardLabelBg,
    '--wb-card-label-fg': theme.ui.cardLabelFg,
    '--wb-card-border': theme.ui.cardBorder,
    '--wb-card-border-focus': theme.ui.cardBorderFocus,
  };
}
