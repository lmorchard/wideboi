import { LitElement, html, css } from 'lit';
import { customElement, property } from 'lit/decorators.js';

export type ContextMenuAction = 'copy' | 'paste' | 'select-all';

@customElement('wideboi-context-menu')
export class WideboiContextMenu extends LitElement {
  static styles = css`
    :host {
      display: contents;
    }
    .context-menu-backdrop {
      position: fixed;
      top: 0;
      left: 0;
      width: 100vw;
      height: 100vh;
      z-index: 9999;
      background: transparent;
    }
    .context-menu {
      position: fixed;
      z-index: 10000;
      min-width: 160px;
      background: var(--wb-bg-surface, #252526);
      border: 1px solid var(--wb-border-divider, #454545);
      border-radius: 4px;
      box-shadow: 0 4px 12px rgba(0, 0, 0, 0.4);
      padding: 4px;
      display: flex;
      flex-direction: column;
      gap: 2px;
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;
      font-size: 13px;
      user-select: none;
    }
    .context-menu-item {
      display: flex;
      align-items: center;
      justify-content: space-between;
      width: 100%;
      padding: 6px 10px;
      background: none;
      border: none;
      border-radius: 3px;
      color: var(--wb-fg-default, #cccccc);
      font-size: inherit;
      text-align: left;
      cursor: pointer;
      outline: none;
    }
    .context-menu-item:hover:not(:disabled),
    .context-menu-item:focus-visible:not(:disabled) {
      background: var(--wb-focus, #094771);
      color: #ffffff;
    }
    .context-menu-item:disabled {
      opacity: 0.4;
      cursor: not-allowed;
    }
    .context-menu-label {
      flex: 1;
    }
    .context-menu-shortcut {
      margin-left: 16px;
      font-size: 11px;
      opacity: 0.7;
    }
  `;

  @property({ type: Boolean }) open = false;
  @property({ type: Number }) x = 0;
  @property({ type: Number }) y = 0;
  @property({ type: Boolean }) hasSelection = false;
  @property({ type: Number }) paneId = 0;

  private handleKeyDown = (e: KeyboardEvent) => {
    if (!this.open) return;
    if (e.key === 'Escape') {
      this.close();
      e.stopPropagation();
      e.preventDefault();
      return;
    }
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      const items = Array.from(
        this.renderRoot.querySelectorAll<HTMLButtonElement>('.context-menu-item:not(:disabled)')
      );
      if (items.length === 0) return;
      const activeIdx = items.indexOf(this.shadowRoot?.activeElement as HTMLButtonElement);
      if (e.key === 'ArrowDown') {
        const next = activeIdx >= 0 && activeIdx < items.length - 1 ? items[activeIdx + 1] : items[0];
        next.focus();
      } else {
        const prev = activeIdx > 0 ? items[activeIdx - 1] : items[items.length - 1];
        prev.focus();
      }
      e.preventDefault();
    }
  };

  public close() {
    this.dispatchEvent(new CustomEvent('close', { bubbles: true, composed: true }));
  }

  private triggerAction(action: ContextMenuAction) {
    this.dispatchEvent(new CustomEvent('action', {
      detail: { action, paneId: this.paneId },
      bubbles: true,
      composed: true,
    }));
    this.close();
  }

  override connectedCallback() {
    super.connectedCallback();
    window.addEventListener('keydown', this.handleKeyDown);
  }

  override disconnectedCallback() {
    super.disconnectedCallback();
    window.removeEventListener('keydown', this.handleKeyDown);
  }

  override firstUpdated() {
    this.focusFirstEnabledItem();
  }

  override updated(changedProperties: Map<string, any>) {
    if (changedProperties.has('open') && this.open) {
      setTimeout(() => this.focusFirstEnabledItem(), 0);
    }
  }

  private focusFirstEnabledItem() {
    if (!this.open) return;
    this.renderRoot.querySelector<HTMLButtonElement>('.context-menu-item:not(:disabled)')?.focus();
  }

  override render() {
    if (!this.open) return html``;

    const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad|iPod/.test(navigator.platform);
    const copyShortcut = isMac ? '⌘C' : 'Ctrl+Shift+C';
    const pasteShortcut = isMac ? '⌘V' : 'Ctrl+V';

    const menuWidth = 180;
    const menuHeight = 110;
    const posX = typeof window !== 'undefined'
      ? Math.max(8, Math.min(window.innerWidth - menuWidth - 8, this.x))
      : this.x;
    const posY = typeof window !== 'undefined'
      ? Math.max(8, Math.min(window.innerHeight - menuHeight - 8, this.y))
      : this.y;

    return html`
      <div class="context-menu-backdrop" @pointerdown=${() => this.close()} @contextmenu=${(e: Event) => { e.preventDefault(); this.close(); }}>
        <div
          class="context-menu"
          style=${`left: ${posX}px; top: ${posY}px;`}
          role="menu"
          aria-label="Terminal context menu"
          @pointerdown=${(e: Event) => e.stopPropagation()}
          @contextmenu=${(e: Event) => e.preventDefault()}
        >
          <button
            class="context-menu-item"
            role="menuitem"
            ?disabled=${!this.hasSelection}
            @click=${() => this.triggerAction('copy')}
          >
            <span class="context-menu-label">Copy</span>
            <span class="context-menu-shortcut">${copyShortcut}</span>
          </button>
          <button
            class="context-menu-item"
            role="menuitem"
            @click=${() => this.triggerAction('paste')}
          >
            <span class="context-menu-label">Paste</span>
            <span class="context-menu-shortcut">${pasteShortcut}</span>
          </button>
          <button
            class="context-menu-item"
            role="menuitem"
            @click=${() => this.triggerAction('select-all')}
          >
            <span class="context-menu-label">Select All</span>
          </button>
        </div>
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'wideboi-context-menu': WideboiContextMenu;
  }
}
