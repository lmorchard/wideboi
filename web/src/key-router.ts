import { VerbType } from './gen/internal/protocol/wirepb/wideboi_pb';

export type KeyRouterAction =
  | { type: 'ignore' }
  | { type: 'forward' }
  | { type: 'send_literal_key' }
  | { type: 'verb'; verb: VerbType }
  | { type: 'scroll'; delta: number }
  | { type: 'toggle_cards' }
  | { type: 'pan'; direction: number }
  | { type: 'toggle_follow_pty' }
  | { type: 'focus_column'; column: number }
  | { type: 'toggle_help' }
  | { type: 'toggle_settings' }
  | { type: 'search' }
  | { type: 'prompt' }
  | { type: 'palette' }
  | { type: 'quit' }
  | { type: 'detach' };

export interface ParsedPrefix {
  name: string;
  label: string;
  key: string;
  code?: string;
}

export function parsePrefix(input?: string): ParsedPrefix {
  const norm = (input || '').trim().toLowerCase();
  if (norm === 'ctrl+space' || norm === 'ctrl+ ') {
    return { name: 'ctrl+space', label: 'Ctrl+Space', key: ' ', code: 'Space' };
  }
  const match = /^ctrl\+([a-z])$/.exec(norm);
  if (match) {
    const letter = match[1];
    return {
      name: `ctrl+${letter}`,
      label: `Ctrl+${letter.toUpperCase()}`,
      key: letter,
      code: `Key${letter.toUpperCase()}`,
    };
  }
  return { name: 'ctrl+b', label: 'Ctrl+B', key: 'b', code: 'KeyB' };
}

export const Action = {
  VERB: 1,
  SCROLL: 2,
  QUIT: 3,
  DETACH: 4,
  HELP: 5,
  EXIT: 6,
  FOCUS_COLUMN: 7,
  TOGGLE_LAYOUT: 8,
  SEARCH: 9,
  PAN: 10,
  TOGGLE_FOLLOW_PTY: 11,
  PROMPT: 12,
  PALETTE: 13,
} as const;

export interface KeyBindingLike {
  actionName: string;
  key: string;
  aliases: string[];
  action: number;
  verb?: VerbType;
  scroll?: number;
  pan?: number;
  column?: number;
  barGroup?: string;
  long?: string;
  helpGroup?: string;
  helpKey?: string;
  needsDetach?: boolean;
  essential?: boolean;
  noRepeat?: boolean;
}

export interface HelpEntry {
  keys: string[];
  description: string;
}

export const DEFAULT_BINDINGS: KeyBindingLike[] = [
  { actionName: 'focus_left', key: 'h', aliases: ['left'], action: Action.VERB, verb: VerbType.FOCUS_LEFT, long: 'focus the column to the left' },
  { actionName: 'focus_right', key: 'l', aliases: ['right'], action: Action.VERB, verb: VerbType.FOCUS_RIGHT, long: 'focus the column to the right' },
  { actionName: 'scroll_down', key: 'j', aliases: [], action: Action.SCROLL, scroll: -10, long: "scroll this pane's history down" },
  { actionName: 'scroll_up', key: 'k', aliases: [], action: Action.SCROLL, scroll: 10, long: "scroll this pane's history up" },
  { actionName: 'new_column', key: 'n', aliases: [], action: Action.VERB, verb: VerbType.NEW_COLUMN, long: 'open a new column' },
  { actionName: 'cycle_width', key: 'w', aliases: [], action: Action.VERB, verb: VerbType.CYCLE_WIDTH, long: "cycle this column's width" },
  { actionName: 'shrink_width', key: 'o', aliases: [], action: Action.VERB, verb: VerbType.SHRINK_WIDTH, long: "shrink this column's width" },
  { actionName: 'grow_width', key: 'p', aliases: [], action: Action.VERB, verb: VerbType.GROW_WIDTH, long: "grow this column's width" },
  { actionName: 'move_left', key: 'y', aliases: [], action: Action.VERB, verb: VerbType.MOVE_LEFT, long: 'move this column left' },
  { actionName: 'move_right', key: 'u', aliases: [], action: Action.VERB, verb: VerbType.MOVE_RIGHT, long: 'move this column right' },
  { actionName: 'kill_pane', key: 'x', aliases: [], action: Action.VERB, verb: VerbType.KILL_PANE, long: 'kill the focused pane' },
  { actionName: 'smart_jump', key: 'a', aliases: [], action: Action.VERB, verb: VerbType.SMART_JUMP, long: 'jump to a pane wanting attention' },
  { actionName: 'toggle_status', key: 's', aliases: [], action: Action.VERB, verb: VerbType.TOGGLE_STATUS, long: 'open or focus pane status dashboard' },
  { actionName: 'focus_last', key: 'tab', aliases: [], action: Action.VERB, verb: VerbType.FOCUS_LAST, long: 'focus the previously focused pane' },
  ...[1, 2, 3, 4, 5, 6, 7, 8, 9].map(n => ({
    actionName: `focus_column_${n}`, key: `${n}`, aliases: [], action: Action.FOCUS_COLUMN, column: n,
    long: `focus column ${n}`, helpGroup: 'focus a column by position, 0 the last', helpKey: '0-9',
  })),
  { actionName: 'focus_column_last', key: '0', aliases: [], action: Action.FOCUS_COLUMN, column: -1,
    long: 'focus the last column', helpGroup: 'focus a column by position, 0 the last', helpKey: '0-9' },
  { actionName: 'prompt', key: ':', aliases: [], action: Action.PROMPT, noRepeat: true, long: 'open command prompt' },
  { actionName: 'palette', key: 'space', aliases: [' '], action: Action.PALETTE, noRepeat: true, long: 'open command palette' },
  { actionName: 'search', key: '/', aliases: [], action: Action.SEARCH, noRepeat: true, long: "search the focused pane's history" },
  { actionName: 'help', key: '?', aliases: [], action: Action.HELP, long: 'show this help' },
  { actionName: 'detach', key: 'd', aliases: [], action: Action.DETACH, needsDetach: true, barGroup: 'd detach', long: 'detach, leaving the session running' },
  { actionName: 'toggle_cards', key: 'c', aliases: [], action: Action.TOGGLE_LAYOUT, noRepeat: true, long: 'toggle the card layout' },
  { actionName: 'claim_size', key: 'S', aliases: [], action: Action.VERB, verb: VerbType.CLAIM_SIZE, noRepeat: true, long: 'claim session size for this window' },
  { actionName: 'pan_left', key: 'H', aliases: [], action: Action.PAN, pan: -1, noRepeat: true, long: 'pan focused pane left' },
  { actionName: 'pan_right', key: 'L', aliases: [], action: Action.PAN, pan: 1, noRepeat: true, long: 'pan focused pane right' },
  { actionName: 'follow_pty', key: 'f', aliases: [], action: Action.TOGGLE_FOLLOW_PTY, noRepeat: true, long: 'toggle following PTY widths' },
  { actionName: 'quit', key: 'q', aliases: [], action: Action.QUIT, essential: true, barGroup: 'q quit', long: 'quit wideboi and close every pane' },
  { actionName: 'exit', key: 'esc', aliases: [], action: Action.EXIT, essential: true, long: 'leave control mode' },
];

const REPEATABLE_VERBS = new Set([
  VerbType.FOCUS_LEFT,
  VerbType.FOCUS_RIGHT,
  VerbType.GROW_WIDTH,
  VerbType.SHRINK_WIDTH,
  VerbType.MOVE_LEFT,
  VerbType.MOVE_RIGHT,
]);

function normalizeBrowserKey(key: string, code?: string): string {
  const k = key.toLowerCase();
  if (k === 'arrowleft') return 'left';
  if (k === 'arrowright') return 'right';
  if (k === 'arrowup') return 'up';
  if (k === 'arrowdown') return 'down';
  if (k === 'escape') return 'esc';
  if (k === ' ' || k === 'space' || code === 'Space') return 'space';
  return k;
}

function matchesBinding(b: KeyBindingLike, rawKey: string, normKey: string, e: KeyboardEvent): boolean {
  if (b.key === '?' && (rawKey === '?' || (rawKey === '/' && e.shiftKey))) return true;

  // When Ctrl is held, suppress bindings with noRepeat
  if (e.ctrlKey) {
    if (b.noRepeat) {
      return false;
    }
    return b.key.toLowerCase() === normKey;
  }

  // Not Ctrl-modified single characters: case must match
  if (rawKey.length === 1) {
    if (b.key === rawKey) return true;
    for (const alias of b.aliases) {
      if (alias === rawKey) return true;
    }
    return false;
  }

  // Named keys (e.g. 'Escape', 'Tab', 'Space', 'ArrowLeft')
  if (b.key === rawKey) return true;
  for (const alias of b.aliases) {
    if (alias === rawKey) return true;
  }
  if (b.key.toLowerCase() === normKey) return true;
  for (const alias of b.aliases) {
    if (alias.toLowerCase() === normKey) return true;
  }
  return false;
}

function dispatchAction(b: KeyBindingLike): KeyRouterAction {
  switch (b.action) {
    case Action.VERB:
      return { type: 'verb', verb: b.verb ?? VerbType.UNSPECIFIED };
    case Action.SCROLL:
      return { type: 'scroll', delta: b.scroll ?? 0 };
    case Action.TOGGLE_LAYOUT:
      return { type: 'toggle_cards' };
    case Action.PAN:
      return { type: 'pan', direction: b.pan ?? 0 };
    case Action.TOGGLE_FOLLOW_PTY:
      return { type: 'toggle_follow_pty' };
    case Action.FOCUS_COLUMN:
      return { type: 'focus_column', column: b.column === -1 ? 0 : (b.column ?? 0) };
    case Action.HELP:
      return { type: 'toggle_help' };
    case Action.SEARCH:
      return { type: 'search' };
    case Action.PROMPT:
      return { type: 'prompt' };
    case Action.PALETTE:
      return { type: 'palette' };
    case Action.QUIT:
      return { type: 'quit' };
    case Action.DETACH:
      return { type: 'detach' };
    case Action.EXIT:
    default:
      return { type: 'ignore' };
  }
}

function formatDisplayKey(k: string): string {
  if (k === 'left') return '←';
  if (k === 'right') return '→';
  if (k === 'up') return '↑';
  if (k === 'down') return '↓';
  if (k === 'space') return 'Space';
  if (k === 'tab') return 'Tab';
  if (k === 'esc') return 'Esc';
  return k;
}

export class KeyRouter {
  private prefix: ParsedPrefix;
  private prefixActive = false;
  private bindings: KeyBindingLike[] = [...DEFAULT_BINDINGS];

  constructor(prefixName: string = 'ctrl+b', bindings?: KeyBindingLike[]) {
    this.prefix = parsePrefix(prefixName);
    if (bindings && bindings.length > 0) {
      this.bindings = [...bindings];
    }
  }

  get inPrefix(): boolean {
    return this.prefixActive;
  }

  get prefixLabel(): string {
    return this.prefix.label;
  }

  get prefixName(): string {
    return this.prefix.name;
  }

  setPrefix(prefixName: string) {
    this.prefix = parsePrefix(prefixName);
    this.prefixActive = false;
  }

  setBindings(bindings: KeyBindingLike[]) {
    if (bindings && bindings.length > 0) {
      this.bindings = [...bindings];
    }
  }

  get activeBindings(): KeyBindingLike[] {
    return this.bindings;
  }

  get helpEntries(): HelpEntry[] {
    const entries: HelpEntry[] = [];
    let sawDigits = false;

    for (const b of this.bindings) {
      if (b.action === Action.FOCUS_COLUMN) {
        if (!sawDigits) {
          sawDigits = true;
          entries.push({
            keys: ['1–9', '0'],
            description: 'Focus column by position (0 is last)',
          });
        }
        continue;
      }
      if (b.action === Action.EXIT) {
        continue;
      }
      const keys = [formatDisplayKey(b.key), ...b.aliases.map(formatDisplayKey)];
      let desc = b.long || b.actionName;
      if (desc) {
        desc = desc.charAt(0).toUpperCase() + desc.slice(1);
      }
      entries.push({ keys, description: desc });
    }

    entries.push({ keys: [','], description: 'Toggle settings dialog' });
    entries.push({ keys: ['Esc', 'Ctrl+C'], description: 'Cancel prefix mode' });
    return entries;
  }

  reset() {
    this.prefixActive = false;
  }

  private matchesPrefix(e: KeyboardEvent): boolean {
    if (!e.ctrlKey || e.shiftKey || e.altKey || e.metaKey) return false;
    if (this.prefix.key === ' ' && (e.key === ' ' || e.code === 'Space')) return true;
    return e.key.toLowerCase() === this.prefix.key || e.code === this.prefix.code;
  }

  handle(e: KeyboardEvent): KeyRouterAction {
    if (e.key === 'Control' || e.key === 'Shift' || e.key === 'Alt' || e.key === 'Meta') {
      return { type: 'ignore' };
    }

    if (!this.prefixActive) {
      if (this.matchesPrefix(e)) {
        this.prefixActive = true;
        return { type: 'ignore' };
      }
      return { type: 'forward' };
    }

    // Already in prefix mode.
    // 1. Doubled prefix sends the literal prefix key and exits.
    if (this.matchesPrefix(e)) {
      this.prefixActive = false;
      return { type: 'send_literal_key' };
    }

    // 2. Normalize key for matching
    const rawKey = e.key;
    const normKey = normalizeBrowserKey(rawKey, e.code);

    // 3. Match against configured/default bindings first
    for (const b of this.bindings) {
      if (matchesBinding(b, rawKey, normKey, e)) {
        const isRepeatable = b.action === Action.VERB && b.verb !== undefined && REPEATABLE_VERBS.has(b.verb) && !b.noRepeat;
        if (!(isRepeatable && e.ctrlKey)) {
          this.prefixActive = false;
        }
        return dispatchAction(b);
      }
    }

    // 4. Web settings dialog toggle on unbound comma
    if (e.key === ',') {
      this.prefixActive = false;
      return { type: 'toggle_settings' };
    }

    // 5. Escape or Ctrl+C exits prefix mode when not otherwise bound
    const keyLower = e.key.toLowerCase();
    if (keyLower === 'escape' || (e.ctrlKey && keyLower === 'c')) {
      this.prefixActive = false;
      return { type: 'ignore' };
    }

    // Any other key breaks out of prefix mode.
    this.prefixActive = false;
    return { type: 'ignore' };
  }
}
