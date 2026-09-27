import { LitElement, html, css } from 'lit';
import { customElement, property } from 'lit/decorators.js';
import { repeat } from 'lit/directives/repeat.js';
import { wideboiAppStyles } from '../wideboi-app.styles';

@customElement('wideboi-mobile-bar')
export class WideboiMobileBar extends LitElement {
  static styles = [
    wideboiAppStyles,
    css`
      :host {
        display: none;
      }
      @media (max-width: 480px) {
        :host {
          display: block;
        }
      }
    `,
  ];

  @property({ type: Array }) activePanes: number[] = [];
  @property({ type: Number }) focusedPaneId = 0;
  @property({ type: Object }) paneTitles: Record<number, string> = {};
  @property({ type: Number }) currentZoom = 1.0;
  @property({ type: Number }) currentMinZoom = 0.5;

  private handlePrev = () => {
    this.dispatchEvent(new CustomEvent('pane-move', { detail: -1, bubbles: true, composed: true }));
  };

  private handleNext = () => {
    this.dispatchEvent(new CustomEvent('pane-move', { detail: 1, bubbles: true, composed: true }));
  };

  private handleSelect = (e: Event) => {
    const id = Number((e.target as HTMLSelectElement).value);
    this.dispatchEvent(new CustomEvent('pane-select', { detail: id, bubbles: true, composed: true }));
  };

  private handleZoom = (delta: number) => {
    this.dispatchEvent(new CustomEvent('zoom-step', { detail: delta, bubbles: true, composed: true }));
  };

  private handleZoomReset = () => {
    this.dispatchEvent(new CustomEvent('zoom-reset', { bubbles: true, composed: true }));
  };

  private handleSettings = () => {
    this.dispatchEvent(new CustomEvent('open-settings', { bubbles: true, composed: true }));
  };

  private handleCommandMenu = () => {
    this.dispatchEvent(new CustomEvent('open-command-menu', { bubbles: true, composed: true }));
  };

  render() {
    const currentIndex = this.activePanes.indexOf(this.focusedPaneId);
    return html`
      <div class="mobile-bar">
        <button aria-label="Previous pane" ?disabled=${currentIndex <= 0}
          @click=${this.handlePrev}>‹</button>
        <select aria-label="Mobile pane" @change=${this.handleSelect}>
          ${repeat(this.activePanes, id => id, id => html`
            <option value=${id} .selected=${id === this.focusedPaneId}>[${id}] ${this.paneTitles[id] || 'Terminal'}</option>
          `)}
        </select>
        <div class="mobile-zoom">
          <button aria-label="Zoom out" ?disabled=${this.currentZoom <= this.currentMinZoom}
            @click=${() => this.handleZoom(-0.25)}>−</button>
          <button class="zoom-reset" aria-label="Reset zoom"
            @click=${this.handleZoomReset}>${Math.round(this.currentZoom * 100)}%</button>
          <button aria-label="Zoom in" ?disabled=${this.currentZoom >= 2.0}
            @click=${() => this.handleZoom(0.25)}>+</button>
        </div>
        <button aria-label="Next pane" ?disabled=${currentIndex >= this.activePanes.length - 1}
          @click=${this.handleNext}>›</button>
        <button class="mobile-cmd-btn" aria-label="Command menu" title="Command menu" @click=${this.handleCommandMenu}>⌘</button>
        <button class="mobile-settings-btn" aria-label="Settings" title="Settings" @click=${this.handleSettings}>⚙</button>
      </div>
    `;
  }
}
