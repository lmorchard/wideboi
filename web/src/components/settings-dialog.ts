import { LitElement, html, css } from 'lit';
import { customElement, property } from 'lit/decorators.js';
import { wideboiAppStyles } from '../wideboi-app.styles';
import { AVAILABLE_FONTS } from '../fonts';
import { listThemes } from '../themes';
import { getPref, setPref } from '../prefs';

@customElement('wideboi-settings')
export class WideboiSettings extends LitElement {
  static styles = [
    wideboiAppStyles,
    css`
      :host {
        display: contents;
      }
    `,
  ];

  @property({ type: Boolean }) open = false;
  @property({ type: String }) themeId = 'dark';
  @property({ type: String }) prefixSetting = 'ctrl+b';
  @property({ type: String }) layoutMode: 'cards' | 'scroll' = 'cards';
  @property({ type: Boolean }) cards = true;
  @property({ type: String }) fontFamily = 'monospace';
  @property({ type: Number }) fontSize = 14;
  @property({ type: String }) fontSpec = '14px monospace';

  focusCloseButton() {
    this.renderRoot.querySelector<HTMLButtonElement>('.settings-dialog .close-btn')?.focus();
  }

  private handleClose = () => {
    this.dispatchEvent(new CustomEvent('close', { bubbles: true, composed: true }));
  };

  private handleFontFamily = (e: Event) => {
    const val = (e.target as HTMLSelectElement).value;
    this.dispatchEvent(new CustomEvent('font-family-change', { detail: val, bubbles: true, composed: true }));
  };

  private handleFontSize = (e: Event) => {
    const val = Number((e.target as HTMLInputElement).value);
    this.dispatchEvent(new CustomEvent('font-size-change', { detail: val, bubbles: true, composed: true }));
  };

  private handleFontStep = (delta: number) => {
    this.dispatchEvent(new CustomEvent('font-size-step', { detail: delta, bubbles: true, composed: true }));
  };

  private handleFontReset = () => {
    this.dispatchEvent(new CustomEvent('font-size-reset', { bubbles: true, composed: true }));
  };

  private handleTheme = (e: Event) => {
    const val = (e.target as HTMLSelectElement).value;
    this.dispatchEvent(new CustomEvent('theme-change', { detail: val, bubbles: true, composed: true }));
  };

  private handlePrefix = (e: Event) => {
    const val = (e.target as HTMLSelectElement).value;
    this.dispatchEvent(new CustomEvent('prefix-change', { detail: val, bubbles: true, composed: true }));
  };

  private handleLayout = (e: Event) => {
    const val = (e.target as HTMLSelectElement).value;
    this.dispatchEvent(new CustomEvent('layout-change', { detail: val, bubbles: true, composed: true }));
  };

  private handleToggleNotifications = async () => {
    const current = getPref('notifications');
    if (!current) {
      if (typeof Notification !== 'undefined' && Notification.requestPermission && Notification.permission !== 'granted') {
        await Notification.requestPermission();
      }
      setPref('notifications', true);
    } else {
      setPref('notifications', false);
    }
    this.requestUpdate();
  };

  render() {
    return html`
      <div class="settings-overlay" @click=${this.handleClose}>
        <div class="settings-dialog" role="dialog" aria-modal="true" aria-labelledby="settings-title" @click=${(e: Event) => e.stopPropagation()}>
          <h3 id="settings-title">
            <span>wideboi Settings</span>
            <button class="close-btn" @click=${this.handleClose} aria-label="Close settings">×</button>
          </h3>

          <div class="settings-section">
            <h4>Terminal Font</h4>
            <div class="settings-row">
              <label for="settings-font-family">Family</label>
              <select id="settings-font-family" @change=${this.handleFontFamily}>
                ${AVAILABLE_FONTS.map(f => html`
                  <option value=${f.id} .selected=${this.fontFamily === f.id}>${f.name}</option>
                `)}
              </select>
            </div>
            <div class="settings-row">
              <label for="settings-font-size">Size (px)</label>
              <div class="font-size-controls">
                <button type="button" aria-label="Decrease font size" @click=${() => this.handleFontStep(-1)}>−</button>
                <input id="settings-font-size" type="number" min="8" max="48"
                  .value=${this.fontSize.toString()}
                  @change=${this.handleFontSize} />
                <button type="button" aria-label="Increase font size" @click=${() => this.handleFontStep(1)}>+</button>
                <button type="button" @click=${this.handleFontReset}>Reset</button>
              </div>
            </div>
            <div class="settings-preview" style="font: ${this.fontSpec};">
              <div class="preview-line">0123456789 ABCDEFGHIJKLMNOPQRSTUVWXYZ abcdefghijklmnopqrstuvwxyz</div>
              <div class="preview-line symbols">⚡    󰘧  󰊤      󰌠  (Nerd Font Icons)</div>
            </div>
          </div>

          <div class="settings-section">
            <h4>Preferences</h4>
            <div class="settings-row">
              <label for="settings-theme">Color Theme</label>
              <select id="settings-theme" @change=${this.handleTheme}>
                ${listThemes().map(t => html`
                  <option value=${t.id} .selected=${t.id === this.themeId}>${t.name}</option>
                `)}
              </select>
            </div>
            <div class="settings-row">
              <label for="settings-prefix-key">Prefix Key</label>
              <select id="settings-prefix-key" @change=${this.handlePrefix}>
                <option value="ctrl+b" .selected=${this.prefixSetting === 'ctrl+b'}>Ctrl+B</option>
                <option value="ctrl+a" .selected=${this.prefixSetting === 'ctrl+a'}>Ctrl+A</option>
                <option value="ctrl+space" .selected=${this.prefixSetting === 'ctrl+space'}>Ctrl+Space</option>
              </select>
            </div>
            <div class="settings-row">
              <label for="settings-layout-mode">Layout Mode</label>
              <select id="settings-layout-mode" @change=${this.handleLayout}>
                <option value="cards" .selected=${this.cards}>Cards</option>
                <option value="scroll" .selected=${!this.cards}>Scroll</option>
              </select>
            </div>
            <div class="settings-row">
              <label for="settings-notifications">Desktop Notifications</label>
              <button id="settings-notifications" type="button" @click=${this.handleToggleNotifications}>
                ${!getPref('notifications')
                  ? 'Disabled (Click to Enable)'
                  : typeof Notification !== 'undefined' && Notification.permission === 'granted'
                    ? 'Enabled (Click to Disable)'
                    : 'Enable Notifications'}
              </button>
            </div>
          </div>
        </div>
      </div>
    `;
  }
}
