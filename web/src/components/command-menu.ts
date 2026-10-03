import { LitElement, html, css } from 'lit';
import { customElement, property } from 'lit/decorators.js';
import { wideboiAppStyles } from '../wideboi-app.styles';

export interface CommandItem {
  id: string;
  label: string;
  icon: string;
  shortcut: string;
  description?: string;
}

@customElement('wideboi-command-menu')
export class WideboiCommandMenu extends LitElement {
  static styles = [
    wideboiAppStyles,
    css`
      :host {
        display: contents;
      }
    `,
  ];

  @property({ type: Boolean }) open = false;
  @property({ type: String }) prefixLabel = 'Ctrl+B';
  @property({ type: String }) layoutMode: 'cards' | 'scroll' = 'cards';
  @property({ type: Boolean }) followPTY = false;

  private previousActiveElement: HTMLElement | null = null;

  public focusCloseButton() {
    this.renderRoot.querySelector<HTMLButtonElement>('.command-menu-header .close-btn')?.focus();
  }

  public focusFirstItem() {
    this.renderRoot.querySelector<HTMLButtonElement>('.command-menu-list .command-menu-item')?.focus();
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

    if (e.key === 'Tab') {
      const focusable = Array.from(
        this.renderRoot.querySelectorAll<HTMLButtonElement>('button')
      ).filter(el => !el.disabled);
      if (focusable.length === 0) return;

      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      const active = (this.shadowRoot?.activeElement || document.activeElement) as HTMLButtonElement | null;

      if (e.shiftKey) {
        if (!active || active === first || !focusable.includes(active)) {
          last.focus();
          e.preventDefault();
        }
      } else {
        if (!active || active === last || !focusable.includes(active)) {
          first.focus();
          e.preventDefault();
        }
      }
    }
  };

  private triggerCommand = (cmd: string) => {
    this.dispatchEvent(new CustomEvent('command', { detail: cmd, bubbles: true, composed: true }));
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

  override firstUpdated() {
    this.focusFirstItem();
  }

  override render() {
    if (!this.open) return html``;

    const commands: CommandItem[] = [
      { id: 'palette', label: 'Command Palette', icon: '🎯', shortcut: 'Space', description: 'Search & execute commands' },
      { id: 'prompt', label: 'Command Prompt', icon: '⌨️', shortcut: ':', description: 'Interactive command prompt' },
      { id: 'paste', label: 'Paste from Clipboard', icon: '📋', shortcut: 'v', description: 'Paste into focused pane' },
      { id: 'new-pane', label: 'New Pane', icon: '➕', shortcut: 'n', description: 'Open new terminal column' },
      { id: 'close-pane', label: 'Close Pane', icon: '✕', shortcut: 'x', description: 'Close focused terminal' },
      { id: 'search', label: 'Search Scrollback', icon: '🔍', shortcut: '/', description: 'Find in history' },
      { id: 'toggle-cards', label: this.layoutMode === 'cards' ? 'Switch to Scroll Mode' : 'Switch to Cards Mode', icon: '🗂️', shortcut: 'c', description: 'Toggle layout view' },
      { id: 'toggle-follow-pty', label: this.followPTY ? 'Unfollow PTY Width' : 'Follow PTY Width', icon: '📐', shortcut: 'f', description: 'Pane width tracking' },
      { id: 'settings', label: 'Settings', icon: '⚙', shortcut: ',', description: 'Preferences and fonts' },
      { id: 'help', label: 'Help', icon: '❓', shortcut: '?', description: 'Keys and help reference' },
    ];

    return html`
      <div class="command-menu-overlay" @click=${this.handleBackdropClick}>
        <div class="command-menu-dialog" role="dialog" aria-modal="true" aria-labelledby="command-menu-title" @click=${(e: Event) => e.stopPropagation()}>
          <div class="command-menu-header">
            <h3 id="command-menu-title">
              <span>Commands</span>
              <span class="command-menu-prefix-tip">Prefix: ${this.prefixLabel}</span>
            </h3>
            <button class="close-btn" @click=${this.handleClose} aria-label="Close command menu">✕</button>
          </div>
          <div class="command-menu-list" role="menu">
            ${commands.map(cmd => html`
              <button
                class="command-menu-item ${cmd.id === 'palette' ? 'primary-action' : ''}"
                role="menuitem"
                aria-label=${cmd.label}
                @click=${() => this.triggerCommand(cmd.id)}>
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
