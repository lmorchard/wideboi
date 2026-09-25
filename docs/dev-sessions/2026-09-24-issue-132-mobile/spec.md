# Issue #132: narrow browser control

## Decision

The issue discussion expanded the original “peek in” idea to interactive use.
At a viewport width of 480 CSS pixels or less, show one pane at a time with a
picker and previous/next buttons. The pane remains at its server-defined grid
size; its view pans in both directions. Touch never sends terminal mouse input
in this layout.

The input dock holds a draft. Send transmits the draft text, and Enter is a
separate key. A row of terminal keys covers Esc, Tab, arrows, Backspace, Enter,
and one-shot Ctrl with C, D, and Z. Pasting and IME composition into the draft
must not transmit text early. Existing hardware-keyboard input still works.

The browser's visual viewport height controls the narrow layout height when
the on-screen keyboard appears. The pane view shrinks and keeps following its
bottom if the user was already at the bottom. Browser resize messages are
suppressed in the narrow layout so opening the keyboard cannot resize the
shared PTY. Initial attach still reports the available grid size.

## Verification

- Playwright checks pane navigation, Send/Enter separation, touch dragging,
  absence of mouse messages, key codes/modifiers, IME and paste isolation,
  and visual viewport shrink without PTY resize.
- The existing browser and unit suites cover desktop layout and input paths.
