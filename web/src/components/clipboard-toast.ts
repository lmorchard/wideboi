import { LitElement, html, css } from 'lit';
import { customElement, property } from 'lit/decorators.js';

export const PREVIEW_LINES = 3;

/** First PREVIEW_LINES lines of text, with an ellipsis line if there were more. */
export function clipboardPreview(text: string): string {
  const lines = text.split('\n');
  const head = lines.slice(0, PREVIEW_LINES);
  return lines.length > PREVIEW_LINES ? [...head, '…'].join('\n') : head.join('\n');
}

/**
 * Asks before a pane's clipboard write (OSC 52) reaches the system
 * clipboard. Browsers only allow clipboard writes inside a user gesture,
 * and this one arrives over the socket, so the Copy click is that
 * gesture -- and the preview is the human check against a pane quietly
 * replacing what you meant to paste.
 *
 * It never takes focus: typing keeps going to the pane underneath.
 * Emits `copy` and `dismiss`.
 */
@customElement('wideboi-clipboard-toast')
export class WideboiClipboardToast extends LitElement {
  static styles = css`
    :host {
      position: fixed;
      right: 16px;
      bottom: 16px;
      z-index: 9000;
    }
    .toast {
      max-width: 44ch;
      padding: 10px 12px;
      background: var(--wb-bg-toolbar, #252526);
      color: var(--wb-fg-primary, #cccccc);
      border: 1px solid var(--wb-border, #454545);
      border-radius: 6px;
      box-shadow: 0 4px 12px rgba(0, 0, 0, 0.4);
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;
      font-size: 13px;
    }
    .count {
      color: var(--wb-fg-muted, #999999);
      font-size: 12px;
      margin-top: 2px;
    }
    .preview {
      margin: 8px 0;
      padding: 6px 8px;
      max-height: 5em;
      overflow: hidden;
      white-space: pre;
      text-overflow: ellipsis;
      background: var(--wb-bg-input, #1e1e1e);
      border: 1px solid var(--wb-border-divider, #454545);
      border-radius: 4px;
      font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
      font-size: 12px;
    }
    .actions {
      display: flex;
      justify-content: flex-end;
      gap: 8px;
    }
    button {
      padding: 4px 12px;
      border: 1px solid var(--wb-border, #454545);
      border-radius: 4px;
      background: var(--wb-bg-btn, #3c3c3c);
      color: var(--wb-fg-primary, #cccccc);
      font-size: inherit;
      cursor: pointer;
    }
    button:hover {
      background: var(--wb-bg-btn-hover, #505050);
    }
    button.copy {
      background: var(--wb-focus, #094771);
      color: #ffffff;
    }
  `;

  @property({ type: Number }) paneId = 0;
  @property({ type: String }) paneTitle = '';
  @property({ type: String }) text = '';

  render() {
    const count = [...this.text].length.toLocaleString();
    return html`
      <div class="toast" role="status" aria-live="polite">
        <div class="title">📋 ${this.paneTitle} (pane ${this.paneId}) wants to copy</div>
        <div class="count">${count} characters</div>
        <pre class="preview">${clipboardPreview(this.text)}</pre>
        <div class="actions">
          <button type="button" class="dismiss" @click=${() => this.emit('dismiss')}>Dismiss</button>
          <button type="button" class="copy" @click=${() => this.emit('copy')}>Copy</button>
        </div>
      </div>
    `;
  }

  private emit(name: 'copy' | 'dismiss') {
    this.dispatchEvent(new CustomEvent(name, { bubbles: true, composed: true }));
  }
}
