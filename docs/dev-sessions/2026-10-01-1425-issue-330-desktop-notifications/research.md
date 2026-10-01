# Research: Issue 330 Desktop Notifications

- Host Terminal Escape Sequences:
  - OSC 9: `\x1b]9;<title>: <message>\x07`
  - OSC 99: `\x1b]99;;<title>: <message>\x07`
  - Control character sanitization: Any control characters (< 0x20, 0x7f, 0x80..0x9f) in title/body must be replaced with spaces to avoid breaking the host terminal escape parser.
- Prior Review Findings Addressed from PR 356/354:
  1. Restored panes in `Server.RestorePanes` must also have their `OnBell` callbacks registered.
  2. Text emitted to host OSC must be sanitized.
  3. Settings dialog in web client must preserve layout stability so acceptance tests don't time out on `.close-btn`.
