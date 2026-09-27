import { LitElement, html, css } from 'lit';
import { customElement, property } from 'lit/decorators.js';
import { repeat } from 'lit/directives/repeat.js';
import { PaneStatus } from '../gen/internal/protocol/wirepb/wideboi_pb';
import { wideboiAppStyles } from '../wideboi-app.styles';

function statusGlyph(status: PaneStatus | undefined): string {
  switch (status) {
    case PaneStatus.WORKING: return '»';
    case PaneStatus.NEEDS_INPUT: return '!';
    case PaneStatus.DONE: return '✓';
    case PaneStatus.FAILED: return '✗';
    default: return '';
  }
}

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
  @property({ type: Object }) paneStatuses: Record<number, PaneStatus> = {};
  @property({ type: Object }) paneScrolls: Record<number, string> = {};
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

  render() {
    const currentIndex = this.activePanes.indexOf(this.focusedPaneId);
    return html`
      <div class="mobile-bar">
        <button class="pane-nav-btn prev-pane-btn" aria-label="Previous pane" ?disabled=${currentIndex <= 0}
          @click=${this.handlePrev}>‹</button>
        <select aria-label="Mobile pane" @change=${this.handleSelect}>
          ${repeat(this.activePanes, id => id, id => {
            const glyph = statusGlyph(this.paneStatuses[id]);
            const scroll = this.paneScrolls[id];
            const title = this.paneTitles[id] || 'Terminal';
            const label = `[${id}] ${glyph ? `${glyph} ` : ''}${title}${scroll ? ` [${scroll}]` : ''}`;
            return html`
              <option value=${id} .selected=${id === this.focusedPaneId}>${label}</option>
            `;
          })}
        </select>
        <div class="mobile-zoom">
          <button aria-label="Zoom out" ?disabled=${this.currentZoom <= this.currentMinZoom}
            @click=${() => this.handleZoom(-0.25)}>−</button>
          <button class="zoom-reset" aria-label="Reset zoom"
            @click=${this.handleZoomReset}>${Math.round(this.currentZoom * 100)}%</button>
          <button aria-label="Zoom in" ?disabled=${this.currentZoom >= 2.0}
            @click=${() => this.handleZoom(0.25)}>+</button>
        </div>
        <button class="pane-nav-btn next-pane-btn" aria-label="Next pane" ?disabled=${currentIndex >= this.activePanes.length - 1}
          @click=${this.handleNext}>›</button>
      </div>
    `;
  }
}
