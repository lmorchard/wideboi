import { VerbType } from './gen/internal/protocol/wirepb/wideboi_pb';

export type KeyRouterAction =
  | { type: 'ignore' }
  | { type: 'forward' }
  | { type: 'send_literal_key' }
  | { type: 'verb'; verb: VerbType }
  | { type: 'scroll'; delta: number }
  | { type: 'toggle_cards' }
  | { type: 'focus_column'; column: number }
  | { type: 'toggle_help' };

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

const REPEATABLE_VERBS = new Set([
  VerbType.FOCUS_LEFT,
  VerbType.FOCUS_RIGHT,
  VerbType.GROW_WIDTH,
  VerbType.SHRINK_WIDTH,
  VerbType.MOVE_LEFT,
  VerbType.MOVE_RIGHT,
]);

export class KeyRouter {
  private prefix: ParsedPrefix;
  private prefixActive = false;

  constructor(prefixName: string = 'ctrl+b') {
    this.prefix = parsePrefix(prefixName);
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

    const key = e.key.toLowerCase();

    // 2. Escape or Ctrl+C exits prefix mode.
    if (key === 'escape' || (e.ctrlKey && key === 'c')) {
      this.prefixActive = false;
      return { type: 'ignore' };
    }

    // 3. Layout toggle
    if (key === 'c') {
      this.prefixActive = false;
      return { type: 'toggle_cards' };
    }

    // 4. Help overlay toggle
    if (key === '?' || (e.shiftKey && key === '/')) {
      this.prefixActive = false;
      return { type: 'toggle_help' };
    }

    // 5. Column jumps: 1-9 and 0
    if (/^[0-9]$/.test(key)) {
      this.prefixActive = false;
      return { type: 'focus_column', column: parseInt(key, 10) };
    }

    // 6. Scrolling: j (down), k (up)
    if (key === 'j') {
      this.prefixActive = false;
      return { type: 'scroll', delta: -10 };
    }
    if (key === 'k') {
      this.prefixActive = false;
      return { type: 'scroll', delta: 10 };
    }

    // 7. Verbs
    let verb = VerbType.UNSPECIFIED;
    switch (key) {
      case 'h': case 'arrowleft': verb = VerbType.FOCUS_LEFT; break;
      case 'l': case 'arrowright': verb = VerbType.FOCUS_RIGHT; break;
      case 'n': verb = VerbType.NEW_COLUMN; break;
      case 'w': verb = VerbType.CYCLE_WIDTH; break;
      case 'x': verb = VerbType.KILL_PANE; break;
      case 'a': verb = VerbType.SMART_JUMP; break;
      case 's': verb = VerbType.TOGGLE_STATUS; break;
      case 'p': verb = VerbType.GROW_WIDTH; break;
      case 'o': verb = VerbType.SHRINK_WIDTH; break;
      case 'y': verb = VerbType.MOVE_LEFT; break;
      case 'u': verb = VerbType.MOVE_RIGHT; break;
      case 'tab': verb = VerbType.FOCUS_LAST; break;
    }

    if (verb !== VerbType.UNSPECIFIED) {
      if (!(e.ctrlKey && REPEATABLE_VERBS.has(verb))) {
        this.prefixActive = false;
      }
      return { type: 'verb', verb };
    }

    // Any other key breaks out of prefix mode.
    this.prefixActive = false;
    return { type: 'ignore' };
  }
}
