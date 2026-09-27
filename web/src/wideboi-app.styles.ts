import { css } from 'lit';

export const appHostStyles = css`
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
`;

export const wideboiAppStyles = css`
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
    .status.search-bar {
      position: absolute;
      top: 0;
      bottom: 0;
      left: 0;
      right: 0;
      display: flex;
      align-items: center;
      gap: 0.4rem;
      padding: 0 0.5rem;
      background: var(--wb-bg-toolbar, #252526);
      overflow-x: auto;
      scrollbar-width: none;
      font: 12px monospace;
      color: var(--wb-fg-primary, #ccc);
      z-index: 10;
      white-space: nowrap;
    }
    .status.search-bar::-webkit-scrollbar {
      display: none;
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
      position: relative;
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
    .pane-tabs-wrapper {
      position: relative;
      display: flex;
      align-items: center;
      min-width: 0;
      max-width: 100%;
      flex: 0 1 auto;
      min-height: 26px;
    }
    .pane-tabs-wrapper.searching {
      flex: 1 1 auto;
      min-width: min(100%, 460px);
    }
    .pane-tabs {
      display: flex;
      align-items: center;
      gap: 4px;
      overflow-x: auto;
      scrollbar-width: none;
      max-width: 100%;
      flex: 1 1 auto;
      min-width: 0;
    }
    .pane-tabs::-webkit-scrollbar {
      display: none;
    }
    .pane-tab {
      display: inline-flex;
      align-items: center;
      gap: 5px;
      background: var(--wb-bg-btn, #3c3c3c);
      color: var(--wb-fg-primary, #ccc);
      border: 1px solid var(--wb-border-divider, #555);
      border-radius: 3px;
      padding: 0.2rem 0.5rem;
      font-size: 12px;
      font-family: inherit;
      cursor: pointer;
      white-space: nowrap;
      user-select: none;
      flex: 0 0 auto;
    }
    .pane-tab:hover {
      background: var(--wb-bg-btn-hover, #4c4c4c);
      color: #fff;
    }
    .pane-tab[aria-selected="true"] {
      background: var(--wb-bg-app, #1e1e1e);
      border-color: var(--wb-focus, #007fd4);
      color: #fff;
      font-weight: 600;
    }
    .pane-tab .tab-focus-dot {
      font-size: 10px;
      line-height: 1;
      color: var(--wb-fg-muted, #888);
    }
    .pane-tab[aria-selected="true"] .tab-focus-dot {
      color: var(--wb-focus, #007fd4);
    }
    .pane-tab .tab-title {
      max-width: 140px;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .pane-tab .tab-status {
      font-weight: bold;
    }
    .pane-tab .tab-status.working { color: #58a6ff; }
    .pane-tab .tab-status.needs-input { color: #d29922; }
    .pane-tab .tab-status.done { color: #3fb950; }
    .pane-tab .tab-status.failed { color: #f85149; }
    .pane-tab .tab-scroll {
      color: var(--wb-fg-muted, #aaa);
      font-size: 11px;
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
      gap: 0.25rem;
      padding: 0.25rem;
      background: var(--wb-bg-toolbar, #252526);
      color: var(--wb-fg-primary, #ccc);
      font: 13px sans-serif;
      border-top: 1px solid var(--wb-border, #3c3c3c);
    }
    .mobile-bar select { flex: 1; min-width: 60px; }
    .mobile-bar button {
      min-width: 30px;
      font-size: 16px;
      line-height: 1;
      padding: 0 0.35rem;
    }
    .mobile-bar .pane-nav-btn {
      min-width: 44px;
      font-size: 22px;
      padding: 0 0.5rem;
      font-weight: bold;
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
      gap: 0.15rem;
    }
    .mobile-zoom button {
      min-width: 26px;
      padding: 0;
    }
    .mobile-zoom .zoom-reset {
      min-width: 38px;
      font-size: 11px;
      padding: 0;
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
      .toolbar.searching {
        display: flex;
        position: fixed;
        bottom: 0;
        left: 0;
        right: 0;
        z-index: 25;
      }
      .toolbar.searching > :not(.pane-tabs-wrapper) {
        display: none;
      }
      .toolbar.searching .pane-tabs-wrapper {
        width: 100%;
        height: 36px;
      }
      .mobile-bar { display: flex; }
      .mobile-dock { display: flex; }
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
    .mobile-settings-btn, .mobile-cmd-btn {
      background: var(--wb-bg-btn, #333);
      border: 1px solid var(--wb-border-divider, #555);
      color: var(--wb-fg-primary, #ccc);
      border-radius: 4px;
      padding: 0 0.4rem;
      min-width: 36px;
      height: 40px;
      font-size: 16px;
      cursor: pointer;
      display: flex;
      align-items: center;
      justify-content: center;
      box-sizing: border-box;
      flex-shrink: 0;
    }
    .mobile-settings-btn:hover, .mobile-cmd-btn:hover {
      background: var(--wb-bg-btn-hover, #444);
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
    .command-menu-overlay {
      position: fixed;
      top: 0; left: 0; right: 0; bottom: 0;
      background: rgba(0, 0, 0, 0.7);
      display: flex;
      align-items: center;
      justify-content: center;
      z-index: 50;
      padding: 1rem;
    }
    .command-menu-dialog {
      background: var(--wb-bg-toolbar, #252526);
      border: 1px solid var(--wb-border, #3c3c3c);
      border-radius: 8px;
      width: 100%;
      max-width: 440px;
      max-height: 85vh;
      overflow-y: auto;
      display: flex;
      flex-direction: column;
      box-shadow: 0 8px 32px rgba(0, 0, 0, 0.6);
      color: var(--wb-fg-primary, #eee);
    }
    .command-menu-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      padding: 0.75rem 1rem;
      border-bottom: 1px solid var(--wb-border, #3c3c3c);
    }
    .command-menu-header h3 {
      margin: 0;
      font-size: 16px;
      font-weight: 600;
      display: flex;
      align-items: baseline;
      gap: 0.6rem;
    }
    .command-menu-prefix-tip {
      font-size: 11px;
      font-weight: normal;
      color: var(--wb-fg-muted, #888);
    }
    .command-menu-header .close-btn {
      background: transparent;
      border: none;
      color: var(--wb-fg-primary, #eee);
      font-size: 16px;
      cursor: pointer;
      padding: 0.2rem 0.5rem;
      border-radius: 4px;
    }
    .command-menu-header .close-btn:hover {
      background: var(--wb-bg-btn, #3c3c3c);
    }
    .command-menu-list {
      display: flex;
      flex-direction: column;
      padding: 0.5rem;
      gap: 0.35rem;
      overflow-y: auto;
    }
    .command-menu-item {
      display: flex;
      align-items: center;
      gap: 0.75rem;
      padding: 0.6rem 0.75rem;
      background: var(--wb-bg-btn, #333);
      border: 1px solid var(--wb-border-divider, #444);
      border-radius: 6px;
      color: var(--wb-fg-primary, #eee);
      font-size: 14px;
      text-align: left;
      cursor: pointer;
      transition: background 0.15s, border-color 0.15s;
      min-height: 44px;
      box-sizing: border-box;
    }
    .command-menu-item:hover, .command-menu-item:focus-visible {
      background: var(--wb-focus, #0e639c);
      border-color: #1177bb;
      color: #fff;
    }
    .command-menu-item:active {
      transform: translateY(1px);
    }
    .command-menu-item.primary-action {
      background: #1b2e3e;
      border-color: #0e639c;
    }
    .command-menu-icon {
      font-size: 18px;
      flex-shrink: 0;
      width: 24px;
      text-align: center;
    }
    .command-menu-info {
      display: flex;
      flex-direction: column;
      flex: 1;
      min-width: 0;
    }
    .command-menu-label {
      font-weight: 500;
      line-height: 1.2;
    }
    .command-menu-desc {
      font-size: 11px;
      color: var(--wb-fg-muted, #999);
      margin-top: 2px;
    }
    .command-menu-item:hover .command-menu-desc {
      color: #ddd;
    }
    .command-menu-shortcut {
      font-family: monospace;
      font-size: 12px;
      background: rgba(0, 0, 0, 0.3);
      padding: 0.2rem 0.45rem;
      border-radius: 4px;
      border: 1px solid rgba(255, 255, 255, 0.1);
      color: var(--wb-focus, #79c0ff);
      flex-shrink: 0;
    }
    .command-menu-item:hover .command-menu-shortcut {
      color: #fff;
      border-color: rgba(255, 255, 255, 0.3);
    }
    @media (max-width: 480px) {
      .command-menu-overlay {
        align-items: flex-end;
        padding: 0;
      }
      .command-menu-dialog {
        max-width: 100%;
        border-radius: 12px 12px 0 0;
        border-bottom: none;
        max-height: 75vh;
        padding-bottom: max(0.5rem, env(safe-area-inset-bottom));
      }
    }
`;
