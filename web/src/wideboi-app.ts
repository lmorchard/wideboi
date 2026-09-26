import { LitElement, html, css } from 'lit';
import { customElement, query, state } from 'lit/decorators.js';
import { repeat } from 'lit/directives/repeat.js';
import { WideboiClient } from './client';
import { termSettings, measureCellWidth, PaneStore, selectionText, type CellPoint } from './pane-state';
import { reconcileFocus } from './focus';
import { WideboiPane } from './wideboi-pane';
import { cardLayout } from './card-layout';
import { sendKeyboardInput, sendTextInput } from './input';
import { KeyRouter } from './key-router';
import { consumeLinkToken } from './token';
import { RenderStats, formatSummary, statsEnabled } from './stats';
import { MouseKind, MsgHistorySnapshot, MsgPaneMetadata, PaneStatus, VerbType, type ColumnData } from './gen/internal/protocol/wirepb/wideboi_pb';
import { createSearchSession, applySnapshot, cancelSearch, liveSearch, formatSearchStatus, type SearchState } from './search';
import { executeMacro, macroEndsWithEnter, DEFAULT_MACROS, type Macro, type MacroStep } from './macros';
import { AVAILABLE_FONTS } from './fonts';
import { getTheme, listThemes, getThemeCSSVariables, type Theme } from './themes';

const linkToken = typeof window !== 'undefined' ? consumeLinkToken(window.location, window.history) : '';
const STATS_REPORT_MS = 5000;
const NARROW_VIEW = '(max-width: 480px)';

@customElement('wideboi-app')
export class WideboiApp extends LitElement {
  static styles = css`
    :host {
      display: flex;
      flex-direction: column;
      z-index: 20;
      width: 100vw;
      height: var(--app-height, 100vh);
      overflow: hidden;
      background: var(--wb-bg-app, #1e1e1e);
      color: var(--wb-fg-primary, #ccc);
      position: relative;
    }
    .terminal-shell {
      display: flex;
      flex: 1;
      flex-direction: column;
      min-height: 0;
      min-width: 0;
      font: 14px monospace;
      color: var(--wb-fg-primary, #ccc);
      position: relative;
    }
    .title, .status {
      height: 16.8px;
      line-height: 16.8px;
      flex: none;
      overflow: hidden;
      white-space: nowrap;
      text-overflow: ellipsis;
      color: var(--wb-fg-primary, #ccc);
    }
    .status.search-bar {
      position: absolute;
      bottom: 0;
      left: 0;
      right: 0;
      height: 22px;
      line-height: 22px;
      display: flex;
      align-items: center;
      gap: 0.4rem;
      padding: 0 0.5rem;
      background: var(--wb-bg-toolbar, #252526);
      border-top: 1px solid var(--wb-border, #3c3c3c);
      overflow: hidden;
      font: 12px monospace;
      color: var(--wb-fg-primary, #ccc);
      z-index: 15;
    }
    .status.search-bar input.search-input {
      background: var(--wb-bg-input, #1e1e1e);
      border: 1px solid var(--wb-border-divider, #555);
      color: var(--wb-fg-primary, #ccc);
      padding: 1px 4px;
      font: 12px monospace;
      border-radius: 2px;
      outline: none;
      height: 18px;
      box-sizing: border-box;
      width: 140px;
    }
    .status.search-bar input.search-input:focus {
      border-color: var(--wb-focus, #007fd4);
    }
    .status.search-bar button.search-btn {
      background: var(--wb-bg-btn, #3c3c3c);
      color: var(--wb-fg-primary, #ccc);
      border: 1px solid var(--wb-border-divider, #555);
      padding: 0 5px;
      height: 18px;
      line-height: 16px;
      font-size: 11px;
      border-radius: 2px;
      cursor: pointer;
    }
    .status.search-bar button.search-btn:hover:not(:disabled) {
      background: var(--wb-bg-btn-hover, #4c4c4c);
      color: #fff;
    }
    .status.search-bar button.search-btn:disabled {
      opacity: 0.5;
      cursor: default;
    }
    .status.search-bar .search-msg {
      margin-left: 0.4rem;
      color: var(--wb-fg-muted, #aaa);
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .pane-strip {
      display: flex;
      flex: 1;
      min-height: 0;
      min-width: 0;
      overflow-x: auto;
      overflow-y: hidden;
      scrollbar-width: thin;
      scrollbar-gutter: stable;
      overscroll-behavior-x: contain;
      background: var(--wb-bg-pane, #1e1e1e);
    }
    .pane-strip.cards {
      position: relative;
      overflow: hidden;
    }
    .pane-strip.cards wideboi-pane { position: absolute; top: 0; }
    .card-count {
      position: absolute;
      bottom: 0;
      z-index: 1000;
      padding: 2px 5px;
      background: var(--wb-bg-toolbar, #252526);
      color: var(--wb-fg-primary, #ccc);
      pointer-events: none;
    }
    .card-count.right { right: 0; }

    .toolbar {
      background: var(--wb-bg-toolbar, #252526);
      border-top: 1px solid var(--wb-border, #3c3c3c);
      padding: 0.4rem 0.6rem;
      display: flex;
      flex-wrap: wrap;
      gap: 0.4rem 0.5rem;
      align-items: center;
      white-space: nowrap;
      flex: none;
      z-index: 5;
      font-size: 13px;
    }
    .toolbar select {
      background: var(--wb-bg-btn, #3c3c3c);
      color: var(--wb-fg-primary, #cccccc);
      border: 1px solid var(--wb-border-divider, #555);
      padding: 0.25rem;
      border-radius: 3px;
      outline: none;
    }
    .toolbar input {
      background: var(--wb-bg-input, #1e1e1e);
      color: var(--wb-fg-primary, #cccccc);
      border: 1px solid var(--wb-border-divider, #555);
      padding: 0.25rem;
      border-radius: 3px;
      outline: none;
    }
    .claim-size-btn {
      background: var(--wb-bg-btn, #3c3c3c);
      color: var(--wb-fg-primary, #cccccc);
      border: 1px solid var(--wb-border-divider, #555);
      padding: 0.3rem 0.6rem;
      border-radius: 3px;
      cursor: pointer;
      font-size: 13px;
    }
    .claim-size-btn:hover {
      background: var(--wb-bg-btn-hover, #4c4c4c);
      color: #ffffff;
    }
    .toolbar label {
      color: var(--wb-fg-muted, #aaa);
      white-space: nowrap;
    }
    .toolbar .tip {
      color: var(--wb-fg-muted, #666);
      margin-left: auto;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    @media (max-width: 650px) {
      .toolbar .tip { display: none; }
    }
    .mobile-bar, .mobile-dock { display: none; }
    .mobile-bar {
      align-items: center;
      gap: 0.4rem;
      padding: 0.35rem;
      background: var(--wb-bg-toolbar, #252526);
      color: var(--wb-fg-primary, #ccc);
      font: 13px sans-serif;
      border-top: 1px solid var(--wb-border, #3c3c3c);
    }
    .mobile-bar select { flex: 1; min-width: 0; }
    .mobile-bar button {
      min-width: 52px;
      font-size: 20px;
      line-height: 1;
      padding: 0 0.8rem;
    }
    .mobile-bar button, .mobile-dock button, .mobile-bar select {
      min-height: 40px;
      border: 1px solid var(--wb-border-divider, #555);
      border-radius: 4px;
      background: var(--wb-bg-btn, #3c3c3c);
      color: var(--wb-fg-primary, #eee);
      font-size: 14px;
    }
    .mobile-theme-select {
      background: var(--wb-bg-btn, #3c3c3c);
      color: var(--wb-fg-primary, #eee);
      border: 1px solid var(--wb-border-divider, #555);
      border-radius: 4px;
      padding: 0.2rem 0.4rem;
      font-size: 12px;
    }
    .mobile-bar button:disabled, .mobile-dock button:disabled { opacity: 0.4; }
    .mobile-zoom {
      display: flex;
      align-items: center;
      gap: 0.2rem;
    }
    .mobile-zoom button {
      min-width: 32px;
      padding: 0 0.35rem;
    }
    .mobile-zoom .zoom-reset {
      min-width: 48px;
      font-size: 12px;
    }
    .mobile-dock {
      flex-direction: column;
      gap: 0.3rem;
      padding: 0.35rem;
      padding-bottom: max(0.35rem, env(safe-area-inset-bottom));
      background: var(--wb-bg-toolbar, #252526);
      border-top: 1px solid var(--wb-border, #3c3c3c);
    }
    .mobile-input-bar {
      display: flex;
      gap: 0.3rem;
      align-items: center;
      min-height: 40px;
    }
    .mobile-mode-toggle {
      display: inline-flex;
      border-radius: 4px;
      overflow: hidden;
      border: 1px solid var(--wb-border-divider, #555);
      flex-shrink: 0;
      height: 40px;
      box-sizing: border-box;
    }
    .mobile-mode-toggle button {
      border: none;
      border-radius: 0;
      padding: 0 0.55rem;
      background: var(--wb-bg-btn, #333);
      color: var(--wb-fg-muted, #aaa);
      font-size: 13px;
      cursor: pointer;
      height: 100%;
      display: flex;
      align-items: center;
      justify-content: center;
    }
    .mobile-mode-toggle button.active, .mobile-mode-toggle button[aria-pressed="true"] {
      background: var(--wb-focus, #0e639c);
      color: #fff;
    }
    .mobile-draft-input, .mobile-direct-input {
      flex: 1;
      min-width: 0;
      height: 40px;
      line-height: 38px;
      box-sizing: border-box;
      padding: 0 0.6rem;
      border-radius: 4px;
      font: 14px sans-serif;
      vertical-align: middle;
    }
    .mobile-draft-input {
      border: 1px solid var(--wb-border-divider, #555);
      background: var(--wb-bg-input, #333);
      color: var(--wb-fg-primary, #eee);
    }
    .mobile-direct-input {
      border: 1px solid var(--wb-focus, #0e639c);
      background: var(--wb-bg-input, #1e1e1e);
      color: var(--wb-fg-primary, #eee);
    }
    .mobile-send-btn {
      flex-shrink: 0;
      min-width: 50px;
      height: 40px;
      padding: 0 0.6rem;
      border: 1px solid var(--wb-border-divider, #555);
      border-radius: 4px;
      background: var(--wb-bg-btn, #333);
      color: var(--wb-fg-primary, #eee);
      cursor: pointer;
      font-size: 13px;
      display: flex;
      align-items: center;
      justify-content: center;
      box-sizing: border-box;
    }
    .mobile-macros-btn {
      flex-shrink: 0;
      min-width: 56px;
      height: 40px;
      border: 1px solid var(--wb-border-divider, #555);
      border-radius: 4px;
      padding: 0 0.5rem;
      background: var(--wb-bg-btn, #333);
      color: var(--wb-fg-primary, #eee);
      font-size: 13px;
      cursor: pointer;
      display: flex;
      align-items: center;
      justify-content: center;
      box-sizing: border-box;
    }
    .mobile-macros-btn.active, .mobile-macros-btn[aria-expanded="true"] {
      background: var(--wb-focus, #0e639c);
      color: #fff;
    }
    .mobile-macros-sheet {
      position: absolute;
      bottom: 0;
      left: 0;
      right: 0;
      background: var(--wb-bg-toolbar, #252526);
      border-top: 2px solid var(--wb-focus, #0e639c);
      box-shadow: 0 -4px 16px rgba(0, 0, 0, 0.5);
      z-index: 30;
      display: flex;
      flex-direction: column;
      max-height: 70vh;
      overflow-y: auto;
      padding: 0.5rem;
      gap: 0.5rem;
    }
    .mobile-keys-section {
      display: flex;
      flex-direction: column;
      gap: 0.3rem;
      padding-bottom: 0.4rem;
      border-bottom: 1px solid var(--wb-border, #3c3c3c);
    }
    .mobile-macros-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      padding-bottom: 0.3rem;
      border-bottom: 1px solid var(--wb-border, #3c3c3c);
    }
    .mobile-macros-header span {
      font-weight: bold;
      font-size: 14px;
      color: var(--wb-fg-primary, #fff);
    }
    .mobile-macros-header .sheet-actions {
      display: flex;
      gap: 0.5rem;
      align-items: center;
    }
    .mobile-macros-header button {
      min-height: 40px;
      min-width: 44px;
      padding: 0 0.8rem;
      font-size: 14px;
      font-weight: 500;
      background: var(--wb-bg-btn, #3c3c3c);
      color: var(--wb-fg-primary, #eee);
      border: 1px solid var(--wb-border-divider, #555);
      border-radius: 4px;
      cursor: pointer;
      display: inline-flex;
      align-items: center;
      justify-content: center;
    }
    .mobile-macros-header button:active {
      background: var(--wb-bg-btn-hover, #4c4c4c);
    }
    .mobile-macros-header button.close-btn {
      font-size: 16px;
      padding: 0 0.9rem;
    }
    .mobile-macros-grid {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 0.4rem;
    }
    .mobile-macro-item {
      display: flex;
      justify-content: space-between;
      align-items: center;
      padding: 0.5rem 0.6rem;
      background: var(--wb-bg-btn, #333);
      border: 1px solid var(--wb-border, #444);
      border-radius: 4px;
      color: var(--wb-fg-primary, #eee);
      font-size: 13px;
      cursor: pointer;
      text-align: left;
    }
    .mobile-macro-item:active {
      background: var(--wb-focus, #0e639c);
    }
    .mobile-macro-name {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .mobile-macro-enter {
      font-size: 11px;
      background: var(--wb-bg-input, #222);
      padding: 0.1rem 0.3rem;
      border-radius: 3px;
      color: var(--wb-focus, #79c0ff);
      margin-left: 0.3rem;
      flex-shrink: 0;
    }
    .macro-editor-modal {
      position: absolute;
      top: 0; left: 0; right: 0; bottom: 0;
      background: rgba(0, 0, 0, 0.7);
      display: flex;
      align-items: center;
      justify-content: center;
      z-index: 50;
      padding: 1rem;
    }
    .macro-editor-dialog {
      background: var(--wb-bg-toolbar, #252526);
      border: 1px solid var(--wb-border, #3c3c3c);
      border-radius: 6px;
      width: 100%;
      max-width: 440px;
      max-height: 85vh;
      overflow-y: auto;
      display: flex;
      flex-direction: column;
      gap: 0.6rem;
      padding: 1rem;
      color: var(--wb-fg-primary, #eee);
    }
    .macro-editor-list {
      display: flex;
      flex-direction: column;
      gap: 0.3rem;
      max-height: 35vh;
      overflow-y: auto;
    }
    .macro-editor-row {
      display: flex;
      justify-content: space-between;
      align-items: center;
      background: var(--wb-bg-btn, #333);
      padding: 0.4rem 0.6rem;
      border-radius: 4px;
      font-size: 13px;
    }
    .macro-editor-row button {
      background: #552222;
      color: #ff9999;
      border: 1px solid #883333;
      border-radius: 3px;
      padding: 0.2rem 0.4rem;
      cursor: pointer;
    }
    .macro-editor-form {
      display: flex;
      flex-direction: column;
      gap: 0.4rem;
      background: var(--wb-bg-pane, #1e1e1e);
      padding: 0.6rem;
      border-radius: 4px;
      border: 1px solid var(--wb-border, #3c3c3c);
    }
    .macro-editor-form input, .macro-editor-form select {
      background: var(--wb-bg-input, #2d2d2d);
      border: 1px solid var(--wb-border-divider, #444);
      border-radius: 3px;
      color: var(--wb-fg-primary, #eee);
      padding: 0.3rem 0.5rem;
      font-size: 13px;
    }
    .macro-draft-steps {
      display: flex;
      flex-direction: column;
      gap: 0.2rem;
      background: var(--wb-bg-toolbar, #252526);
      padding: 0.3rem 0.5rem;
      border-radius: 4px;
      border: 1px dashed var(--wb-border-divider, #555);
    }
    .macro-draft-step-row {
      display: flex;
      justify-content: space-between;
      align-items: center;
      font-size: 12px;
      color: var(--wb-focus, #79c0ff);
    }
    .macro-draft-step-row button {
      background: transparent;
      border: none;
      color: #ff9999;
      cursor: pointer;
      font-size: 11px;
    }
    .macro-editor-actions {
      display: flex;
      justify-content: flex-end;
      gap: 0.4rem;
    }
    .macro-editor-actions button {
      padding: 0.3rem 0.7rem;
      border-radius: 4px;
      border: 1px solid var(--wb-border-divider, #555);
      background: var(--wb-bg-btn, #3c3c3c);
      color: var(--wb-fg-primary, #eee);
      cursor: pointer;
    }
    .macro-editor-actions button.primary {
      background: var(--wb-focus, #0e639c);
      border-color: var(--wb-focus, #1177bb);
      color: #fff;
    }
    .mobile-keys { display: grid; grid-template-columns: repeat(6, minmax(0, 1fr)); gap: 0.3rem; }
    .mobile-keys button { min-width: 0; padding: 0; }
    .mobile-keys button[aria-pressed="true"] { background: var(--wb-focus, #0e639c); }
    .mobile-ctrl-palette {
      display: grid;
      grid-template-columns: repeat(5, minmax(0, 1fr));
      gap: 0.3rem;
      background: var(--wb-bg-input, #1b2e3e);
      padding: 0.25rem;
      border-radius: 4px;
      border: 1px solid var(--wb-focus, #0e639c);
    }
    .mobile-ctrl-palette button {
      min-width: 0;
      padding: 0.25rem 0;
      font-size: 13px;
      font-weight: bold;
      background: var(--wb-bg-btn, #23435c);
      color: var(--wb-focus, #79c0ff);
    }
    .pane-strip.mobile { overflow: hidden; }
    .pane-strip.mobile wideboi-pane { width: 100% !important; border: 0; box-shadow: none !important; }
    .pane-strip.mobile wideboi-pane::after { box-shadow: none !important; }
    .pane-strip.mobile wideboi-pane:not([focused]) { display: none; }
    @media (max-width: 480px) {
      .toolbar { display: none; }
      .mobile-bar { display: flex; }
      .mobile-dock { display: flex; }
      .title { display: none; }
    }
    .overlay {
      position: absolute;
      top: 0; left: 0; right: 0; bottom: 0;
      background: rgba(0, 0, 0, 0.6);
      display: flex;
      flex-direction: column;
      z-index: 20;
      align-items: center;
      justify-content: center;
      color: var(--wb-fg-primary, #cccccc);
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
    }
    .connection-box {
      background: var(--wb-bg-toolbar, #252526);
      padding: 2rem;
      border-radius: 6px;
      box-shadow: 0 4px 12px rgba(0,0,0,0.5);
      border: 1px solid var(--wb-border, #3c3c3c);
      display: flex;
      flex-direction: column;
      z-index: 20;
      gap: 1rem;
      width: 320px;
      color: var(--wb-fg-primary, #cccccc);
    }
    .connection-box h2 {
      margin: 0;
      font-size: 1.1rem;
      font-weight: 500;
      color: var(--wb-fg-primary, #ffffff);
    }
    input {
      background: var(--wb-bg-input, #3c3c3c);
      border: 1px solid var(--wb-border-divider, #3c3c3c);
      color: var(--wb-fg-primary, #cccccc);
      padding: 0.6rem;
      font-size: 1rem;
      border-radius: 3px;
      outline: none;
      transition: border-color 0.2s;
    }
    input:focus {
      border: 1px solid var(--wb-focus, #007fd4);
    }
    button {
      background: var(--wb-focus, #0e639c);
      color: white;
      border: none;
      padding: 0.6rem;
      font-size: 1rem;
      cursor: pointer;
      border-radius: 3px;
      transition: background 0.2s;
    }
    button:hover {
      opacity: 0.9;
    }
    .stats-overlay {
      position: fixed;
      right: 0.5rem;
      bottom: 1.5rem;
      margin: 0;
      padding: 0.4rem 0.6rem;
      background: var(--wb-bg-toolbar, rgba(37, 37, 38, 0.9));
      border: 1px solid var(--wb-border, #3c3c3c);
      color: var(--wb-fg-primary, #cccccc);
      font: 11px monospace;
      z-index: 30;
      pointer-events: none;
    }
    .help-overlay {
      position: absolute;
      top: 0; left: 0; right: 0; bottom: 0;
      background: rgba(0, 0, 0, 0.6);
      display: flex;
      align-items: center;
      justify-content: center;
      z-index: 25;
      color: var(--wb-fg-primary, #cccccc);
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, monospace;
    }
    .help-dialog {
      background: var(--wb-bg-toolbar, #252526);
      border: 1px solid var(--wb-border, #454545);
      border-radius: 6px;
      padding: 1.5rem;
      max-width: 580px;
      width: 90%;
      max-height: 85vh;
      overflow-y: auto;
      box-shadow: 0 8px 24px rgba(0,0,0,0.6);
      color: var(--wb-fg-primary, #ccc);
    }
    .help-dialog h3 {
      margin: 0 0 1rem;
      font-size: 1.1rem;
      font-weight: 600;
      display: flex;
      justify-content: space-between;
      align-items: center;
      border-bottom: 1px solid var(--wb-border, #3c3c3c);
      padding-bottom: 0.5rem;
      color: var(--wb-fg-primary, #fff);
    }
    .help-dialog .close-btn {
      background: transparent;
      border: none;
      color: var(--wb-fg-muted, #999);
      font-size: 1.2rem;
      cursor: pointer;
      padding: 0 0.5rem;
    }
    .help-dialog .close-btn:hover {
      color: var(--wb-fg-primary, #fff);
    }
    .help-table {
      width: 100%;
      border-collapse: collapse;
      font-size: 13px;
      margin-bottom: 1rem;
    }
    .help-table th, .help-table td {
      padding: 4px 8px;
      text-align: left;
    }
    .help-table th {
      color: var(--wb-fg-muted, #888);
      font-weight: normal;
      border-bottom: 1px solid var(--wb-border, #3c3c3c);
    }
    .help-table kbd {
      background: var(--wb-bg-btn, #333);
      border: 1px solid var(--wb-border-divider, #555);
      border-radius: 3px;
      padding: 1px 5px;
      font-family: monospace;
      color: var(--wb-fg-primary, #eee);
    }
    .toolbar .help-btn {
      background: var(--wb-bg-btn, #333);
      color: var(--wb-fg-primary, #ccc);
      border: 1px solid var(--wb-border-divider, #555);
      padding: 0.25rem 0.6rem;
      font-size: 12px;
      border-radius: 3px;
      cursor: pointer;
    }
    .toolbar .help-btn:hover {
      background: var(--wb-bg-btn-hover, #444);
      color: #fff;
    }
    .toolbar .settings-btn {
      background: #333;
      color: #ccc;
      border: 1px solid #555;
      padding: 0.25rem 0.6rem;
      font-size: 12px;
      border-radius: 3px;
      cursor: pointer;
    }
    .toolbar .settings-btn:hover {
      background: #444;
      color: #fff;
    }
    .mobile-settings-btn {
      background: #252526;
      border: 1px solid #3c3c3c;
      color: #ccc;
      border-radius: 4px;
      padding: 0.25rem 0.6rem;
      font-size: 14px;
      cursor: pointer;
      display: flex;
      align-items: center;
      justify-content: center;
    }
    .mobile-settings-btn:hover {
      background: var(--wb-bg-btn-hover, #333);
      color: #fff;
    }
    .settings-overlay {
      position: absolute;
      top: 0; left: 0; right: 0; bottom: 0;
      background: rgba(0, 0, 0, 0.6);
      display: flex;
      align-items: center;
      justify-content: center;
      z-index: 25;
      color: var(--wb-fg-primary, #cccccc);
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, monospace;
    }
    .settings-dialog {
      background: var(--wb-bg-toolbar, #252526);
      border: 1px solid var(--wb-border, #454545);
      border-radius: 6px;
      padding: 1.5rem;
      max-width: 580px;
      width: 90%;
      max-height: 85vh;
      overflow-y: auto;
      box-shadow: 0 8px 24px rgba(0,0,0,0.6);
      color: var(--wb-fg-primary, #ccc);
    }
    .settings-dialog h3 {
      margin: 0 0 1rem;
      font-size: 1.1rem;
      font-weight: 600;
      display: flex;
      justify-content: space-between;
      align-items: center;
      border-bottom: 1px solid var(--wb-border, #3c3c3c);
      padding-bottom: 0.5rem;
      color: var(--wb-fg-primary, #fff);
    }
    .settings-dialog .close-btn {
      background: transparent;
      border: none;
      color: var(--wb-fg-muted, #999);
      font-size: 1.2rem;
      cursor: pointer;
      padding: 0 0.5rem;
    }
    .settings-dialog .close-btn:hover {
      color: var(--wb-fg-primary, #fff);
    }
    .settings-section {
      margin-bottom: 1.25rem;
      padding: 0.75rem 1rem;
      background: var(--wb-bg-pane, #202020);
      border-radius: 4px;
      border: 1px solid var(--wb-border, #383838);
    }
    .settings-section h4 {
      margin: 0 0 0.75rem 0;
      font-size: 0.85rem;
      text-transform: uppercase;
      letter-spacing: 0.05em;
      color: var(--wb-fg-muted, #aaa);
    }
    .settings-row {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 0.75rem;
      font-size: 13px;
    }
    .settings-row:last-child {
      margin-bottom: 0;
    }
    .settings-row label {
      color: var(--wb-fg-primary, #bbb);
    }
    .settings-row select, .settings-row input[type="number"] {
      background: var(--wb-bg-input, #1e1e1e);
      color: var(--wb-fg-primary, #eee);
      border: 1px solid var(--wb-border-divider, #555);
      border-radius: 3px;
      padding: 0.25rem 0.5rem;
      font-size: 13px;
    }
    .font-size-controls {
      display: flex;
      align-items: center;
      gap: 0.25rem;
    }
    .font-size-controls button {
      background: var(--wb-bg-btn, #333);
      color: var(--wb-fg-primary, #eee);
      border: 1px solid var(--wb-border-divider, #555);
      border-radius: 3px;
      padding: 0.2rem 0.5rem;
      cursor: pointer;
      font-size: 13px;
    }
    .font-size-controls button:hover {
      background: var(--wb-bg-btn-hover, #444);
    }
    .font-size-controls input {
      width: 50px;
      text-align: center;
    }
    .settings-preview {
      margin-top: 0.75rem;
      padding: 0.75rem;
      background: var(--wb-bg-app, #141414);
      border: 1px solid var(--wb-border, #2a2a2a);
      border-radius: 4px;
      color: var(--wb-focus, #4ec9b0);
      overflow-x: auto;
      white-space: pre;
      line-height: 1.4;
    }
    .settings-preview .symbols {
      color: var(--wb-fg-primary, #ce9178);
      margin-top: 0.25rem;
    }
    .error {
      color: #f14c4c;
      font-size: 0.9rem;
      margin-top: -0.5rem;
    }
  `;

  @query('.pane-strip')
  private paneStrip!: HTMLElement;

  private client: WideboiClient | null = null;
  // Present only with ?stats=1; everything downstream treats undefined as off.
  private readonly stats = (typeof window !== 'undefined' && statsEnabled(window.location.search)) ? new RenderStats() : undefined;
  private panes = new PaneStore(this.stats);
  private resizeObserver: ResizeObserver;
  private readonly narrowMedia = (typeof window !== 'undefined' && window.matchMedia) ? window.matchMedia(NARROW_VIEW) : { matches: false } as MediaQueryList;
  private cellWidth = 1;
  private statsTimer?: ReturnType<typeof setInterval>;

  @state()
  private statsText = '';

  @state()
  private connected = false;

  @state()
  private activePanes: number[] = [];

  @state()
  private columns: ColumnData[] = [];

  @state()
  private paneTitles: Record<number, string> = {};

  @state()
  private paneMetadata: Record<number, MsgPaneMetadata> = {};

  @state()
  private focusedPaneId = 0;

  @state()
  private wsUrl = typeof window !== 'undefined' ? `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/ws` : '';

  @state()
  private token = linkToken;

  @state()
  private errorMsg = '';

  @state()
  private showHelp = false;

  @state()
  private showSettings = false;

  @state()
  private themeId = (() => {
    try {
      return getTheme(localStorage.getItem('wideboi.theme')).id;
    } catch {
      return 'dark';
    }
  })();

  get currentTheme(): Theme {
    return getTheme(this.themeId);
  }

  private applyTheme(id: string) {
    const theme = getTheme(id);
    this.themeId = theme.id;
    const vars = getThemeCSSVariables(theme);
    for (const [key, val] of Object.entries(vars)) {
      this.style.setProperty(key, val);
    }
  }

  public setTheme(id: string) {
    const validId = getTheme(id).id;
    this.applyTheme(validId);
    try {
      localStorage.setItem('wideboi.theme', validId);
    } catch {
      // ignore
    }
    this.requestUpdate();
  }

  private handleThemeSelect = (e: Event) => {
    const val = (e.target as HTMLSelectElement).value;
    this.setTheme(val);
  };

  @state()
  private prefixSetting = (() => {
    try {
      return localStorage.getItem('wideboi.prefix') || 'ctrl+b';
    } catch {
      return 'ctrl+b';
    }
  })();

  private keyRouter = new KeyRouter(this.prefixSetting);
  private previousFocusId = 0;
  private pendingFocusId = 0;
  @state()
  private paneStatuses: Record<number, PaneStatus> = {};
  private listeners?: AbortController;
  private pointer?: { id: number; pane: WideboiPane; button: number; tracking: boolean; focusOnClick: boolean; dragged: boolean; start: CellPoint };
  private selectedPane?: WideboiPane;
  private movement = new Map<number, Animation>();
  private lastSentSize?: { cols: number; rows: number };
  @state() private layoutMode: 'scroll' | 'cards' = 'cards';
  @state() private displayWidths: Record<number, number> = {};
  @state() private followPTY = false;
  private pendingReveal = new Set<number>();
  @state() private mobile = this.narrowMedia.matches;
  @state() private mobileDraft = '';
  @state() private mobileCtrl = false;
  @state() private mobileInputMode: 'draft' | 'direct' = 'draft';
  @state() private showMacros = false;
  @state() private showMacroEditor = false;
  @state() private draftMacroSteps: MacroStep[] = [];
  @state() private macros: Macro[] = (() => {
    try {
      const stored = localStorage.getItem('wideboi.macros');
      if (stored) {
        const parsed = JSON.parse(stored);
        if (Array.isArray(parsed) && parsed.length > 0) return parsed;
      }
    } catch {
      // ignore
    }
    return DEFAULT_MACROS;
  })();
  @state() private paneZooms = new Map<number, number>();
  @state() private searchState: SearchState | null = null;
  private pendingNav: 0 | 1 | -1 = 0;
  private cardFirst = 0;
  private cardViewportWidth = 0;
  private stripScrollLeft = 0;
  private stackFocusId: number | null = 0;
  private focusTransition = 0;

  private finishFocusStack(paneID: number, transition: number) {
    const animations = Array.from(this.movement.values()).filter(animation =>
      animation.playState === 'running' || animation.playState === 'paused');
    void Promise.allSettled(animations.map(animation => animation.finished)).then(() => {
      if (this.mobile || this.layoutMode !== 'cards' || this.focusedPaneId !== paneID || this.focusTransition !== transition) return;
      if (Array.from(this.movement.values()).some(animation =>
        animation.playState === 'running' || animation.playState === 'paused')) {
        this.finishFocusStack(paneID, transition);
        return;
      }
      this.stackFocusId = paneID;
      this.requestUpdate();
    });
  }

  public startSearch = () => {
    if (!this.focusedPaneId) return;
    const fp = this.panes.get(this.focusedPaneId);
    const priorOffset = fp?.scrollOffset ?? 0;
    const priorHistoryLen = fp?.scrollbackLen ?? 0;
    const query = this.searchState?.query || '';
    this.searchState = createSearchSession(this.focusedPaneId, priorOffset, priorHistoryLen, query);
    this.pendingNav = 0;
    void this.updateComplete.then(() => {
      const input = this.shadowRoot?.querySelector<HTMLInputElement>('.search-input');
      if (input) {
        input.focus();
        input.select();
      }
    });
  };

  public commitSearch() {
    if (!this.searchState || !this.searchState.query || !this.client) return;
    this.searchState = { ...this.searchState, status: 'searching' };
    this.pendingNav = 0;
    this.client.send({ case: 'historyRequest', value: { paneId: this.searchState.paneId } });
  }

  public navigateSearch(direction: 1 | -1) {
    if (!this.searchState || !this.searchState.query || !this.client) return;
    this.pendingNav = direction;
    this.searchState = { ...this.searchState, status: 'searching' };
    this.client.send({ case: 'historyRequest', value: { paneId: this.searchState.paneId } });
  }

  public acceptSearch() {
    if (!this.searchState) return;
    this.searchState = null;
    this.pendingNav = 0;
    this.focusedPane()?.focusInput();
  }

  public cancelSearch() {
    if (!this.searchState || !this.client) return;
    const cmd = cancelSearch(this.searchState);
    this.client.send({
      case: 'scroll',
      value: {
        paneId: cmd.paneId,
        delta: 0,
        setAbsolute: true,
        offset: cmd.offset,
        anchorHistory: cmd.anchorHistory,
        historyLen: cmd.historyLen,
      },
    });
    this.searchState = null;
    this.pendingNav = 0;
    this.focusedPane()?.focusInput();
  }

  public liveSearch() {
    if (!this.searchState || !this.client) return;
    const cmd = liveSearch(this.searchState);
    this.client.send({
      case: 'scroll',
      value: {
        paneId: cmd.paneId,
        delta: 0,
        setAbsolute: true,
        offset: cmd.offset,
        anchorHistory: cmd.anchorHistory,
        historyLen: cmd.historyLen,
      },
    });
    this.searchState = null;
    this.pendingNav = 0;
    this.focusedPane()?.focusInput();
  }

  private handleHistorySnapshot(snapshot: MsgHistorySnapshot) {
    if (!this.searchState || this.searchState.paneId !== snapshot.paneId || !this.client) return;
    const res = applySnapshot(this.searchState, snapshot, this.pendingNav);
    this.pendingNav = 0;
    this.searchState = res.nextState;
    if (res.scrollMsg) {
      this.client.send({
        case: 'scroll',
        value: {
          paneId: res.scrollMsg.paneId,
          delta: 0,
          setAbsolute: true,
          offset: res.scrollMsg.offset,
          anchorHistory: res.scrollMsg.anchorHistory,
          historyLen: res.scrollMsg.historyLen,
        },
      });
    }
  }

  private focusPane(paneID: number) {
    if (!this.activePanes.includes(paneID)) return;
    if (this.searchState && this.searchState.paneId !== paneID) {
      this.cancelSearch();
    }
    if (paneID === this.focusedPaneId) {
      void this.updateComplete.then(() => {
        if (!this.mobile) this.focusedPane()?.focusInput();
        this.revealFocus();
      });
      return;
    }
    const previous = this.panePositions();
    const transition = ++this.focusTransition;
    if (this.layoutMode === 'cards' && !this.mobile) this.stackFocusId = null;
    this.previousFocusId = this.focusedPaneId;
    this.focusedPaneId = paneID;
    void this.updateComplete.then(() => {
      if (this.layoutMode === 'cards' && !this.mobile) {
        this.animateReorder(previous);
        this.finishFocusStack(paneID, transition);
      }
      if (!this.mobile) this.focusedPane()?.focusInput();
      this.revealFocus();
    });
  }

  private focusedPane(): WideboiPane | undefined {
    return Array.from(this.paneStrip?.querySelectorAll('wideboi-pane') || [])
      .find(element => element.paneId === this.focusedPaneId);
  }

  private revealPendingCursor(paneID: number) {
    if (!this.pendingReveal.delete(paneID)) return;
    void this.updateComplete.then(() => Array.from(this.paneStrip.querySelectorAll('wideboi-pane'))
      .find(pane => pane.paneId === paneID)?.revealCursor());
  }

  private editWidth(width: number) {
    const id = this.focusedPaneId;
    if (!id || !Number.isInteger(width) || width < 20 || width > 4096) return;
    this.followPTY = false;
    this.displayWidths = { ...this.displayWidths, [id]: width };
    this.client?.send({ case: 'setPaneWidth', value: { paneId: id, width } });
  }

  private cycleWidth(verb: VerbType) {
    const current = this.displayWidths[this.focusedPaneId];
    if (!current) return;
    let next = current;
    if (verb === VerbType.CYCLE_WIDTH) next = [40, 60, 80].find(width => width > current) ?? 40;
    if (verb === VerbType.GROW_WIDTH) next = Math.min(4096, current + 10);
    if (verb === VerbType.SHRINK_WIDTH) next = Math.max(20, current - 10);
    this.editWidth(next);
  }

  private revealFocus() {
    if (this.mobile || this.layoutMode === 'cards') return;
    const pane = this.focusedPane();
    if (!pane) return;
    pane.scrollIntoView({ block: 'nearest', inline: 'nearest',
      behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' });
  }

  private panePositions(): Map<number, number> {
    return new Map(Array.from(this.paneStrip?.querySelectorAll('wideboi-pane') || [])
      .map(pane => [pane.paneId, pane.getBoundingClientRect().left]));
  }

  private animateReorder(previous: Map<number, number>) {
    if (this.mobile) return;
    for (const animation of this.movement.values()) animation.cancel();
    this.movement.clear();
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;
    for (const pane of this.paneStrip.querySelectorAll('wideboi-pane')) {
      const oldLeft = previous.get(pane.paneId);
      if (oldLeft === undefined) continue;
      const offset = oldLeft - pane.getBoundingClientRect().left;
      if (Math.abs(offset) < 1) continue;
      const animation = pane.animate(
        [{ transform: `translateX(${offset}px)` }, { transform: 'translateX(0)' }],
        { duration: 180, easing: 'ease-out' });
      this.movement.set(pane.paneId, animation);
      animation.onfinish = () => this.movement.delete(pane.paneId);
    }
  }

  private getGridSize() {
    return {
      cols: Math.floor(this.paneStrip.clientWidth / this.cellWidth),
      rows: Math.floor(this.paneStrip.clientHeight / termSettings.cellHeight) + 2,
    };
  }

  private sendResizeIfChanged() {
    if (this.mobile || !this.client || !this.connected || !this.lastSentSize) return;
    const size = this.getGridSize();
    if (size.cols > 0 && size.rows > 2 &&
        (size.cols !== this.lastSentSize.cols || size.rows !== this.lastSentSize.rows)) {
      this.client.send({ case: 'resize', value: size });
      this.lastSentSize = size;
    }
  }

  constructor() {
    super();

    this.resizeObserver = new ResizeObserver(() => {
      if (this.cardViewportWidth !== this.paneStrip.clientWidth) {
        this.cardViewportWidth = this.paneStrip.clientWidth;
        if (this.layoutMode === 'cards') this.requestUpdate();
      }
      this.sendResizeIfChanged();
    });
  }

  async firstUpdated() {
    this.listeners = new AbortController();
    this.setupViewport();
    try { await document.fonts.load(termSettings.font); } catch (e) {}
    this.cellWidth = measureCellWidth();
    this.resizeObserver.observe(this.paneStrip);
    this.requestUpdate();
    
    this.setupKeyboard();
    this.setupMouse();
  }

  connectedCallback() {
    super.connectedCallback();
    this.applyTheme(this.themeId);
    if (this.paneStrip && !this.listeners) {
      this.listeners = new AbortController();
      this.setupViewport();
      this.resizeObserver.observe(this.paneStrip);
      this.setupKeyboard();
      this.setupMouse();
    }
  }

  private setupViewport() {
    const syncWidth = () => {
      this.mobile = this.narrowMedia.matches;
      this.syncVisibleHeight();
      if (!this.mobile) this.sendResizeIfChanged();
    };
    this.narrowMedia.addEventListener('change', syncWidth, { signal: this.listeners?.signal });
    window.visualViewport?.addEventListener('resize', () => this.syncVisibleHeight(),
      { signal: this.listeners?.signal });
    this.syncVisibleHeight();
  }

  private syncVisibleHeight() {
    if (this.mobile && window.visualViewport) {
      this.style.setProperty('--app-height', `${window.visualViewport.height}px`);
    } else {
      this.style.removeProperty('--app-height');
    }
  }

  disconnectedCallback() {
    super.disconnectedCallback();
    this.listeners?.abort();
    this.listeners = undefined;
    this.pointer = undefined;
    this.keyRouter.reset();
    for (const animation of this.movement.values()) animation.cancel();
    this.movement.clear();
    this.resizeObserver.disconnect();
    this.stopStatsReport();
    if (this.client) {
      this.client.disconnect();
      this.client = null;
    }
    this.connected = false;
  }

  private startStatsReport() {
    const stats = this.stats;
    if (!stats) return;
    this.stopStatsReport();
    stats.reset();
    let windowStart = performance.now();
    this.statsTimer = setInterval(() => {
      const now = performance.now();
      const line = formatSummary(stats.summary(now - windowStart));
      stats.reset();
      windowStart = now;
      console.info('[wideboi stats]', line);
      this.statsText = line.split(' | ').join('\n');
    }, STATS_REPORT_MS);
  }

  // Clears the text too (startStatsReport calls this), so a reconnect shows
  // "collecting…" rather than the previous connection's numbers.
  private stopStatsReport() {
    this.statsText = '';
    if (this.statsTimer === undefined) return;
    clearInterval(this.statsTimer);
    this.statsTimer = undefined;
  }

  private connectClient() {
    if (this.client) {
      this.client.disconnect();
    }
    this.stopStatsReport();
    this.connected = false;
    this.lastSentSize = undefined;
    this.keyRouter.reset();
    this.mobileDraft = '';
    this.mobileCtrl = false;
    this.mobileInputMode = 'draft';
    this.showMacros = false;
    this.showMacroEditor = false;
    this.paneZooms.clear();
    this.pendingFocusId = 0;
    this.panes = new PaneStore(this.stats);
    this.selectedPane = undefined;
    this.columns = [];
    this.displayWidths = {};
    this.pendingReveal.clear();
    this.activePanes = [];
    this.focusedPaneId = 0;
    this.previousFocusId = 0;
    this.stackFocusId = 0;
    this.focusTransition++;
    
    this.errorMsg = '';
    
    let url: URL;
    try {
      url = new URL(this.wsUrl);
      if (url.protocol !== 'ws:' && url.protocol !== 'wss:') {
        throw new Error('invalid WebSocket protocol');
      }
    } catch {
      this.errorMsg = 'Enter a valid WebSocket URL.';
      return;
    }
    // Accept an older token-bearing WebSocket URL entered in the form, but
    // remove its query credential before the browser opens the connection.
    const token = this.token || new URLSearchParams(url.hash.slice(1)).get('token') || url.searchParams.get('token') || '';
    url.searchParams.delete('token');
    url.hash = '';
    const client = new WideboiClient(url.toString(), token, this.stats);
    this.client = client;
    
    client.onConnect = () => {
      if (this.client !== client) return;
      console.log('Connected to server');
      this.connected = true;
      this.startStatsReport();
      this.errorMsg = '';
      void this.updateComplete.then(() => {
        if (this.client === client && this.connected) this.sendAttach();
      });
    };

    client.onDisconnect = () => {
      if (this.client !== client) return;
      this.connected = false;
      this.keyRouter.reset();
      this.mobileCtrl = false;
      this.stopStatsReport();
      this.errorMsg = 'Disconnected from server.';
    }
    client.onMessage = (message) => {
      if (this.client !== client) return;

      switch (message.msg.case) {
        case 'layoutSnapshot': {
          const snapshot = message.msg.value;
          const previous = this.panePositions();
          const previousFocus = this.focusedPaneId;
          this.focusedPaneId = reconcileFocus(this.columns, snapshot.columns, this.focusedPaneId);
          const widths = { ...this.displayWidths };
          for (const column of snapshot.columns) {
            if (widths[column.paneId] === undefined || this.followPTY) widths[column.paneId] = column.width;
          }
          for (const id of Object.keys(widths)) {
            if (!snapshot.columns.some(column => column.paneId === Number(id))) delete widths[Number(id)];
          }
          this.displayWidths = widths;
          this.columns = snapshot.columns;
          this.activePanes = snapshot.columns.map(c => c.paneId);
          if (this.focusedPaneId !== previousFocus) {
            this.stackFocusId = this.focusedPaneId;
            this.focusTransition++;
          } else if (this.stackFocusId !== null && !this.activePanes.includes(this.stackFocusId)) {
            this.stackFocusId = this.focusedPaneId;
          }
          if (this.pendingFocusId && this.activePanes.includes(this.pendingFocusId)) {
            this.focusPane(this.pendingFocusId);
            this.pendingFocusId = 0;
          }
          if (!this.activePanes.includes(this.previousFocusId)) this.previousFocusId = 0;
          this.paneStatuses = snapshot.paneStatuses;
          this.paneTitles = snapshot.paneTitles;
          const activeSet = new Set(this.activePanes);
          let metaPruned = false;
          const nextMeta = { ...this.paneMetadata };
          for (const id of Object.keys(nextMeta)) {
            if (!activeSet.has(Number(id))) {
              delete nextMeta[Number(id)];
              metaPruned = true;
            }
          }
          if (metaPruned) this.paneMetadata = nextMeta;
          void this.updateComplete.then(() => {
            this.animateReorder(previous);
            this.revealFocus();
            this.sendResizeIfChanged();
          });
          break;
        }
        case 'paneCreated':
          this.pendingFocusId = message.msg.value.paneId;
          break;
        case 'paneUpdate':
          this.panes.update(message.msg.value);
          this.requestUpdate();
          this.revealPendingCursor(message.msg.value.paneId);
          break;
        case 'panePatch':
          if (!this.panes.patch(message.msg.value)) {
            client.send({ case: 'paneResync', value: { paneId: message.msg.value.paneId } });
          }
          this.requestUpdate();
          this.revealPendingCursor(message.msg.value.paneId);
          break;
        case 'paneClosed': {
          const closedId = message.msg.value.paneId;
          this.paneZooms.delete(closedId);
          const previous = this.panePositions();
          this.panes.close(closedId);
          const previousColumns = this.columns;
          const nextColumns = previousColumns.filter(column => column.paneId !== closedId);
          const nextFocus = reconcileFocus(previousColumns, nextColumns, this.focusedPaneId);
          const focusChanged = nextFocus !== this.focusedPaneId;
          this.columns = nextColumns;
          this.activePanes = nextColumns.map(column => column.paneId);
          this.focusedPaneId = nextFocus;
          let transition = this.focusTransition;
          if (this.stackFocusId === closedId || focusChanged) {
            this.stackFocusId = this.layoutMode === 'cards' && !this.mobile && focusChanged ? null : nextFocus;
            transition = ++this.focusTransition;
          }
          if (this.previousFocusId === closedId) this.previousFocusId = 0;
          if (this.pendingFocusId === closedId) this.pendingFocusId = 0;
          if (this.searchState && this.searchState.paneId === closedId) {
            this.searchState = null;
            this.pendingNav = 0;
          }
          if (this.pointer?.pane.paneId === closedId) this.pointer = undefined;
          if (this.selectedPane?.paneId === closedId) this.selectedPane = undefined;
          if (this.paneMetadata[closedId]) {
            const next = { ...this.paneMetadata };
            delete next[closedId];
            this.paneMetadata = next;
          }
          void this.updateComplete.then(() => {
            this.animateReorder(previous);
            if (focusChanged) {
              if (!this.mobile) this.focusedPane()?.focusInput();
              if (this.layoutMode === 'cards' && !this.mobile) this.finishFocusStack(nextFocus, transition);
            }
            const animations = Array.from(this.movement.values());
            void Promise.allSettled(animations.map(animation => animation.finished)).then(() => {
              if (this.focusedPaneId === nextFocus) this.revealFocus();
            });
            this.sendResizeIfChanged();
          });
          break;
        }
        case 'paneMetadata': {
          const meta = message.msg.value;
          this.paneMetadata = { ...this.paneMetadata, [meta.paneId]: meta };
          break;
        }
        case 'focusPane': {
          this.focusPane(message.msg.value.paneId);
          break;
        }
        case 'historySnapshot': {
          this.handleHistorySnapshot(message.msg.value);
          break;
        }
        case 'macrosSnapshot': {
          const snap = message.msg.value;
          if (snap.macros) {
            this.macros = snap.macros.map(m => ({
              name: m.name,
              steps: m.steps.map(s => ({
                text: s.text || undefined,
                key: s.key || undefined,
                code: s.code || undefined,
                ctrl: s.ctrl || undefined,
                alt: s.alt || undefined,
                shift: s.shift || undefined,
              })),
            }));
            try {
              localStorage.setItem('wideboi.macros', JSON.stringify(this.macros));
            } catch {
              // ignore
            }
          }
          break;
        }
      }
    };

    client.connect();
  }

  private setupKeyboard() {
    const fromFormControl = (e: Event) => e.composedPath().some(node =>
      node instanceof HTMLInputElement || node instanceof HTMLTextAreaElement ||
      node instanceof HTMLSelectElement || node instanceof HTMLButtonElement);
    document.addEventListener('keydown', (e) => {
      if (!this.connected || !this.client) return;
      if (this.showHelp) {
        if (e.key === 'Escape' || e.key === '?' || (e.ctrlKey && e.key === 'c')) {
          this.closeHelp();
          e.preventDefault();
        }
        return;
      }
      if (this.showSettings) {
        if (e.key === 'Escape' || e.key === ',' || (e.ctrlKey && e.key === 'c')) {
          this.closeSettings();
          e.preventDefault();
        }
        return;
      }
      if (fromFormControl(e)) return;
      if ((e.ctrlKey || e.metaKey) && (e.key === 'f' || e.code === 'KeyF')) {
        e.preventDefault();
        this.startSearch();
        return;
      }
      if (e.isComposing || e.key === 'Process' || e.key === 'Dead') return;

      if (this.searchState) {
        if (e.key === 'Escape') {
          e.preventDefault();
          this.cancelSearch();
          return;
        }
        if (e.key === 'Enter') {
          e.preventDefault();
          this.acceptSearch();
          return;
        }
        if (e.ctrlKey && (e.key === 'g' || e.code === 'KeyG')) {
          e.preventDefault();
          this.liveSearch();
          return;
        }
        if (e.key === 'n' && !e.ctrlKey && !e.altKey && !e.metaKey) {
          e.preventDefault();
          this.navigateSearch(1);
          return;
        }
        if (e.key === 'N' && !e.ctrlKey && !e.altKey && !e.metaKey) {
          e.preventDefault();
          this.navigateSearch(-1);
          return;
        }
      }

      const action = this.keyRouter.handle(e);
      switch (action.type) {
        case 'ignore':
          e.preventDefault();
          return;
        case 'search':
          this.startSearch();
          e.preventDefault();
          return;
        case 'send_literal_key':
        case 'forward':
          if (sendKeyboardInput(this.client, this.focusedPaneId, e)) {
            this.pendingReveal.add(this.focusedPaneId);
            this.focusedPane()?.revealCursor();
            e.preventDefault();
          }
          return;
        case 'scroll':
          this.client.send({ case: 'scroll', value: { paneId: this.focusedPaneId, delta: action.delta } });
          e.preventDefault();
          return;
        case 'toggle_cards':
          this.setLayoutMode(this.layoutMode === 'cards' ? 'scroll' : 'cards');
          e.preventDefault();
          return;
        case 'pan':
          this.focusedPane()?.panCells(action.direction * 10);
          e.preventDefault();
          return;
        case 'toggle_follow_pty':
          this.followPTY = !this.followPTY;
          if (this.followPTY) this.displayWidths = Object.fromEntries(this.columns.map(column => [column.paneId, column.width]));
          e.preventDefault();
          return;
        case 'focus_column':
          this.focusColumnByIndex(action.column);
          e.preventDefault();
          return;
        case 'toggle_help':
          this.toggleHelp();
          e.preventDefault();
          return;
        case 'toggle_settings':
          this.toggleSettings();
          e.preventDefault();
          return;
        case 'verb': {
          const verb = action.verb;
          const index = this.activePanes.indexOf(this.focusedPaneId);
          if (verb === VerbType.FOCUS_LEFT && index > 0) this.focusPane(this.activePanes[index - 1]);
          else if (verb === VerbType.FOCUS_RIGHT && index >= 0 && index < this.activePanes.length - 1) this.focusPane(this.activePanes[index + 1]);
          else if (verb === VerbType.FOCUS_LAST) this.focusPane(this.previousFocusId);
          else if (verb === VerbType.SMART_JUMP) {
            const rank = (status: PaneStatus | undefined) =>
              status === PaneStatus.FAILED ? 3 : status === PaneStatus.DONE ? 2 : status === PaneStatus.NEEDS_INPUT ? 1 : 0;
            const target = this.activePanes.reduce((best, id) => {
              const score = rank(this.paneStatuses[id]);
              return score > rank(this.paneStatuses[best]) ||
                (score > 0 && score === rank(this.paneStatuses[best]) && id < best) ? id : best;
            }, 0);
            if (target) this.focusPane(target);
          } else if ([VerbType.CYCLE_WIDTH, VerbType.GROW_WIDTH, VerbType.SHRINK_WIDTH].includes(verb)) {
            this.cycleWidth(verb);
          } else if (![VerbType.FOCUS_LEFT, VerbType.FOCUS_RIGHT, VerbType.SMART_JUMP, VerbType.FOCUS_LAST].includes(verb)) {
            this.client.send({ case: 'verb', value: { verb, paneId: this.focusedPaneId } });
          }
          e.preventDefault();
          return;
        }
      }
    }, { signal: this.listeners?.signal });

    document.addEventListener('paste', (e) => {
      if (!this.connected || !this.client || fromFormControl(e)) return;
      const value = e.clipboardData?.getData('text/plain') || '';
      if (!sendTextInput(this.client, this.focusedPaneId, value)) return;
      this.pendingReveal.add(this.focusedPaneId);
      this.focusedPane()?.revealCursor();
      e.preventDefault();
    }, { signal: this.listeners?.signal });

    document.addEventListener('compositionend', (e) => {
      if (!this.connected || !this.client || fromFormControl(e)) return;
      sendTextInput(this.client, this.focusedPaneId, (e as CompositionEvent).data);
    }, { signal: this.listeners?.signal });
  }

  private setupMouse() {
    this.paneStrip.addEventListener('scroll', () => {
      if (this.layoutMode === 'cards' && this.paneStrip.scrollLeft) this.paneStrip.scrollLeft = 0;
    }, { signal: this.listeners?.signal });

    this.paneStrip.addEventListener('pointerdown', (e) => {
      if (!this.connected || !this.client) return;
      if (this.mobile) return;
      const pane = this.eventPane(e);
      if (!pane) return;
      const start = pane.cellAt(e.clientX, e.clientY);
      this.pointer = {
        id: e.pointerId, pane,
        button: e.button === 0 ? 1 : e.button === 2 ? 3 : 2,
        tracking: pane.paneId === this.focusedPaneId && this.panes.mouseTracking(pane.paneId),
        focusOnClick: pane.paneId !== this.focusedPaneId, dragged: false, start,
      };
      pane.setPointerCapture(e.pointerId);
      this.selectedPane?.clearSelection();
      this.selectedPane = undefined;
      if (this.pointer.tracking) this.sendPointerMouse(MouseKind.PRESS, e);
      e.preventDefault();
    }, { signal: this.listeners?.signal });

    this.paneStrip.addEventListener('pointermove', (e) => {
      if (this.mobile) return;
      const press = this.pointer;
      if (!press || press.id !== e.pointerId) return;
      const end = press.pane.cellAt(e.clientX, e.clientY);
      if (end.x !== press.start.x || end.y !== press.start.y) press.dragged = true;
      if (press.tracking) this.sendPointerMouse(MouseKind.MOTION, e);
      else if (press.button === 1) {
        press.pane.setSelection(press.start, end);
        this.selectedPane = press.pane;
      }
      e.preventDefault();
    }, { signal: this.listeners?.signal });

    const release = (e: PointerEvent) => {
      if (this.mobile) return;
      if (!this.pointer || this.pointer.id !== e.pointerId) return;
      if (this.pointer.tracking) this.sendPointerMouse(MouseKind.RELEASE, e);
      else {
        if (e.type === 'pointerup' && this.pointer.focusOnClick && !this.pointer.dragged) {
          this.pointer.pane.clearSelection();
          this.focusPane(this.pointer.pane.paneId);
        } else {
          const press = this.pointer;
          const text = selectionText(this.panes.get(press.pane.paneId), press.start,
            press.pane.cellAt(e.clientX, e.clientY));
          if (text && navigator.clipboard?.writeText) void navigator.clipboard.writeText(text).catch(() => {});
        }
      }
      const pane = this.pointer.pane;
      this.pointer = undefined;
      if (pane.hasPointerCapture(e.pointerId)) pane.releasePointerCapture(e.pointerId);
      e.preventDefault();
    };
    this.paneStrip.addEventListener('pointerup', release, { signal: this.listeners?.signal });
    this.paneStrip.addEventListener('pointercancel', release, { signal: this.listeners?.signal });

    this.paneStrip.addEventListener('click', (e) => {
      if (!this.mobile) return;
      const pane = this.eventPane(e);
      if (pane) {
        this.focusPane(pane.paneId);
        if (this.mobileInputMode === 'direct') {
          this.renderRoot.querySelector<HTMLInputElement>('.mobile-input-bar .mobile-direct-input')?.focus();
        } else {
          this.renderRoot.querySelector<HTMLInputElement>('.mobile-input-bar .mobile-draft-input')?.focus();
        }
      }
    }, { signal: this.listeners?.signal });

    this.paneStrip.addEventListener('wheel', (e) => {
      if (this.mobile) return;
      if (!this.connected || !this.client) return;
      // Leave horizontal gestures to the pane viewport or outer strip.
      if (e.shiftKey || Math.abs(e.deltaX) > Math.abs(e.deltaY)) return;
      const pane = this.eventPane(e);
      if (pane) {
        // A short client scrolls within the live terminal grid. Alt+wheel
        // explicitly navigates terminal history instead.
        if (pane.hasVerticalOverflow && !e.altKey) return;
        e.preventDefault();
        // e.deltaY > 0 means scrolling down (towards bottom/newer).
        // e.deltaY < 0 means scrolling up (towards top/older).
        // In MsgScroll, Delta > 0 is up (older), Delta < 0 is down (newer).
        // A standard wheel step is often 3 lines.
        const delta = e.deltaY > 0 ? -3 : 3;
        this.client.send({ case: 'scroll', value: { paneId: pane.paneId, delta } });
      }
    }, { passive: false, signal: this.listeners?.signal });
  }

  private eventPane(e: Event): WideboiPane | undefined {
    return e.composedPath().find(node => node instanceof WideboiPane) as WideboiPane | undefined;
  }

  private sendPointerMouse(kind: MouseKind, e: PointerEvent) {
    const press = this.pointer;
    if (!press || !this.client || !this.connected) return;
    const { x, y } = press.pane.cellAt(e.clientX, e.clientY);
    this.client.send({ case: 'mouse', value: {
      paneId: press.pane.paneId, kind, x, y,
      button: press.button,
      mod: (e.shiftKey ? 1 : 0) | (e.altKey ? 2 : 0) | (e.ctrlKey ? 4 : 0)
    } });
  }

  private sendAttach() {
     if (!this.client) return;
     const size = this.getGridSize();
     this.client.send({ case: 'attach', value: { cols: size.cols, rows: size.rows } });
     this.lastSentSize = size;
  }

  private handleUrlChange(e: Event) {
    this.wsUrl = (e.target as HTMLInputElement).value;
  }

  private handleTokenChange(e: Event) {
    this.token = (e.target as HTMLInputElement).value;
  }

  private handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      this.connectClient();
    }
  }



  private handlePaneSelect(e: Event) {
    const select = e.target as HTMLSelectElement;
    const paneID = parseInt(select.value, 10);
    if (paneID > 0 && this.client && this.connected) {
      this.focusPane(paneID);
    }
  }

  get currentZoom(): number {
    return this.zoomForPane(this.focusedPaneId);
  }

  zoomForPane(paneId: number): number {
    const zoom = this.paneZooms.get(paneId);
    if (zoom !== undefined) return zoom;
    if (this.mobile) {
      return this.minZoomForPane(paneId);
    }
    return 1.0;
  }

  minZoomForPane(paneId: number): number {
    const pane = this.panes.get(paneId);
    if (!pane || !this.paneStrip) return 0.5;
    const termWidth = pane.cols * this.cellWidth;
    const termHeight = pane.rows * termSettings.cellHeight;
    const viewWidth = Math.max(1, (this.paneStrip.clientWidth || window.innerWidth) - 2);
    const viewHeight = Math.max(1, (this.paneStrip.clientHeight || (window.innerHeight - 100)) - 2);
    if (termWidth <= 0 || termHeight <= 0) return 0.5;
    const fitX = viewWidth / termWidth;
    const fitY = viewHeight / termHeight;
    const fitZoom = Math.min(fitX, fitY);
    return Math.max(0.25, Math.min(1.0, Math.floor(fitZoom * 100) / 100));
  }

  get currentMinZoom(): number {
    return this.minZoomForPane(this.focusedPaneId);
  }

  private stepZoom(delta: number) {
    if (!this.focusedPaneId) return;
    const current = this.currentZoom;
    const minZoom = this.currentMinZoom;
    const next = Math.max(minZoom, Math.min(2.0, Math.round((current + delta) * 100) / 100));
    if (next !== current) {
      this.paneZooms.set(this.focusedPaneId, next);
      this.requestUpdate();
    }
  }

  private resetZoom() {
    if (!this.focusedPaneId) return;
    const minZoom = this.currentMinZoom;
    const current = this.currentZoom;
    const target = current !== minZoom ? minZoom : 1.0;
    this.paneZooms.set(this.focusedPaneId, target);
    this.requestUpdate();
  }

  private handleZoomChange(e: CustomEvent<{ paneId: number; zoom: number }>) {
    const { paneId, zoom } = e.detail;
    if (paneId && zoom) {
      this.paneZooms.set(paneId, zoom);
      this.requestUpdate();
    }
  }

  private moveMobilePane(delta: number) {
    const index = this.activePanes.indexOf(this.focusedPaneId);
    const next = this.activePanes[index + delta];
    if (next !== undefined) this.focusPane(next);
  }

  private sendMobileKey(key: string, code: string) {
    if (!this.client || !this.connected || !this.focusedPaneId) return;
    const ctrl = this.mobileCtrl;
    const event = new KeyboardEvent('keydown', { key, code, ctrlKey: ctrl });
    if (sendKeyboardInput(this.client, this.focusedPaneId, event)) {
      this.pendingReveal.add(this.focusedPaneId);
      this.focusedPane()?.revealCursor();
    }
    this.mobileCtrl = false;
  }

  private handleMobileDraftKey(e: KeyboardEvent) {
    if (!this.client || !this.connected) return;
    if (e.key === 'Enter') {
      if (this.mobileDraft) {
        this.sendMobileDraft();
      }
      e.preventDefault();
      return;
    }
    if (this.mobileCtrl && e.key.length === 1) {
      this.sendMobileKey(e.key, e.code);
      e.preventDefault();
    } else if (e.ctrlKey || e.altKey || e.key === 'Escape' || e.key === 'Tab' ||
               (this.mobileDraft === '' && e.key.startsWith('Arrow'))) {
      if (sendKeyboardInput(this.client, this.focusedPaneId, e)) {
        this.pendingReveal.add(this.focusedPaneId);
        this.focusedPane()?.revealCursor();
        e.preventDefault();
      }
    }
  }

  private handleMobileDirectKey(e: KeyboardEvent) {
    if (!this.client || !this.connected || !this.focusedPaneId) return;
    if (this.mobileCtrl && e.key.length === 1) {
      this.sendMobileKey(e.key, e.code);
      e.preventDefault();
      const input = e.target as HTMLInputElement;
      if (input) input.value = '';
      return;
    }
    if (sendKeyboardInput(this.client, this.focusedPaneId, e)) {
      this.pendingReveal.add(this.focusedPaneId);
      this.focusedPane()?.revealCursor();
      e.preventDefault();
    }
    const input = e.target as HTMLInputElement;
    if (input) input.value = '';
  }

  private handleMobileDirectInput(e: Event) {
    const input = e.target as HTMLInputElement;
    if (!input || !input.value || !this.client || !this.connected || !this.focusedPaneId) return;
    if (sendTextInput(this.client, this.focusedPaneId, input.value)) {
      this.pendingReveal.add(this.focusedPaneId);
      this.focusedPane()?.revealCursor();
    }
    input.value = '';
  }

  private sendMobileDraft() {
    if (!this.client || !this.connected || !this.mobileDraft) return;
    const text = this.mobileDraft;
    if (sendTextInput(this.client, this.focusedPaneId, text)) {
      const enterEvent = typeof KeyboardEvent !== 'undefined'
        ? new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter' })
        : ({ key: 'Enter', code: 'Enter', shiftKey: false, altKey: false, ctrlKey: false, metaKey: false, isComposing: false, repeat: false } as unknown as KeyboardEvent);
      sendKeyboardInput(this.client, this.focusedPaneId, enterEvent);
      this.pendingReveal.add(this.focusedPaneId);
      this.focusedPane()?.revealCursor();
      this.mobileDraft = '';
      const input = this.renderRoot.querySelector<HTMLInputElement>('.mobile-input-bar .mobile-draft-input');
      if (input) input.value = '';
    }
  }

  private executeMobileMacro(macro: Macro) {
    if (!this.client || !this.connected || !this.focusedPaneId) return;
    executeMacro(this.client, this.focusedPaneId, macro);
    this.pendingReveal.add(this.focusedPaneId);
    this.focusedPane()?.revealCursor();
    this.showMacros = false;
  }

  private saveMacrosToServer() {
    if (!this.client || !this.connected) return;
    this.client.send({
      case: 'saveMacros',
      value: {
        macros: this.macros.map(m => ({
          name: m.name,
          steps: m.steps.map(s => ({
            text: s.text || '',
            key: s.key || '',
            code: s.code || '',
            ctrl: !!s.ctrl,
            alt: !!s.alt,
            shift: !!s.shift,
          })),
        })),
      },
    });
    try {
      localStorage.setItem('wideboi.macros', JSON.stringify(this.macros));
    } catch {
      // ignore
    }
  }

  private deleteMacro(index: number) {
    this.macros = this.macros.filter((_, i) => i !== index);
    try {
      localStorage.setItem('wideboi.macros', JSON.stringify(this.macros));
    } catch {
      // ignore
    }
  }

  private addDraftStep(type: 'text' | 'key', val: string, ctrl: boolean) {
    if (!val) return;
    if (type === 'text') {
      this.draftMacroSteps = [...this.draftMacroSteps, { text: val }];
    } else {
      this.draftMacroSteps = [...this.draftMacroSteps, {
        key: val,
        code: val.length === 1 ? `Key${val.toUpperCase()}` : val,
        ctrl: ctrl || undefined,
      }];
    }
  }

  private removeDraftStep(index: number) {
    this.draftMacroSteps = this.draftMacroSteps.filter((_, i) => i !== index);
  }

  private resetMacrosToDefault() {
    this.macros = DEFAULT_MACROS;
    try {
      localStorage.setItem('wideboi.macros', JSON.stringify(this.macros));
    } catch {
      // ignore
    }
  }

  private openHelp() {
    this.showHelp = true;
    this.showSettings = false;
    void this.updateComplete.then(() => {
      this.renderRoot.querySelector<HTMLButtonElement>('.help-dialog .close-btn')?.focus();
    });
  }

  private closeHelp() {
    this.showHelp = false;
    void this.updateComplete.then(() => {
      if (!this.mobile) this.focusedPane()?.focusInput();
    });
  }

  private toggleHelp() {
    if (this.showHelp) {
      this.closeHelp();
    } else {
      this.openHelp();
    }
  }

  private openSettings() {
    this.showSettings = true;
    this.showHelp = false;
    void this.updateComplete.then(() => {
      this.renderRoot.querySelector<HTMLButtonElement>('.settings-dialog .close-btn')?.focus();
    });
  }

  private closeSettings() {
    this.showSettings = false;
    void this.updateComplete.then(() => {
      if (!this.mobile) this.focusedPane()?.focusInput();
    });
  }

  private toggleSettings() {
    if (this.showSettings) {
      this.closeSettings();
    } else {
      this.openSettings();
    }
  }

  private async handleFontChange(e: Event) {
    const select = e.target as HTMLSelectElement;
    termSettings.fontFamily = select.value;
    termSettings.save();
    try { await document.fonts.load(termSettings.font); } catch (e) {}
    this.cellWidth = measureCellWidth();
    this.requestUpdate();
    this.sendResizeIfChanged();
  }

  private applyFontSize(size: number) {
    const clamped = Math.max(8, Math.min(48, size));
    termSettings.fontSize = clamped;
    termSettings.save();
    this.cellWidth = measureCellWidth();
    this.requestUpdate();
    this.sendResizeIfChanged();
  }

  private handleFontSizeChange(e: Event) {
    const input = e.target as HTMLInputElement;
    const size = parseInt(input.value, 10) || 14;
    this.applyFontSize(size);
  }

  private stepFontSize(delta: number) {
    this.applyFontSize(termSettings.fontSize + delta);
  }

  private resetFontSize() {
    this.applyFontSize(14);
  }

  private handlePrefixChange(e: Event) {
    const val = (e.target as HTMLSelectElement).value;
    this.prefixSetting = val;
    try {
      localStorage.setItem('wideboi.prefix', val);
    } catch {
      // ignore localStorage quota/disabled errors
    }
    this.keyRouter.setPrefix(val);
  }

  private focusColumnByIndex(colIndex: number) {
    if (this.columns.length === 0) return;
    let targetPaneId: number | undefined;
    if (colIndex === 0) {
      targetPaneId = this.columns[this.columns.length - 1]?.paneId;
    } else if (colIndex >= 1 && colIndex <= this.columns.length) {
      targetPaneId = this.columns[colIndex - 1]?.paneId;
    }
    if (targetPaneId !== undefined) {
      this.focusPane(targetPaneId);
    }
  }

  claimSize() {
    if (!this.client || !this.connected) return;
    this.sendResizeIfChanged();
    this.client.send({
      case: 'verb',
      value: { verb: VerbType.CLAIM_SIZE, paneId: this.focusedPaneId, widths: this.displayWidths },
    });
  }

  private setLayoutMode(mode: 'cards' | 'scroll') {
    if (mode !== 'cards' && mode !== 'scroll') return;
    if (mode === this.layoutMode) return;
    const previous = this.panePositions();
    if (mode === 'cards') {
      this.stackFocusId = this.focusedPaneId;
      this.stripScrollLeft = this.paneStrip.scrollLeft;
      // Cancel a smooth focus reveal still in progress in the strip.
      this.paneStrip.scrollTo({ left: 0, behavior: 'instant' });
    }
    this.focusTransition++;
    this.layoutMode = mode;
    void this.updateComplete.then(() => {
      this.paneStrip.scrollTo({ left: mode === 'cards' ? 0 : this.stripScrollLeft, behavior: 'instant' });
      this.animateReorder(previous);
      const animations = Array.from(this.movement.values());
      void Promise.allSettled(animations.map(animation => animation.finished)).then(() => {
        if (this.layoutMode === mode) this.revealFocus();
      });
    });
  }

  private handleWidthInput(e: Event) { this.editWidth(Number((e.target as HTMLInputElement).value)); }

  private handleLayoutSelect(e: Event) {
    const mode = (e.target as HTMLSelectElement).value;
    if (mode === 'cards' || mode === 'scroll') {
      this.setLayoutMode(mode);
    }
  }

  render() {
    const cards = this.layoutMode === 'cards' && !this.mobile;
    const displayWidth = (column: ColumnData) =>
      this.mobile ? Math.max(1, Math.floor(this.cardViewportWidth / this.cellWidth)) :
      this.displayWidths[column.paneId] ?? column.width;
    const displayColumns = this.columns.map(column => ({ paneId: column.paneId, width: displayWidth(column) }));
    const stackFocusId = this.stackFocusId === null ? null :
      (this.activePanes.includes(this.stackFocusId) ? this.stackFocusId : this.focusedPaneId);
    const layout = cards ? cardLayout(displayColumns, this.focusedPaneId,
      Math.floor(this.cardViewportWidth / this.cellWidth), this.cardFirst, stackFocusId) : undefined;
    if (layout) this.cardFirst = layout.first;
    const placements = new Map(layout?.placements.map(p => [p.paneId, p]));
    return html`
      <div class="terminal-shell">
        <div class=${this.mobile ? 'pane-strip mobile' : cards ? 'pane-strip cards' : 'pane-strip'}>
          ${repeat(this.columns, column => column.paneId, column => {
            const placement = placements.get(column.paneId);
            return html`
            <wideboi-pane
              style=${`width: ${this.mobile ? '100%' : `${displayWidth(column) * this.cellWidth}px`}; --divider-width: ${cards || this.mobile ? 0 : this.cellWidth}px; ${cards ? `left: ${(placement?.left ?? 0) * this.cellWidth}px; z-index: ${placement?.z ?? 0}; visibility: ${placement?.visible ? 'visible' : 'hidden'}` : ''}`}
              .paneId=${column.paneId}
              .pane=${this.panes.get(column.paneId)}
              .theme=${this.currentTheme}
              .focused=${column.paneId === this.focusedPaneId}
              .cardMode=${cards}
              .cardLabel=${`[${column.paneId}] ${this.paneTitles[column.paneId] || 'Terminal'}`}
              .running=${this.connected}
              .cellWidth=${this.cellWidth}
              .displayCols=${displayWidth(column)}
              .zoom=${this.zoomForPane(column.paneId)}
              .minZoom=${this.minZoomForPane(column.paneId)}
              .stats=${this.stats}
              @zoom-change=${this.handleZoomChange}
              aria-label=${`Pane ${column.paneId}`}
            ></wideboi-pane>
          `; })}
          ${cards && layout?.hiddenLeft ? html`<span class="card-count left">+${layout.hiddenLeft}</span>` : ''}
          ${cards && layout?.hiddenRight ? html`<span class="card-count right">+${layout.hiddenRight}</span>` : ''}
        </div>
        <div class="title">${this.paneTitles[this.focusedPaneId] ||
          (this.focusedPaneId ? `Pane ${this.focusedPaneId}` : '')}</div>
        ${this.searchState ? html`
          <div class="status search-bar">
            <span>search /</span>
            <input
              class="search-input"
              type="text"
              .value=${this.searchState.query}
              @input=${(e: InputEvent) => {
                if (this.searchState) {
                  this.searchState = {
                    ...this.searchState,
                    query: (e.target as HTMLInputElement).value,
                    status: 'input',
                  };
                }
              }}
              @keydown=${(e: KeyboardEvent) => {
                if (e.key === 'Enter') {
                  e.preventDefault();
                  if (this.searchState?.status === 'input') {
                    this.commitSearch();
                  } else if (this.searchState?.matches.length) {
                    this.navigateSearch(e.shiftKey ? -1 : 1);
                  }
                } else if (e.key === 'Escape') {
                  e.preventDefault();
                  this.cancelSearch();
                } else if (e.ctrlKey && (e.key === 'g' || e.code === 'KeyG')) {
                  e.preventDefault();
                  this.liveSearch();
                }
              }}
              placeholder="find in history..."
            />
            <button
              class="search-btn prev-btn"
              ?disabled=${!this.searchState.matches.length}
              @click=${() => this.navigateSearch(-1)}
              title="Previous match (Shift+Enter or N)"
            >▲ Prev</button>
            <button
              class="search-btn next-btn"
              ?disabled=${!this.searchState.matches.length}
              @click=${() => this.navigateSearch(1)}
              title="Next match (Enter or n)"
            >▼ Next</button>
            <button class="search-btn keep-btn" @click=${() => this.acceptSearch()} title="Keep current scroll (Enter)">Keep</button>
            <button class="search-btn restore-btn" @click=${() => this.cancelSearch()} title="Restore previous view (Esc)">Restore</button>
            <button class="search-btn live-btn" @click=${() => this.liveSearch()} title="Jump to bottom (Ctrl+G)">Live</button>
            <span class="search-msg">
              ${formatSearchStatus(this.searchState)}
            </span>
          </div>
        ` : html`
          <div class="status">${(() => {
            const fp = this.panes.get(this.focusedPaneId);
            const focusScroll = (fp && fp.scrollOffset > 0) ? `focus: [${this.focusedPaneId} ★] [scroll +${fp.scrollOffset}${fp.unreadOutput ? ' ⤓' : ''}]  ` : '';
            const items = this.columns.map(column =>
              `[${column.paneId}] ${PaneStatus[this.paneStatuses[column.paneId] ?? PaneStatus.IDLE] || ''}`
            ).join('  ');
            return `${focusScroll}${items}`;
          })()}</div>
        `}
      </div>
      ${this.connected ? html`
        <div class="toolbar">
          <label for="focus-pane">Focus Pane:</label>
          <select id="focus-pane" @change=${this.handlePaneSelect}>
            ${repeat(this.activePanes, id => id, id => html`
              <option value=${id} .selected=${id === this.focusedPaneId}>[${id}] ${this.paneTitles[id] || 'Terminal'}</option>
            `)}
          </select>
          <label for="layout-mode">Layout:</label>
          <select id="layout-mode" aria-label="Layout" @change=${this.handleLayoutSelect}>
            <option value="scroll" .selected=${!cards}>Scroll</option>
            <option value="cards" .selected=${cards}>Cards</option>
          </select>
          <label for="pane-width">Pane width:</label>
          <input id="pane-width" aria-label="Pane width" type="number" min="20" max="4096"
            .value=${String(this.displayWidths[this.focusedPaneId] ?? '')} @change=${this.handleWidthInput}>
          <label><input type="checkbox" aria-label="Follow PTY widths" .checked=${this.followPTY}
            @change=${() => { this.followPTY = !this.followPTY; if (this.followPTY) this.displayWidths = Object.fromEntries(this.columns.map(column => [column.paneId, column.width])); }}>Follow PTY</label>
          <label for="prefix-key">Prefix:</label>
          <select id="prefix-key" aria-label="Prefix key" @change=${this.handlePrefixChange}>
            <option value="ctrl+b" .selected=${this.prefixSetting === 'ctrl+b'}>Ctrl+B</option>
            <option value="ctrl+a" .selected=${this.prefixSetting === 'ctrl+a'}>Ctrl+A</option>
            <option value="ctrl+space" .selected=${this.prefixSetting === 'ctrl+space'}>Ctrl+Space</option>
          </select>
          <label for="theme-select">Theme:</label>
          <select id="theme-select" aria-label="Color theme" @change=${this.handleThemeSelect}>
            ${listThemes().map(t => html`
              <option value=${t.id} .selected=${t.id === this.themeId}>${t.name}</option>
            `)}
          </select>
          <button class="claim-size-btn" @click=${this.claimSize} title="Fit session terminal size to this window">Fit to Window</button>
          <button class="claim-size-btn search-btn" @click=${this.startSearch} title="Search pane history (/ or Ctrl+F)">Search</button>
          <button class="settings-btn" @click=${this.toggleSettings} aria-label="Settings" title="Settings (${this.keyRouter.prefixLabel} ,)">⚙ Settings</button>
          <button class="help-btn" @click=${this.toggleHelp} aria-label="Help">Help (?)</button>
          <span class="tip">(Tip: ${this.keyRouter.prefixLabel} then arrows or h/l to switch, ? for help, , for settings)</span>
        </div>
        <div class="mobile-bar">
          <button aria-label="Previous pane" ?disabled=${this.activePanes.indexOf(this.focusedPaneId) <= 0}
            @click=${() => this.moveMobilePane(-1)}>‹</button>
          <select aria-label="Mobile pane" @change=${this.handlePaneSelect}>
            ${repeat(this.activePanes, id => id, id => html`
              <option value=${id} .selected=${id === this.focusedPaneId}>[${id}] ${this.paneTitles[id] || 'Terminal'}</option>
            `)}
          </select>
          <div class="mobile-zoom">
            <button aria-label="Zoom out" ?disabled=${this.currentZoom <= this.currentMinZoom}
              @click=${() => this.stepZoom(-0.25)}>−</button>
            <button class="zoom-reset" aria-label="Reset zoom"
              @click=${() => this.resetZoom()}>${Math.round(this.currentZoom * 100)}%</button>
            <button aria-label="Zoom in" ?disabled=${this.currentZoom >= 2.0}
              @click=${() => this.stepZoom(0.25)}>+</button>
          </div>
          <button aria-label="Next pane" ?disabled=${this.activePanes.indexOf(this.focusedPaneId) >= this.activePanes.length - 1}
            @click=${() => this.moveMobilePane(1)}>›</button>
          <button class="mobile-settings-btn" aria-label="Settings" title="Settings" @click=${this.toggleSettings}>⚙</button>
        </div>
        <div class="mobile-dock">
          <div class="mobile-input-bar">
            <div class="mobile-mode-toggle" role="radiogroup" aria-label="Input mode">
              <button
                class=${this.mobileInputMode === 'draft' ? 'active' : ''}
                aria-pressed=${this.mobileInputMode === 'draft'}
                aria-label="Draft mode"
                @click=${() => { this.mobileInputMode = 'draft'; }}>Draft</button>
              <button
                class=${this.mobileInputMode === 'direct' ? 'active' : ''}
                aria-pressed=${this.mobileInputMode === 'direct'}
                aria-label="Direct input mode"
                @click=${() => {
                  this.mobileInputMode = 'direct';
                  void this.updateComplete.then(() => {
                    this.renderRoot.querySelector<HTMLInputElement>('.mobile-input-bar input.mobile-direct-input')?.focus();
                  });
                }}>Direct</button>
            </div>
            ${this.mobileInputMode === 'draft' ? html`
              <input
                type="text"
                class="mobile-draft-input"
                aria-label="Command or response"
                placeholder="Command or response"
                autocapitalize="off"
                autocorrect="off"
                spellcheck="false"
                .value=${this.mobileDraft}
                @input=${(e: Event) => { this.mobileDraft = (e.target as HTMLInputElement).value; }}
                @keydown=${this.handleMobileDraftKey} />
              <button class="mobile-send-btn" aria-label="Send text" ?disabled=${!this.mobileDraft} @click=${this.sendMobileDraft}>Send</button>
            ` : html`
              <input
                type="text"
                class="mobile-direct-input"
                aria-label="Direct terminal input"
                placeholder="Direct terminal keys…"
                autocapitalize="off"
                autocorrect="off"
                spellcheck="false"
                @keydown=${this.handleMobileDirectKey}
                @input=${this.handleMobileDirectInput} />
            `}
            <button
              class=${this.showMacros ? 'mobile-macros-btn active' : 'mobile-macros-btn'}
              aria-label="Macros panel"
              aria-expanded=${this.showMacros}
              @click=${() => { this.showMacros = !this.showMacros; }}>Macros</button>
          </div>
        </div>
        ${this.showMacros ? html`
          <div class="mobile-macros-sheet" role="region" aria-label="Macros list">
            <div class="mobile-macros-header">
              <span>Keys & Macros</span>
              <div class="sheet-actions">
                <select aria-label="Color theme" @change=${this.handleThemeSelect} class="mobile-theme-select">
                  ${listThemes().map(t => html`
                    <option value=${t.id} .selected=${t.id === this.themeId}>${t.name}</option>
                  `)}
                </select>
                <button aria-label="Edit macros" @click=${() => { this.showMacroEditor = true; this.showMacros = false; }}>Edit</button>
                <button class="close-btn" aria-label="Close macros" @click=${() => { this.showMacros = false; }}>✕</button>
              </div>
            </div>
            <div class="mobile-keys-section">
              <div class="mobile-keys" aria-label="Terminal keys">
                <button aria-label="Escape key" @click=${() => this.sendMobileKey('Escape', 'Escape')}>Esc</button>
                <button aria-label="Tab key" @click=${() => this.sendMobileKey('Tab', 'Tab')}>Tab</button>
                <button aria-label="Control modifier" aria-pressed=${this.mobileCtrl}
                  @click=${() => { this.mobileCtrl = !this.mobileCtrl; }}>Ctrl</button>
                <button aria-label="Left arrow key" @click=${() => this.sendMobileKey('ArrowLeft', 'ArrowLeft')}>←</button>
                <button aria-label="Down arrow key" @click=${() => this.sendMobileKey('ArrowDown', 'ArrowDown')}>↓</button>
                <button aria-label="Up arrow key" @click=${() => this.sendMobileKey('ArrowUp', 'ArrowUp')}>↑</button>
                <button aria-label="Right arrow key" @click=${() => this.sendMobileKey('ArrowRight', 'ArrowRight')}>→</button>
                <button aria-label="Backspace key" @click=${() => this.sendMobileKey('Backspace', 'Backspace')}>⌫</button>
                <button aria-label="Enter key" @click=${() => this.sendMobileKey('Enter', 'Enter')}>↵</button>
              </div>
              ${this.mobileCtrl ? html`
                <div class="mobile-ctrl-palette" aria-label="Ctrl shortcuts">
                  <button aria-label="C key" @click=${() => this.sendMobileKey('c', 'KeyC')}>^C</button>
                  <button aria-label="D key" @click=${() => this.sendMobileKey('d', 'KeyD')}>^D</button>
                  <button aria-label="Z key" @click=${() => this.sendMobileKey('z', 'KeyZ')}>^Z</button>
                  <button aria-label="R key" @click=${() => this.sendMobileKey('r', 'KeyR')}>^R</button>
                  <button aria-label="L key" @click=${() => this.sendMobileKey('l', 'KeyL')}>^L</button>
                  <button aria-label="A key" @click=${() => this.sendMobileKey('a', 'KeyA')}>^A</button>
                  <button aria-label="E key" @click=${() => this.sendMobileKey('e', 'KeyE')}>^E</button>
                  <button aria-label="W key" @click=${() => this.sendMobileKey('w', 'KeyW')}>^W</button>
                  <button aria-label="K key" @click=${() => this.sendMobileKey('k', 'KeyK')}>^K</button>
                  <button aria-label="U key" @click=${() => this.sendMobileKey('u', 'KeyU')}>^U</button>
                </div>
              ` : ''}
            </div>
            <div class="mobile-macros-grid">
              ${this.macros.map(macro => html`
                <button class="mobile-macro-item" @click=${() => this.executeMobileMacro(macro)} aria-label=${`Run macro ${macro.name}`}>
                  <span class="mobile-macro-name">${macro.name}</span>
                  ${macroEndsWithEnter(macro) ? html`<span class="mobile-macro-enter" title="Ends with Enter">↵</span>` : ''}
                </button>
              `)}
            </div>
          </div>
        ` : ''}
      ` : ''}
      ${this.showMacroEditor ? html`
        <div class="macro-editor-modal" @click=${() => { this.showMacroEditor = false; }}>
          <div class="macro-editor-dialog" @click=${(e: Event) => e.stopPropagation()} role="dialog" aria-modal="true" aria-labelledby="macro-editor-title">
            <div class="mobile-macros-header">
              <span id="macro-editor-title">Configure Macros</span>
              <button class="close-btn" aria-label="Close editor" @click=${() => { this.showMacroEditor = false; }}>✕</button>
            </div>
            <div class="macro-editor-list" aria-label="Configured macros">
              ${this.macros.map((m, idx) => html`
                <div class="macro-editor-row">
                  <span>${m.name} ${macroEndsWithEnter(m) ? '↵' : ''}</span>
                  <button aria-label=${`Delete macro ${m.name}`} @click=${() => this.deleteMacro(idx)}>Delete</button>
                </div>
              `)}
            </div>
            <form class="macro-editor-form" @submit=${(e: Event) => {
              e.preventDefault();
              const form = e.target as HTMLFormElement;
              const name = (form.elements.namedItem('macroName') as HTMLInputElement).value.trim();
              const type = (form.elements.namedItem('macroType') as HTMLSelectElement).value as 'text' | 'key';
              const val = (form.elements.namedItem('macroVal') as HTMLInputElement).value;
              const ctrl = (form.elements.namedItem('macroCtrl') as HTMLInputElement).checked;
              const enter = (form.elements.namedItem('macroEnter') as HTMLInputElement).checked;

              let steps: MacroStep[] = [];
              if (this.draftMacroSteps.length > 0) {
                steps = [...this.draftMacroSteps];
                if (enter && !macroEndsWithEnter({ name, steps })) {
                  steps.push({ key: 'Enter', code: 'Enter' });
                }
              } else if (name && val) {
                if (type === 'text') {
                  steps.push({ text: val });
                } else {
                  steps.push({
                    key: val,
                    code: val.length === 1 ? `Key${val.toUpperCase()}` : val,
                    ctrl,
                  });
                }
                if (enter) {
                  steps.push({ key: 'Enter', code: 'Enter' });
                }
              }

              if (name && steps.length > 0) {
                this.macros = [...this.macros, { name, steps }];
                this.draftMacroSteps = [];
                try {
                  localStorage.setItem('wideboi.macros', JSON.stringify(this.macros));
                } catch {
                  // ignore
                }
                form.reset();
              }
            }}>
              <strong>Add New Macro</strong>
              <input name="macroName" placeholder="Macro Name (e.g. Git Log or Vim Quit)" required />
              ${this.draftMacroSteps.length > 0 ? html`
                <div class="macro-draft-steps" aria-label="Steps in new macro">
                  <span style="font-size: 11px; color: var(--wb-fg-muted, #aaa);">Sequence steps:</span>
                  ${this.draftMacroSteps.map((step, idx) => html`
                    <div class="macro-draft-step-row">
                      <span>${idx + 1}. ${step.text ? `Text: "${step.text}"` : `Key: ${step.ctrl ? '^' : ''}${step.key}`}</span>
                      <button type="button" @click=${() => this.removeDraftStep(idx)}>✕</button>
                    </div>
                  `)}
                </div>
              ` : ''}
              <div style="display: flex; gap: 0.3rem;">
                <select name="macroType" style="flex: 0 0 110px;">
                  <option value="text">Text</option>
                  <option value="key">Key</option>
                </select>
                <input name="macroVal" placeholder="Text or key name (e.g. git log or r)" style="flex: 1;" />
              </div>
              <div style="display: flex; gap: 0.8rem; font-size: 12px; align-items: center; flex-wrap: wrap;">
                <label><input type="checkbox" name="macroCtrl" /> Ctrl</label>
                <label><input type="checkbox" name="macroEnter" checked /> Include Enter</label>
                <button type="button" aria-label="Add Step to Sequence" style="margin-left: auto; padding: 0.2rem 0.5rem; font-size: 11px; background: var(--wb-bg-btn, #3c3c3c); color: var(--wb-fg-primary, #eee); border: 1px solid var(--wb-border-divider, #555); border-radius: 3px; cursor: pointer;"
                  @click=${(e: Event) => {
                    const btn = e.target as HTMLElement;
                    const form = btn.closest('form') as HTMLFormElement;
                    const type = (form.elements.namedItem('macroType') as HTMLSelectElement).value as 'text' | 'key';
                    const input = form.elements.namedItem('macroVal') as HTMLInputElement;
                    const ctrl = (form.elements.namedItem('macroCtrl') as HTMLInputElement).checked;
                    if (input.value) {
                      this.addDraftStep(type, input.value, ctrl);
                      input.value = '';
                    }
                  }}>+ Add Step</button>
              </div>
              <button type="submit">Add Macro</button>
            </form>
            <div class="macro-editor-actions">
              <button @click=${this.resetMacrosToDefault}>Reset Defaults</button>
              <button class="primary" aria-label="Save to Server" @click=${() => {
                this.saveMacrosToServer();
                this.showMacroEditor = false;
              }}>Save to Server</button>
            </div>
          </div>
        </div>
      ` : ''}
      ${this.stats ? html`<pre class="stats-overlay">${this.statsText || 'stats: collecting…'}</pre>` : ''}
      ${this.showHelp ? html`
        <div class="help-overlay" @click=${this.closeHelp}>
          <div class="help-dialog" role="dialog" aria-modal="true" aria-labelledby="help-title" @click=${(e: Event) => e.stopPropagation()}>
            <h3 id="help-title">
              <span>wideboi Shortcuts</span>
              <button class="close-btn" @click=${this.closeHelp} aria-label="Close help">×</button>
            </h3>
            <p style="margin-top: 0; color: var(--wb-fg-muted, #aaa);">Prefix: <kbd>${this.keyRouter.prefixLabel}</kbd> (press twice to send literal key)</p>

            <table class="help-table">
              <thead><tr><th>Key after prefix</th><th>Action</th></tr></thead>
              <tbody>
                <tr><td><kbd>h</kbd> / <kbd>←</kbd></td><td>Focus left</td></tr>
                <tr><td><kbd>l</kbd> / <kbd>→</kbd></td><td>Focus right</td></tr>
                <tr><td><kbd>Tab</kbd></td><td>Focus previous pane</td></tr>
                <tr><td><kbd>1</kbd>–<kbd>9</kbd>, <kbd>0</kbd></td><td>Focus column by position (0 is last)</td></tr>
                <tr><td><kbd>a</kbd></td><td>Smart jump (needs input / failed / done)</td></tr>
                <tr><td><kbd>c</kbd></td><td>Toggle cards / scroll layout</td></tr>
                <tr><td><kbd>n</kbd></td><td>New column</td></tr>
                <tr><td><kbd>w</kbd></td><td>Cycle column width</td></tr>
                <tr><td><kbd>o</kbd> / <kbd>p</kbd></td><td>Shrink / grow column width</td></tr>
                <tr><td><kbd>H</kbd> / <kbd>L</kbd></td><td>Pan focused pane left / right</td></tr>
                <tr><td><kbd>f</kbd></td><td>Toggle following PTY widths</td></tr>
                <tr><td><kbd>y</kbd> / <kbd>u</kbd></td><td>Move column left / right</td></tr>
                <tr><td><kbd>j</kbd> / <kbd>k</kbd></td><td>Scroll history down / up</td></tr>
                <tr><td><kbd>/</kbd></td><td>Search focused pane history (Ctrl+F)</td></tr>
                <tr><td><kbd>,</kbd></td><td>Toggle settings dialog</td></tr>
                <tr><td><kbd>x</kbd></td><td>Kill focused pane</td></tr>
                <tr><td><kbd>?</kbd></td><td>Toggle this help</td></tr>
                <tr><td><kbd>Esc</kbd> / <kbd>Ctrl+C</kbd></td><td>Cancel prefix mode</td></tr>
              </tbody>
            </table>
          </div>
        </div>
      ` : ''}
      ${this.showSettings ? html`
        <div class="settings-overlay" @click=${this.closeSettings}>
          <div class="settings-dialog" role="dialog" aria-modal="true" aria-labelledby="settings-title" @click=${(e: Event) => e.stopPropagation()}>
            <h3 id="settings-title">
              <span>wideboi Settings</span>
              <button class="close-btn" @click=${this.closeSettings} aria-label="Close settings">×</button>
            </h3>

            <div class="settings-section">
              <h4>Terminal Font</h4>
              <div class="settings-row">
                <label for="settings-font-family">Family</label>
                <select id="settings-font-family" @change=${this.handleFontChange}>
                  ${AVAILABLE_FONTS.map(f => html`
                    <option value=${f.id} .selected=${termSettings.fontFamily === f.id}>${f.name}</option>
                  `)}
                </select>
              </div>
              <div class="settings-row">
                <label for="settings-font-size">Size (px)</label>
                <div class="font-size-controls">
                  <button type="button" aria-label="Decrease font size" @click=${() => this.stepFontSize(-1)}>−</button>
                  <input id="settings-font-size" type="number" min="8" max="48"
                    .value=${termSettings.fontSize.toString()}
                    @change=${this.handleFontSizeChange} />
                  <button type="button" aria-label="Increase font size" @click=${() => this.stepFontSize(1)}>+</button>
                  <button type="button" @click=${() => this.resetFontSize()}>Reset</button>
                </div>
              </div>
              <div class="settings-preview" style="font: ${termSettings.font};">
                <div class="preview-line">0123456789 ABCDEFGHIJKLMNOPQRSTUVWXYZ abcdefghijklmnopqrstuvwxyz</div>
                <div class="preview-line symbols">⚡    󰘧  󰊤      󰌠  (Nerd Font Icons)</div>
              </div>
            </div>

            <div class="settings-section">
              <h4>Preferences</h4>
              <div class="settings-row">
                <label for="settings-theme">Color Theme</label>
                <select id="settings-theme" @change=${this.handleThemeSelect}>
                  ${listThemes().map(t => html`
                    <option value=${t.id} .selected=${t.id === this.themeId}>${t.name}</option>
                  `)}
                </select>
              </div>
              <div class="settings-row">
                <label for="settings-prefix-key">Prefix Key</label>
                <select id="settings-prefix-key" @change=${this.handlePrefixChange}>
                  <option value="ctrl+b" .selected=${this.prefixSetting === 'ctrl+b'}>Ctrl+B</option>
                  <option value="ctrl+a" .selected=${this.prefixSetting === 'ctrl+a'}>Ctrl+A</option>
                  <option value="ctrl+space" .selected=${this.prefixSetting === 'ctrl+space'}>Ctrl+Space</option>
                </select>
              </div>
              <div class="settings-row">
                <label for="settings-layout-mode">Layout Mode</label>
                <select id="settings-layout-mode" @change=${this.handleLayoutSelect}>
                  <option value="cards" .selected=${cards}>Cards</option>
                  <option value="scroll" .selected=${!cards}>Scroll</option>
                </select>
              </div>
            </div>
          </div>
        </div>
      ` : ''}
      ${!this.connected ? html`
        <div class="overlay">
          <div class="connection-box">
            <h2>Connect to wideboi</h2>
            <input 
              type="text" 
              .value=${this.wsUrl} 
              @input=${this.handleUrlChange}
              @keydown=${this.handleKeydown}
              placeholder=${`${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/ws`}
            />
            <input 
              type="password" 
              .value=${this.token} 
              @input=${this.handleTokenChange}
              @keydown=${this.handleKeydown}
              placeholder="Token (optional)"
            />
            <button @click=${this.connectClient}>Connect</button>
            ${this.errorMsg ? html`<div class="error">${this.errorMsg}</div>` : ''}
          </div>
        </div>
      ` : ''}
    `;
  }
}
