import { LitElement, html, css, PropertyValues } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { wideboiAppStyles } from '../wideboi-app.styles';

export interface CommandItem {
  id: string;
  label: string;
  icon: string;
  shortcut: string;
  description?: string;
}

export function filterCommands(commands: CommandItem[], query: string): CommandItem[] {
  const q = query.trim().toLowerCase();
  if (!q) return commands;
  return commands.filter(cmd =>
    cmd.label.toLowerCase().includes(q) ||
    (cmd.description && cmd.description.toLowerCase().includes(q)) ||
    cmd.shortcut.toLowerCase().includes(q) ||
    cmd.id.toLowerCase().includes(q)
  );
}

@customElement('wideboi-command-palette')
export class WideboiCommandPalette extends LitElement {
  static styles = [
    wideboiAppStyles,
    css`
      :host {
        display: contents;
      }
    `,
  ];

  @property({ type: Boolean }) open = false;
  @property({ type: String }) initialQuery = '';
  @property({ type: String }) prefixLabel = 'Ctrl+B';
  @property({ type: String }) layoutMode: 'cards' | 'scroll' = 'cards';
  @property({ type: Boolean }) followPTY = false;

  @state() private searchQuery = '';
  @state() private selectedIndex = 0;

  private previousActiveElement: HTMLElement | null = null;

  public focusInput() {
    this.renderRoot.querySelector<HTMLInputElement>('.command-palette-input')?.focus();
  }

  private handleClose = () => {
    this.dispatchEvent(new CustomEvent('close', { bubbles: true, composed: true }));
    if (this.previousActiveElement && typeof this.previousActiveElement.focus === 'function') {
      this.previousActiveElement.focus();
    }
  };

  private handleBackdropClick = (e: MouseEvent) => {
    if (e.target === e.currentTarget) {
      this.handleClose();
    }
  };

  private handleKeyDown = (e: KeyboardEvent) => {
    if (!this.open) return;

    if (e.key === 'Escape') {
      this.handleClose();
      e.stopPropagation();
      e.preventDefault();
      return;
    }

    const isPrompt = this.searchQuery.startsWith(':');
    const filtered = this.getFilteredCommands();

    if (e.key === 'ArrowDown') {
      e.preventDefault();
      if (!isPrompt && filtered.length > 0) {
        this.selectedIndex = (this.selectedIndex + 1) % filtered.length;
        this.scrollSelectedIntoView();
      }
      return;
    }

    if (e.key === 'ArrowUp') {
      e.preventDefault();
      if (!isPrompt && filtered.length > 0) {
        this.selectedIndex = (this.selectedIndex - 1 + filtered.length) % filtered.length;
        this.scrollSelectedIntoView();
      }
      return;
    }

    if (e.key === 'Enter') {
      e.preventDefault();
      if (isPrompt) {
        const cmdLine = this.searchQuery.slice(1).trim();
        if (cmdLine) {
          this.dispatchEvent(new CustomEvent('execute-prompt', { detail: cmdLine, bubbles: true, composed: true }));
        }
        this.handleClose();
      } else if (filtered.length > 0) {
        const selected = filtered[this.selectedIndex] || filtered[0];
        this.triggerCommand(selected.id);
        this.handleClose();
      }
      return;
    }

    if (e.key === 'Tab') {
      const focusable = Array.from(
        this.renderRoot.querySelectorAll<HTMLButtonElement | HTMLInputElement>('button, input')
      ).filter(el => !el.disabled);
      if (focusable.length === 0) return;

      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      const active = (this.shadowRoot?.activeElement || document.activeElement);

      if (e.shiftKey) {
        if (!active || active === first || !focusable.includes(active as any)) {
          last.focus();
          e.preventDefault();
        }
      } else {
        if (!active || active === last || !focusable.includes(active as any)) {
          first.focus();
          e.preventDefault();
        }
      }
    }
  };

  private scrollSelectedIntoView() {
    this.updateComplete.then(() => {
      const selectedEl = this.renderRoot.querySelector<HTMLElement>('.command-menu-item.selected');
      selectedEl?.scrollIntoView({ block: 'nearest' });
    });
  }

  private triggerCommand = (cmd: string) => {
    this.dispatchEvent(new CustomEvent('command', { detail: cmd, bubbles: true, composed: true }));
  };

  private handleSearchInput = (e: Event) => {
    const input = e.target as HTMLInputElement;
    this.searchQuery = input.value;
    this.selectedIndex = 0;
  };

  override connectedCallback() {
    super.connectedCallback();
    this.previousActiveElement = (document.activeElement as HTMLElement) || null;
    window.addEventListener('keydown', this.handleKeyDown);
  }

  override disconnectedCallback() {
    super.disconnectedCallback();
    window.removeEventListener('keydown', this.handleKeyDown);
  }

  override willUpdate(changedProps: PropertyValues) {
    if (changedProps.has('open')) {
      if (this.open) {
        this.searchQuery = this.initialQuery || '';
        this.selectedIndex = 0;
      }
    }
  }

  override updated(changedProps: PropertyValues) {
    if (changedProps.has('open') && this.open) {
      setTimeout(() => this.focusInput(), 0);
    }
  }

  public getCommands(): CommandItem[] {
    return [
      { id: 'paste', label: 'Paste from Clipboard', icon: '📋', shortcut: 'v', description: 'Paste text from system clipboard' },
      { id: 'new-pane', label: 'New Pane', icon: '➕', shortcut: 'n', description: 'Open a new terminal column' },
      { id: 'split', label: 'Split Pane', icon: '✂️', shortcut: '"', description: 'Split and open a new pane' },
      { id: 'close-pane', label: 'Close Pane', icon: '✕', shortcut: 'x', description: 'Close focused terminal pane' },
      { id: 'search', label: 'Search Scrollback', icon: '🔍', shortcut: '/', description: 'Search history in focused pane' },
      { id: 'toggle-cards', label: this.layoutMode === 'cards' ? 'Switch to Scroll Mode' : 'Switch to Cards Mode', icon: '🗂️', shortcut: 'c', description: 'Toggle layout view mode' },
      { id: 'toggle-follow-pty', label: this.followPTY ? 'Unfollow PTY Width' : 'Follow PTY Width', icon: '📐', shortcut: 'f', description: 'Toggle pane width tracking' },
      { id: 'grow-width', label: 'Grow Pane Width', icon: '↔️', shortcut: '+', description: 'Increase column width' },
      { id: 'shrink-width', label: 'Shrink Pane Width', icon: '><', shortcut: '-', description: 'Decrease column width' },
      { id: 'cycle-width', label: 'Cycle Column Width', icon: '🔄', shortcut: 'w', description: 'Cycle through preset widths' },
      { id: 'move-left', label: 'Move Pane Left', icon: '⬅️', shortcut: '{', description: 'Swap pane with left neighbor' },
      { id: 'move-right', label: 'Move Pane Right', icon: '➡️', shortcut: '}', description: 'Swap pane with right neighbor' },
      { id: 'focus-left', label: 'Focus Left Pane', icon: '◀️', shortcut: 'h', description: 'Focus neighbor to the left' },
      { id: 'focus-right', label: 'Focus Right Pane', icon: '▶️', shortcut: 'l', description: 'Focus neighbor to the right' },
      { id: 'smart-jump', label: 'Smart Jump', icon: '⚡', shortcut: 'j', description: 'Jump to active or last output pane' },
      { id: 'settings', label: 'Settings', icon: '⚙', shortcut: ',', description: 'Preferences and appearance' },
      { id: 'help', label: 'Help', icon: '❓', shortcut: '?', description: 'Keys and help reference' },
      { id: 'prompt', label: 'Command Prompt', icon: '⌨️', shortcut: ':', description: 'Interactive command prompt (:new, :close, etc.)' },
    ];
  }

  public getFilteredCommands(): CommandItem[] {
    if (this.searchQuery.startsWith(':')) return [];
    return filterCommands(this.getCommands(), this.searchQuery);
  }

  override render() {
    if (!this.open) return html``;

    const isPrompt = this.searchQuery.startsWith(':');
    const filtered = this.getFilteredCommands();

    return html`
      <div class="command-menu-overlay" @click=${this.handleBackdropClick}>
        <div class="command-menu-dialog" role="dialog" aria-modal="true" aria-labelledby="command-palette-title" @click=${(e: Event) => e.stopPropagation()}>
          <div class="command-menu-header">
            <h3 id="command-palette-title">
              <span>${isPrompt ? 'Command Prompt' : 'Command Palette'}</span>
              <span class="command-menu-prefix-tip">Prefix: ${this.prefixLabel}</span>
            </h3>
            <button class="close-btn" @click=${this.handleClose} aria-label="Close command palette">✕</button>
          </div>

          <div class="command-palette-search-box">
            <span class="command-palette-search-icon" aria-hidden="true">${isPrompt ? ':' : '🔍'}</span>
            <input
              type="text"
              class="command-palette-input"
              .value=${this.searchQuery}
              @input=${this.handleSearchInput}
              placeholder=${isPrompt ? 'Enter command (e.g. :new, :close, :split, :run <cmd>, :width <n>)...' : 'Type to search commands, or : for prompt...'}
              aria-label="Command search"
            />
          </div>

          <div class="command-menu-list" role="menu">
            ${isPrompt ? html`
              <div class="command-prompt-preview">
                <span class="command-menu-icon" aria-hidden="true">⌨️</span>
                <div class="command-menu-info">
                  <span class="command-menu-label">Execute: <code>${this.searchQuery.slice(1).trim() || '...'}</code></span>
                  <span class="command-menu-desc">Press Enter to run command</span>
                </div>
              </div>
            ` : filtered.length === 0 ? html`
              <div class="command-palette-no-results">
                No matching commands found. Type <code>:</code> to enter prompt mode.
              </div>
            ` : filtered.map((cmd, idx) => html`
              <button
                class="command-menu-item ${idx === this.selectedIndex ? 'selected' : ''}"
                role="menuitem"
                aria-label=${cmd.label}
                @mouseenter=${() => { this.selectedIndex = idx; }}
                @click=${() => {
                  this.triggerCommand(cmd.id);
                  this.handleClose();
                }}>
                <span class="command-menu-icon" aria-hidden="true">${cmd.icon}</span>
                <div class="command-menu-info">
                  <span class="command-menu-label">${cmd.label}</span>
                  ${cmd.description ? html`<span class="command-menu-desc">${cmd.description}</span>` : ''}
                </div>
                <span class="command-menu-shortcut" title=${`Shortcut: ${this.prefixLabel} ${cmd.shortcut}`}>${cmd.shortcut}</span>
              </button>
            `)}
          </div>
        </div>
      </div>
    `;
  }
}
